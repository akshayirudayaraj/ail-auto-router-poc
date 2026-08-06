"""Materialize N real SWE-bench Verified instances as agentic task dirs.

This is the Step-1 seam D14 promised, scaled to a set (DATA_PLAN Phase 2A): real
SWE-bench Verified instances dropped into the SAME task layout the curated tasks
use, so `run_agentic.py` drives them with no runner change. Only the *source* of
the task is real.

Layout produced (matches agentic/tasks/<id>/):
    tasks/<slug>/
      task.json          id, issue(=problem_statement), test_cmd, F2P/P2P, provenance
      repo/              <repo> checked out at base_commit  (what the agent sees)
      _oracle/           test_patch + gold_patch  (NEVER inside repo/ — firewall)

Ground-truth firewall: the agent sees ONLY repo@base_commit. `test_patch` (the
hidden new test) and `gold_patch` (the reference fix) are written to _oracle/,
outside repo/, used only by the deferred offline grader (the official swebench
harness, which re-derives test_patch from the dataset by instance_id).

Selection (no grading here — just materialization): bias to fast-grading (small
FAIL_TO_PASS/PASS_TO_PASS) and smaller repos (avoid the django/sympy giants —
disk + slow local agent), diversify across repos, and LOG every pick and every
drop (no silent truncation). Contamination: SWE-bench Verified is public, so
these instances carry contamination risk — recorded in the selection manifest.

External deps (git, the SWE-bench dataset via `datasets`) live only here in the
Job-B Python quarantine, never in the stdlib-only Go core (DECISIONS D12/D14). If
the running interpreter lacks `datasets`, this script re-execs itself under
SWEBENCH_PY (the proven ail-self-routing/.venv_swe).
"""

from __future__ import annotations

import argparse
import json
import math
import os
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from firewall_util import hidden_test_leak  # noqa: E402

DATASET = "princeton-nlp/SWE-bench_Verified"

REPO_URL = {"psf/requests": "https://github.com/psf/requests.git"}

# Repos to avoid: huge working trees => slow clone/checkout + a slow local agent,
# and slow offline grading. (DATA_PLAN Phase 2A: "avoid django/sympy giants".)
BIG_REPOS = {
    "django/django", "sympy/sympy", "matplotlib/matplotlib",
    "scikit-learn/scikit-learn", "astropy/astropy", "sphinx-doc/sphinx",
    "pandas-dev/pandas",
}

SWEBENCH_PY = os.environ.get(
    "SWEBENCH_PY",
    str(Path.home() / "development/spectro/ail-self-routing/.venv_swe/bin/python"))


def _ensure_datasets():
    """Re-exec under a python that has `datasets` if this one doesn't."""
    try:
        import datasets  # noqa: F401
        return
    except ImportError:
        pass
    if Path(sys.executable).resolve() == Path(SWEBENCH_PY).resolve():
        raise SystemExit("SWEBENCH_PY lacks `datasets`; install it or set SWEBENCH_PY")
    if not Path(SWEBENCH_PY).exists():
        raise SystemExit(f"no `datasets` here and SWEBENCH_PY not found: {SWEBENCH_PY}")
    os.execv(SWEBENCH_PY, [SWEBENCH_PY, os.path.abspath(__file__), *sys.argv[1:]])


def _as_list(v):
    if isinstance(v, str):
        return json.loads(v)
    return list(v or [])


def _difficulty_proxy(rec: dict) -> dict:
    """Cheap size/difficulty signals used to bias selection to fast, small tasks.
    Prefer the human `difficulty` annotation when the dataset row carries it."""
    f2p = _as_list(rec.get("FAIL_TO_PASS"))
    p2p = _as_list(rec.get("PASS_TO_PASS"))
    patch_lines = len((rec.get("patch") or "").splitlines())
    test_lines = len((rec.get("test_patch") or "").splitlines())
    return {
        "repo": rec["repo"],
        "n_f2p": len(f2p),
        "n_p2p": len(p2p),
        "patch_lines": patch_lines,
        "test_lines": test_lines,
        "difficulty": rec.get("difficulty"),  # may be None
        # Smaller score = smaller/faster task. n_p2p weighted low (only affects
        # grading time, deferred) but not ignored.
        "score": patch_lines + 3 * len(f2p) + test_lines + 0.2 * len(p2p),
    }


