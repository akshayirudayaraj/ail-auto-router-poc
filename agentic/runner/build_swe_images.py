#!/usr/bin/env python3
"""Build the official SWE-bench per-instance images (base -> env -> instance) for
the materialized SWE tasks, so the agent can run INSIDE the real environment
(container_exec.py). This is the swebench harness's own build path — we stay on
the harness.

Must run under a python that has `swebench` + `datasets` (the .venv_swe). The
Makefile target `agentic-swe-images` invokes it with $SWEBENCH_PY.

Usage:
  python build_swe_images.py                 # all materialized swe-* tasks
  python build_swe_images.py <iid> [<iid>…]  # specific instance_ids
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

import docker
from datasets import load_dataset
from swebench.harness.docker_build import build_env_images, build_instance_images

DATASET = "princeton-nlp/SWE-bench_Verified"
TASKS = Path(__file__).resolve().parent.parent / "tasks"


def materialized_instance_ids() -> list[str]:
    ids = []
    for tj in sorted(TASKS.glob("swe-*/task.json")):
        t = json.loads(tj.read_text())
        if t.get("instance_id"):
            ids.append(t["instance_id"])
    return ids


def main() -> int:
    want = sys.argv[1:] or materialized_instance_ids()
    if not want:
        print("no SWE instance ids to build", file=sys.stderr)
        return 1
    ds = load_dataset(DATASET, split="test")
    by_id = {x["instance_id"]: dict(x) for x in ds}
    recs = [by_id[i] for i in want if i in by_id]
    missing = [i for i in want if i not in by_id]
    if missing:
        print(f"[build] WARNING not in dataset: {missing}", file=sys.stderr)
    client = docker.from_env()
    print(f"[build] building images for {len(recs)} instances "
          f"(base -> env -> instance; x86_64, emulated on arm64)…", flush=True)
    # tags are required by this swebench version; "latest" matches container_exec.
    # Env images are shared per repo-version; build them first (tolerate failures
    # so one bad env doesn't block the rest).
    try:
        build_env_images(client, recs, False, 4, None, "latest", "latest")
    except Exception as e:
        print(f"[build] some env images failed (continuing): {e}", flush=True)
    # Build instance images ONE AT A TIME so a single failure (e.g. a missing env
    # image under emulation) never aborts the others. Idempotent/resumable: skip
    # instances whose image already exists.
    from container_exec import instance_image, has_image  # local import
    built, skipped, failed = [], [], []
    for rec in recs:
        iid = rec["instance_id"]
        if has_image(instance_image(iid)):
            skipped.append(iid)
            continue
        try:
            build_instance_images(client, [rec], False, 1, None, "latest", "latest")
            built.append(iid)
        except Exception as e:
            failed.append((iid, str(e).splitlines()[0][:120]))
            print(f"[build] FAILED {iid}: {str(e).splitlines()[0][:120]}", flush=True)
    print(f"[build] done: built={len(built)} already={len(skipped)} failed={len(failed)}")
    if failed:
        print("[build] failed instances:", [f[0] for f in failed])
    return 0


if __name__ == "__main__":
    sys.exit(main())
