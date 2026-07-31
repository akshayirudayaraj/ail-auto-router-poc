#!/usr/bin/env python3
"""
Agentic dual-arm runner: the harness driver + execution oracle.

For each (task, arm) it:
  1. Makes a fresh git checkout of the task's base (buggy) repo.
  2. Runs the REAL Claude Code harness (`claude -p`, stream-json) on the issue,
     with only Read/Edit/Write/Bash and no MCP/hooks, capturing the full event
     stream.
       * FRONTIER arm -> subscription, model sonnet, --strict-mcp-config.
       * LOCAL arm    -> ANTHROPIC_BASE_URL=<proxy>, model routed to Ollama
                         qwen2.5-coder, --bare --strict-mcp-config (the full CC
                         system prompt is ~30k tokens => ~8 min/turn locally, so
                         --bare is required for tractability; see DECISIONS D13).
  3. Captures the git diff (the patch) and mines tool-call fidelity, turns,
     tokens, wall-clock, hit-cap/crash/empty-patch from the event stream (and,
     for local, the proxy's native-vs-rescued fidelity log).
  4. Executes FAIL_TO_PASS + PASS_TO_PASS in Docker -> resolved pass/fail (the
     oracle).

Results are cached per (task, arm, config-hash) so an interrupted overnight run
resumes without re-paying. A hard USD cap on the frontier arm stops paid calls;
running totals are logged.

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
from executor import score_checkout  # noqa: E402

ROOT = os.path.abspath(os.path.join(HERE, ".."))
TASKS_DIR = os.path.join(ROOT, "tasks")
RESULTS_DIR = os.path.join(ROOT, "results")
CHECKOUTS_DIR = os.path.join(ROOT, "checkouts")

# Cost convention mirrors internal/gold: relative units = tokens * price, with
# the frontier rung priced 15x the local rung (ratio is what matters).
PRICE_LOCAL = 1.0
PRICE_FRONTIER = 15.0

ARMS = {
    "local": {
        "model": "claude-sonnet-4",           # name only; proxy routes to Ollama
        "bare": True,
        "max_turns": int(os.environ.get("LOCAL_MAX_TURNS", "20")),
        "timeout": int(os.environ.get("LOCAL_TIMEOUT", "1200")),
        "price": PRICE_LOCAL,
        "use_proxy": True,
    },
    "frontier": {
        "model": os.environ.get("FRONTIER_MODEL", "sonnet"),
        "bare": False,
        "max_turns": int(os.environ.get("FRONTIER_MAX_TURNS", "40")),
        "timeout": int(os.environ.get("FRONTIER_TIMEOUT", "600")),
        "price": PRICE_FRONTIER,
        "use_proxy": False,
    },
}

PROXY_URL = os.environ.get("PROXY_URL", "http://127.0.0.1:8790")
PROXY_LOG = os.environ.get("PROXY_LOG", "/tmp/agentic_proxy.log")
LOCAL_OLLAMA_MODEL = os.environ.get("PROXY_OLLAMA_MODEL", "qwen2.5-coder:14b")
MAX_FRONTIER_USD = float(os.environ.get("MAX_FRONTIER_USD", "6.0"))


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


def run_harness(task, arm, arm_cfg, checkout):
    """Run claude -p in the checkout; return (events, stdout_raw, wall_s, env_desc)."""
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
    try:
        proc = subprocess.run(args, cwd=checkout, env=env, capture_output=True,
                              text=True, timeout=arm_cfg["timeout"], stdin=subprocess.DEVNULL)
        raw = proc.stdout
        timed_out = False
    except subprocess.TimeoutExpired as e:
        raw = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
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
    return events, timed_out, wall, proxy_lines


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


# --------------------------------------------------------------------------
def run_one(task, arm, force=False):
    arm_cfg = ARMS[arm]
    chash = config_hash(arm_cfg, arm)
    rp = result_path(task["id"], arm, chash)
    if os.path.exists(rp) and not force:
        return json.load(open(rp)), True  # cached

    checkout = fresh_checkout(task)
    try:
        events, timed_out, wall, proxy_lines = run_harness(task, arm, arm_cfg, checkout)
        metrics = mine_metrics(events, proxy_lines)
        patch = git_diff(checkout)
        empty_patch = patch.strip() == ""

        # Execution oracle: score the (possibly-edited) checkout.
        score = score_checkout(checkout, task, timeout=240)

        hit_cap = (metrics["result_subtype"] == "error_max_turns") or \
                  (metrics["num_turns"] and metrics["num_turns"] >= arm_cfg["max_turns"])
        cost_units = metrics["total_tokens"] * arm_cfg["price"]

        result = {
            "task_id": task["id"],
            "tier": task["tier"],
            "arm": arm,
            "model": arm_cfg["model"],
            "ollama_model": LOCAL_OLLAMA_MODEL if arm == "local" else None,
            "config_hash": chash,
            "resolved": score["resolved"],
            "fail_to_pass_ok": score["fail_to_pass_ok"],
            "pass_to_pass_ok": score["pass_to_pass_ok"],
            "wall_clock_s": round(wall, 1),
            "timed_out": timed_out,
            "hit_turn_cap": bool(hit_cap),
            "empty_patch": empty_patch,
            "patch_len": len(patch),
            "cost_units": cost_units,
            **metrics,
            "per_node": score["per_node"],
        }
        # persist patch + raw events alongside
        with open(rp, "w") as f:
            json.dump(result, f, indent=2)
        with open(rp.replace(".json", ".patch"), "w") as f:
            f.write(patch)
        return result, False
    finally:
        shutil.rmtree(checkout, ignore_errors=True)


# --------------------------------------------------------------------------
def proxy_healthy():
    import urllib.request
    try:
        with urllib.request.urlopen(PROXY_URL + "/health", timeout=3) as r:
            return json.loads(r.read()).get("status") == "ok"
    except Exception:
        return False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--arms", default="frontier,local",
                    help="comma list: frontier,local")
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
        arms = ["frontier", "local"]

    tasks = load_tasks(subset)
    if not tasks:
        print("no tasks matched", file=sys.stderr)
        return 1

    if "local" in arms and not proxy_healthy():
        print(f"[runner] WARNING: local proxy not healthy at {PROXY_URL}; "
              f"start it with `make agentic-proxy` first. Skipping local arm.",
              file=sys.stderr)
        arms = [a for a in arms if a != "local"]

    print(f"[runner] {len(tasks)} tasks x arms={arms}", file=sys.stderr)
    spent_usd = 0.0
    for task in tasks:
        for arm in arms:
            if arm == "frontier" and spent_usd >= MAX_FRONTIER_USD:
                print(f"[runner] SPEND CAP ${MAX_FRONTIER_USD} reached; "
                      f"skipping frontier {task['id']}", file=sys.stderr)
                continue
            t0 = time.time()
            res, cached = run_one(task, arm, force=args.force)
            tag = "cache" if cached else "ran  "
            if arm == "frontier" and not cached and res.get("reported_cost_usd"):
                spent_usd += res["reported_cost_usd"]
            fid = ""
            if arm == "local":
                fid = (f" native={res.get('native_tool_calls',0)} "
                       f"rescued={res.get('rescued_tool_calls',0)}")
            print(f"[{tag}] {task['id']:24s} {arm:8s} "
                  f"resolved={str(res['resolved']):5s} "
                  f"turns={res.get('num_turns')} "
                  f"toolcalls={res.get('tool_calls_attempted')}{fid} "
                  f"wall={res.get('wall_clock_s')}s "
                  f"toks={res.get('total_tokens')} "
                  f"{'(%.1fs)'%(time.time()-t0)}", file=sys.stderr)
    print(f"[runner] done. frontier spend ~${spent_usd:.2f}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
