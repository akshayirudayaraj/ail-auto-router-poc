#!/usr/bin/env python3
"""
Agentic dual-arm runner: the harness driver. LOG-FIRST, NO GRADING.

For each (task, arm) it:
  1. Makes a fresh git checkout of the task's base (buggy) repo.
  2. Runs the REAL Claude Code harness (`claude -p`, stream-json) on the issue,
     with only Read/Edit/Write/Bash and no MCP/hooks, capturing the full event
     stream.
       * FRONTIER arm -> subscription, model opus (FRONTIER_MODEL), strict-mcp.
       * LOCAL arm    -> ANTHROPIC_BASE_URL=<proxy>, model routed to Ollama
                         gpt-oss:20b, --bare --strict-mcp-config (the full CC
                         system prompt is ~30k tokens => ~8 min/turn locally, so
                         --bare is required for tractability; see DECISIONS D13).
  3. Captures the git diff (the patch) and mines tool-call fidelity, turns,
     tokens, wall-clock, hit-cap/timeout/empty-patch from the event stream (and,
     for local, the proxy's native-vs-rescued fidelity log).
  4. Emits two log artifacts per (task, arm): a portable RawTurn `.session.jsonl`
     (consumed by internal/extract) and the raw CC `.events.jsonl` event stream
     (UI trace + fidelity), plus the `.patch` and a run-record JSON.

It does NOT run tests or a judge and writes NO `resolved`/`outcome` field: all
grading is the deferred offline engine's job (DATA_PLAN.md, invariant "no grading
during generation"). The base `repo/` + `_oracle/` are preserved so the offline
engine can grade later from (base repo + agent diff + test_patch).

Results are cached per (task, arm, config-hash) so an interrupted overnight run
resumes without re-paying. A Max/usage rate-limit on the frontier arm is treated
as a TRANSIENT (not written, not cached) so a lockout never pollutes the corpus;
a theoretical-USD cap on the frontier arm also stops runaway paid calls.

Not portable; agentic/ is the non-portable orchestration boundary (see
agentic/README.md and DECISIONS D12/D13).
"""
import argparse
import glob
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import container_exec  # noqa: E402  (containerized SWE execution)

ROOT = os.path.abspath(os.path.join(HERE, ".."))
TASKS_DIR = os.path.join(ROOT, "tasks")
RESULTS_DIR = os.path.join(ROOT, "results")
CHECKOUTS_DIR = os.path.join(ROOT, "checkouts")

# Cost convention mirrors internal/gold: relative units = tokens * price, with
# the frontier rung priced 15x the local rung (ratio is what matters).
PRICE_LOCAL = 1.0
PRICE_FRONTIER = 15.0

PROXY_URL = os.environ.get("PROXY_URL", "http://127.0.0.1:8790")
PROXY_LOG = os.environ.get("PROXY_LOG", "/tmp/agentic_proxy.log")
LOCAL_OLLAMA_MODEL = os.environ.get("PROXY_OLLAMA_MODEL", "gpt-oss:20b")
FRONTIER_MODEL = os.environ.get("FRONTIER_MODEL", "opus")
MAX_FRONTIER_USD = float(os.environ.get("MAX_FRONTIER_USD", "6.0"))

# The roster is a configurable ordered list (cheap->expensive); the log/record
# schema keys on `served_model`, so adding a model == adding an arm and nothing
# else changes. K=2 now (local, frontier); IRT/kNN/gold all generalize to K>2.
# `served_model` is the real model behind an arm: the Ollama model for local, the
# CLI model alias for frontier. `model` is the name `claude -p` is invoked with
# (the local proxy ignores it and routes to Ollama).
ARMS = {
    "local": {
        "model": "claude-sonnet-4",           # name only; proxy routes to Ollama
        "served_model": LOCAL_OLLAMA_MODEL,
        "bare": True,
        "max_turns": int(os.environ.get("LOCAL_MAX_TURNS", "20")),
        "timeout": int(os.environ.get("LOCAL_TIMEOUT", "1200")),
        "price": PRICE_LOCAL,
        "use_proxy": True,
    },
    "frontier": {
        "model": FRONTIER_MODEL,
        "served_model": FRONTIER_MODEL,
        "bare": False,
        "max_turns": int(os.environ.get("FRONTIER_MAX_TURNS", "40")),
        "timeout": int(os.environ.get("FRONTIER_TIMEOUT", "600")),
        "price": PRICE_FRONTIER,
        "use_proxy": False,
    },
}

