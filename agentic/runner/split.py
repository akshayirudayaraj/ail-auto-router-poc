"""Deterministic train / held-out split of the generated corpus (DATA_PLAN Phase 4).

Partition BEFORE any labeling so the held-out set (which the deferred offline
engine will later grade with the executed oracle for the gold set) is never
contaminated by training. The split is a data-generation concern decided now; the
labeling that fills each side is the offline engine's job, later.

Policy:
  * Split BY TASK (not by session/arm), seeded and deterministic, so no task
    appears on both sides and both arms of a task share a split. Bucketing uses
    sha1(seed:task_id) — NOT Python's per-process-salted hash() — so re-running
    with the same seed is byte-identical.
  * held-out  -> reserved for the executed-oracle gold set (offline, later).
  * train     -> reserved for weak labels (implicit/judge, offline, later).

Output: split_manifest.json — the contract the offline engine + UI consume. Per
session -> {split, provenance, grounding, has_executable_oracle}, plus a per-task
summary. Ordering is by (task_id, arm) only (stable content), so the manifest is
reproducible; a real temporal backtest reads wall-clock timestamps from the logs
themselves, not from this manifest.
"""

from __future__ import annotations

import argparse
import glob
import hashlib
import json
import os
from pathlib import Path

HERE = Path(__file__).resolve().parent
AGENTIC_DIR = Path(os.environ.get("AGENTIC_DIR", str(HERE.parent)))
RESULTS_DIR = AGENTIC_DIR / "results"


def bucket(seed: int, task_id: str) -> float:
    """Deterministic [0,1) bucket for a task id (process-salt-free)."""
    h = hashlib.sha1(f"{seed}:{task_id}".encode()).hexdigest()[:8]
    return int(h, 16) / 0x100000000


def load_records(results_dir: Path) -> list[dict]:
    """Load run-record JSONs (the log-first records + legacy swe_*.json). Skips
    non-record files (e.g. the selection manifest, prediction jsonls)."""
    recs = []
    for p in sorted(glob.glob(str(results_dir / "*.json"))):
        name = os.path.basename(p)
        if name in ("split_manifest.json", "swe_selection.json"):
            continue
        try:
            d = json.load(open(p))
        except Exception:
            continue
        if not isinstance(d, dict) or "task_id" not in d or "arm" not in d:
            continue
        recs.append(d)
    return recs


def build_manifest(recs: list[dict], seed: int, holdout_frac: float) -> dict:
    task_split: dict[str, str] = {}
    for r in recs:
        tid = r["task_id"]
        if tid not in task_split:
            task_split[tid] = "holdout" if bucket(seed, tid) < holdout_frac else "train"

    items = []
    for r in sorted(recs, key=lambda x: (x["task_id"], x.get("arm", ""))):
        tid = r["task_id"]
        items.append({
            "task_id": tid,
            "arm": r.get("arm"),
            "session_id": r.get("session_id", f"{tid}__{r.get('arm')}"),
            "split": task_split[tid],
            "provenance": r.get("provenance"),
            "grounding": r.get("grounding"),
            "has_executable_oracle": r.get("has_executable_oracle"),
        })

    tasks = sorted(task_split)
    return {
        "seed": seed,
        "holdout_frac": holdout_frac,
        "policy": "by_task",
        "n_tasks": len(tasks),
        "n_sessions": len(items),
        "n_holdout_tasks": sum(1 for t in tasks if task_split[t] == "holdout"),
        "n_train_tasks": sum(1 for t in tasks if task_split[t] == "train"),
        "task_split": {t: task_split[t] for t in tasks},
        "items": items,
    }


def verify(manifest: dict) -> None:
    """Assert zero cross-split task leakage: every task maps to exactly one split
    and every session's split matches its task's split."""
    ts = manifest["task_split"]
    for it in manifest["items"]:
        assert it["split"] == ts[it["task_id"]], f"leak: {it['session_id']}"
    train = {t for t, s in ts.items() if s == "train"}
    hold = {t for t, s in ts.items() if s == "holdout"}
    assert train.isdisjoint(hold), "a task appears in both splits"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seed", type=int,
                    default=int(os.environ.get("AIL_SPLIT_SEED", "1337")))
    ap.add_argument("--holdout-frac", type=float,
                    default=float(os.environ.get("AIL_HOLDOUT_FRAC", "0.3")))
    ap.add_argument("--results-dir", default=str(RESULTS_DIR))
    ap.add_argument("--out", default=str(RESULTS_DIR / "split_manifest.json"))
    args = ap.parse_args()

    recs = load_records(Path(args.results_dir))
    manifest = build_manifest(recs, args.seed, args.holdout_frac)
    verify(manifest)
    Path(args.out).write_text(json.dumps(manifest, indent=2))
    print(f"[split] {manifest['n_sessions']} sessions / {manifest['n_tasks']} tasks "
          f"-> {manifest['n_train_tasks']} train, {manifest['n_holdout_tasks']} holdout "
          f"(seed={args.seed}, holdout_frac={args.holdout_frac}) -> {args.out}")


if __name__ == "__main__":
    main()
