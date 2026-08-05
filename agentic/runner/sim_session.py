#!/usr/bin/env python3
"""Simulated user: multi-turn agentic sessions with in-session signals
(DATA_PLAN Phase 3).

Today the runner is single-shot `claude -p`. This drives a CONTINUED session
across turns (via `--resume <session_id>`) so context carries, and inserts a
SCRIPTED, SEEDED, DETERMINISTIC simulated user between turns. The sim-user reacts
to IN-SESSION CUES ONLY — the agent's own visible test runs (Bash tool_results)
and its done/stuck claims — NOT to an offline oracle grade. This keeps the "no
grading during generation" invariant AND stays realistic (a real user reacts to
what they SEE, not to a hidden pass/fail).

Cue -> next user turn:
  * agent's visible tests FAILED or it signalled stuck ->
        paste_error (paste the failing output),  a negative correction, OR
        (with escalation probability) a real local->frontier SWITCH — the next
        turn is served by the frontier arm, producing a GENUINE escalation pair
        (not extract.go's nearest-neighbour approximation).
  * agent signalled success -> a follow-up subtask (moveon) or end.

Output: a RawTurn `.session.jsonl` (served_model per assistant turn, propensity
null, optional cheap `_true_*` seams). NO outcome labels. `internal/extract`
ingests it unchanged.

Run: python3 agentic/runner/sim_session.py --task <id> [--start-arm local]
     [--max-user-turns 4] [--escalate-prob 0.5] [--seed 0]
"""

from __future__ import annotations

import argparse
import json
import os
import random
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import run_agentic as R  # noqa: E402

RESULTS_DIR = Path(R.RESULTS_DIR)


# --------------------------------------------------------------------------
# One turn of `claude -p`, optionally resuming a session, on a chosen arm.
# --------------------------------------------------------------------------
def run_turn(message, arm, checkout, session_id=None):
    arm_cfg = R.ARMS[arm]
    args = [
        "claude", "-p", message,
        "--output-format", "stream-json", "--verbose",
        "--max-turns", str(arm_cfg["max_turns"]),
        "--allowedTools", "Read", "Edit", "Write", "Bash",
        "--permission-mode", "bypassPermissions",
        "--strict-mcp-config",
        "--model", arm_cfg["model"],
    ]
    if arm_cfg["bare"]:
        args.append("--bare")
    if session_id:
        args += ["--resume", session_id]
    env = dict(os.environ)
    if arm_cfg["use_proxy"]:
        env["ANTHROPIC_BASE_URL"] = R.PROXY_URL
        env["ANTHROPIC_API_KEY"] = "dummy-local-key"
    t0 = time.time()
    try:
        proc = subprocess.run(args, cwd=checkout, env=env, capture_output=True,
                              text=True, timeout=arm_cfg["timeout"],
                              stdin=subprocess.DEVNULL)
        raw = proc.stdout
        timed_out = False
    except subprocess.TimeoutExpired as e:
        raw = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
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
    return events, timed_out, wall


def session_id_of(events):
    for ev in events:
        if ev.get("session_id"):
            return ev["session_id"]
    return None


# --------------------------------------------------------------------------
# In-session cue reading (transcript-only — never an oracle grade).
# --------------------------------------------------------------------------
_FAIL_MARKERS = ("failed", "error", "assertionerror", "traceback",
                 "0 passed", "no tests ran", "exit code 1")
_STUCK_MARKERS = ("i'm stuck", "cannot ", "unable to", "not sure how",
                  "need more", "could you", "don't have")
_DONE_MARKERS = ("all tests pass", "tests pass", "passing", "fixed", "done",
                 "complete", "resolved", "should be fixed")


def read_cue(events):
    """Return ('fail'|'stuck'|'done'|'unknown', failing_output_snippet)."""
    fail_snip = ""
    saw_test_fail = False
    for ev in events:
        if ev.get("type") == "user":
            for b in ev.get("message", {}).get("content", []):
                if b.get("type") == "tool_result":
                    txt = b.get("content")
                    if isinstance(txt, list):
                        txt = " ".join(c.get("text", "") for c in txt
                                       if isinstance(c, dict))
                    txt = str(txt or "")
                    low = txt.lower()
                    if b.get("is_error") or any(m in low for m in _FAIL_MARKERS):
                        saw_test_fail = True
                        if txt.strip():
                            fail_snip = txt.strip()[-600:]
    final = R.assistant_text(events).lower()
    if saw_test_fail:
        return "fail", fail_snip
    if any(m in final for m in _STUCK_MARKERS):
        return "stuck", fail_snip
    if any(m in final for m in _DONE_MARKERS):
        return "done", fail_snip
    return "unknown", fail_snip