# Ordered roster (cheap->expensive). Override to reorder / subset arms.
ARM_MODELS = [a for a in os.environ.get("ARM_MODELS", "local,frontier").split(",")
              if a in ARMS]

# How many times to retry a single (task, arm) after a transient Max/usage
# rate-limit before giving up on it for this run (it stays uncached -> free
# resume later). Backoff is RATE_LIMIT_BACKOFF_S * attempt.
RATE_LIMIT_MAX_RETRIES = int(os.environ.get("RATE_LIMIT_MAX_RETRIES", "3"))
RATE_LIMIT_BACKOFF_S = int(os.environ.get("RATE_LIMIT_BACKOFF_S", "120"))


# --------------------------------------------------------------------------
def load_tasks(subset=None):
    tasks = []
    for tj in sorted(glob.glob(os.path.join(TASKS_DIR, "*", "task.json"))):
        t = json.load(open(tj))
        t["_dir"] = os.path.dirname(tj)
        if subset and t["id"] not in subset:
            continue
        tasks.append(t)
    return tasks


def config_hash(arm_cfg, arm):
    key = json.dumps({"arm": arm, "model": arm_cfg["model"], "bare": arm_cfg["bare"],
                      "max_turns": arm_cfg["max_turns"],
                      "ollama": LOCAL_OLLAMA_MODEL if arm == "local" else None},
                     sort_keys=True)
    return hashlib.sha1(key.encode()).hexdigest()[:8]


def result_path(task_id, arm, chash):
    return os.path.join(RESULTS_DIR, f"{task_id}__{arm}__{chash}.json")


def fresh_checkout(task):
    dst = os.path.join(CHECKOUTS_DIR, f"{task['id']}__{int(time.time()*1000)}")
    if os.path.exists(dst):
        shutil.rmtree(dst)
    shutil.copytree(os.path.join(task["_dir"], "repo"), dst)
    subprocess.run(["git", "init", "-q"], cwd=dst, check=True)
    subprocess.run(["git", "add", "-A"], cwd=dst, check=True)
    env = dict(os.environ, GIT_AUTHOR_NAME="base", GIT_AUTHOR_EMAIL="b@x",
               GIT_COMMITTER_NAME="base", GIT_COMMITTER_EMAIL="b@x")
    subprocess.run(["git", "commit", "-qm", "base"], cwd=dst, check=True, env=env)
    return dst


# --------------------------------------------------------------------------
def build_prompt(task):
    return (
        "You are fixing a bug in the Python repository in the current working "
        "directory. The bug and the required behavior are described below. "
        "Read the relevant source files, make the necessary code edits to the "
        "source (do NOT edit the tests), and use the shell to run the tests to "
        "confirm your fix. Keep changes minimal.\n\n"
        f"ISSUE:\n{task['issue']}\n\n"
        f"The test suite lives in tests/. You can run it with: {task['test_cmd']}\n"
        "When the tests pass, you are done."
    )


class RateLimited(Exception):
    """A Max/usage rate-limit (or auth) error from `claude -p`. Treated as a
    TRANSIENT: the (task, arm) is neither written nor cached, so a lockout never
    pollutes the corpus and a later resume re-attempts it free."""


# Markers a frontier Max/usage-limit prints (result-event error text / stderr).
# Deliberately NARROW: only the subscription-cap signals, NOT generic 5xx/"credit
# balance"/local Ollama errors, so a local-arm proxy 500 is never misread as a
# rate limit. Only ever consulted for the frontier arm.
_RATE_LIMIT_MARKERS = (
    "usage limit", "rate limit", "rate_limit", "429",
    "too many requests", "resets at", "overloaded_error",
)


