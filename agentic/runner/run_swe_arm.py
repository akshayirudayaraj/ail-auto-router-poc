"""Step-1 single-arm SWE driver — LOG-FIRST, NO GRADING on the generation path.

Runs ONE arm of the real Claude Code loop on a real SWE-bench Verified task dir
and captures the diff + metrics. It does NOT grade during generation (DATA_PLAN
invariant "no grading during generation"): the `grade()` swebench wrapper below is
PARKED for the deferred offline engine to reuse — it is never called on the
generation path.

NOTE: the canonical generation engine is now `run_agentic.py`, the unified
dual-arm runner, which loads ANY tasks/<id>/task.json (including materialized
SWE tasks) and emits the full log-first artifact set (RawTurn session.jsonl +
events.jsonl + patch + record). This driver survives as a single-arm alternate;
prefer `run_agentic.py` / `make agentic-swe`.

Driving logic mirrors run_agentic.py (same claude -p flags, same fresh-checkout /
git-diff capture, same fidelity mining).

Arms:
  frontier -> claude subscription, model opus (FRONTIER_MODEL), no proxy.
  local    -> ANTHROPIC_BASE_URL=<tool-capable proxy>, model routed to Ollama,
              --bare (tractability). Requires the proxy up (proxyctl.sh).

Offline grading (parked): write a predictions jsonl {instance_id,
model_name_or_path, model_patch} and invoke swebench.harness.run_evaluation
(reused, proven install in ail-self-routing/.venv_swe). The harness applies
test_patch itself, so the ground-truth firewall holds by construction. resolved
== all F2P pass & all P2P still pass.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent                      # agentic/
TASKS_DIR = ROOT / "tasks"
RESULTS_DIR = ROOT / "results"
CHECKOUTS_DIR = ROOT / "checkouts"
PROXY_URL = os.environ.get("PROXY_URL", "http://127.0.0.1:8790")
PROXY_LOG = os.environ.get("PROXY_LOG", "/tmp/agentic_proxy.log")
# The proven swebench harness venv (ail-self-routing). External dep, Job-B only.
SWEBENCH_PY = os.environ.get(
    "SWEBENCH_PY",
    str(Path.home() / "development/spectro/ail-self-routing/.venv_swe/bin/python"))
DATASET = "princeton-nlp/SWE-bench_Verified"

PRICE_LOCAL, PRICE_FRONTIER = 1.0, 15.0
ARMS = {
    "frontier": {"model": os.environ.get("FRONTIER_MODEL", "opus"), "bare": False,
                 "max_turns": 40, "timeout": 900, "price": PRICE_FRONTIER, "proxy": False},
    "local": {"model": "claude-sonnet-4", "bare": True,
              "max_turns": 20, "timeout": 1800, "price": PRICE_LOCAL, "proxy": True},
}


def fresh_checkout(task_dir: Path, task_id: str) -> Path:
    dst = CHECKOUTS_DIR / f"{task_id}__{int(time.time()*1000)}"
    if dst.exists():
        shutil.rmtree(dst)
    CHECKOUTS_DIR.mkdir(exist_ok=True)
    shutil.copytree(task_dir / "repo", dst)
    env = dict(os.environ, GIT_AUTHOR_NAME="base", GIT_AUTHOR_EMAIL="b@x",
               GIT_COMMITTER_NAME="base", GIT_COMMITTER_EMAIL="b@x")
    subprocess.run(["git", "init", "-q"], cwd=dst, check=True)
    subprocess.run(["git", "add", "-A"], cwd=dst, check=True)
    subprocess.run(["git", "commit", "-qm", "base"], cwd=dst, check=True, env=env)
    return dst


def build_prompt(task: dict) -> str:
    return (
        "You are fixing a bug in the Python repository in the current working "
        "directory. Read the relevant source files, make minimal source edits to "
        "fix the described issue, and do NOT edit any test files. Use the shell to "
        "run the existing test suite to check you have not caused regressions "
        "(the final grade uses additional held-out tests you cannot see).\n\n"
        f"ISSUE:\n{task['issue']}\n\n"
        f"You can run the tests with: {task.get('test_cmd', 'python -m pytest -q')}\n"
        "When your fix is complete and the existing tests still pass, stop."
    )


def run_harness(task: dict, arm: str, checkout: Path):
    cfg = ARMS[arm]
    args = [
        "claude", "-p", build_prompt(task),
        "--output-format", "stream-json", "--verbose",
        "--max-turns", str(cfg["max_turns"]),
        "--allowedTools", "Read", "Edit", "Write", "Bash",
        "--permission-mode", "bypassPermissions",
        "--strict-mcp-config",
        "--model", cfg["model"],
    ]
    if cfg["bare"]:
        args.append("--bare")
    # Inherit the interactive env: USER/LOGNAME/HOME/PATH are REQUIRED for the
    # frontier arm's Keychain credential; without them claude -p fails with a
    # misleading "Credit balance is too low".
    env = dict(os.environ)
    if cfg["proxy"]:
        env["ANTHROPIC_BASE_URL"] = PROXY_URL
        env["ANTHROPIC_API_KEY"] = "dummy-local-key"
    proxy_offset = _fsize(PROXY_LOG) if cfg["proxy"] else 0
    t0 = time.time()
    timed_out = False
    try:
        proc = subprocess.run(args, cwd=checkout, env=env, capture_output=True,
                              text=True, timeout=cfg["timeout"], stdin=subprocess.DEVNULL)
        raw = proc.stdout
    except subprocess.TimeoutExpired as e:
        raw = (e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or ""))
        timed_out = True
    wall = time.time() - t0
    events = []
    for line in raw.splitlines():
        line = line.strip()
        if line:
            try:
                events.append(json.loads(line))
            except Exception:
                pass
    proxy_lines = _read_proxy_since(PROXY_LOG, proxy_offset) if cfg["proxy"] else []
    return events, timed_out, wall, proxy_lines


def _fsize(p):
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


def mine_metrics(events, proxy_lines):
    tool_calls = tool_errors = assistant_turns = 0
    in_tok = out_tok = 0
    cost_usd = None
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
            u = ev.get("usage", {}) or {}
            in_tok = u.get("input_tokens", 0) or 0
            out_tok = u.get("output_tokens", 0) or 0
            cost_usd = ev.get("total_cost_usd")
    # tool-call fidelity: native Ollama tool_calls vs prose-JSON rescued (local only).
    # The proxy logs this under a "fidelity" sub-dict, e.g. {"native": N, "rescued": M}.
    native = rescued = 0
    for d in proxy_lines:
        f = d.get("fidelity") or {}
        native += f.get("native", 0) or 0
        rescued += f.get("rescued", 0) or 0
    return {
        "assistant_turns": assistant_turns, "tool_calls": tool_calls,
        "tool_errors": tool_errors, "input_tokens": in_tok, "output_tokens": out_tok,
        "cost_usd": cost_usd, "native_tool_calls": native, "rescued_tool_calls": rescued,
    }


def git_diff(checkout: Path) -> str:
    p = subprocess.run(["git", "diff"], cwd=checkout, capture_output=True, text=True)
    return p.stdout


def grade(instance_id: str, arm: str, diff: str) -> dict:
    """OFFLINE-ENGINE ONLY — the parked swebench grader. NOT called on the
    generation path (DATA_PLAN: no grading during generation). Kept here so the
    deferred offline executed-oracle branch can reuse this proven wrapper.

    Grade the produced diff with the official swebench harness."""
    RESULTS_DIR.mkdir(exist_ok=True)
    run_id = f"step1_{arm}"
    preds = RESULTS_DIR / f"pred_{run_id}.jsonl"
    preds.write_text(json.dumps({
        "instance_id": instance_id,
        "model_name_or_path": arm,
        "model_patch": diff,
    }) + "\n")
    cmd = [
        SWEBENCH_PY, "-m", "swebench.harness.run_evaluation",
        "--dataset_name", DATASET,
        "--predictions_path", str(preds),
        "--run_id", run_id,
        "--instance_ids", instance_id,
        "--cache_level", "env",
        "--timeout", "1800",
        "--max_workers", "1",
    ]
    subprocess.run(cmd, cwd=str(RESULTS_DIR), check=False)
    # harness writes logs/run_evaluation/<run_id>/<arm>/<iid>/report.json
    rep = (RESULTS_DIR / "logs" / "run_evaluation" / run_id / arm / instance_id / "report.json")
    if not rep.exists():
        return {"resolved": False, "graded": False, "note": "no report.json (empty/failed patch)"}
    data = json.loads(rep.read_text())[instance_id]
    return {"resolved": bool(data.get("resolved")), "graded": True,
            "patch_applied": data.get("patch_successfully_applied"),
            "tests_status": data.get("tests_status")}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--task", default="swe-psf__requests-1142")
    ap.add_argument("--arm", required=True, choices=["frontier", "local"])
    args = ap.parse_args()

    task_dir = TASKS_DIR / args.task
    task = json.loads((task_dir / "task.json").read_text())
    iid = task["instance_id"]
    print(f"[{args.arm}] task={args.task} instance={iid}", flush=True)

    checkout = fresh_checkout(task_dir, task["id"])
    events, timed_out, wall, proxy_lines = run_harness(task, args.arm, checkout)
    diff = git_diff(checkout)
    metrics = mine_metrics(events, proxy_lines)
    print(f"[{args.arm}] wall={wall:.0f}s turns={metrics['assistant_turns']} "
          f"tool_calls={metrics['tool_calls']} diff_bytes={len(diff)} "
          f"native/rescued={metrics['native_tool_calls']}/{metrics['rescued_tool_calls']} "
          f"timed_out={timed_out}", flush=True)

    # NO grading during generation. Preserve the diff + metrics; the base repo/
    # + _oracle/ (test_patch) let the offline swebench branch grade later.
    out = {
        "task_id": task["id"], "instance_id": iid, "arm": args.arm,
        "served_model": ARMS[args.arm]["model"], "model": ARMS[args.arm]["model"],
        "price_rung": ARMS[args.arm]["price"],
        "provenance": "swe_verified", "grounding": "benchmark",
        "has_executable_oracle": True, "grader": "swebench",
        "wall_s": round(wall, 1), "timed_out": timed_out,
        "diff": diff, "diff_bytes": len(diff), "empty_patch": not diff.strip(),
        "metrics": metrics,
    }
    res_path = RESULTS_DIR / f"swe_{task['id']}__{args.arm}.json"
    res_path.write_text(json.dumps(out, indent=2))
    print(f"[{args.arm}] wrote {res_path} (no grade — offline)", flush=True)


if __name__ == "__main__":
    main()
