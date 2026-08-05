#!/usr/bin/env python3
"""Containerized agent execution — run `claude -p` INSIDE the official SWE-bench
per-instance image so the agent operates in the REAL environment (correct Python
+ pinned deps + repo@base_commit at /testbed), can actually run the tests and
iterate, and generation env == grading env. Fixes the host-execution gap where
the agent could not run the real suite (ModuleNotFoundError / dep conflicts) and
polluted the host.

Design (disk-lean): rather than bake `claude` into 26 per-instance images, a
shared "toolbox" (a Linux/x86 node runtime + the `@anthropic-ai/claude-code` npm
package) is built ONCE into a host dir and bind-mounted read-only into each
instance container; `claude` runs there via `docker exec`. The instance image is
the official `sweb.eval.x86_64.<id>` (pulled/built by the swebench harness), so we
stay on the harness.

Networking: the container needs network for LLM calls. The LOCAL arm reaches the
host Anthropic->Ollama proxy at host.docker.internal:8790 (fidelity still logged
host-side); the FRONTIER arm uses CLAUDE_CODE_OAUTH_TOKEN (subscription, from
`claude setup-token`) — the macOS Keychain is not reachable inside Linux.

x86_64 images run under emulation on Apple Silicon (slower but correct).
"""

from __future__ import annotations

import json
import os
import subprocess
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
TOOLBOX_DIR = HERE.parent / ".claude_toolbox"   # host dir, bind-mounted into containers
PLATFORM = os.environ.get("AGENT_PLATFORM", "linux/amd64")
NODE_IMAGE = os.environ.get("AGENT_NODE_IMAGE", "node:20-slim")
CLAUDE_NPM = os.environ.get("CLAUDE_NPM_SPEC", "@anthropic-ai/claude-code")
# The frontier arm authenticates as the Max SUBSCRIPTION via a long-lived token
# (`claude setup-token`). To avoid ever handling the plaintext credential, the
# token lives in an env-file that docker reads directly (--env-file); this code
# only references the PATH, never the contents. The file must contain a line:
#   CLAUDE_CODE_OAUTH_TOKEN=<token>
OAUTH_ENV_FILE = os.environ.get("AGENT_OAUTH_ENV_FILE",
                                os.path.expanduser("~/.claude_oauth.env"))


def instance_image(instance_id: str) -> str:
    return f"sweb.eval.x86_64.{instance_id}:latest"


def _docker(*args, **kw):
    return subprocess.run(["docker", *args], capture_output=True, text=True, **kw)


def has_image(name: str) -> bool:
    return _docker("image", "inspect", name).returncode == 0


def ensure_toolbox() -> Path:
    """Build the shared node+claude toolbox once into TOOLBOX_DIR (Linux/x86).
    Idempotent: skips if claude is already present."""
    claude_bin = TOOLBOX_DIR / "npm" / "bin" / "claude"
    node_bin = TOOLBOX_DIR / "node"
    if claude_bin.exists() and node_bin.exists():
        return TOOLBOX_DIR
    TOOLBOX_DIR.mkdir(parents=True, exist_ok=True)
    print(f"[container] building claude toolbox in {TOOLBOX_DIR} (once)…")
    # Copy the node binary + install claude (prefix) into the mounted dir so both
    # the runtime and the CLI travel together into instance containers.
    script = (
        "set -e; cp \"$(command -v node)\" /out/node; "
        f"npm install -g --prefix /out/npm {CLAUDE_NPM}; "
        "echo TOOLBOX_OK"
    )
    r = _docker("run", "--rm", "--platform", PLATFORM,
                "-v", f"{TOOLBOX_DIR}:/out", NODE_IMAGE, "bash", "-lc", script)
    if r.returncode != 0 or "TOOLBOX_OK" not in r.stdout:
        raise RuntimeError(f"toolbox build failed:\n{r.stdout}\n{r.stderr}")
    return TOOLBOX_DIR


def _claude_cmd(arm_cfg) -> str:
    parts = [
        "claude", "-p",
        "--output-format", "stream-json", "--verbose",
        "--max-turns", str(arm_cfg["max_turns"]),
        "--allowedTools", "Read", "Edit", "Write", "Bash",
        "--permission-mode", "bypassPermissions",
        "--strict-mcp-config",
        "--model", arm_cfg["model"],
    ]
    if arm_cfg["bare"]:
        parts.append("--bare")
    return " ".join(parts)