# Error result subtypes that are capability/turn signals, NOT rate limits.
_NON_RATELIMIT_SUBTYPES = {"error_max_turns"}


def _result_error_text(result):
    """Human-readable error text from a result event — deliberately NOT the whole
    serialized event, whose `usage`/`anthropic-ratelimit-*` metadata is present on
    EVERY call (success included) and would false-match '429'/'rate_limit'."""
    parts = [str(result.get("subtype") or "")]
    for k in ("result", "error", "message"):
        v = result.get(k)
        if isinstance(v, str):
            parts.append(v)
        elif isinstance(v, dict):
            parts.append(json.dumps(v))
    return " ".join(parts)


def detect_rate_limit(events, raw, stderr):
    """True iff a FRONTIER run FAILED on a Max/usage rate-limit (as opposed to a
    success, a genuine capability failure, or a timeout). Caller gates this to
    arm=='frontier' — a local Ollama arm can never be Max-rate-limited.

    A completed (non-error) run is never rate-limited regardless of the
    rate-limit *metadata* CC embeds in the stream; only an errored / result-less
    run has its error text + stderr scanned for the narrow usage-cap markers."""
    result = None
    for ev in events:
        if ev.get("type") == "result":
            result = ev
    if result is not None:
        if not result.get("is_error"):
            return False                                   # completed OK
        if result.get("subtype") in _NON_RATELIMIT_SUBTYPES:
            return False                                   # turn cap, not a limit
        hay = _result_error_text(result) + "\n" + (stderr or "")
    else:
        # No result event: the failure detail is only in stderr / the raw tail.
        hay = (stderr or "") + "\n" + (raw or "")[-2000:]
    low = hay.lower()
    return any(m in low for m in _RATE_LIMIT_MARKERS)


def tool_summary(block):
    """Compact one-line summary of a single tool_use block, e.g. '[Bash] pytest -q'
    or '[Edit] test_requests.py'. Keeps the RawTurn readable without the full
    tool payload (which lives in .events.jsonl)."""
    name = block.get("name", "tool")
    inp = block.get("input", {}) or {}
    if name == "Bash" and inp.get("command"):
        cmd = str(inp["command"]).strip().splitlines()[0] if str(inp["command"]).strip() else ""
        return f"[Bash] {cmd[:140]}"
    for k in ("file_path", "path", "notebook_path"):
        if inp.get(k):
            return f"[{name}] {os.path.basename(str(inp[k]))}"
    if name == "Read" or name == "Grep" or name == "Glob":
        return f"[{name}] {str(inp.get('pattern') or inp.get('path') or '')[:80]}".rstrip()
    return f"[{name}]"


def assistant_turns(events, served_model):
    """One RawTurn-shaped dict PER assistant EVENT (not collapsed), in order.
    content = the assistant's text for that turn + a compact summary of its
    tool_use actions; served_model set on every turn. This preserves the ordered
    turn sequence internal/extract mines (per-turn served_model → escalation
    detection). CC type:'user' events are tool_result plumbing, NOT user turns,
    so they are intentionally skipped here."""
    out = []
    for ev in events:
        if ev.get("type") != "assistant":
            continue
        texts, tools = [], []
        for b in ev.get("message", {}).get("content", []):
            if b.get("type") == "text" and b.get("text", "").strip():
                texts.append(b["text"].strip())
            elif b.get("type") == "tool_use":
                tools.append(tool_summary(b))
        content = "\n".join(texts + tools) if (texts or tools) else "[no output]"
        out.append({"role": "assistant", "content": content,
                    "served_model": served_model})
    return out


def assistant_text(events):
    """Concatenated assistant text across the stream (single string). Kept for
    callers that want the whole response as one blob (e.g. logging/judging)."""
    return "\n".join(t["content"] for t in assistant_turns(events, None))


