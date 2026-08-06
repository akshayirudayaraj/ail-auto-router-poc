"""Shared firewall helpers: decide whether the HIDDEN test content (the lines
test_patch ADDS) is visible in the agent-facing repo/.

Keying off test-method NAMES is wrong for SWE-bench: many FAIL_TO_PASS tests are
MODIFICATIONS of tests that already exist at base_commit, so the method name
legitimately pre-exists in repo/. What must stay hidden is the NEW content the
test_patch introduces (new assertions / a new test body). So we check the
substantive lines test_patch ADDS: if a strong majority are already present in
repo/, the hidden test leaked; otherwise it did not.
"""

from __future__ import annotations

from pathlib import Path


def _substantive_added_lines(diff_text: str) -> list[str]:
    """Substantive '+' lines from a unified diff (added content, not context/
    headers). Filters trivial lines that would incidentally match."""
    out = []
    for ln in diff_text.splitlines():
        if not ln.startswith("+") or ln.startswith("+++"):
            continue
        body = ln[1:].strip()
        if len(body) < 12:
            continue
        if not any(c.isalnum() for c in body):
            continue
        # skip pure imports / decorators that often already exist in repo
        if body.startswith(("import ", "from ", "@")):
            continue
        out.append(" ".join(body.split()))
    # de-dup, keep order
    seen, uniq = set(), []
    for b in out:
        if b not in seen:
            seen.add(b)
            uniq.append(b)
    return uniq


def hidden_test_leak(tdir: Path, threshold: float = 0.7, min_lines: int = 3) -> list[str]:
    """Return the leaked lines if the hidden test content is visible in repo/,
    else []. A leak = a strong majority (>=threshold) of the substantive lines
    test_patch adds are already present verbatim in the agent-visible repo/."""
    tp = tdir / "_oracle" / "test_patch.diff"
    if not tp.exists():
        return []
    added = _substantive_added_lines(tp.read_text(errors="ignore"))
    if len(added) < min_lines:
        # too few substantive lines to judge by fraction; require ALL present
        if added and _all_present(tdir, added):
            return added
        return []
    present = [a for a in added if _present_in_repo(tdir, a)]
    if len(present) / len(added) >= threshold:
        return present
    return []


def _repo_text(tdir: Path) -> str:
    if not hasattr(_repo_text, "_cache"):
        _repo_text._cache = {}
    key = str(tdir)
    if key not in _repo_text._cache:
        chunks = []
        for f in (tdir / "repo").rglob("*.py"):
            try:
                chunks.append(" ".join(f.read_text(errors="ignore").split()))
            except Exception:
                pass
        _repo_text._cache[key] = "\n".join(chunks)
    return _repo_text._cache[key]


def _present_in_repo(tdir: Path, line: str) -> bool:
    return line in _repo_text(tdir)


def _all_present(tdir: Path, lines: list[str]) -> bool:
    txt = _repo_text(tdir)
    return all(l in txt for l in lines)
