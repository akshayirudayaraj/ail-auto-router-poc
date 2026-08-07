#!/usr/bin/env python3
"""
Self-contained swebench grading for the offline executed-oracle branch.

Lifted out of the (now-retired) run_swe_arm.py so the offline engine
(grade_offline.py) no longer depends on that superseded driver — letting PR#1
delete run_swe_arm.py cleanly. This is the `grader=="swebench"` path: hand the
agent's produced diff to the OFFICIAL swebench harness, which applies the hidden
test_patch itself inside the per-instance image and reports resolved/tests_status.

Requires a python with swebench + datasets installed (SWEBENCH_PY) and the
per-instance images built (agentic/runner/build_swe_images.py). Absent that env,
grade_offline.py skips swebench sessions (never emits a label) — see its preflight.
"""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

HERE = Path(__file__).resolve().parent
RESULTS_DIR = HERE.parent / "results"
DATASET = os.environ.get("SWEBENCH_DATASET", "princeton-nlp/SWE-bench_Verified")
SWEBENCH_PY = os.environ.get(
    "SWEBENCH_PY",
    str(Path.home() / "dev/ail-routing-test-swe/.venv-swe/bin/python"))


def available() -> bool:
    """True iff the swebench harness python exists."""
    return Path(SWEBENCH_PY).exists()


def grade(instance_id: str, arm: str, diff: str, results_dir: Path | None = None) -> dict:
    """Grade one SWE-bench instance's produced diff via the official harness.

    Returns {resolved, graded, patch_applied?, tests_status?, note?}. graded=True
    means the harness actually ran (incl. empty/non-applying patch -> resolved=False);
    graded=False means we could not grade (caller should skip, not label).
    """
    rd = Path(results_dir) if results_dir else RESULTS_DIR
    rd.mkdir(parents=True, exist_ok=True)
    run_id = f"offline_{arm}"
    preds = rd / f"pred_{run_id}.jsonl"
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
    subprocess.run(cmd, cwd=str(rd), check=False)
    rep = rd / "logs" / "run_evaluation" / run_id / arm / instance_id / "report.json"
    if not rep.exists():
        return {"resolved": False, "graded": False, "note": "no report.json (empty/failed patch)"}
    data = json.loads(rep.read_text())[instance_id]
    return {
        "resolved": bool(data.get("resolved")),
        "graded": True,
        "patch_applied": data.get("patch_successfully_applied"),
        "tests_status": data.get("tests_status"),
    }
