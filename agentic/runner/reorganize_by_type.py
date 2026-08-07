"""Partition the flat results/ dir into per-TYPE subdirectories, by provenance:

  results/
    synthetic/        provenance not in {swe_verified, internal_usage}
    semi-synthetic/   provenance == swe_verified   (SWE-bench Verified)
    logs/             provenance == internal_usage  (real CC session logs)

Type is intrinsic to how a session was produced, so it's a stable physical
partition. (Oracle-vs-implicit is a *derived, mutable* label — that stays a
facet/column, not a folder.) Idempotent: re-running is a no-op; a file already
in its correct subdir is skipped. All readers resolve either layout via
internal/resultsfs (Go) and recursive globs (Python), so this can run any time
after generation/grading. Non-record files (labels/, calibration/, manifests)
stay at the results/ root.
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import shutil
from pathlib import Path

HERE = Path(__file__).resolve().parent
RESULTS_DIR = HERE.parent / "results"

SUBDIRS = ("synthetic", "semi-synthetic", "logs")
SIDE_EXTS = (".json", ".patch", ".events.jsonl", ".session.jsonl")


def type_of(provenance: str) -> str:
    if provenance == "swe_verified":
        return "semi-synthetic"
    if provenance == "internal_usage":
        return "logs"
    return "synthetic"


def run_records(results_dir: Path):
    """Every run-record .json under results/ (flat or already in a subdir),
    excluding labels/ and calibration/ and the manifests."""
    for p in sorted(glob.glob(str(results_dir / "**" / "*.json"), recursive=True)):
        if os.sep + "labels" + os.sep in p or os.sep + "calibration" + os.sep in p:
            continue
        base = os.path.basename(p)
        if base in ("split_manifest.json", "swe_selection.json", "gold_meta.json"):
            continue
        try:
            rec = json.load(open(p))
        except Exception:
            continue
        if not isinstance(rec, dict) or "task_id" not in rec:
            continue
        yield p, rec


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--results-dir", default=str(RESULTS_DIR))
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    rd = Path(args.results_dir)

    moved, skipped = 0, 0
    counts: dict[str, int] = {d: 0 for d in SUBDIRS}
    for jpath, rec in run_records(rd):
        sub = type_of(rec.get("provenance", ""))
        counts[sub] += 1
        stem = os.path.basename(jpath)[: -len(".json")]
        cur_dir = os.path.dirname(jpath)
        dst_dir = rd / sub
        if os.path.abspath(cur_dir) == os.path.abspath(dst_dir):
            skipped += 1
            continue
        dst_dir.mkdir(parents=True, exist_ok=True)
        # move the record + all co-located side files (patch/events/session)
        for ext in SIDE_EXTS:
            src = os.path.join(cur_dir, stem + ext)
            if os.path.exists(src):
                dst = dst_dir / (stem + ext)
                if args.dry_run:
                    print(f"  {src} -> {dst}")
                else:
                    shutil.move(src, dst)
                moved += 1

    print(f"[reorganize] {'(dry-run) ' if args.dry_run else ''}moved {moved} files, "
          f"{skipped} records already in place | per-type: " +
          " · ".join(f"{d}={counts[d]}" for d in SUBDIRS))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
