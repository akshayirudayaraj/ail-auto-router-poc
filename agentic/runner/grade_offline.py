#!/usr/bin/env python3
"""
Executed-oracle branch of the offline label engine (OFFLINE_ENGINE_PLAN §3).

Generation assigns NO outcome. This script reads the pre-outcome session artifacts
under a results dir and, for every session whose task has an executable oracle,
grades the agent's produced patch by RUNNING THE HIDDEN TESTS — the only
non-circular label in the framework. It writes `executed` LabelRecords (matching
internal/label.LabelRecord's JSON shape) that the Go side consumes unchanged.

Two grading paths, kept separate (DECISIONS D16), selected by the task's `grader`:
  * docker_pytest — self-contained tasks: apply the agent diff to a fresh checkout
    of tasks/<id>/repo, run FAIL_TO_PASS + PASS_TO_PASS in the hermetic
    python:3.11-slim image (executor.py). Resolved iff all F2P pass AND all P2P
    still pass.
  * swebench — real SWE-bench Verified: hand the agent diff to the OFFICIAL swebench
    harness (swebench_grade.grade), which applies test_patch itself inside the
    per-instance image. Same env the agent ran in (execution=="container").

CORRECTNESS RULE: only emit a label when the oracle actually ran. If the grader
environment is missing (no Docker image / no swebench venv), SKIP the session and
emit nothing — never record outcome=0 for an ungraded session (that would poison
the calibration answer key). An empty or non-applying patch IS a real graded
failure (resolved=False), not a skip.

Non-portable (Docker/swebench) — the agentic/ orchestration boundary. Idempotent:
per-session results cache under labels/_executed_cache/; labels/executed.jsonl is
rebuilt from the cache each run, so re-runs don't duplicate.
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from executor import score_checkout, IMAGE as PYTEST_IMAGE  # noqa: E402
import swebench_grade  # noqa: E402  (self-contained; no dep on the retired run_swe_arm)

LABELER_VERSION = "executed-v1"


# --------------------------------------------------------------------------
# environment preflight — never grade with a missing oracle env
# --------------------------------------------------------------------------
def docker_image_exists(image: str) -> bool:
    try:
        r = subprocess.run(["docker", "image", "inspect", image],
                            capture_output=True, timeout=30)
        return r.returncode == 0
    except Exception:
        return False


def swebench_available() -> bool:
    return swebench_grade.available()


# --------------------------------------------------------------------------
# grading paths
# --------------------------------------------------------------------------
def _apply_patch(checkout: Path, diff: str) -> bool:
    """Apply a unified diff to a fresh git checkout. Returns True iff it applied."""
    patch_file = checkout / "_agent.patch"
    patch_file.write_text(diff)
    for cmd in (["git", "apply", "--whitespace=nowarn", str(patch_file)],
                ["patch", "-p1", "-i", str(patch_file)]):
        r = subprocess.run(cmd, cwd=checkout, capture_output=True, text=True)
        if r.returncode == 0:
            patch_file.unlink(missing_ok=True)
            return True
    patch_file.unlink(missing_ok=True)
    return False


def grade_docker_pytest(task: dict, task_dir: Path, diff: str, timeout: int) -> dict:
    """Apply the agent diff to tasks/<id>/repo and run F2P/P2P in Docker."""
    if not diff.strip():
        return {"graded": True, "resolved": False, "note": "empty patch — no change"}
    checkout = task_dir / f"_grade_{int(time.time()*1000)}"
    shutil.copytree(task_dir / "repo", checkout)
    try:
        env = dict(os.environ, GIT_AUTHOR_NAME="base", GIT_AUTHOR_EMAIL="b@x",
                   GIT_COMMITTER_NAME="base", GIT_COMMITTER_EMAIL="b@x")
        subprocess.run(["git", "init", "-q"], cwd=checkout, check=True)
        subprocess.run(["git", "add", "-A"], cwd=checkout, check=True)
        subprocess.run(["git", "commit", "-qm", "base"], cwd=checkout, check=True, env=env)
        if not _apply_patch(checkout, diff):
            return {"graded": True, "resolved": False, "note": "patch did not apply"}
        score = score_checkout(str(checkout), task, timeout=timeout)
        return {
            "graded": True,
            "resolved": bool(score["resolved"]),
            "fail_to_pass_ok": score["fail_to_pass_ok"],
            "pass_to_pass_ok": score["pass_to_pass_ok"],
            "per_node": score["per_node"],
        }
    finally:
        shutil.rmtree(checkout, ignore_errors=True)


def grade_swebench(instance_id: str, arm: str, diff: str) -> dict:
    """Grade a real SWE-bench instance via the official harness (swebench_grade)."""
    if not diff.strip():
        return {"graded": True, "resolved": False, "note": "empty patch — no change"}
    res = swebench_grade.grade(instance_id, arm, diff)  # {resolved, graded, tests_status,...}
    res.setdefault("graded", False)
    return res


# --------------------------------------------------------------------------
# corpus plumbing
# --------------------------------------------------------------------------
def load_split_by_task(results_dir: Path) -> dict:
    p = results_dir / "split_manifest.json"
    if not p.exists():
        return {}
    try:
        return json.loads(p.read_text()).get("task_split", {}) or {}
    except Exception:
        return {}


def iter_run_records(results_dir: Path):
    # Layout-agnostic: run records may be flat or under type subdirs (synthetic/,
    # semi-synthetic/, logs/). Exclude the labels/ and calibration/ .json files.
    paths = [p for p in glob.glob(str(results_dir / "**" / "*.json"), recursive=True)
             if os.sep + "labels" + os.sep not in p and os.sep + "calibration" + os.sep not in p]
    for p in sorted(paths):
        base = os.path.basename(p)
        if base in ("split_manifest.json", "gold_meta.json"):
            continue
        try:
            rr = json.loads(Path(p).read_text())
        except Exception:
            continue
        if rr.get("task_id"):
            yield os.path.splitext(base)[0], rr


def label_record(rr: dict, split: str, resolved: bool, evidence: dict, ts: int) -> dict:
    return {
        "session_id": rr.get("session_id") or rr["_key"],
        "task_id": rr["task_id"],
        "model": rr.get("served_model", ""),
        "arm": rr.get("arm", ""),
        "split": split,
        "provenance": rr.get("provenance", ""),
        "has_executable_oracle": True,
        "outcome": 1 if resolved else 0,
        "label_source": "executed",
        "label_confidence": 1.0,
        "labeler_version": LABELER_VERSION,
        "evidence": evidence,
        "timestamp": ts,
    }


# --------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--results", default=str(HERE.parent / "results"))
    ap.add_argument("--tasks", default=str(HERE.parent / "tasks"))
    ap.add_argument("--session", default="", help="grade only this session key")
    ap.add_argument("--force", action="store_true", help="ignore the per-session cache")
    ap.add_argument("--timeout", type=int, default=300)
    args = ap.parse_args()

    results = Path(args.results)
    tasks = Path(args.tasks)
    cache_dir = results / "labels" / "_executed_cache"
    cache_dir.mkdir(parents=True, exist_ok=True)
    split_by_task = load_split_by_task(results)

    have_pytest_img = docker_image_exists(PYTEST_IMAGE)
    have_swebench = swebench_available()
    print(f"[grade] docker_pytest env={'ok' if have_pytest_img else 'MISSING '+PYTEST_IMAGE} | "
          f"swebench env={'ok' if have_swebench else 'MISSING'}", file=sys.stderr)

    graded = skipped_env = skipped_cache = skipped_noora = 0
    for key, rr in iter_run_records(results):
        rr["_key"] = key
        if args.session and key != args.session:
            continue
        if not rr.get("has_executable_oracle"):
            skipped_noora += 1
            continue

        cache_f = cache_dir / f"{key}.json"
        if cache_f.exists() and not args.force:
            skipped_cache += 1
            continue

        task_dir = tasks / rr["task_id"]
        task = json.loads((task_dir / "task.json").read_text())
        grader = task.get("grader", "docker_pytest")
        # Normalize legacy tags: the Step-1 task used "swebench-harness".
        is_swe = grader in ("swebench", "swebench-harness")
        diff = (results / rr.get("patch_path", f"{key}.patch")).read_text() \
            if (results / rr.get("patch_path", f"{key}.patch")).exists() else ""

        # preflight: skip (emit nothing) when the required oracle env is absent
        if not is_swe and not have_pytest_img:
            skipped_env += 1
            print(f"[grade] SKIP {key}: docker_pytest image missing", file=sys.stderr)
            continue
        if is_swe and not have_swebench:
            skipped_env += 1
            print(f"[grade] SKIP {key}: swebench env missing", file=sys.stderr)
            continue

        # A per-session grading error (e.g. a missing repo/ checkout, a patch that
        # won't apply) must SKIP that session, never crash the whole run — the
        # correctness rule is "grade only when the oracle actually ran".
        try:
            if is_swe:
                res = grade_swebench(task.get("instance_id", ""), rr.get("arm", "test"), diff)
            else:
                res = grade_docker_pytest(task, task_dir, diff, args.timeout)
        except Exception as e:
            skipped_env += 1
            print(f"[grade] SKIP {key}: grading error ({type(e).__name__}: {e})", file=sys.stderr)
            continue

        if not res.get("graded"):
            skipped_env += 1
            print(f"[grade] SKIP {key}: not graded ({res.get('note','?')})", file=sys.stderr)
            continue

        split = split_by_task.get(rr["task_id"], "")
        rec = label_record(rr, split, res["resolved"],
                           {k: v for k, v in res.items() if k != "resolved"},
                           int(time.time()))
        cache_f.write_text(json.dumps(rec, indent=2))
        graded += 1
        print(f"[grade] {key:52s} grader={grader:13s} resolved={res['resolved']}", file=sys.stderr)

    # rebuild the consolidated executed.jsonl from the cache (idempotent, no dupes)
    out = results / "labels" / "executed.jsonl"
    recs = [json.loads(f.read_text()) for f in sorted(cache_dir.glob("*.json"))]
    with open(out, "w") as f:
        for r in recs:
            f.write(json.dumps(r) + "\n")

    print(f"[grade] graded={graded} cached={skipped_cache} skipped_env={skipped_env} "
          f"no_oracle={skipped_noora} -> {out} ({len(recs)} records)", file=sys.stderr)


if __name__ == "__main__":
    main()
