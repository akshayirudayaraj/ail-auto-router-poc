"""Ground-truth firewall test for a real-SWE or generated task dir.

Asserts the agent-visible repo/ + issue never leaked the hidden oracle:
  * the FAIL_TO_PASS test methods (added by test_patch) are absent from repo/,
  * the gold patch's changes are NOT already applied in repo/,
  * the _oracle/ artifacts live outside repo/,
  * the ISSUE text does not embed the fix (generated tasks add this leak vector:
    a Claude-Max-authored issue could paste the solution).

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

    # 4. the ISSUE text must not embed the fix (generated-task leak vector).
    issue_leak_check(task.get("issue", ""), gold)

    print(f"FIREWALL OK for {task_id}: hidden tests {hidden} absent; "
          f"oracle quarantined; gold fix not pre-applied; issue clean")


def issue_leak_check(issue: str, gold_patch: str) -> None:
    """Reject an issue that embeds the solution. Conservative on purpose: a real
    problem statement legitimately names identifiers, so we flag only STRONG
    evidence — a pasted diff, or several substantive gold-added code lines
    reproduced verbatim in the issue.
    """
    if not issue or not gold_patch:
        return
    low = issue.lower()
    # (a) a pasted patch/diff is an unambiguous leak.
    for marker in ("```diff", "--- a/", "+++ b/", "\ndiff --git", "@@ "):
        if marker in issue:
            raise SystemExit(f"BREACH: issue embeds a patch/diff (marker {marker!r})")

    # (b) substantive gold-added code lines reproduced verbatim in the issue.
    def _norm(s: str) -> str:
        return " ".join(s.split())

    issue_norm = _norm(issue).lower()
    added = []
    for ln in gold_patch.splitlines():
        if ln.startswith("+") and not ln.startswith("+++"):
            body = ln[1:].strip()
            # substantive = long enough and looks like code, not a bare token
            if len(body) >= 12 and any(c in body for c in "=(){}:[]") and body[0] != "#":
                added.append(_norm(body).lower())
    verbatim = [a for a in set(added) if a in issue_norm]
    if len(verbatim) >= 2:
        raise SystemExit(
            f"BREACH: issue reproduces {len(verbatim)} gold-fix code lines verbatim: "
            f"{verbatim[:3]}")


if __name__ == "__main__":
    check(sys.argv[1] if len(sys.argv) > 1 else "swe-psf__requests-1142")