def select_instances(ds, n: int, per_repo_cap: int | None = None) -> tuple[list, dict]:
    """Rank + pick N instances deterministically, diversified across repos.
    Returns (chosen_records, manifest) where manifest logs picks and drops."""
    dropped_big = []
    dropped_bad = []
    scored = []
    for rec in ds:
        repo = rec["repo"]
        if repo in BIG_REPOS:
            dropped_big.append(rec["instance_id"])
            continue
        try:
            d = _difficulty_proxy(rec)
        except Exception as e:  # malformed row
            dropped_bad.append((rec.get("instance_id"), str(e)))
            continue
        if d["n_f2p"] < 1:
            dropped_bad.append((rec["instance_id"], "no FAIL_TO_PASS"))
            continue
        scored.append((d["score"], rec["instance_id"], rec, d))

    # Deterministic: smallest score first, tie-break on instance_id.
    scored.sort(key=lambda x: (x[0], x[1]))

    if per_repo_cap is None:
        # Diversify: no single repo dominates the set.
        per_repo_cap = max(1, math.ceil(n / 5))

    chosen, chosen_meta, per_repo = [], [], {}
    skipped_cap = []
    for score, iid, rec, d in scored:
        if len(chosen) >= n:
            break
        repo = rec["repo"]
        if per_repo.get(repo, 0) >= per_repo_cap:
            skipped_cap.append(iid)
            continue
        per_repo[repo] = per_repo.get(repo, 0) + 1
        chosen.append(rec)
        chosen_meta.append({"instance_id": iid, **d})

    manifest = {
        "dataset": DATASET,
        "requested_n": n,
        "selected_n": len(chosen),
        "per_repo_cap": per_repo_cap,
        "per_repo_counts": per_repo,
        "selected": chosen_meta,
        "dropped_big_repo": {"count": len(dropped_big), "repos": sorted(BIG_REPOS)},
        "dropped_malformed": dropped_bad,
        "skipped_for_repo_cap_count": len(skipped_cap),
        "contamination_note": (
            "SWE-bench Verified is a PUBLIC benchmark; these instances carry "
            "contamination risk (models may have seen the fixes in training). "
            "Recorded per DATA_PLAN Phase 2A; prefer SWE-bench Pro / held-out later."
        ),
    }
    return chosen, manifest


def run(cmd, cwd=None):
    subprocess.run(cmd, cwd=cwd, check=True)


def materialize(rec: dict, tasks_dir: Path) -> tuple[Path, bool]:
    """Materialize one instance. Returns (task_dir, created) — created=False if it
    already existed (resumable; not an error)."""
    iid = rec["instance_id"]
    repo = rec["repo"]
    slug = f"swe-{iid}"
    tdir = tasks_dir / slug
    if tdir.exists():
        return tdir, False
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
    task = {
        "id": slug,
        "tier": "swe-verified",
        "issue": rec["problem_statement"],
        "test_cmd": "python -m pytest -q",
        "fail_to_pass": _as_list(rec.get("FAIL_TO_PASS")),
        "pass_to_pass": _as_list(rec.get("PASS_TO_PASS")),
        # --- provenance/grounding tags (DATA_PLAN Phase 6) ---
        "provenance": "swe_verified",
        "grounding": "benchmark",
        "has_executable_oracle": True,
        "grader": "swebench",           # graded OFFLINE by the official harness
        # --- SWE metadata ---
        "source": "swe-bench-verified",
        "instance_id": iid,
        "repo": repo,
        "base_commit": rec["base_commit"],
        "environment_setup_commit": rec.get("environment_setup_commit"),
    }
    (tdir / "task.json").write_text(json.dumps(task, indent=2))
    return tdir, True


