# Agentic, execution-grounded evaluation track

This directory adds an **agentic** arm to the router framework. The base repo
labels each task with a single-shot LLM-judge verdict on templated text — which
is (a) **not agentic** (real Claude Code drives a multi-turn tool loop over a
repo) and (b) **circular** (the judge that trains also grades). This track fixes
both: it runs each task to completion **inside the real Claude Code harness** for
a local and a frontier model, and labels the outcome by **executing the repo's
hidden tests** (a non-circular oracle). It fills the `GoldRow.Executable` seam.

## The Go / Python boundary (see DECISIONS D12)

The repo keeps a strict portable-Go core. This track honors it:

- **Go, stdlib-only (portable):** the schema (`GoldRow.Executable`), the gold
  assembly + scoring integration (`internal/agentic`, `cmd/agentic`), and the
  entire eval harness that consumes the executed gold set unchanged.
- **Python + Docker (non-portable, lives here under `agentic/`):** the
  harness-driving glue and test execution — things Go should not carry:
  driving the `claude` CLI, an Anthropic→Ollama translation proxy, and
  Docker-based pytest execution.

## Layout

```
agentic/
  proxy/ollama_anthropic_proxy.py  Anthropic /v1/messages  ->  Ollama /api/chat
  proxy/proxyctl.sh                start|stop|health for the proxy
  runner/build_tasks.py            materialize + self-validate the task set
  runner/validate_tasks.py         fail-before / pass-after check per task
  runner/executor.py               run a checkout's tests in Docker (the oracle)
  runner/run_agentic.py            drive claude -p per (task,arm); mine metrics
  runner/report.py                 write RESULTS_AGENTIC.md
  exec/Dockerfile                  hermetic python:3.11-slim + pytest image
  tasks/<id>/                      task.json + repo/ (buggy) + _reference/ (fix)
  results/<task>__<arm>__<hash>.json   cached per-(task,arm) executed results
```

## The two arms (same tool protocol, measured for fidelity)

| arm | model | auth | harness |
|---|---|---|---|
| **frontier** | `claude-sonnet` (latest, CLI alias) | logged-in subscription | `claude -p … --strict-mcp-config --model sonnet` |
| **local** | `qwen2.5-coder:14b` (Ollama) | `ANTHROPIC_BASE_URL`→proxy, dummy key | same, plus `--bare` |

The proxy exposes an Anthropic Messages API (`POST /v1/messages`, with
`tool_use`/`tool_result` content blocks and SSE streaming) and translates to
Ollama's `/api/chat`, so **the local model drives the exact same tool protocol
as frontier**. That is the whole point: it lets us measure **tool-call
fidelity**, not just reasoning. The proxy logs, per response, whether a tool
call arrived as a **native** Ollama `tool_calls` entry or had to be **rescued**
from bare prose-JSON the model emitted as text (`qwen2.5-coder` does the latter
~100% of the time — see RESULTS_AGENTIC.md).

`--bare` on the local arm is a tractability necessity: the full Claude Code
system prompt is ~30k tokens, which costs **~8 min/turn** on a local 14B model;
`--bare` trims it to ~1k tokens/turn. It also skips keychain reads (which the
local arm doesn't need). See DECISIONS D13.

## Run it

```
make agentic-smoke     # 1 task, BOTH arms, tool-call fidelity smoke
make agentic           # full pipeline (resumable/cached): both arms on all
                       # tasks -> executed gold -> existing eval harness ->
                       # RESULTS_AGENTIC.md
```

Prerequisites: Docker (execution oracle), Ollama with `qwen2.5-coder:14b`
(`ollama pull qwen2.5-coder:14b`) + `nomic-embed-text`, and a logged-in `claude`
CLI. Everything is cached per (task, arm), so an interrupted overnight run
resumes without re-paying; the frontier arm is USD-capped (`MAX_FRONTIER_USD`).

## Tasks (execution ground truth)

Docker **is** available here, so execution runs in containers. The task set is a
**curated, self-contained** SWE-bench-style set (11 real Python bug-fix tasks
with FAIL_TO_PASS/PASS_TO_PASS pytest suites, spanning easy→hard) rather than a
subset of upstream SWE-bench Verified — chosen for a controllable difficulty
spread (the routing signal needs some tasks local can do and some it can't) and
a tractable, hermetic overnight run. Each task is auto-validated fail-before /
pass-after. See DECISIONS D14 for why, and how to point the same runner at real
SWE-bench Verified.