def reconstruct_raw_turns(events, session_id, served_model, prompt, t0):
    """Portable RawTurn JSONL session for a single-shot `claude -p` run: turn 0 =
    the genuine user prompt, then ONE assistant turn per assistant event (with
    tool summaries + served_model). The rich per-tool payload lives in
    .events.jsonl; this is the ordered turn view internal/extract consumes.
    propensity is null (every task runs on every rung deterministically)."""
    ts = int(t0)
    turns = [{"session_id": session_id, "turn_index": 0, "timestamp": ts,
              "role": "user", "content": prompt}]
    for i, at in enumerate(assistant_turns(events, served_model), start=1):
        turns.append({"session_id": session_id, "turn_index": i, "timestamp": ts, **at})
    return turns


def run_harness(task, arm, arm_cfg, checkout):
    """Run claude -p in the checkout; return
    (events, timed_out, wall_s, proxy_lines, raw, stderr)."""
    prompt = build_prompt(task)
    args = [
        "claude", "-p", prompt,
        "--output-format", "stream-json", "--verbose",
        "--max-turns", str(arm_cfg["max_turns"]),
        "--allowedTools", "Read", "Edit", "Write", "Bash",
        "--permission-mode", "bypassPermissions",
        "--strict-mcp-config",
        "--model", arm_cfg["model"],
    ]
    if arm_cfg["bare"]:
        args.append("--bare")

    env = dict(os.environ)
    if arm_cfg["use_proxy"]:
        env["ANTHROPIC_BASE_URL"] = PROXY_URL
        env["ANTHROPIC_API_KEY"] = "dummy-local-key"

    proxy_offset = _file_size(PROXY_LOG) if arm_cfg["use_proxy"] else 0
    t0 = time.time()
    stderr = ""
    try:
        proc = subprocess.run(args, cwd=checkout, env=env, capture_output=True,
                              text=True, timeout=arm_cfg["timeout"], stdin=subprocess.DEVNULL)
        raw = proc.stdout
        stderr = proc.stderr or ""
        timed_out = False
    except subprocess.TimeoutExpired as e:
        raw = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
        stderr = e.stderr.decode() if isinstance(e.stderr, bytes) else (e.stderr or "")
        timed_out = True
    wall = time.time() - t0

    events = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            events.append(json.loads(line))
        except Exception:
            pass
    proxy_lines = _read_proxy_since(PROXY_LOG, proxy_offset) if arm_cfg["use_proxy"] else []
    return events, timed_out, wall, proxy_lines, raw, stderr


def _file_size(p):
    try:
        return os.path.getsize(p)
    except OSError:
        return 0


def _read_proxy_since(p, offset):
    out = []
    try:
        with open(p) as f:
            f.seek(offset)
            for line in f:
                try:
                    d = json.loads(line)
                    if d.get("event") == "response":
                        out.append(d)
                except Exception:
                    pass
    except OSError:
        pass
    return out


