"""Test the deterministic split (DATA_PLAN Phase 4 acceptance):
  * every task/session lands in exactly one of train/holdout (no leakage);
  * re-running with the same seed is byte-identical.
Run: python3 agentic/runner/test_split.py
"""
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from split import build_manifest, verify  # noqa: E402


def _fake_recs():
    recs = []
    for i in range(25):
        tid = f"task-{i:02d}"
        for arm in ("local", "frontier"):
            recs.append({
                "task_id": tid, "arm": arm, "session_id": f"{tid}__{arm}",
                "provenance": "swe_verified" if i % 2 else "synthetic",
                "grounding": "benchmark" if i % 2 else "oss_history",
                "has_executable_oracle": True,
            })
    return recs


def main():
    recs = _fake_recs()
    m1 = build_manifest(recs, seed=1337, holdout_frac=0.3)
    verify(m1)

    # No cross-split leakage: a task never appears in both splits.
    train = {t for t, s in m1["task_split"].items() if s == "train"}
    hold = {t for t, s in m1["task_split"].items() if s == "holdout"}
    assert train.isdisjoint(hold), "task in both splits"
    assert len(train) + len(hold) == m1["n_tasks"] == 25

    # Both arms of a task share its split.
    by_task = {}
    for it in m1["items"]:
        by_task.setdefault(it["task_id"], set()).add(it["split"])
    assert all(len(v) == 1 for v in by_task.values()), "arms of a task disagree on split"

    # Deterministic: same seed -> byte-identical manifest (input order shuffled).
    m2 = build_manifest(list(reversed(recs)), seed=1337, holdout_frac=0.3)
    assert json.dumps(m1, sort_keys=True) == json.dumps(m2, sort_keys=True), \
        "split not reproducible for the same seed"

    # A different seed generally yields a different partition.
    m3 = build_manifest(recs, seed=42, holdout_frac=0.3)
    assert m3["task_split"] != m1["task_split"], "seed had no effect (suspicious)"

    print(f"OK: 25 tasks -> {m1['n_train_tasks']} train / {m1['n_holdout_tasks']} holdout; "
          f"no leakage; reproducible; seed-sensitive.")


if __name__ == "__main__":
    main()
