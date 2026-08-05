# Sample corpus + artifact spec (for the offline label engine)

This documents the exact artifacts the log-first generation produces, so the
**offline scoring engine** (deferred in DATA_PLAN — this is its input contract)
can be built against them. A complete worked example ships in this commit:
`gen-bugfix-wordcount` (both arms). Real SWE-bench Verified sessions accrue in
`agentic/results/` as the in-container batch runs.

**Hard rule the engine must honor:** generation assigns **no outcome**. Every
artifact below is pre-outcome; the engine's whole job is to add outcomes from
(a) the executed oracle, (b) an LLM judge, or (c) signal heuristics.

## Per-session artifacts (in `agentic/results/`)

Naming: `<task_id>__<arm>__<config_hash>.<ext>`. One session = one `(task, arm)`.

| file | what it is | who consumes it |
|---|---|---|
| `…​.json` | **run record** — metadata + mined metrics, NO outcome | engine keys on `provenance`/`grounding`/`has_executable_oracle`/`execution`; UI |
| `…​.session.jsonl` | **portable RawTurn log** (`internal/schema.RawTurn`) | `internal/extract` → pointwise/pairwise; LLM-judge; signal heuristics |
| `…​.events.jsonl` | **raw Claude Code event stream** (every `tool_use`/`tool_result`, usage) | fidelity mining; UI trace; anything needing per-tool detail |
| `…​.patch` | the agent's `git diff` | **executed oracle** (apply to base repo) |

Plus one corpus-level file:

| file | what it is |
|---|---|
| `split_manifest.json` | per session → `{split: train\|holdout, provenance, grounding, has_executable_oracle}`. **Grade the executed-oracle gold set on `holdout` only; weak-label `train`.** Deterministic (seed 1337, by task). |

### Run record fields (`…​.json`)

```jsonc
{
  "task_id", "arm", "served_model",        // "opus" | "gpt-oss:20b"
  "provenance",   // "swe_verified" | "synthetic" | "templated"(quarantined)
  "grounding",    // "benchmark" | "oss_history" | "synthetic_repo"
  "execution",    // "container" (real swebench env) | "host"
  "has_executable_oracle": true,
  "split": null,                            // filled from split_manifest
  "session_log_path", "events_log_path", "patch_path",
  "metrics": {"num_turns", "tool_calls_attempted", "native_tool_calls",
              "rescued_tool_calls", "tool_calls_errored", "input/output/total_tokens",
              "wall_clock_s", "timed_out", "hit_turn_cap", "empty_patch",
              "reported_cost_usd", "result_subtype", "is_error"},
  "config_hash"
  // NOTE: no `resolved` / `outcome` — that's the engine's job.
}
```

### RawTurn (`…​.session.jsonl`, one object per line)

```jsonc
{"session_id", "turn_index", "timestamp", "role": "user"|"assistant",
 "content", "served_model"?, "propensity": null}
```
`propensity` is **null by construction** — every task runs on every rung
deterministically, so there is no logging policy and no propensity. Off-policy
(IPS/DR) is therefore out of reach for this corpus; use the dual-arm gold for
absolute numbers. Multi-turn sim-user sessions (Phase 3) alternate user/assistant
and can carry a real local→frontier escalation (a served-model change across
turns).

## The task-dir seam (the oracle the engine grades against)

Each task lives in `agentic/tasks/<id>/`:

```
task.json     # id, tier, issue, test_cmd, fail_to_pass[], pass_to_pass[],
              # provenance, grounding, grader, (instance_id/repo/base_commit for SWE)
repo/         # buggy base the agent saw   (SWE repo/ is gitignored — regenerate
              #   via materialize_swe.py + tasks/swe_selection.json)
_oracle/      # test_patch.diff + gold_patch.diff  — NEVER inside repo/ (firewall)
```

## How the engine turns an artifact into an outcome

1. **Executed oracle (strongest; `has_executable_oracle` + `holdout`).**
   `outcome = resolved(base repo + <session>.patch + _oracle/test_patch)`:
   - `grader=="swebench"` → the official swebench harness (`run_swe_arm.py:grade`,
     parked for reuse) applies `test_patch` and runs F2P/P2P in the per-instance
     image. **Run the agent's patch in the SAME image the agent ran in**
     (`execution=="container"`) so grade env == generation env.
   - `grader=="docker_pytest"` → the in-repo `executor.py:score_checkout`
     (self-contained tasks). Resolved iff all F2P pass AND all P2P still pass.
2. **LLM-as-judge (medium).** Frontier judge over `(issue, session.jsonl assistant
   content)` → adequacy. Use for `train` where no executed oracle is desired.
3. **Signal heuristics (weak).** Mine the user turns in `session.jsonl` (sim-user
   reactions: paste_error / correction / switch / moveon) → implicit labels.
   `internal/extract` already derives these; the engine can reuse it.

`internal/schema` already defines the output shapes the engine fills:
`PointwiseRow` (`outcome`, `label_source`), `PairwiseRow`, `GoldRow` (dual-arm
`outcome_local`/`outcome_frontier`, `executable`).

## Worked example — `gen-bugfix-wordcount` (in this commit)

A `word_count` that splits on a single space (so `"  a   b  "` miscounts).
FAIL_TO_PASS: `test_word_count_extra_spaces`; PASS_TO_PASS: `test_word_count_basic`.
`_oracle/gold_patch.diff` fixes it (`split(" ")` → `split()`).

- `gen-bugfix-wordcount__local__*` — served by gpt-oss:20b, 5/5 native tool calls,
  non-empty patch.
- `gen-bugfix-wordcount__frontier__*` — served by opus.
- To label: apply each arm's `.patch` to `tasks/gen-bugfix-wordcount/repo/` and run
  the tests (`grader=="docker_pytest"`) → the two arms' outcomes → one `GoldRow`.

Real SWE examples (executed via the swebench harness, `execution=container`) land
as the batch completes; they are where the router's local-fails/frontier-passes
signal lives.