# --------------------------------------------------------------------------
def mine_metrics(events, proxy_lines):
    """Extract turns, tool-call fidelity, tokens, cost signals from the CC event
    stream (+ proxy fidelity log for local)."""
    tool_calls = 0
    tool_errors = 0
    assistant_turns = 0
    result_event = None
    for ev in events:
        t = ev.get("type")
        if t == "assistant":
            assistant_turns += 1
            for b in ev.get("message", {}).get("content", []):
                if b.get("type") == "tool_use":
                    tool_calls += 1
        elif t == "user":
            for b in ev.get("message", {}).get("content", []):
                if b.get("type") == "tool_result" and b.get("is_error"):
                    tool_errors += 1
        elif t == "result":
            result_event = ev

    usage = (result_event or {}).get("usage", {}) or {}
    in_tok = int(usage.get("input_tokens", 0) or 0)
    out_tok = int(usage.get("output_tokens", 0) or 0)
    cache_read = int(usage.get("cache_read_input_tokens", 0) or 0)
    cache_create = int(usage.get("cache_creation_input_tokens", 0) or 0)

    # Local proxy native-vs-rescued fidelity (raw, harness-faithful metric).
    native = sum(p["fidelity"]["native"] for p in proxy_lines)
    rescued = sum(p["fidelity"]["rescued"] for p in proxy_lines)

    res_subtype = (result_event or {}).get("subtype")
    num_turns = (result_event or {}).get("num_turns", assistant_turns)
    cost_usd = (result_event or {}).get("total_cost_usd")

    return {
        "assistant_turns": assistant_turns,
        "num_turns": num_turns,
        "tool_calls_attempted": tool_calls,
        "tool_calls_errored": tool_errors,
        "any_valid_tool_call": tool_calls > 0,
        "input_tokens": in_tok,
        "output_tokens": out_tok,
        "cache_read_tokens": cache_read,
        "cache_creation_tokens": cache_create,
        "total_tokens": in_tok + out_tok + cache_read + cache_create,
        "native_tool_calls": native,
        "rescued_tool_calls": rescued,
        "proxy_requests": len(proxy_lines),
        "result_subtype": res_subtype,
        "reported_cost_usd": cost_usd,
        "is_error": bool((result_event or {}).get("is_error")),
        "had_result_event": result_event is not None,
    }


def git_diff(checkout):
    p = subprocess.run(["git", "diff"], cwd=checkout, capture_output=True, text=True)
    return p.stdout


def task_provenance(task):
    """provenance in {templated, swe_verified, synthetic}. Inferred when absent
    (older curated/self-contained tasks predate the tag)."""
    if task.get("provenance"):
        return task["provenance"]
    return "swe_verified" if task["id"].startswith("swe-") else "synthetic"


def task_grounding(task):
    """grounding in {benchmark, oss_history, synthetic_repo}."""
    if task.get("grounding"):
        return task["grounding"]
    return "benchmark" if task_provenance(task) == "swe_verified" else "synthetic_repo"


def has_executable_oracle(task):
    """True iff the task carries an executable oracle offline grading can use: a
    quarantined _oracle/ (real-SWE/generated) or an in-repo FAIL_TO_PASS set."""
    if os.path.isdir(os.path.join(task["_dir"], "_oracle")):
        return True
    return bool(task.get("fail_to_pass"))


def is_swe_container_task(task):
    """SWE-bench Verified instances run the agent INSIDE the official per-instance
    image (real env, agent can run the tests, gen env == grade env). Self-contained
    tasks (pure-Python) stay on host — they run correctly there. Override with
    AGENT_FORCE_HOST=1 to force host execution everywhere (debug)."""
    if os.environ.get("AGENT_FORCE_HOST") == "1":
        return False
    return bool(task.get("instance_id")) and (
        task.get("provenance") == "swe_verified" or task.get("grader") == "swebench")


