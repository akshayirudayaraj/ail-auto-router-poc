# TASK_SPEC — authoring a Source-3 (generated) task

This is the exact contract a Claude-Max session fills in to produce ONE runner-ready
task. Follow it precisely: `agentic/synth/validate_task.py` is the gate, and it
rejects anything that deviates. Generation is interactive (a Claude session authors
the task); the repo supplies the **spec (this file) + gate**, not an API generator.

The task, once accepted, is driven by the UNMODIFIED Phase-1 runner (`run_agentic.py`)
across both rungs, exactly like a SWE-bench task. **No grading happens at generation
time** — you author the task + its oracle; outcomes are assigned later by the offline
engine.

## Directory layout

```
agentic/tasks/<id>/
  task.json          # metadata + the F2P/P2P oracle split (schema below)
  repo/              # the BUGGY base repo the agent sees (issue lives in the repo tree)
  _oracle/
    gold_patch.diff  # the reference fix as a unified diff (a/… b/… prefixes)  [option A]
  _reference/        # OR the reference-fixed file tree (mirrors repo/ paths)   [option B]
```

Provide the reference fix as EITHER `_oracle/gold_patch.diff` (preferred, `patch -p1`
applies it) OR a `_reference/` file tree — not both required. `_oracle/` and
`_reference/` MUST live outside `repo/` (firewall).

## task.json schema (all keys required unless marked optional)

```json
{
  "id": "gen-<archetype>-<slug>",          // unique; dir name is agentic/tasks/<id>/
  "tier": "gen-easy | gen-medium | gen-hard",
  "issue": "Problem statement the agent sees. Describe the BUG/'REQUIRED behavior only. MUST NOT contain the fix, a diff, or verbatim solution code.",
  "test_cmd": "python -m pytest -q",       // how the agent runs tests in repo/
  "fail_to_pass": ["tests/test_x.py::test_a"],   // FAIL on buggy base, PASS after fix
  "pass_to_pass": ["tests/test_x.py::test_b"],   // PASS before AND after (guards regressions)
  "provenance": "synthetic",               // fixed value for Source 3
  "grounding": "oss_history | synthetic_repo",   // prefer oss_history
  "has_executable_oracle": true,
  "grader": "docker_pytest",               // offline grader that will score runs later
  "archetype": "bug-fix | refactor | feature | test-writing | migration",  // optional but encouraged
  "language": "python",                    // optional
  "source_repo": "owner/name",             // optional (oss_history: the real repo)
  "source_commit": "<sha>"                 // optional (oss_history: the bug-fix commit)
}
```

## Grounding (prefer real OSS history)

- **oss_history (preferred).** Snapshot a permissively-licensed repo (see
  `oss_repos.md`) at a real bug-fix commit's PARENT (the buggy state) into `repo/`.
  The commit's added/changed tests become FAIL_TO_PASS; still-passing related tests
  become PASS_TO_PASS. You author `ISSUE`/select the split — you do NOT invent the
  tests, so the oracle stays real and executable. Record `source_repo`/`source_commit`.
- **synthetic_repo (fallback).** Where no clean history fits a target archetype,
  author a small self-contained repo + tests (like the curated `build_tasks.py` set,
  but in-chat). Weaker grounding; use only when necessary.

## Hard rules the gate enforces (read before authoring)

1. **Firewall.** The hidden FAIL_TO_PASS test bodies must NOT appear in `repo/`.
   `_oracle/`/`_reference/` must be OUTSIDE `repo/`. The ISSUE must not embed the fix
   (no pasted diff; no ≥2 gold-added code lines reproduced verbatim).
2. **Fail-before / pass-after.** On the buggy `repo/`, every FAIL_TO_PASS test must
   FAIL and every PASS_TO_PASS must PASS. After applying the reference fix, ALL of
   F2P + P2P must PASS. If either half is off, the oracle is meaningless → rejected.
3. **Runs in the executor.** Tests must run under the hermetic `python:3.11-slim` +
   pytest image (`agentic/exec/`), `--network none`. No network, no extra system deps
   beyond what the image + repo provide. Keep repos small.

## Diversify + target the decision boundary

Spread authored tasks across archetype (bug-fix / refactor / feature / test-writing /
migration), language, and difficulty. After a first batch runs and is scored offline,
author MORE tasks in the difficulty band that turns out **local-fails / frontier-passes**
— that boundary is where the router earns its keep.

## Acceptance

Run `python3 agentic/synth/validate_task.py <id>`. Expect `ACCEPT <id>: …`. Only then
is the task admissible into the corpus and run through `run_agentic.py`.
