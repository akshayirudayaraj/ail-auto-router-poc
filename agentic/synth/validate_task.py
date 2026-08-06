#!/usr/bin/env python3
"""Validation gate for a generated (Source-3) task dir — the repo's real
deliverable for DATA_PLAN Phase 2B.

A Claude-Max session AUTHORS a task dir (issue + repo + oracle); this gate is what
decides whether it is admissible. For a submitted tasks/<id>/ it asserts:

  (a) fail-before / pass-after — the buggy base repo/ FAILS its FAIL_TO_PASS and
      keeps PASS_TO_PASS; applying the reference fix (_reference/ or
      _oracle/gold_patch.diff) makes ALL of F2P + P2P pass. This proves the oracle
      is real and executable (reuses the Docker pytest executor — this is
      VALIDATION, not generation grading).
  (b) the firewall (firewall_gate.check): hidden F2P tests absent from repo/,
      _oracle/ quarantined, gold fix not pre-applied, and the ISSUE text does not
      embed the fix.
  (c) task.json schema completeness (provenance / grounding / grader set).

A task is admissible only if all three pass. Grading of the RUN outputs remains
the deferred offline engine's job — this gate only certifies the task itself.

Run: python3 agentic/synth/validate_task.py <task_id> [--all]
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
RUNNER = HERE.parent / "runner"
TASKS_DIR = HERE.parent / "tasks"
sys.path.insert(0, str(RUNNER))
from executor import score_checkout  # noqa: E402
import firewall_gate  # noqa: E402

REQUIRED_KEYS = ["id", "tier", "issue", "test_cmd", "fail_to_pass", "pass_to_pass",
                 "provenance", "grounding", "has_executable_oracle"]
VALID_PROVENANCE = {"templated", "swe_verified", "synthetic"}
VALID_GROUNDING = {"benchmark", "oss_history", "synthetic_repo"}


def _copy_repo(task_id: str) -> str:
    src = TASKS_DIR / task_id / "repo"
    dst = tempfile.mkdtemp(prefix=f"val_{task_id}_")
    shutil.copytree(src, dst, dirs_exist_ok=True)
    return dst


def _apply_reference(task_id: str, checkout: str) -> str:
    """Apply the reference fix into a checkout. Prefer a _reference/ file tree;
    fall back to _oracle/gold_patch.diff via `patch -p1`. Returns the method."""
    tdir = TASKS_DIR / task_id
    ref = tdir / "_reference"
    if ref.is_dir():
        for p in ref.rglob("*"):
            if p.is_file():
                dst = Path(checkout) / p.relative_to(ref)
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy(p, dst)
        return "_reference"
    gold = tdir / "_oracle" / "gold_patch.diff"
    if gold.is_file():
        r = subprocess.run(["patch", "-p1", "-i", str(gold)], cwd=checkout,
                           capture_output=True, text=True)
        if r.returncode != 0:
            raise SystemExit(f"gold_patch failed to apply: {r.stdout}\n{r.stderr}")
        return "_oracle/gold_patch.diff"
    raise SystemExit("no reference fix: need _reference/ or _oracle/gold_patch.diff")


def check_schema(task: dict) -> list[str]:
    problems = []
    for k in REQUIRED_KEYS:
        if k not in task or task[k] in (None, "", []):
            problems.append(f"missing/empty '{k}'")
    if task.get("provenance") not in VALID_PROVENANCE:
        problems.append(f"provenance {task.get('provenance')!r} not in {VALID_PROVENANCE}")
    if task.get("grounding") not in VALID_GROUNDING:
        problems.append(f"grounding {task.get('grounding')!r} not in {VALID_GROUNDING}")
    return problems


def validate(task_id: str) -> bool:
    tdir = TASKS_DIR / task_id
    if not (tdir / "task.json").exists():
        print(f"REJECT {task_id}: no task.json")
        return False
    task = json.loads((tdir / "task.json").read_text())

    # (c) schema
    problems = check_schema(task)
    if problems:
        print(f"REJECT {task_id}: schema — {'; '.join(problems)}")
        return False

    # (b) firewall (raises SystemExit on breach — incl. ISSUE-text leak)
    try:
        firewall_gate.check(task_id)
    except SystemExit as e:
        print(f"REJECT {task_id}: firewall — {e}")
        return False

    # (a) fail-before / pass-after
    buggy = _copy_repo(task_id)
    try:
        b = score_checkout(buggy, task)
    finally:
        shutil.rmtree(buggy, ignore_errors=True)
    if b["fail_to_pass_ok"]:
        print(f"REJECT {task_id}: FAIL_TO_PASS already passes on the buggy base "
              f"(oracle not discriminative)")
        return False
    if not b["pass_to_pass_ok"]:
        print(f"REJECT {task_id}: PASS_TO_PASS fails on the buggy base "
              f"(repo broken): {json.dumps(b['per_node'])}")
        return False

    ref = _copy_repo(task_id)
    try:
        method = _apply_reference(task_id, ref)
        r = score_checkout(ref, task)
    finally:
        shutil.rmtree(ref, ignore_errors=True)
    if not r["resolved"]:
        print(f"REJECT {task_id}: reference fix ({method}) does not resolve "
              f"F2P+P2P: {json.dumps(r['per_node'])}")
        return False

    print(f"ACCEPT {task_id}: schema OK; firewall OK; buggy fails F2P & keeps P2P; "
          f"reference resolves all (via {method}) [{task['provenance']}/{task['grounding']}]")
    return True


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("task_id", nargs="?", default=None)
    ap.add_argument("--all", action="store_true",
                    help="validate every synthetic (provenance=synthetic) task")
    args = ap.parse_args()

    if args.all:
        ids = []
        for tj in sorted(TASKS_DIR.glob("*/task.json")):
            t = json.loads(tj.read_text())
            if t.get("provenance") == "synthetic":
                ids.append(t["id"])
        if not ids:
            print("no synthetic tasks to validate")
            return 0
        ok = all(validate(i) for i in ids)
        return 0 if ok else 1

    if not args.task_id:
        print("usage: validate_task.py <task_id> | --all", file=sys.stderr)
        return 2
    return 0 if validate(args.task_id) else 1


if __name__ == "__main__":
    sys.exit(main())
