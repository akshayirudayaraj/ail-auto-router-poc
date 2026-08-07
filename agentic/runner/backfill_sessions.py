#!/usr/bin/env python3
"""Backfill: regenerate every single-shot session's portable `.session.jsonl`
from its complete `.events.jsonl` — no agent re-run.

The old reconstruct_raw_turns collapsed each session to 2 RawTurns (prompt + one
concatenated blob), destroying the ordered turn view internal/extract mines. The
rich `.events.jsonl` was always complete, so we can cheaply rebuild the correct
`.session.jsonl` (1 user turn + one assistant turn per assistant event, with tool
summaries + served_model) for all already-captured sessions.

Only applies to run_agentic single-shot outputs (which have a `.events.jsonl`).
sim_session.py writes its own multi-turn `.session.jsonl` directly (no events
file) and is not touched here.

Usage: python agentic/runner/backfill_sessions.py [--results-dir DIR] [--dry-run]
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import run_agentic as R  # noqa: E402


def _prompt_for(base: str, rec: dict) -> str:
    """The genuine initial user prompt: prefer the existing session's turn-0 (it
    already held the full prompt even in the broken 2-line form); else rebuild it
    from the task."""
    sp = base + ".session.jsonl"
    if os.path.exists(sp):
        try:
            with open(sp) as f:
                first = json.loads(f.readline() or "{}")
            if first.get("role") == "user" and first.get("content"):
                return first["content"], int(first.get("timestamp", 0))
        except Exception:
            pass
    tid = rec.get("task_id")
    if tid:
        tasks = R.load_tasks({tid})
        if tasks:
            return R.build_prompt(tasks[0]), 0
    return "", 0


def backfill(results_dir: str, dry_run: bool) -> int:
    n_ok = n_skip = 0
    for ev_path in sorted(glob.glob(os.path.join(results_dir, "**", "*.events.jsonl"), recursive=True)):
        base = ev_path[: -len(".events.jsonl")]
        rec_path = base + ".json"
        if not os.path.exists(rec_path):
            n_skip += 1
            continue
        rec = json.load(open(rec_path))
        served = rec.get("served_model")
        session_id = rec.get("session_id") or os.path.basename(base)
        events = [json.loads(l) for l in open(ev_path) if l.strip()]
        prompt, ts = _prompt_for(base, rec)
        turns = R.reconstruct_raw_turns(events, session_id, served, prompt, ts)
        out = base + ".session.jsonl"
        n_assist = sum(1 for t in turns if t["role"] == "assistant")
        print(f"[backfill] {os.path.basename(out):58s} -> {len(turns):3d} turns "
              f"(1 user + {n_assist} assistant)")
        if not dry_run:
            with open(out, "w") as f:
                for t in turns:
                    f.write(json.dumps(t) + "\n")
        n_ok += 1
    print(f"[backfill] {'would rewrite' if dry_run else 'rewrote'} {n_ok} sessions"
          f" ({n_skip} skipped: no run record)")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--results-dir", default=str(HERE.parent / "results"))
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    return backfill(args.results_dir, args.dry_run)


if __name__ == "__main__":
    sys.exit(main())