def sim_user_next(cue, fail_snip, rng, escalate_prob):
    """Deterministic (seeded) next user turn from the cue. Returns
    (action, message, escalate) — escalate=True means serve the NEXT turn on the
    frontier arm (a real local->frontier switch)."""
    if cue in ("fail", "stuck"):
        if rng.random() < escalate_prob:
            msg = ("That still isn't working. Let me bring in a stronger model to "
                   "take over.\n\n"
                   + (f"The failing output was:\n{fail_snip}\n\n" if fail_snip else "")
                   + "Please diagnose and fix it.")
            return "switch", msg, True
        if cue == "fail" and fail_snip:
            return "paste_error", (f"The tests are still failing:\n\n{fail_snip}\n\n"
                                   "Please look at this and fix it."), False
        return "correction", ("That doesn't look right yet — the required behavior "
                              "still isn't met. Please re-read the issue and try a "
                              "different approach."), False
    if cue == "done":
        return "moveon", ("Great. Now also make sure the fix handles the empty / "
                          "edge-case input, and run the tests once more to confirm."), False
    return "moveon", ("Please run the test suite now and make sure everything passes; "
                      "fix anything that doesn't."), False


# --------------------------------------------------------------------------
def simulate(task, start_arm, max_user_turns, escalate_prob, seed):
    rng = random.Random(seed)
    checkout = R.fresh_checkout(task)
    raw_turns = []
    ti = 0
    ts0 = int(time.time())
    session_id = None
    label = f"{task['id']}__sim__{start_arm}"

    def add_user(text):
        nonlocal ti
        raw_turns.append({"session_id": label, "turn_index": ti, "timestamp": ts0,
                          "role": "user", "content": text})
        ti += 1

    def add_assistant(text, served):
        nonlocal ti
        raw_turns.append({"session_id": label, "turn_index": ti, "timestamp": ts0,
                          "role": "assistant", "content": text, "served_model": served})
        ti += 1

    try:
        arm = start_arm
        prompt = R.build_prompt(task)
        add_user(prompt)
        escalated_once = False
        for step in range(max_user_turns + 1):
            events, timed_out, wall = run_turn(
                prompt, arm, checkout, session_id=session_id)
            if session_id is None:
                session_id = session_id_of(events)
            add_assistant(R.assistant_text(events) or "[no text]",
                          R.ARMS[arm]["served_model"])
            cue, fail_snip = read_cue(events)
            print(f"[sim] turn {step} arm={arm} cue={cue} wall={wall:.0f}s "
                  f"session={session_id}", file=sys.stderr)
            if step >= max_user_turns:
                break
            action, msg, escalate = sim_user_next(cue, fail_snip, rng, escalate_prob)
            add_user(msg)
            if escalate and arm == "local":
                arm = "frontier"          # the real local->frontier switch
                escalated_once = True
                session_id = None         # fresh frontier turn (replay handoff)
            prompt = msg
        # Guarantee >=1 real escalation for the acceptance if none occurred and we
        # started local: do a final frontier hand-off turn on the same context.
        if start_arm == "local" and not escalated_once:
            hand = ("Let me bring in a stronger model. Please review the current "
                    "state of the fix and make sure all tests pass.")
            add_user(hand)
            events, timed_out, wall = run_turn(hand, "frontier", checkout,
                                               session_id=None)
            add_assistant(R.assistant_text(events) or "[no text]",
                          R.ARMS["frontier"]["served_model"])
            print(f"[sim] forced escalation turn arm=frontier wall={wall:.0f}s",
                  file=sys.stderr)
    finally:
        import shutil
        shutil.rmtree(checkout, ignore_errors=True)

    out = RESULTS_DIR / f"{label}.session.jsonl"
    with open(out, "w") as f:
        for t in raw_turns:
            f.write(json.dumps(t) + "\n")
    served = [t.get("served_model") for t in raw_turns if t["role"] == "assistant"]
    escalations = sum(1 for a, b in zip(served, served[1:])
                      if a and b and a != b and "gpt-oss" in a)
    print(f"[sim] wrote {out} — {len(raw_turns)} turns, served={served}, "
          f"real escalations={escalations}")
    return out, escalations


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--task", required=True)
    ap.add_argument("--start-arm", default="local", choices=["local", "frontier"])
    ap.add_argument("--max-user-turns", type=int, default=3)
    ap.add_argument("--escalate-prob", type=float, default=0.6)
    ap.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()
    tasks = R.load_tasks({args.task})
    if not tasks:
        print(f"task {args.task} not found", file=sys.stderr)
        return 1
    os.makedirs(RESULTS_DIR, exist_ok=True)
    simulate(tasks[0], args.start_arm, args.max_user_turns, args.escalate_prob,
             args.seed)
    return 0


if __name__ == "__main__":
    sys.exit(main())