def run_agent_in_container(instance_id, arm, arm_cfg, prompt, *,
                           proxy_url="http://host.docker.internal:8790",
                           timeout=1200):
    """Run one agent turn inside the instance container. Returns
    (events, timed_out, wall_s, patch, raw, stderr). The agent's Bash/Edit/Write
    act on /testbed in the REAL env; the diff is captured from /testbed."""
    img = instance_image(instance_id)
    if not has_image(img):
        raise RuntimeError(
            f"missing image {img} — build it first: `make agentic-swe-images`")
    ensure_toolbox()

    cname = f"agent_{instance_id}_{arm}_{int(time.time()*1000)}"
    path_env = "/opt/claude:/opt/claude/npm/bin:/usr/local/bin:/usr/bin:/bin"
    # IS_SANDBOX=1 lets `claude -p --permission-mode bypassPermissions` run as root
    # (swebench containers are root); without it claude refuses for safety.
    docker_flags = ["-e", f"PATH={path_env}", "-e", "IS_SANDBOX=1"]
    if arm_cfg["use_proxy"]:
        docker_flags += ["-e", f"ANTHROPIC_BASE_URL={proxy_url}",
                         "-e", "ANTHROPIC_API_KEY=dummy-local-key"]
    else:
        # Frontier: subscription token via --env-file (docker reads it; this
        # process never touches the plaintext credential).
        if not os.path.exists(OAUTH_ENV_FILE):
            raise RuntimeError(
                f"frontier arm needs the subscription token file {OAUTH_ENV_FILE} "
                f"(a line 'CLAUDE_CODE_OAUTH_TOKEN=<token>' from `claude setup-token`)")
        docker_flags += ["--env-file", OAUTH_ENV_FILE]

    # 1. start a long-lived container from the instance image (repo@base at /testbed)
    up = _docker("run", "-d", "--name", cname, "--platform", PLATFORM,
                 "-w", "/testbed",
                 "-v", f"{TOOLBOX_DIR}:/opt/claude:ro",
                 *docker_flags, img, "sleep", "infinity")
    if up.returncode != 0:
        raise RuntimeError(f"container start failed: {up.stderr}")
    try:
        # ensure /testbed is a clean git repo so `git diff` captures the agent's edits
        _docker("exec", cname, "bash", "-lc",
                "cd /testbed && git config --global --add safe.directory /testbed "
                "&& git add -A && git stash -q --include-untracked 2>/dev/null; "
                "git stash drop -q 2>/dev/null; true")
        # 2. run the agent, prompt piped via stdin (avoids shell-quoting the prompt)
        t0 = time.time()
        timed_out = False
        try:
            proc = subprocess.run(
                ["docker", "exec", "-i", cname, "bash", "-lc", _claude_cmd(arm_cfg)],
                input=prompt, capture_output=True, text=True, timeout=timeout)
            raw, stderr = proc.stdout, proc.stderr
        except subprocess.TimeoutExpired as e:
            raw = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
            stderr = e.stderr.decode() if isinstance(e.stderr, bytes) else (e.stderr or "")
            timed_out = True
        wall = time.time() - t0
        events = []
        for line in raw.splitlines():
            line = line.strip()
            if line:
                try:
                    events.append(json.loads(line))
                except Exception:
                    pass
        # 3. capture the patch from inside the container
        diff = _docker("exec", cname, "bash", "-lc", "cd /testbed && git add -A && git diff --cached")
        patch = diff.stdout if diff.returncode == 0 else ""
        return events, timed_out, wall, patch, raw, stderr
    finally:
        _docker("rm", "-f", cname)


if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser(description="smoke: run the agent in a SWE container")
    ap.add_argument("--instance", required=True)
    ap.add_argument("--arm", default="local", choices=["local", "frontier"])
    args = ap.parse_args()
    arm_cfg = {"model": "claude-sonnet-4" if args.arm == "local" else
               os.environ.get("FRONTIER_MODEL", "opus"),
               "bare": args.arm == "local", "max_turns": 15,
               "use_proxy": args.arm == "local"}
    ev, to, wall, patch, raw, err = run_agent_in_container(
        args.instance, args.arm, arm_cfg,
        "Read the repo, run the tests to see the failure, fix the bug in the "
        "source (not the tests), and re-run the tests until they pass.",
        timeout=900)
    print(f"events={len(ev)} timed_out={to} wall={wall:.0f}s patch_bytes={len(patch)}")
    print("PATCH:\n", patch[:800])
