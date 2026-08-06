"""Ground-truth firewall test for a real-SWE or generated task dir.

Asserts the agent-visible repo/ + issue never leaked the hidden oracle:
  * the FAIL_TO_PASS test methods (added by test_patch) are absent from repo/,
  * the gold patch's changes are NOT already applied in repo/,
  * the _oracle/ artifacts live outside repo/,
  * the ISSUE text does not embed the fix (generated tasks add this leak vector:
    a Claude-Max-authored issue could paste the solution).

Run: python3 agentic/runner/firewall_gate.py [task_id]
Exit 0 = firewall holds. Non-zero = breach.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from firewall_util import hidden_test_leak  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
TASKS = ROOT / "tasks"


def check(task_id: str) -> None:
    tdir = TASKS / task_id
    task = json.loads((tdir / "task.json").read_text())
    repo = tdir / "repo"
    py = list(repo.rglob("*.py"))
    grounding = task.get("grounding")
    # A HIDDEN-test oracle (SWE / oss_history) keeps FAIL_TO_PASS out of repo/ and
    # grades with test_patch. A self-contained synthetic_repo task legitimately
    # SHIPS its tests in repo/ (the agent runs them; the oracle re-runs the same
    # tests offline), so the hidden-test-absent check does not apply there.
    hidden_test_oracle = grounding in ("benchmark", "oss_history")

    hidden = [t.rsplit("::", 1)[-1] for t in task["fail_to_pass"]]
    # 1. the HIDDEN test content (lines test_patch adds) must be absent from
    # repo/ — hidden-test oracles only. Keys off added lines, not method names
    # (modified F2P tests legitimately pre-exist at base_commit; firewall_util).
    if hidden_test_oracle:
        leaked = hidden_test_leak(tdir)
        if leaked:
            raise SystemExit(f"BREACH: hidden test content visible in repo/ "
                             f"({len(leaked)} added lines, e.g. {leaked[:2]})")

    # 2. oracle artifacts must be quarantined outside repo/. A hidden-test oracle
    # needs test_patch; a self-contained task needs a reference fix (gold_patch or
    # _reference/) since its tests already live in repo/.
    if hidden_test_oracle:
        assert (tdir / "_oracle" / "test_patch.diff").exists(), "oracle test_patch missing"
    assert (tdir / "_oracle" / "gold_patch.diff").exists() or (tdir / "_reference").is_dir(), \
        "no reference fix: need _oracle/gold_patch.diff or _reference/"
    if (repo / "_oracle").exists():
        raise SystemExit("BREACH: _oracle/ is inside the agent-visible repo/")

    # 3. the gold fix must NOT already be applied (repo is the *buggy* base).
    # SKIP for swe_verified: the repo is checked out at base_commit by
    # construction, so the fix is definitionally NOT applied; the substring
    # heuristic below false-positives on small real patches whose added lines
    # coincidentally match existing code. Meaningful only for GENERATED tasks,
    # where an author could accidentally ship the fix. Require >=3 substantive
    # added lines before trusting "all present" to avoid 1-liner false hits.
    gold_path = tdir / "_oracle" / "gold_patch.diff"
    gold = gold_path.read_text() if gold_path.exists() else ""
    if gold and task.get("provenance") != "swe_verified":
        added = [ln[1:].strip() for ln in gold.splitlines()
                 if ln.startswith("+") and not ln.startswith("+++") and len(ln.strip()) > 6]
        if len(added) >= 3:
            for target in py:
                txt = target.read_text(errors="ignore")
                hits = [a for a in added if a and a in txt]
                if len(hits) >= len(added):  # whole fix already present
                    raise SystemExit(f"BREACH: gold fix appears already applied in {target}")

    # 4. the ISSUE text must not embed the fix — GENERATED tasks only. This leak
    # vector exists because a Claude-authored issue could paste the solution; a
    # real SWE-bench problem statement is GIVEN (and often includes reproduction
    # code that legitimately overlaps the fix), so the check does not apply there.
    if task.get("provenance") == "synthetic" and gold:
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
    # The issue-leak check looks ONLY at gold-ADDED lines (the fix), so showing
    # the BUGGY/current code is fine — only pasting the FIX trips it. A fix is
    # often a single line, so even ONE distinctive added code line reproduced
    # verbatim is a leak; two shorter lines also trip it.
    def _norm(s: str) -> str:
        return " ".join(s.split())

    issue_norm = _norm(issue).lower()
    strong, weak = [], []
    for ln in gold_patch.splitlines():
        if ln.startswith("+") and not ln.startswith("+++"):
            body = ln[1:].strip()
            if body.startswith("#") or not any(c in body for c in "=(){}[]"):
                continue  # comments / non-code
            n = _norm(body).lower()
            if len(body) >= 18:
                strong.append(n)   # one distinctive fix line is enough
            elif len(body) >= 10:
                weak.append(n)
    strong_hits = [a for a in set(strong) if a in issue_norm]
    weak_hits = [a for a in set(weak) if a in issue_norm]
    if strong_hits or len(weak_hits) >= 2:
        raise SystemExit(
            f"BREACH: issue reproduces gold-fix code verbatim: "
            f"{(strong_hits + weak_hits)[:3]}")


if __name__ == "__main__":
    check(sys.argv[1] if len(sys.argv) > 1 else "swe-psf__requests-1142")