# --------------------------------------------------------------------------
def run_one(task, arm, force=False):
    """Run one (task, arm) LOG-FIRST with NO grading. Returns (record, cached).
    Raises RateLimited on a transient Max/usage lockout (record NOT written, so a
    later resume re-attempts it free)."""
    arm_cfg = ARMS[arm]
    chash = config_hash(arm_cfg, arm)
    rp = result_path(task["id"], arm, chash)
    if os.path.exists(rp) and not force:
        return json.load(open(rp)), True  # cached

    swe = is_swe_container_task(task)
    checkout = None if swe else fresh_checkout(task)
    try:
        if swe:
            # Agent runs INSIDE the official swebench per-instance image; local-arm
            # calls still traverse the host proxy, so fidelity is logged host-side.
            proxy_offset = _file_size(PROXY_LOG) if arm_cfg["use_proxy"] else 0
            events, timed_out, wall, patch, raw, stderr = \
                container_exec.run_agent_in_container(
                    task["instance_id"], arm, arm_cfg, build_prompt(task),
                    timeout=arm_cfg["timeout"])
            proxy_lines = _read_proxy_since(PROXY_LOG, proxy_offset) if arm_cfg["use_proxy"] else []
        else:
            events, timed_out, wall, proxy_lines, raw, stderr = run_harness(
                task, arm, arm_cfg, checkout)
            patch = git_diff(checkout)

        # A frontier Max/usage rate-limit is a TRANSIENT, not a capability
        # outcome. Bail before writing anything so the (task, arm) stays uncached
        # and free to retry. Only the frontier arm can be Max-rate-limited; a
        # local Ollama proxy error is a real (capability/infra) result, not this.
        if arm == "frontier" and not timed_out and detect_rate_limit(events, raw, stderr):
            raise RateLimited(f"{task['id']} {arm}")

        metrics = mine_metrics(events, proxy_lines)
        empty_patch = patch.strip() == ""

        hit_cap = (metrics["result_subtype"] == "error_max_turns") or \
                  (metrics["num_turns"] and metrics["num_turns"] >= arm_cfg["max_turns"])
        cost_units = metrics["total_tokens"] * arm_cfg["price"]
        served_model = arm_cfg["served_model"]
        session_id = f"{task['id']}__{arm}__{chash}"

        # --- Two log artifacts (DATA_PLAN Phase 1). No grading, no outcome. ---
        # 1. Portable RawTurn session log (internal/extract consumes this).
        session_path = rp.replace(".json", ".session.jsonl")
        with open(session_path, "w") as f:
            for t in reconstruct_raw_turns(events, session_id, served_model,
                                           build_prompt(task), time.time() - wall):
                f.write(json.dumps(t) + "\n")
        # 2. Raw CC event stream (UI trace drill-in + fidelity mining).
        events_path = rp.replace(".json", ".events.jsonl")
        with open(events_path, "w") as f:
            for ev in events:
                f.write(json.dumps(ev) + "\n")
        # 3. The patch (git diff), preserved for offline grading.
        patch_path = rp.replace(".json", ".patch")
        with open(patch_path, "w") as f:
            f.write(patch)

        # --- Run record. NO resolved / fail_to_pass_ok / outcome. -------------
        result = {
            "task_id": task["id"],
            "tier": task.get("tier"),
            "arm": arm,
            "served_model": served_model,
            "model": arm_cfg["model"],
            "ollama_model": LOCAL_OLLAMA_MODEL if arm == "local" else None,
            "provenance": task_provenance(task),
            "grounding": task_grounding(task),
            "execution": "container" if swe else "host",  # where the agent ran
            "split": None,                       # assigned by Phase 4 (split.py)
            "has_executable_oracle": has_executable_oracle(task),
            "config_hash": chash,
            "session_id": session_id,
            "session_log_path": os.path.basename(session_path),
            "events_log_path": os.path.basename(events_path),
            "patch_path": os.path.basename(patch_path),
            "wall_clock_s": round(wall, 1),
            "timed_out": timed_out,
            "hit_turn_cap": bool(hit_cap),
            "empty_patch": empty_patch,
            "patch_len": len(patch),
            "cost_units": cost_units,
            **metrics,
        }
        with open(rp, "w") as f:
            json.dump(result, f, indent=2)
        return result, False
    finally:
        # The ephemeral host checkout is discarded; the patch is already persisted
        # and the base repo/ + _oracle/ live in tasks/<id>/ for offline grading.
        # (SWE tasks run in a container that container_exec already removed.)
        if checkout:
            shutil.rmtree(checkout, ignore_errors=True)


# --------------------------------------------------------------------------
def proxy_healthy():
    import urllib.request
    try:
        with urllib.request.urlopen(PROXY_URL + "/health", timeout=3) as r:
            return json.loads(r.read()).get("status") == "ok"
    except Exception:
        return False


