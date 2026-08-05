"""Emit ONE real dual-arm execution-grounded gold row into the executable gold set.

Reads the frontier + local arm results (from run_swe_arm.py) for a SWE-bench
Verified task and appends a canonical GoldRow (Executable=true) to
data_agentic/gold.jsonl, ALONGSIDE the existing curated rows (append-if-absent,
never overwrite), then updates gold_meta.json to reflect the real provenance.

Why hand-emit rather than the Go assembler (cmd/agentic): that assembler consumes
run_agentic.py's result format and grades with the curated python:3.11-slim Docker
executor. A real SWE-bench Verified instance is driven by the SAME runner but
graded by the official swebench harness (run_swe_arm.py) — a separate result
format and grading path (see DECISIONS: two grading paths). For the single Step-1
row, emitting directly keeps that separation clean. Batch SWE assembly is Step 2.
"""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent          # agentic/
RESULTS = ROOT / "results"
GOLD = ROOT.parent / "data_agentic" / "gold.jsonl"
META = ROOT.parent / "data_agentic" / "gold_meta.json"

_HARD = re.compile(r"\b(efficient|optimal|O\(|complexity|concurren|thread|async|"
                   r"race|deadlock|edge case|unicode|encoding|overflow|precision)\b", re.I)
_IMPER = re.compile(r"\b(fix|add|implement|make|ensure|return|handle|remove|update|"
                    r"support|allow|prevent)\b", re.I)


def features(text: str) -> dict:
    lines = text.splitlines()
    words = text.split()
    digits = sum(c.isdigit() for c in text)
    return {
        "prompt_len": len(text),
        "prompt_tokens_approx": max(1, len(text) // 4),
        "attached_context_tokens": 0,
        "tool_count": 0,
        "turn_type": "open",
        "code_fence_count": text.count("```") // 2,
        "question_count": text.count("?"),
        "imperative_verb_count": len(_IMPER.findall(text)),
        "hard_keyword_score": len(_HARD.findall(text)),
        "line_count": len(lines),
        "digit_ratio": round(digits / max(1, len(text)), 4),
    }


def load_arm(task_id: str, arm: str) -> dict:
    p = RESULTS / f"swe_{task_id}__{arm}.json"
    return json.loads(p.read_text())


def main(task_id: str = "swe-psf__requests-1142"):
    fr = load_arm(task_id, "frontier")
    lo = load_arm(task_id, "local")
    task = json.loads((ROOT / "tasks" / task_id / "task.json").read_text())
    issue = task["issue"]

    def cost(arm):  # billable-token proxy, mirroring the existing gold convention
        m = arm["metrics"]
        return int((m.get("input_tokens") or 0) + (m.get("output_tokens") or 0))

    row = {
        "prompt_id": task_id,
        "prompt_text": issue,
        "features": features(issue),
        "outcome_local": int(bool(lo["outcome"])),
        "outcome_frontier": int(bool(fr["outcome"])),
        "local_model": "gpt-oss:20b (via Anthropic->Ollama proxy; 100% native tool-call fidelity)",
        "frontier_model": "claude-sonnet (CLI alias; via subscription)",
        "cost_local": cost(lo),
        "cost_frontier": cost(fr),
        "executable": True,
        # --- provenance (extra keys; Go's json.Unmarshal ignores unknowns) ---
        "source": "swe-bench-verified",
        "instance_id": task["instance_id"],
        "grader": "swebench-harness",
        "local_native_tool_calls": lo["metrics"].get("native_tool_calls"),
        "local_rescued_tool_calls": lo["metrics"].get("rescued_tool_calls"),
    }

    GOLD.parent.mkdir(exist_ok=True)
    existing = []
    if GOLD.exists():
        existing = [json.loads(l) for l in GOLD.read_text().splitlines() if l.strip()]
    if any(r.get("prompt_id") == task_id for r in existing):
        print(f"row {task_id} already present ({len(existing)} rows) — not duplicating")
    else:
        with GOLD.open("a") as f:
            f.write(json.dumps(row) + "\n")
        existing.append(row)
        print(f"appended real SWE-Verified row -> {GOLD} (now {len(existing)} rows)")

    # update meta to reflect the mixed set + real provenance
    meta = json.loads(META.read_text()) if META.exists() else {}
    meta["n"] = len(existing)
    meta["executable"] = True
    meta["synthetic"] = False
    meta["real_swe_verified_instances"] = sorted(
        {r.get("instance_id") for r in existing if r.get("source") == "swe-bench-verified"})
    meta["note"] = ("mixed executable gold: curated SWE-shaped rows (qwen2.5-coder) "
                    "+ real SWE-bench Verified rows (gpt-oss:20b, official swebench harness)")
    META.write_text(json.dumps(meta, indent=2))
    print(f"updated {META}: n={meta['n']} real={meta['real_swe_verified_instances']}")
    print("\ngold row:")
    print(json.dumps({k: row[k] for k in ("prompt_id", "outcome_local", "outcome_frontier",
                                          "cost_local", "cost_frontier", "executable",
                                          "source", "local_native_tool_calls")}, indent=2))


if __name__ == "__main__":
    main()
