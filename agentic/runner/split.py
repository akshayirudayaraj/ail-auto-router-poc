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


def task_cells(results_dir: Path) -> dict[str, str]:
    """Classify each task by its EXECUTED dual-arm outcome, so the split can be
    stratified: 'disagree' (local != frontier — the escalation signal the router
    both learns from and is measured on), 'agree' (both arms same outcome), or
    absent (not both arms executed -> cannot be gold -> always train). Reads the
    fused canonical labels; returns {} if none, in which case the split falls back
    to a uniform holdout fraction."""
    resolved = results_dir / "labels" / "resolved.jsonl"
    if not resolved.exists():
        return {}
    by: dict[str, dict[str, int]] = {}
    for line in open(resolved):
        line = line.strip()
        if not line:
            continue
        r = json.loads(line)
        if r.get("label_source") != "executed":
            continue
        by.setdefault(r["task_id"], {})[r.get("arm")] = r.get("outcome")
    cells: dict[str, str] = {}
    for t, arms in by.items():
        if "local" in arms and "frontier" in arms:
            cells[t] = "disagree" if arms["local"] != arms["frontier"] else "agree"
    return cells


def build_manifest(recs: list[dict], seed: int, holdout_frac: float,
                   cells: dict[str, str] | None = None,
                   disagree_frac: float = 0.5, agree_frac: float = 0.25) -> dict:
    # Stratified, disagreement-enriched holdout: reserve HALF the escalation-worthy
    # (disagreement) pairs for the gold eval so the leaderboard has real signal,
    # while keeping the other half in train so the routers can learn the boundary;
    # only a quarter of the (less informative) agreement pairs go to gold. Tasks
    # with no executed dual-arm outcome can never be gold, so they stay in train.
    # Falls back to a uniform holdout_frac when no executed labels exist yet.
    cells = cells or {}
    stratified = bool(cells)
    task_split: dict[str, str] = {}
    for r in recs:
        tid = r["task_id"]
        if tid in task_split:
            continue
        if stratified:
            cell = cells.get(tid)
            frac = disagree_frac if cell == "disagree" else agree_frac if cell == "agree" else 0.0
        else:
            frac = holdout_frac
        task_split[tid] = "holdout" if bucket(seed, tid) < frac else "train"

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
    # per-cell holdout/train breakdown (for transparency in the manifest)
    strata: dict[str, dict[str, int]] = {}
    if stratified:
        for t in tasks:
            c = cells.get(t, "none")
            strata.setdefault(c, {"holdout": 0, "train": 0})[task_split[t]] += 1
    return {
        "seed": seed,
        "holdout_frac": holdout_frac,
        "policy": "stratified_disagreement_enriched" if stratified else "by_task",
        "disagree_frac": disagree_frac if stratified else None,
        "agree_frac": agree_frac if stratified else None,
        "strata": strata,
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
    ap.add_argument("--disagree-frac", type=float,
                    default=float(os.environ.get("AIL_DISAGREE_FRAC", "0.5")))
    ap.add_argument("--agree-frac", type=float,
                    default=float(os.environ.get("AIL_AGREE_FRAC", "0.25")))
    ap.add_argument("--results-dir", default=str(RESULTS_DIR))
    ap.add_argument("--out", default=str(RESULTS_DIR / "split_manifest.json"))
    args = ap.parse_args()

    recs = load_records(Path(args.results_dir))
    cells = task_cells(Path(args.results_dir))
    manifest = build_manifest(recs, args.seed, args.holdout_frac, cells,
                              args.disagree_frac, args.agree_frac)
    verify(manifest)
    Path(args.out).write_text(json.dumps(manifest, indent=2))
    print(f"[split] {manifest['n_sessions']} sessions / {manifest['n_tasks']} tasks "
          f"-> {manifest['n_train_tasks']} train, {manifest['n_holdout_tasks']} holdout "
          f"(policy={manifest['policy']}, seed={args.seed}) -> {args.out}")
    if manifest.get("strata"):
        print(f"[split] strata (holdout/train): " +
              " · ".join(f"{c}={v['holdout']}/{v['train']}" for c, v in sorted(manifest["strata"].items())))


if __name__ == "__main__":
    main()