def run_with_retry(task, arm, force=False):
    """run_one wrapped in Max/usage rate-limit backoff. Returns (record, cached)
    or (None, False) if the (task, arm) was left uncached after exhausting
    retries (a later resume re-attempts it free)."""
    for attempt in range(1, RATE_LIMIT_MAX_RETRIES + 1):
        try:
            return run_one(task, arm, force=force)
        except RateLimited as e:
            wait = RATE_LIMIT_BACKOFF_S * attempt
            print(f"[runner] RATE-LIMIT on {e} (attempt {attempt}/"
                  f"{RATE_LIMIT_MAX_RETRIES}); backing off {wait}s. Not cached — "
                  f"resume is free.", file=sys.stderr)
            if attempt < RATE_LIMIT_MAX_RETRIES:
                time.sleep(wait)
    print(f"[runner] giving up on {task['id']} {arm} for this run (rate-limited); "
          f"left uncached.", file=sys.stderr)
    return None, False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--arms", default=",".join(ARM_MODELS),
                    help="comma list of arms (ordered cheap->expensive)")
    ap.add_argument("--tasks", default="", help="comma list of task ids (default all)")
    ap.add_argument("--smoke", action="store_true",
                    help="one easy task, both arms (fidelity smoke)")
    ap.add_argument("--force", action="store_true", help="ignore cache")
    args = ap.parse_args()

    os.makedirs(RESULTS_DIR, exist_ok=True)
    os.makedirs(CHECKOUTS_DIR, exist_ok=True)

    arms = [a for a in args.arms.split(",") if a in ARMS]
    subset = set(x for x in args.tasks.split(",") if x) or None
    if args.smoke:
        subset = {"easy-01-reverse-words"}
        arms = list(ARM_MODELS)

    tasks = load_tasks(subset)
    if not tasks:
        print("no tasks matched", file=sys.stderr)
        return 1

    if "local" in arms and not proxy_healthy():
        print(f"[runner] WARNING: local proxy not healthy at {PROXY_URL}; "
              f"start it with `make agentic-proxy` first. Skipping local arm.",
              file=sys.stderr)
        arms = [a for a in arms if a != "local"]

    print(f"[runner] {len(tasks)} tasks x arms={arms} "
          f"(frontier={FRONTIER_MODEL}, local={LOCAL_OLLAMA_MODEL})", file=sys.stderr)
    spent_usd = 0.0
    for task in tasks:
        for arm in arms:
            if arm == "frontier" and spent_usd >= MAX_FRONTIER_USD:
                print(f"[runner] SPEND CAP ${MAX_FRONTIER_USD} reached; "
                      f"skipping frontier {task['id']}", file=sys.stderr)
                continue
            t0 = time.time()
            try:
                res, cached = run_with_retry(task, arm, force=args.force)
            except container_exec.MissingImageError as e:
                print(f"[skip ] {task['id']:24s} {arm:8s} no image ({e}); "
                      f"run `make agentic-swe-images`", file=sys.stderr)
                continue
            if res is None:            # rate-limited, left uncached this run
                continue
            tag = "cache" if cached else "ran  "
            if arm == "frontier" and not cached and res.get("reported_cost_usd"):
                spent_usd += res["reported_cost_usd"]
            fid = ""
            if arm == "local":
                fid = (f" native={res.get('native_tool_calls',0)} "
                       f"rescued={res.get('rescued_tool_calls',0)}")
            # NO grading during generation -> no resolved/outcome to print.
            print(f"[{tag}] {task['id']:24s} {arm:8s} "
                  f"turns={res.get('num_turns')} "
                  f"toolcalls={res.get('tool_calls_attempted')}{fid} "
                  f"empty_patch={res.get('empty_patch')} "
                  f"timed_out={res.get('timed_out')} "
                  f"wall={res.get('wall_clock_s')}s "
                  f"toks={res.get('total_tokens')} "
                  f"{'(%.1fs)'%(time.time()-t0)}", file=sys.stderr)
    print(f"[runner] done. frontier theoretical spend ~${spent_usd:.2f} "
          f"(subscription: not billed; a Max cap is a rate limit, not $).",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