def firewall_check(tdir: Path) -> None:
    """Assert the agent-visible repo/ does not contain the HIDDEN test content.

    Keys off the substantive lines test_patch ADDS (the genuinely hidden bits),
    not F2P method names — many SWE-bench F2P tests are MODIFICATIONS of tests
    that already exist at base_commit, so the method name legitimately pre-exists
    in repo/ and a name-based check false-breaches them. See firewall_util.
    """
    leaked = hidden_test_leak(tdir)
    if leaked:
        raise SystemExit(
            f"FIREWALL BREACH: hidden test content visible in repo/ "
            f"({len(leaked)} added test lines present, e.g. {leaked[:2]})")
    if (tdir / "repo" / "_oracle").exists():
        raise SystemExit("FIREWALL BREACH: _oracle/ is inside repo/")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--n", type=int, default=20, help="how many instances to select")
    ap.add_argument("--instances", default="",
                    help="comma list of explicit instance_ids (overrides selection)")
    ap.add_argument("--instance-id", default=None, help="single instance (back-compat)")
    ap.add_argument("--record-json", default=None, help="pre-fetched record JSON (single)")
    ap.add_argument("--per-repo-cap", type=int, default=None)
    ap.add_argument("--tasks-dir",
                    default=str(Path(__file__).resolve().parent.parent / "tasks"))
    args = ap.parse_args()
    tasks_dir = Path(args.tasks_dir)
    tasks_dir.mkdir(parents=True, exist_ok=True)

    # Back-compat: a single pre-fetched record needs no dataset.
    if args.record_json:
        rec = json.loads(Path(args.record_json).read_text())
        tdir, created = materialize(rec, tasks_dir)
        firewall_check(tdir)
        print(f"[materialize] {'created' if created else 'exists'} {tdir}")
        return

    _ensure_datasets()
    from datasets import load_dataset
    ds = load_dataset(DATASET, split="test")

    explicit = [x for x in args.instances.split(",") if x]
    if args.instance_id:
        explicit.append(args.instance_id)
    if explicit:
        by_id = {x["instance_id"]: dict(x) for x in ds}
        chosen = [by_id[i] for i in explicit if i in by_id]
        missing = [i for i in explicit if i not in by_id]
        manifest = {"dataset": DATASET, "explicit": explicit, "missing": missing,
                    "selected_n": len(chosen)}
    else:
        chosen, manifest = select_instances(ds, args.n, args.per_repo_cap)

    # Write the selection manifest FIRST (no silent truncation — every pick/drop
    # is on the record even if a later clone fails).
    sel_path = tasks_dir / "swe_selection.json"
    sel_path.write_text(json.dumps(manifest, indent=2))
    print(f"[materialize] selection -> {sel_path} "
          f"(selected {manifest.get('selected_n')} of requested {args.n})")

    created_n = existed_n = failed_n = 0
    for rec in chosen:
        try:
            tdir, created = materialize(rec, tasks_dir)
            firewall_check(tdir)
            created_n += created
            existed_n += (not created)
            print(f"[materialize] {'created' if created else 'exists '} "
                  f"{rec['instance_id']:32s} repo={rec['repo']}")
        except SystemExit as e:            # firewall breach — surface, keep going
            failed_n += 1
            print(f"[materialize] FAILED {rec['instance_id']}: {e}", file=sys.stderr)
        except Exception as e:             # clone/checkout error — log, keep going
            failed_n += 1
            print(f"[materialize] ERROR  {rec['instance_id']}: {e}", file=sys.stderr)
    print(f"[materialize] done: created={created_n} existed={existed_n} "
          f"failed={failed_n}")


if __name__ == "__main__":
    main()
