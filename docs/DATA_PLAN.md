# Data Plan — generate solid router data (agentic, log-first)

> Handoff doc for the next agent/session. Written 2026-08-05. Owner: Akshay.
> Read this top-to-bottom before touching code. Then read, in order:
> `docs/archive/STEP1_PLAN.md` (what "Step 1" already delivered), `ROUTER_BRAINSTORM.md`
> **§2A** (the data-source definitions this plan implements), and `DECISIONS.md`
> **D11-ag–D16** (the agentic track's existing choices).

---

## Scope — read this first

**IN scope: DATA GENERATION and DATA SLICING.** Produce trustworthy session logs
by running the real Claude Code harness agentically across the model rungs, then
deterministically slice them into train / held-out sets. That is the entire job
right now.

**OUT of scope: the offline scoring engine** (deferred, designed later). It has
three branches, all of which *consume* logs and none of which run during
generation:

1. **Executed oracle** — run a task's hidden tests against the produced patch →
   non-circular pass/fail. Two implementations (see "grading paths" below). This
   is the strong label for the eval gold set; do NOT throw it away.
2. **LLM-as-a-judge** — a frontier judge over `(prompt, response)` → adequacy.
3. **Signal heuristics** — regex + LLM over user behavior → weak implicit labels.

The single hard rule that follows: **no grading, judging, or outcome scoring
happens during log generation.** The agentic run emits a log (and preserves the
artifacts an oracle would later need) and stops. Everything that assigns an
*outcome* is the offline engine's job, later.

---

## 0. Orientation — what this repo is (for an agent with no context)

`ail-routing-test` is the POC harness for a **predictive auto-router** for Claude
Code: for every CC call, pick the cheapest model still *adequate* on a 2-rung
ladder `{local open-weight, frontier Claude}`; default local, burst to frontier
only when local would fail. This repo builds the **data → train → eval** tooling.
It does **not** build meta-routing or the cascade branch (both intentionally out).

Three pillars, all already wired end-to-end:

- **Pillar 1 — label engine** (`internal/generate`, `internal/extract`).
- **Pillar 2 — routers** (`internal/router`): RouteLLM-logistic, IRT-1PL, kNN real;
  encoder-MLP / SLM-head are Go stubs.
- **Pillar 3 — eval** (`internal/eval`): dual-arm gold, temporal backtest,
  off-policy IPS+DR, guardrail suite, policy layer.

Plus an **agentic, execution-grounded track** (`agentic/` Python + Docker, bridged
into Go by `internal/agentic` + `cmd/agentic`) that runs the REAL `claude -p`
harness on a task for a local and a frontier arm. This plan makes that track the
*only* way logs are produced.

### Non-negotiable invariants (violating any silently corrupts the data)

1. **Go core stays stdlib-only.** Everything in `internal/`, `cmd/` is portable Go
   with zero third-party modules (it ports into the gateway). All orchestration —
   SWE-bench batching, the simulated user, any Docker/HTTP glue — lives under
   `agentic/` in **Python**. No new Go deps. (DECISIONS D1.)
2. **One serving path.** The only way a log is produced is an **agentic run**: the
   `claude -p` tool loop, with the local arm reaching Ollama through the
   Anthropic→Ollama proxy (`agentic/proxy/`). The old single-shot `messages→text`
   serving path (`internal/backend` Ollama `/api/generate`) is NOT used to generate
   logs; it survives only for the deferred judge/offline engine.
3. **No grading during generation.** The runner captures the transcript, the diff,
   and fidelity/turn/token/latency metrics, preserves the task's oracle artifacts,
   and stops. It never runs tests or a judge. (This is a change — see Phase 1.)
4. **Ground-truth firewall holds for every task.** The agent sees ONLY the problem
   statement + the repo at its base (buggy) state. Hidden tests (`test_patch`) and
   any reference fix (`gold_patch` / `_reference`) live OUTSIDE the agent-visible
   `repo/` (in `_oracle/`), preserved for offline grading. `test_firewall.py` must
   pass. Generated tasks (Phase 2B) add a new leak vector — the issue text — so the
   firewall check must also assert the issue contains no solution.
5. **RTX-6000 / Gemma box is off-limits** (D14/Step 4). Throwaway Ollama only.
6. **Spend discipline.** Frontier `$` hard-capped (`MAX_FRONTIER_USD`, default $6);
   every `(task, arm, config-hash)` result cached to `agentic/results/`;
   interrupted runs resume free.

### The task-directory contract (the seam every task source plugs into)

`agentic/runner/run_agentic.py` loads **any** `agentic/tasks/<id>/task.json` via
glob, so a "task source" is anything that writes this layout:

```text
agentic/tasks/<id>/
  task.json      # {id, tier, issue, test_cmd, fail_to_pass[], pass_to_pass[],
                 #  provenance, grounding, grader}
  repo/          # the buggy repo handed to the agent (issue in ISSUE.md); tests present but graded offline
  _oracle/       # (real-SWE / generated) test_patch + gold_patch — NEVER in repo/, preserved for offline grading
  _reference/    # (curated) reference-fixed files — validation only, NEVER in repo/
```

Two task sources matter now:

- **Source 2 — semi-synthetic (SWE-bench Verified).** Tasks are *given*, not
  generated. `materialize_swe.py` (Phase 2A scales it).
- **Source 3 — fully synthetic (generated).** Task + repo/harness/oracle are
  **generated interactively via a Claude Max instance** (not an automated API
  generator in this repo). Grounded in real OSS history where possible. The repo's
  job is the task-dir **spec + validation gate**, not the generation itself
  (Phase 2B).

The existing templated generator `internal/generate` is demoted to a plumbing/CI
fixture only (Phase 6) — its outputs are never treated as signal.

### Generation spec — how much, which models, what schema

**The generation unit is one `(task, model)` session.** Every task is run once per
model rung, so a roster of K models × T tasks = K·T agentic sessions. No outcome is
assigned at generation time (that's the offline engine).

**Model roster (this test).** A **configurable list** — adding a model = adding an
arm, and the schema keys on `served_model`, so nothing else changes:

- **frontier = `opus`** (CLI alias, latest Opus, via the logged-in Claude
  subscription — no API key/billing). *Change from today's `sonnet` default:* set
  `FRONTIER_MODEL=opus`. Opus is pricier per token, so the `MAX_FRONTIER_USD` cap
  matters more — keep it and log running spend.
- **local = `gpt-oss:20b`** (Ollama, via the Anthropic→Ollama proxy). Confirmed
  100% native tool-call fidelity (D16).
- The roster is `ARM_MODELS` (ordered cheap→expensive); K=2 now, but IRT/kNN/gold
  all generalize to K>2 if a mid-rung is added later.

**Volume targets (knobs, not laws — bounded by local GPU throughput + frontier $).**

| Source | tasks | × models | = sessions | notes |
|---|--:|--:|--:|---|
| 2A — SWE-bench Verified | 20–50 | 2 | 40–100 | real-benchmark distribution |
| 2B — generated (Claude Max) | 10–30 | 2 | 20–60 | OSS-grounded, diversified |
| **total** | **30–80** | **2** | **60–160** | overnight-tractable, resumable/cached |

With the simulated user (Phase 3) each session is multi-turn (≈2–6 turns), so
turn-count is a few× the session count. Start at the low end (≈20 SWE + ≈10
generated) to validate end-to-end, then scale. Every `(task, arm, config-hash)` is
cached, so scaling is incremental and interrupted runs resume free.

**Output schema — three artifacts per session, plus a corpus manifest.** Nothing
here carries an `outcome`; outcomes are added later by the offline engine.

1. **Portable session log — `RawTurn` JSONL** (one object per turn; the schema
   `internal/schema.RawTurn` already defines this and `internal/extract` consumes
   it unchanged):

   ```text
   {session_id, turn_index, timestamp, role, content,
    served_model,            // set on assistant turns: "opus" | "gpt-oss:20b"
    propensity?,             // logging-policy prob (for off-policy); null for deterministic arms
    _true_*?}                // hidden seams (grader-only), populated only where cheaply known
   ```

2. **Rich trace — raw CC event stream** (the full `claude -p --output-format
   stream-json` events: every `tool_use`, `tool_result`, native-vs-rescued fidelity,
   token usage). Feeds the UI trace drill-in (Phase 5) and fidelity mining. This is
   the detail the flat `RawTurn` intentionally drops.

3. **Run record — per `(task, arm)` JSON** (extend `internal/agentic.Result`;
   **drop `resolved`/`fail_to_pass_ok` — no grading at generation**):

   ```text
   {task_id, arm, served_model, provenance, grounding, tier, split,
    session_log_path, patch_path, has_executable_oracle,
    metrics:{assistant_turns, tool_calls, native_tool_calls, rescued_tool_calls,
             tool_errors, input_tokens, output_tokens, total_tokens,
             wall_clock_s, timed_out, hit_turn_cap, empty_patch, reported_cost_usd},
    config_hash}
   ```

4. **Corpus manifest — `split_manifest.json`** (Phase 4): per task/session →
   `{split: train|holdout, provenance, grounding, has_executable_oracle}`.

**Downstream shapes (filled later, not now).** `internal/schema` already defines
`PointwiseRow` (per `(model, prompt)` → `outcome`, `label_source`), `PairwiseRow`,
and `GoldRow` (dual-arm `outcome_local`/`outcome_frontier`, `executable`). The
offline engine populates their `outcome` fields from the logs + preserved oracle;
generation only produces artifacts 1–4 above.

**`propensity` stays null in this corpus — and that is correct.** Every task is run
on every rung deterministically (no logging policy is *choosing* a model), so there
is no propensity to record. Off-policy evaluation (IPS / doubly-robust) needs
ε-greedy propensities, which only exist once a *real router* is serving live traffic
and exploring. So off-policy eval is **out of reach for synthetic/semi-synthetic
data by construction** — it comes online only after deployment. This matches the
brainstorm (§6): the off-policy method "cannot help choose the first router." Do not
try to synthesize propensities; leave the field null and rely on the dual-arm gold
(both rungs observed on every task) for absolute numbers instead.

---

## 1. Why this plan exists

The plumbing is proven; the DATA is not yet trustworthy. Three problems, fixed
here at the generation layer (scoring is deferred):

- **P1 — Source 3 is templated, not run.** `internal/generate` renders stub text
  and plants outcomes; nothing is actually executed. **Fix (Phase 2B):** Source 3
  becomes an agentic track too — Claude Max generates only the task + repo/harness/
  oracle; the sessions are then *run* through the real harness across rungs, exactly
  like Source 2. Outcomes are assigned later by the offline engine.
- **P2 — Source 2 is 1 real instance + 11 toys.** **Fix (Phase 2A):** scale to
  20–50 real SWE-bench Verified instances through the runner.
- **P3 — the local arm is confounded.** Its failures mix tool-call-fidelity
  collapse (qwen2.5-coder → prose-JSON, 0% native) with latency timeouts under GPU
  contention. **Fix (Phase 1):** default local to `gpt-oss:20b` (D16 proved 100%
  native), run on an uncontended GPU, and record `timed_out` distinctly so the
  offline engine can separate "local can't" from "local too slow".

### The reframe

**Source 2 and Source 3 share ONE generation engine — the agentic dual-arm
runner. They differ only in where the task comes from.**

| | Source 2 — semi-synthetic | Source 3 — fully synthetic |
|---|---|---|
| Task origin | SWE-bench Verified (given) | generated via Claude Max, OSS-grounded |
| Repo / harness / oracle | from the benchmark | generated |
| Session run | real CC harness, per rung | **same engine** |
| Outcome | assigned later by offline engine (executed oracle available) | assigned later by offline engine (executed oracle where the task carries tests) |

ROUTER_BRAINSTORM §2A Source 3 principles this implements: ground tasks in real OSS
repos (revert a bug-fix commit → "these tests fail, fix it"; a merged PR → a
feature task with its tests as the oracle); prefer an executable oracle; diversify
across archetype/language/scale/difficulty; play each task through the same harness
across rungs; simulate the user so implicit signals exist; target the decision
boundary (local-fails-frontier-passes); tag provenance.

---

## 2. Phases

Do them in order. Each has a clean acceptance gate.

### Phase 1 — Agentic runner: one serving path, log-first, no grading

**Goal.** A single runner that produces a clean **session log** + patch + metrics
per `(task, arm)`, serves a fidelity-clean local model, records latency distinctly,
and does **no** grading.

**Why.** Removes P3's confound and enforces the "no grading during generation"
rule. Today `run_agentic.py:run_one` calls `score_checkout` inline — that has to go.

**Files.**

- `agentic/runner/run_agentic.py` — remove the inline `score_checkout` call; local
  default; latency flags; emit the raw session log.
- `agentic/runner/run_swe_arm.py` — remove its inline `grade()`/swebench call from
  the generation path (grading is offline now); keep the swebench *implementation*
  parked for the offline engine to reuse later.
- `agentic/runner/executor.py` — **do not call during generation**; it becomes an
  offline-engine component (leave it in place, unused by the runner).
- `internal/schema/schema.go` — the `RawTurn` raw-log format is the log deliverable;
  extend `GoldRow` later (Phase 5) with provenance/latency, not now.

**Approach.**

1. **Model roster.** Make the arms a configurable ordered list `ARM_MODELS`
   (cheap→expensive), not a hardcoded pair. For this test: `local = gpt-oss:20b`
   (`PROXY_OLLAMA_MODEL`), `frontier = opus` (`FRONTIER_MODEL=opus` — change from
   today's `sonnet`, via the subscription). Each task runs once per rung.
   `make agentic-smoke` should show local `native>0, rescued=0`.
2. **Strip grading; emit two log artifacts.** `run_one` captures: (a) the portable
   **`RawTurn` JSONL** session log (`session_id, turn_index, timestamp, role,
   content, served_model, propensity`) for extraction; (b) the **raw CC event
   stream** (tool_use/tool_result, native/rescued, usage) for the UI + fidelity;
   plus the `git diff` (saved as `.patch`) and mined metrics (turns, `tool_calls`,
   `native/rescued`, tokens, `wall_clock_s`, `timed_out`, `hit_turn_cap`,
   `empty_patch`, `reported_cost_usd`). It does NOT run tests or a judge, and the
   run record carries NO `resolved`/`outcome` field.
3. **Preserve the oracle seam.** Keep `_oracle/` (test_patch + gold_patch) and the
   base `repo/` intact so the offline executed-oracle branch can grade later from
   `(base repo + agent diff + test_patch)`. The diff is already persisted; ensure
   nothing needed for offline grading is deleted with the checkout.
4. **Separate latency from capability.** Persist `timed_out` / `hit_turn_cap` as
   first-class fields on the result and the log. A timeout is a real routing signal
   but NOT a capability outcome — never collapse them.
5. **Tool-error backstop.** Surface malformed/rescued tool calls in the metrics so
   the offline engine can down-weight protocol failures vs genuine capability misses.

**Acceptance.**

- [ ] `make agentic-smoke` on `gpt-oss:20b`: native tool calls > 0, rescued 0.
- [ ] A run produces a `RawTurn` JSONL session log + `.patch` + metrics, and calls
      NO grader/judge (grep the runner: no `score_checkout`, no swebench invocation
      on the generation path).
- [ ] `_oracle/` + base `repo/` survive a run so offline grading is reconstructable.
- [ ] `timed_out` recorded distinctly from capability.
- [ ] DECISIONS entry: one serving path, no-grading-during-generation, local swap.

**Non-goals.** No outcome labels; no gold assembly; no eval.

---

### Phase 2 — Task sources feeding the runner

#### Phase 2A — Source 2 at scale: 20–50 SWE-bench Verified instances

**Goal.** Materialize 20–50 real instances into the task-dir contract and run both
arms through the Phase-1 runner → real logs on a real-benchmark distribution.

**Files.** `agentic/runner/materialize_swe.py` (1 → N; selection + stratification);
`Makefile` (`agentic-swe` target: materialize N → run both arms; **no grade step**).

**Approach.**

1. Select N from `princeton-nlp/SWE-bench_Verified`, biased to fast-grading (small
   F2P/P2P) and smaller repos (avoid django/sympy giants — disk + slow local agent).
   Stratify by difficulty if metadata allows. Log which instances and why — **no
   silent truncation**.
2. Materialize each into `tasks/<id>/` with `_oracle/` quarantine (generalize the
   existing single-instance loop), `provenance="swe_verified"`,
   `grounding="benchmark"`, `grader="swebench"`.
3. Run both arms (Phase-1 runner; cached; frontier `$` cap; local on an uncontended
   GPU with `gpt-oss:20b`). Output: raw logs + patches + metrics. **No grading.**
4. Note contamination: SWE-bench Verified is public → contamination risk; record it
   in the manifest; prefer SWE-bench Pro / held-out later (§2A limitation).

**Acceptance.**

- [ ] ≥20 instances materialized; `test_firewall.py` green on all.
- [ ] Both arms run; logs + patches + metrics persisted and cached.
- [ ] Instance selection + any drops logged; contamination noted in the manifest.

**Non-goals.** Not the full 500 set; no SWE-bench Pro yet; no grading.

#### Phase 2B — Source 3: generated tasks via Claude Max + a validation gate

**Goal.** Turn Claude-Max-authored tasks into runner-ready task dirs, validated so
their oracle is real and the firewall holds — then run them through the Phase-1
runner. Generation is interactive (Claude Max); the repo supplies the **spec +
gate**, not an API generator.

**Files.** new `agentic/synth/TASK_SPEC.md` (the exact task-dir contract a Claude
Max session fills in); new `agentic/synth/validate_task.py` (the gate);
`agentic/runner/test_firewall.py` (extend: scan issue text for solution leakage);
`agentic/synth/oss_repos.md` (permissively-licensed repo allowlist + commits).

**Approach.**

1. **Grounding (preferred).** In a Claude Max session, snapshot a permissively-
   licensed OSS repo at a commit and derive the task from real history: a real
   bug-fix commit → check out its parent (buggy) state; the commit's added/changed
   tests are the FAIL_TO_PASS oracle; still-passing tests are PASS_TO_PASS.
   Claude Max writes ISSUE.md and selects the F2P/P2P split — it does **not** invent
   the tests, so the oracle stays real/executable.
2. **Self-contained fallback.** Where no clean history fits a target archetype,
   Claude Max generates a small self-contained repo + tests (like `build_tasks.py`
   but authored in-chat). Tag `grounding="synthetic_repo"` (weaker) vs
   `grounding="oss_history"`. Prefer grounded.
3. **The gate (`validate_task.py`) — the repo's real deliverable here.** For every
   submitted task dir it asserts: (a) fail-before / pass-after (base repo fails F2P,
   `_reference`/`gold_patch` makes F2P pass and keeps P2P) — this proves the oracle;
   (b) the firewall (`test_firewall.py`): no `_oracle`/`_reference` content inside
   `repo/`, and the ISSUE text does not contain the fix; (c) `task.json` schema
   completeness (provenance/grounding/grader set). A task is only accepted into
   `tasks/` if the gate passes.
4. **Diversify + target the boundary.** Stratify authored tasks across archetype
   (bug-fix / refactor / feature / test-writing / migration), language, difficulty.
   After a first batch runs, prioritize authoring more tasks in the difficulty band
   that (once scored offline) turns out to be local-fails-frontier-passes.
5. Run accepted tasks through the Phase-1 runner (both arms). **No grading.**

**Acceptance.**

- [ ] `TASK_SPEC.md` documents the task-dir contract precisely enough for a Claude
      Max session to produce a valid task unaided.
- [ ] `validate_task.py` accepts ≥1 OSS-history task (fail-before/pass-after +
      firewall + schema) and rejects a deliberately leaky one.
- [ ] The accepted task runs through the unmodified Phase-1 runner, both arms.
- [ ] `provenance="synthetic"` + `grounding` tag set on the task + its logs.

**Non-goals.** No automated API generator; no thousands of tasks (start ~10–30);
no multi-language beyond what runs cheaply; no grading.

---

### Phase 3 — Simulated user: multi-turn sessions with implicit signals

**Goal.** Produce multi-turn session logs (not single-shot) so the deferred offline
engine can later mine REAL implicit signals + REAL escalation pairs — grounded in
what actually happened in-session.

**Why.** Today the runner is single-shot `claude -p`. §2A principle 5 wants a
simulated user so implicit signals exist. Crucially, the sim-user must react to
**in-session cues only** (the agent's own test runs visible in the transcript, its
claims of done/stuck) — NOT to an offline oracle grade — so the "no grading during
generation" rule holds and the behavior stays realistic (a real user reacts to what
they see).

**Files.** new `agentic/runner/sim_session.py`; reuse `internal/schema` `RawTurn`;
verify `internal/extract` consumes the output unchanged (don't rewrite it).

**Approach.**

1. Continue one CC session across turns (`claude -p --resume` / session id, or by
   replaying message history) so context carries.
2. The **simulated user** emits the next user turn from in-session cues: if the
   agent's own visible test run failed or it signalled stuck → paste the failing
   output (`paste_error`), a negative correction, or (with some probability) a
   local→frontier **switch** (a *real* escalation → a genuine escalation pair, not
   the nearest-neighbour approximation `extract.go` uses today); if the agent
   signalled success → a follow-up subtask (`moveon`) or end. Start
   scripted/deterministic and seeded; an LLM-driven user is a later upgrade.
3. Emit the session as `RawTurn` JSONL (with `served_model`, `propensity`, and — for
   the deferred grader only — hidden `_true_*` seams if cheap). No outcome labels.

**Acceptance.**

- [ ] A multi-turn session with ≥1 real escalation is produced in `RawTurn` JSONL.
- [ ] `internal/extract` ingests it unchanged (spot-check only — extraction/labeling
      is the offline engine's job, deferred).
- [ ] The sim-user demonstrably reacts to in-session cues, not an oracle grade.

**Non-goals.** No labeling/judging; no full conversational user model day one.

---

### Phase 4 — Slice: deterministic train / held-out split

**Goal.** Partition the generated data into train vs held-out **before any
labeling**, so the held-out set (which the offline engine will later grade with the
executed oracle for the gold set) is never contaminated by training.

**Why.** §2B: eval must be on data the router never trained on. The split is a
data-generation concern (decide it now); the labeling that fills each side is the
offline engine's job (later).

**Files.** new `agentic/runner/split.py` (or a Go tool under `cmd/` if it stays
stdlib) writing a `split_manifest.json`; the manifest is read by whatever labels/
evals later.

**Approach.**

1. **Split by prompt/task**, seeded and deterministic, so no task appears on both
   sides. Record the fraction and seed. (Note session+time ordering too, so a later
   temporal backtest can also split on time without re-slicing.)
2. **Held-out slice** = reserved for the executed-oracle gold set (offline, later).
   **Train slice** = reserved for weak labels (implicit/judge, offline, later).
3. Write a `split_manifest.json`: for each task/session → `{split: train|holdout,
   provenance, grounding, has_executable_oracle}`. This is the contract the offline
   engine consumes; it must be able to answer "what may I train on, what must I hold
   out" without re-deriving anything.

**Acceptance.**

- [ ] A seeded split assigns every task/session to exactly one of train/holdout;
      zero cross-split task leakage (a test asserts it).
- [ ] `split_manifest.json` records split + provenance + oracle-availability per item.
- [ ] Re-running the split with the same seed is identical (deterministic).

**Non-goals.** No labeling; no eval; the split policy for *real prod logs* (Source 1)
is out of scope (blocked on prod logging).

---

### Phase 5 — UI: browse all generated data by category + drill into full traces

**Goal.** Extend the existing web console so anyone can see every generated
session at a glance — a filterable table sliced by category — and click any row to
read the **full agentic trace** (every CC turn, tool call, tool result, and the
final diff). This is how we visually QA the data as it accrues.

**Why.** The corpus is now heterogeneous (two sources, multiple tiers, train vs
held-out, some with executable oracles) and multi-turn. A table + drill-in is the
fastest way to catch bad tasks, firewall leaks, or degenerate runs before they
reach the offline engine.

**Files.** `internal/server/handlers.go` + `server.go` (new read-only endpoints
over the agentic logs / `split_manifest.json`), `internal/server/static/` (the SPA
— add a data-browser view + a trace-detail view); it must stay stdlib-only
(`net/http` + embedded SPA, no external deps — invariant 1). Reuse the pattern of
the existing **Traces** and **Labeled data** views; this is a view, not a
reimplementation.

**Approach.**

1. **Table view, filterable by category.** One row per `(task, arm)` session,
   columns: `task_id`, `source` (swe_verified / synthetic / templated),
   `grounding`, `tier`, `arm`, `split` (train / holdout), `has_executable_oracle`,
   turns, `tool_calls` (native/rescued), tokens, `wall_s`, `timed_out`,
   `empty_patch`. Facet filters on every category field; the table reads the logs +
   `split_manifest.json` from Phase 4/5.
2. **Drill-in to the full trace.** Clicking a row opens the session: the ordered CC
   turns (user / assistant), each `tool_use` with its arguments and the
   `tool_result` (and `is_error`), the simulated-user turns (Phase 3) inline, and
   the final `.patch`. For the local arm, show native-vs-rescued per tool call so
   fidelity is visible. Show the task's ISSUE + (behind an explicit "reveal oracle"
   toggle, never by default) the `_oracle/` contents for auditing the firewall.
3. **No scoring in the UI.** Outcomes/labels are the offline engine's job — the UI
   shows what happened (turns, diff, metrics), not a pass/fail verdict, until the
   offline engine exists. When it does, this view is where its labels surface.
4. Wire a `make serve`-style entry that points at the agentic data dir
   (`AIL_DATA_DIR=data_agentic`), so the console can browse either corpus.

**Acceptance.**

- [ ] The console lists every generated session in a table, filterable by source /
      tier / arm / split / oracle-availability.
- [ ] Clicking a row shows the full multi-turn trace: turns, tool calls + results,
      sim-user turns, and the diff; local rows show native/rescued fidelity.
- [ ] The oracle is hidden by default and only shown behind an explicit reveal
      (so the UI itself can't cause a firewall slip in screenshots/demos).
- [ ] Still stdlib-only; no new Go deps; runs via `make serve`.

**Non-goals.** No editing from the UI; no scoring/label display until the offline
engine lands; no auth (local dev console).

---

### Phase 6 — Provenance, manifest, and quarantine the templated generator

**Goal.** Make the generated corpus self-describing so the offline engine can
consume it safely, and stop the old templated generator from polluting signal.

**Files.** `internal/schema` (provenance/latency fields on the log/gold structs),
`internal/generate` (quarantine), `Makefile`, `README.md`, `DECISIONS.md`.

**Approach.**

1. **Tag everything.** Every session log / task carries `provenance ∈
   {templated, swe_verified, synthetic}`, `grounding ∈ {benchmark, oss_history,
   synthetic_repo}`, `split ∈ {train, holdout}`, and `has_executable_oracle: bool`.
   These are the fields the offline engine keys on.
2. **Quarantine `internal/generate`.** It stays only as a fast, free, no-model-call
   **plumbing/CI fixture** for exercising downstream Go code. Its outputs are tagged
   `provenance=templated` and must never be reported as signal. DECISIONS entry so a
   future agent doesn't treat its numbers as meaningful.
3. Update `README.md` (data-source table reflecting one serving path + deferred
   offline engine) and add DECISIONS entries for every choice above.

**Acceptance.**

- [ ] Every generated item is provenance/grounding/split/oracle-tagged.
- [ ] `internal/generate` output is unmistakably marked plumbing-only; docs say so.
- [ ] README/DECISIONS updated.

---

### Phase 7 — Growing the gold set: the repeatable batch loop

Once the pipeline exists, the gold set is grown in **batches** by a single driver
(`scratchpad/batch_pipeline.sh`, generalizing the Phase 1–6 stages):

```
materialize (easy / new-only / tiny-patch, repo-restricted)
  → build per-instance images → run BOTH arms → grade (executed oracle)
  → fuse → split → re-fuse → materialize
```

Selection levers on `materialize_swe.py`:

- `--easy` — restrict to the human `<15 min fix` difficulty tier (the strongest
  prior that the local rung can plausibly solve it).
- `--new-only` — skip already-materialized instances so a batch spends its budget
  on genuinely new tasks.
- `--max-patch-lines N` — let a **tiny-patch instance in a big repo** through the
  big-repo exclusion (the easy small-repo pool is finite and quickly exhausted).
- `--repos` / `SWE_REPOS` — **repo allowlist**. In practice **django-only**:
  django images build reliably at the 7.7GB Docker cap, whereas sympy/scikit-learn
  images OOM (exit 137) and their long tasks time out.

Split fractions are tuned so the scarce, valuable cells survive the holdout:
`both_pass`/`disagree` hold out ~0.5 each (both are scarce and carry the routing
signal), while the abundant `both_fail` holds out only ~0.08 (realistic ballast
that would otherwise flood the gold and crush the oracle local-share headline).
Env-overridable via `AIL_BOTHPASS_FRAC` / `AIL_DISAGREE_FRAC` / `AIL_BOTHFAIL_FRAC`.

**Empirical finding (see DECISIONS D23, `docs/EVAL_PROGRESSION.md`).** Growing the
corpus with SWE-bench Verified tasks skews it toward the **disagree** cell,
because `gpt-oss:20b` solves few of them — so the oracle ceiling and router AUC
*fall* as the set grows. SWE-bench Verified behaves as a hard-escalation stress
set, not a reliable source of `both_pass` (local-adequate) rows. Sourcing
`both_pass` likely needs an easier task track or a stronger local rung.

---

## 3. Sequencing & dependencies

```text
Phase 1 (runner: one path, no grading, de-confounded) ──┬──► 2A (SWE-bench scale)
                                                         └──► 2B (generated via Claude Max)
Phase 1 ──► Phase 3 (simulated user)      [starts once the log schema lands]
2A, 2B, 3 ──► Phase 4 (slice)  ──►  Phase 6 (provenance/manifest, continuous)
2A, 2B, 3 ──► Phase 5 (UI: browse + traces)   [build once logs exist]
```

Do **1 → 2A → 2B**, slot **3** in after 1, then **4**; build the **UI (5)** as
soon as logs exist (it's how you QA everything else), and fold provenance **(6)**
in continuously. 2A before 2B: real-benchmark logs first (cheap, validates the
log-first runner), then widen with generated tasks.

## 4. Explicitly deferred (the offline scoring engine — DO NOT build here)

- Executed-oracle grading — two implementations, kept separate:
  `docker_pytest` (`executor.py`, self-contained tasks) and the **official swebench
  harness** (real SWE-bench instances, applies `test_patch`, per-instance env). Both
  are branches of the *executed* label; they consume the preserved diff + `_oracle/`.
- LLM-as-a-judge branch (adequacy over `(prompt, response)`).
- Signal-heuristics branch (regex + LLM over user behavior).
- Router training and any `internal/eval` changes.

These consume the logs + `split_manifest.json` this plan produces. Design them once
the data is solid.

## 5. Open decisions to record in DECISIONS.md as you go

- Local model + tool-error backstop policy (Phase 1).
- SWE-bench instance selection criteria + contamination stance (Phase 2A).
- Task-dir spec + validation-gate rules + firewall issue-leak check (Phase 2B).
- Simulated-user cue policy + escalation probability, scripted-vs-LLM (Phase 3).
- Split fraction, seed, and by-prompt-vs-by-session policy (Phase 4).
- UI: which category facets matter most + trace-detail layout (Phase 5).
- Provenance schema + templated-generator quarantine (Phase 6).

## 6. What "solid data" means when this is done

- All logs come from **one path** (agentic runs), across both rungs, with the local
  arm de-confounded (fidelity clean, latency recorded separately).
- Coverage spans a real-benchmark distribution (2A) and generated, OSS-grounded,
  diversity-stratified tasks (2B), with executable oracles preserved for later.
- Sessions are multi-turn with real in-session user reactions (3).
- The corpus is deterministically split into train / held-out and fully
  provenance-tagged (4, 6), ready for the offline scoring engine to label safely,
  and every session is browsable by category with full trace drill-in (5).
