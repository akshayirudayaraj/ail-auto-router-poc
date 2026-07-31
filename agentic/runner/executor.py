#!/usr/bin/env python3
"""
Execution oracle: run a repo checkout's pytest suite inside the hermetic Docker
image and apply the SWE-bench pass/fail rule.

A checkout PASSES iff every FAIL_TO_PASS test passes AND every PASS_TO_PASS test
still passes. This is the only non-circular label in the whole framework: the
agent's produced code either makes the hidden tests pass or it does not.
"""
import json
import subprocess
import tempfile
import os

IMAGE = os.environ.get("AGENTIC_IMAGE", "agentic-runner:py311")


def run_pytest_in_docker(checkout_dir: str, test_paths: list[str], timeout: int = 300) -> dict:
    """Run the given pytest node ids in Docker against checkout_dir.

    Returns {passed: [...], failed: [...], errored: bool, raw: str}. The
    checkout is mounted read-only-ish (bind mount); tests run offline
    (--network none).
    """
    # Use pytest's JSON-ish machine output via -rA + exit code parsing. We run
    # each node explicitly so a collection error on one file doesn't mask others.
    with tempfile.NamedTemporaryFile("w", suffix=".py", delete=False) as _:
        pass
    args = [
        "docker", "run", "--rm", "--network", "none",
        "-v", f"{checkout_dir}:/work",
        "-w", "/work",
        IMAGE,
        "python", "-m", "pytest", "-p", "no:cacheprovider", "-q",
        "--no-header", "-rN",
    ] + test_paths
    try:
        proc = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
        raw = proc.stdout + "\n" + proc.stderr
    except subprocess.TimeoutExpired:
        return {"passed": [], "failed": test_paths, "errored": True,
                "raw": "TIMEOUT", "timed_out": True}

    passed, failed = _parse_pytest(raw, test_paths)
    return {"passed": passed, "failed": failed,
            "errored": proc.returncode not in (0, 1), "raw": raw,
            "returncode": proc.returncode}


def _parse_pytest(raw: str, test_paths: list[str]) -> tuple[list[str], list[str]]:
    """Determine per-node pass/fail. We asked pytest to run exactly test_paths;
    parse the PASSED/FAILED/ERROR lines. Fallback: a node not reported as passed
    counts as failed."""
    passed, failed = set(), set()
    for line in raw.splitlines():
        s = line.strip()
        # pytest -rN doesn't emit per-test; rely on the compact result markers
        # emitted with -rA-style. We instead re-run parse over the summary.
        if "PASSED" in s:
            passed.add(s.split(" ")[0])
        elif "FAILED" in s or "ERROR" in s:
            failed.add(s.split(" ")[0])
    # If markers absent (default -q), fall back to explicit per-node runs.
    reported = passed | failed
    unknown = [t for t in test_paths if t not in reported]
    return list(passed), list(failed) + unknown


def score_checkout(checkout_dir: str, task: dict, timeout: int = 300) -> dict:
    """Run FAIL_TO_PASS + PASS_TO_PASS and apply the SWE-bench rule.

    Runs each node id individually so we get a reliable per-node verdict
    regardless of pytest summary formatting.
    """
    f2p = task["fail_to_pass"]
    p2p = task["pass_to_pass"]
    results = {}
    all_nodes = f2p + p2p
    # Run the whole set once; if parsing is ambiguous, run node-by-node.
    for node in all_nodes:
        r = run_pytest_in_docker(checkout_dir, [node], timeout=timeout)
        ok = (r.get("returncode") == 0) and not r.get("timed_out")
        results[node] = {"passed": ok, "timed_out": r.get("timed_out", False)}

    f2p_pass = all(results[n]["passed"] for n in f2p) if f2p else True
    p2p_pass = all(results[n]["passed"] for n in p2p) if p2p else True
    resolved = bool(f2p_pass and p2p_pass)
    return {
        "resolved": resolved,
        "fail_to_pass_ok": f2p_pass,
        "pass_to_pass_ok": p2p_pass,
        "per_node": results,
    }


if __name__ == "__main__":
    import sys
    checkout, taskjson = sys.argv[1], sys.argv[2]
    task = json.load(open(taskjson))
    print(json.dumps(score_checkout(checkout, task), indent=2))
