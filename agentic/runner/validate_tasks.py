#!/usr/bin/env python3
"""
Validate every task is well-formed against the execution oracle:
  * BUGGY checkout: FAIL_TO_PASS must FAIL, PASS_TO_PASS must PASS.
  * REFERENCE checkout (buggy repo + _reference fix applied): everything PASSES.

If a task doesn't satisfy this, its executed labels would be meaningless, so we
refuse to ship it. Run after build_tasks.py.
"""
import json
import os
import shutil
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from executor import score_checkout  # noqa: E402

TASKS_DIR = os.path.abspath(os.path.join(HERE, "..", "tasks"))


def _copy_repo(task_id: str) -> str:
    src = os.path.join(TASKS_DIR, task_id, "repo")
    dst = tempfile.mkdtemp(prefix=f"val_{task_id}_")
    shutil.copytree(src, dst, dirs_exist_ok=True)
    return dst


def _apply_reference(task_id: str, checkout: str) -> None:
    ref = os.path.join(TASKS_DIR, task_id, "_reference")
    for root, _, files in os.walk(ref):
        for fn in files:
            srcp = os.path.join(root, fn)
            rel = os.path.relpath(srcp, ref)
            dstp = os.path.join(checkout, rel)
            os.makedirs(os.path.dirname(dstp), exist_ok=True)
            shutil.copy(srcp, dstp)


def main() -> int:
    ids = [d for d in sorted(os.listdir(TASKS_DIR))
           if os.path.isdir(os.path.join(TASKS_DIR, d))]
    all_ok = True
    for tid in ids:
        tj = os.path.join(TASKS_DIR, tid, "task.json")
        if not os.path.exists(tj):
            continue
        task = json.load(open(tj))

        buggy = _copy_repo(tid)
        b = score_checkout(buggy, task)
        shutil.rmtree(buggy, ignore_errors=True)

        ref = _copy_repo(tid)
        _apply_reference(tid, ref)
        r = score_checkout(ref, task)
        shutil.rmtree(ref, ignore_errors=True)

        # Well-formed: buggy has F2P failing (resolved False, but P2P ok),
        # reference resolves everything.
        buggy_ok = (not b["fail_to_pass_ok"]) and b["pass_to_pass_ok"]
        ref_ok = r["resolved"]
        ok = buggy_ok and ref_ok
        all_ok = all_ok and ok
        print(f"{'OK ' if ok else 'BAD'} {task['tier']:6s} {tid:24s} "
              f"buggy[F2P_fails={not b['fail_to_pass_ok']} P2P_ok={b['pass_to_pass_ok']}] "
              f"ref[resolved={r['resolved']}]")
        if not ok:
            print(f"    buggy detail: {json.dumps(b['per_node'])}")
            print(f"    ref detail:   {json.dumps(r['per_node'])}")
    print("\nALL TASKS WELL-FORMED" if all_ok else "\nSOME TASKS ARE BROKEN")
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
