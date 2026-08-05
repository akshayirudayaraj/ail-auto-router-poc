"""Materialize ONE real SWE-bench Verified instance as an agentic task dir.

This is the Step-1 seam D14 promised: a real SWE-bench Verified instance dropped
into the SAME task layout the curated tasks use, so `run_agentic.py` drives it
with no runner change. Only the *source* of the task becomes real.

Layout produced (matches agentic/tasks/<id>/):
    tasks/<slug>/
      task.json          id, issue(=problem_statement), test_cmd, F2P/P2P, swe metadata
      repo/              psf/requests checked out at base_commit  (what the agent sees)
      _oracle/           test_patch + gold_patch  (NEVER inside repo/ — firewall)

Ground-truth firewall: the agent sees ONLY repo@base_commit. `test_patch` (the
hidden new test) and `gold_patch` (the reference fix) are written to _oracle/,
outside repo/, and are used only by the grader (the official swebench harness,
which re-derives test_patch from the dataset by instance_id — see grade_swe.py).

External deps (git, the SWE-bench dataset) live only here in the Job-B Python
quarantine, never in the stdlib-only Go core (DECISIONS D12/D14).
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

REPO_URL = {"psf/requests": "https://github.com/psf/requests.git"}


def load_instance(instance_id: str, record_json: str | None) -> dict:
    if record_json:
        return json.loads(Path(record_json).read_text())
    # fall back to the dataset (needs a python with `datasets`, e.g. .venv_swe)
    from datasets import load_dataset
    ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
    for x in ds:
        if x["instance_id"] == instance_id:
            return dict(x)
    raise SystemExit(f"instance {instance_id} not found")


def run(cmd, cwd=None):
    subprocess.run(cmd, cwd=cwd, check=True)


def materialize(rec: dict, tasks_dir: Path) -> Path:
    iid = rec["instance_id"]
    repo = rec["repo"]
    slug = f"swe-{iid}"
    tdir = tasks_dir / slug
    if tdir.exists():
        raise SystemExit(f"{tdir} already exists — refusing to overwrite")
    (tdir / "_oracle").mkdir(parents=True)

    # 1. clone repo @ base_commit into repo/ (what the agent sees)
    url = REPO_URL.get(repo) or f"https://github.com/{repo}.git"
    repo_dir = tdir / "repo"
    run(["git", "clone", "--quiet", url, str(repo_dir)])
    run(["git", "checkout", "--quiet", rec["base_commit"]], cwd=repo_dir)
    # Strip .git so repo/ is a plain source tree, matching the curated-task format.
    # The runner's fresh_checkout does its own `git init` + "base" commit; leaving
    # the upstream .git makes that commit a no-op ("nothing to commit") and aborts.
    import shutil as _sh
    _sh.rmtree(repo_dir / ".git", ignore_errors=True)

    # 2. firewall: oracle artifacts go OUTSIDE repo/
    (tdir / "_oracle" / "test_patch.diff").write_text(rec["test_patch"])
    (tdir / "_oracle" / "gold_patch.diff").write_text(rec["patch"])

    # 3. task.json in the existing curated-task schema + SWE provenance
    f2p = rec["FAIL_TO_PASS"]
    p2p = rec["PASS_TO_PASS"]
    if isinstance(f2p, str):
        f2p = json.loads(f2p)
    if isinstance(p2p, str):
        p2p = json.loads(p2p)
    task = {
        "id": slug,
        "tier": "swe-verified",
        "issue": rec["problem_statement"],
        "test_cmd": "python -m pytest -q",
        "fail_to_pass": f2p,
        "pass_to_pass": p2p,
        # --- SWE provenance (Step-1 real-instance marker) ---
        "source": "swe-bench-verified",
        "instance_id": iid,
        "repo": repo,
        "base_commit": rec["base_commit"],
        "environment_setup_commit": rec.get("environment_setup_commit"),
        "grader": "swebench-harness",  # graded by official harness, not the curated Docker executor
    }
    (tdir / "task.json").write_text(json.dumps(task, indent=2))
    return tdir


def firewall_check(tdir: Path) -> None:
    """Assert the agent-visible repo/ does not contain the HIDDEN tests.

    The hidden tests are exactly the FAIL_TO_PASS methods introduced by
    test_patch — NOT every test in the diff. PASS_TO_PASS tests legitimately
    pre-exist at base_commit (and appear as diff context), so keying off the diff
    directly over-flags them. We key off the F2P method names instead.
    """
    task = json.loads((tdir / "task.json").read_text())
    hidden = [t.rsplit("::", 1)[-1] for t in task["fail_to_pass"]]
    leaked = []
    for f in (tdir / "repo").rglob("*.py"):
        txt = f.read_text(errors="ignore")
        for t in hidden:
            if f"def {t}" in txt:
                leaked.append((t, str(f.relative_to(tdir))))
    if leaked:
        raise SystemExit(f"FIREWALL BREACH: hidden F2P tests present in repo/: {leaked}")
    if (tdir / "repo" / "_oracle").exists():
        raise SystemExit("FIREWALL BREACH: _oracle/ is inside repo/")
    print(f"firewall OK: hidden F2P tests {hidden} absent from agent-visible repo/")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--instance-id", default="psf__requests-1142")
    ap.add_argument("--record-json", default=None, help="pre-fetched instance record JSON")
    ap.add_argument("--tasks-dir", default=str(Path(__file__).resolve().parent.parent / "tasks"))
    args = ap.parse_args()
    rec = load_instance(args.instance_id, args.record_json)
    tdir = materialize(rec, Path(args.tasks_dir))
    firewall_check(tdir)
    print(f"materialized {tdir}")
    print(json.dumps({k: v for k, v in json.loads((tdir / 'task.json').read_text()).items()
                      if k != 'issue'}, indent=2))


if __name__ == "__main__":
    main()
