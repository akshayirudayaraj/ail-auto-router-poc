"""Ground-truth firewall test for the real SWE-bench task dir.

Asserts the agent-visible repo/ never contained the hidden oracle:
  * the FAIL_TO_PASS test methods (added by test_patch) are absent from repo/,
  * the gold patch's changes are NOT already applied in repo/,
  * the _oracle/ artifacts live outside repo/.

Run: python3 agentic/runner/test_firewall.py [task_id]
Exit 0 = firewall holds. Non-zero = breach.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TASKS = ROOT / "tasks"


def check(task_id: str) -> None:
    tdir = TASKS / task_id
    task = json.loads((tdir / "task.json").read_text())
    repo = tdir / "repo"

    # 1. hidden FAIL_TO_PASS tests must be absent from the agent-visible repo
    hidden = [t.rsplit("::", 1)[-1] for t in task["fail_to_pass"]]
    py = list(repo.rglob("*.py"))
    for t in hidden:
        for f in py:
            if f"def {t}" in f.read_text(errors="ignore"):
                raise SystemExit(f"BREACH: hidden test {t} present in {f}")

    # 2. oracle artifacts must be quarantined outside repo/
    assert (tdir / "_oracle" / "test_patch.diff").exists(), "oracle test_patch missing"
    assert (tdir / "_oracle" / "gold_patch.diff").exists(), "oracle gold_patch missing"
    if (repo / "_oracle").exists():
        raise SystemExit("BREACH: _oracle/ is inside the agent-visible repo/")

    # 3. the gold fix must NOT already be applied (repo is the *buggy* base)
    gold = (tdir / "_oracle" / "gold_patch.diff").read_text()
    added = [ln[1:].strip() for ln in gold.splitlines()
             if ln.startswith("+") and not ln.startswith("+++") and len(ln.strip()) > 6]
    for target in py:
        txt = target.read_text(errors="ignore")
        hits = [a for a in added if a and a in txt]
        if len(hits) >= max(1, len(added)) and added:  # whole fix already present
            raise SystemExit(f"BREACH: gold fix appears already applied in {target}")

    print(f"FIREWALL OK for {task_id}: hidden tests {hidden} absent; "
          f"oracle quarantined; gold fix not pre-applied")


if __name__ == "__main__":
    check(sys.argv[1] if len(sys.argv) > 1 else "swe-psf__requests-1142")
