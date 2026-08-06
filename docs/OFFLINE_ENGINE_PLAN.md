# Offline Scoring Engine — plan (labels from logs)

> Handoff doc for the next agent/session. Written 2026-08-05. Owner: Akshay.
> Read `DATA_PLAN.md` first — this engine CONSUMES the artifacts that plan
> produces and is the "offline scoring engine" that plan explicitly defers. Also
> read `ROUTER_BRAINSTORM.md`, especially §2A (label engine) and §2B (label ladder), and
> `DECISIONS.md` D5–D9 (firewall, label-source ordering, eval guardrails).

---

## Scope — read this first

**Input:** the session logs, patches, run records, and `split_manifest.json` that
`DATA_PLAN.md` generation produces. **Output:** an `outcome ∈ {0,1}` per
`(task, model)` session, with a `label_source` and a calibrated `label_confidence`,
materialized into the `PointwiseRow` / `PairwiseRow` / `GoldRow` datasets the
existing routers and eval harness already consume (`internal/schema`).

This engine assigns outcomes **offline, after generation** — never during a run
(DATA_PLAN invariant 3). It has three label branches:

1. **Executed oracle** — run hidden tests against the produced patch → non-circular
   pass/fail (`label_source=executed`). The strong label; the calibration anchor.
2. **LLM-as-a-judge** — a frontier judge over a distilled *evidence pack* → adequacy
   (`label_source=judge`). Canonical only where no oracle exists.
3. **Heuristics** — regex (+ LLM for the ambiguous residue) over the simulated
   user's reactions → weak implicit outcome (`label_source=implicit`). Every session
   is agentic AND gets a sim-user reaction (DATA_PLAN Phase 3 is universal), so
   heuristics always accompany the judge on oracle-less logs.

Out of scope: router training and eval-harness changes (they already exist);
generating the logs (that's `DATA_PLAN.md`); Source 1 real-prod logs (blocked on
prod logging).

---

## 0. Orientation (for an agent with no context)

`ail-routing-test` builds a predictive auto-router for Claude Code: for each call
pick the cheapest adequate model on a `{local, frontier}` ladder. To train/eval it
we need `(model, prompt) → outcome` data. Generation (`DATA_PLAN.md`) runs the real
`claude -p` harness on tasks across model rungs and stores **logs** but assigns **no
outcomes**. This engine turns those logs into outcomes.

### Non-negotiable invariants

1. **No circularity.** Eval/gold labels must come from a source STRICTLY STRONGER
   than training labels: `executed > human > judge > implicit`
   (`schema.LabelStrength`). The engine enforces this when it materializes datasets;
   the eval harness WARNs if violated (DECISIONS D9).
2. **Logs are immutable; labels are an append-only layer.** Re-labeling (new judge
   prompt, new heuristic) writes new `LabelRecord`s; it never mutates a log. Keep
   EVERY label — the disagreements and the oracle-vs-judge overlaps are the
   calibration signal.
3. **Go/Python boundary (DATA_PLAN invariant 1).** Executed branch = Python under
   `agentic/` (Docker / swebench). Judge + heuristics + evidence-pack reader +
   resolve + calibration = **stdlib Go** (`internal/extract`, `internal/backend`,
   `cmd/label`). The judge is a single-shot `Backend.Judge` call — the one allowed
   non-generation use of the Job-A serving path. No new Go deps.
4. **Deterministic + cached.** Every judge call is content-hash cached (the backend
   already does this); the evidence-pack reader is a pure function; seeds are fixed.
   Re-runs are free; interrupted runs resume.
5. **Firewall already held upstream.** The agent never saw `test_patch`/`gold_patch`
   during generation (DATA_PLAN invariant 4). The executed branch may read
   `_oracle/`; the judge/heuristics branches must NOT (they'd leak the answer into
   the label). Enforce with a load-path guard, mirroring `StripHidden`.

---

## 1. Storage — logs vs labels

Logs are immutable; labels append. The oracle indicator (`has_executable_oracle`)
lives on the run record and in `split_manifest.json` and is what routes each log.

```text
data_agentic/
  logs/<task>__<arm>.rawturns.jsonl   # portable RawTurn log (heuristics + fallback judge input)
  logs/<task>__<arm>.events.json      # full CC event stream (evidence-pack source)
  logs/<task>__<arm>.patch            # produced diff (executed oracle + evidence pack)
  runs/<task>__<arm>.run.json         # metrics + has_executable_oracle
  labels/executed.jsonl               # append-only, one branch per file
  labels/judge.jsonl
  labels/implicit.jsonl
  calibration/report.json             # judge/implicit vs executed agreement (§6)
  split_manifest.json
  pointwise.jsonl / pairwise.jsonl / gold.jsonl   # MATERIALIZED (logs + resolved labels)
```

### `LabelRecord` (identical shape in every `labels/*.jsonl`)

```text
{session_id, task_id, model, arm, split, provenance, has_executable_oracle,
 outcome: 0|1,
 label_source: "executed" | "judge" | "implicit",
 label_confidence: float,      // calibrated (§6)
 labeler_version: string,      // rubric/heuristic/harness version, for reproducibility
 evidence: {...},              // branch-specific (below)
 timestamp}
```

`evidence` per branch:

- executed → `{fail_to_pass:{...}, pass_to_pass:{...}, per_node:{...}}`
- judge → `{verdict, score, rationale, k_votes?, evidence_pack_ref, rubric_version}`
- implicit → `{signal, matched_turn_index, signal_confidence}`

### Resolve step

`resolve_labels` picks, per `(task, model)`, the **strongest available** label as
the canonical `PointwiseRow.outcome`, using `schema.LabelStrength`. All records are
retained; resolution is a re-runnable downstream step (so a better fusion rule
re-resolves without re-labeling).

---

## 2. Routing logic (has_oracle × split)

|                    | held-out (gold)                              | train                          |
|--------------------|----------------------------------------------|--------------------------------|
| **oracle present** | `executed` = canonical; judge/heuristics on a SAMPLE → calibration only | `executed` = canonical; sample → calibration |
| **oracle absent**  | `judge` (canonical), flagged weaker in gold  | `judge` + `heuristics` = canonical |

Consequences: (a) gold/eval labels are always ≥ train-label strength → no
circularity; (b) we do **not** judge oracle-bearing logs for their label (the oracle
already gives it) — only a **representative sample** is judged, purely to measure
judge accuracy vs executed truth (§6).

---

## 3. Branch 1 — Executed oracle (Python, `agentic/`)

Reuses the generation-time mechanics, run offline. The agent run and grading are
separate; grading is independent (we re-run the tests ourselves — never trust the
agent's own in-session test output for the label). That independence is what makes
it non-circular.

**Mechanics.** For each run record with `has_executable_oracle=true`:

1. Fresh checkout of `repo @ base` (the buggy state) from `tasks/<id>/repo`.
2. Apply **the model's patch** (`logs/<...>.patch`).
3. Apply the **hidden test patch** (`_oracle/test_patch`) — never visible to the agent.
4. Run the tests in a hermetic Docker container (`--network none`).
5. **Resolved = all `FAIL_TO_PASS` pass AND all `PASS_TO_PASS` still pass.**

**Two harnesses (kept separate — DATA_PLAN grading paths).** Selected by the task's
`grader` field:

- `docker_pytest` (self-contained tasks) → `agentic/runner/executor.py`, generic
  `python:3.11-slim` + pytest. Tests are dependency-free and mounted in.
- `swebench` (real SWE-bench instances) → the official swebench harness, which builds
  a per-instance Docker image with that repo's exact deps, applies patch + test_patch,
  and writes `report.json` with `resolved`.

Emits `label_source=executed`, `confidence=1.0`, `evidence` = per-node results.

---

## 4. Branch 2 — LLM-as-a-judge (Go, `internal/extract` + `internal/backend.Judge`)

Canonical label for oracle-less sessions; run on a sample of oracle-bearing sessions
for calibration only.

### 4.1 Input = the Option-5 evidence pack (NOT the raw diff, NOT the full transcript)

The judge can't run code, so it does structured code review. Feeding it the bare
unified diff is too thin (hunk-only, no execution evidence); feeding it the full
transcript is noisy and lets it anchor on the agent's self-narrative. The evidence
pack is the middle path: full change context + the agent's *real* verification
results, denoised.

### 4.2 The evidence pack — a DETERMINISTIC reader (no LLM in extraction)

`BuildEvidencePack(events, patch, baseRepo, runMetrics) → EvidencePack`, a pure
stdlib-Go function over artifacts we already keep. Cheap, cacheable, reproducible,
adds no model error. Five components:

1. **Changed-file contents (post-edit).** Don't keep the checkout — `git apply` the
   `.patch` onto a temp copy of base `repo/` and read the changed files (full content
   or a generous window around each hunk). Gives the judge surrounding logic, not
   just the hunk (the biggest accuracy lever).
2. **Verification runs (the key signal).** Walk the event stream: for each `assistant`
   event pull `tool_use` blocks where `name=="Bash"`; pair each with its `tool_result`
   via `tool_use_id`. Filter to verification commands via an allowlist (`pytest`,
   `python -m pytest`, `go test`, `npm test`, `tox`, `make …`, `python -c …`, repro
   scripts). Keep: command, pass/fail (parse pytest's `=== N passed, M failed ===` or
   `is_error`), truncated stdout/stderr tail.
3. **Final agent claim.** The last `assistant` text block — included but **tagged
   `agent_claim` (untrusted)** so the judge weighs it against actual outputs.
4. **Degeneracy flags.** From run metrics: `tool_errors`, `native/rescued`,
   `empty_patch`, `timed_out`, `hit_turn_cap`, total tool calls — catch "never ran
   anything" / "looped on errors".
5. **Task framing.** The issue text and (if any) which tests were visible to the agent.

**Extraction rules / tradeoffs (all deterministic):**

- Also capture the *last few* Bash commands + outputs regardless of allowlist match
  (agents verify near the end), under a char budget — covers custom test commands.
- Truncate large outputs to summary line + head/tail with a cap; for pytest extract
  the summary line.
- When exit status is ambiguous (only text + `is_error`), mark it `unknown` rather
  than guessing (and let that lower confidence).
- **Provenance-tag every fact** `observed` (command output) vs `agent_claim` (prose),
  so both the judge and calibration can separate corroborated from asserted success.
- **Degradation:** if there is no verification evidence (agent couldn't run anything),
  the pack falls back to components 1 + 3 (static review). This is exactly why the
  agent-workspace decision in `DATA_PLAN` (run the agent in the task's
  container-with-deps) matters — no deps ⇒ thin pack ⇒ weaker judge.

The **same reader powers the Phase-5 UI trace view** (render exactly what the judge
saw) — build it once.

### 4.3 Rendered pack (what the judge receives)

```text
ISSUE: <problem statement>

CHANGED FILES (post-edit):
  calc/eval.py:
    <full post-edit content or hunk±window>

VERIFICATION RUNS (observed):
  $ python -m pytest -q tests/        → "3 passed" (exit ok)
  $ python -c "from calc.eval import evaluate; print(evaluate('2+3*4'))" → "14" (exit ok)

AGENT CLAIM (self-report, untrusted):
  "Rewrote the evaluator to respect precedence; all tests pass."

RUN FLAGS: tool_errors=0, empty_patch=false, timed_out=false, turns=6
```

### 4.4 Verdict, self-consistency, caching

- Rubric prompt → structured `{adequate: bool, confidence: 0–1, rationale}`. Frontier
  judge (Opus/Sonnet via the subscription path).
- **Targeted k=3 self-consistency (NOT blanket).** Single call by default; escalate to
  k=3 (majority vote; inter-sample agreement → confidence) only for (a) the
  calibration sample and (b) low-confidence / near-threshold single verdicts.
- **Calibrate on the same input shape:** the oracle-bearing calibration sample must be
  judged with the *identical* evidence-pack modality it'll see on oracle-less logs, or
  the measured accuracy won't transfer.
- Content-hash cache on `(evidence_pack, rubric_version, k)`.

---

## 5. Branch 3 — Heuristics (Go, extend `internal/extract/signals.go`)

Weak implicit outcome from the **simulated user's reactions** (DATA_PLAN Phase 3).
Every session is agentic **and** gets at least one sim-user reaction turn (Phase 3 is
applied to all generation), so heuristics always have input — there are no
user-reaction-free ("single-shot") logs, and every oracle-less session has BOTH a
judge and a heuristic label to fuse (§6.2).

- Mine signals from the sim-user turns: `switch` (real local→frontier escalation,
  strongest) / `paste_error` / `negative` / `retry` → inadequate; `moveon` /
  `complete` → adequate. Regex first (`signals.go` already does this); an LLM
  classifier only for the ambiguous residue.
- **Roll up to a session outcome:** any escalation/paste_error/negative on the
  attempt → `outcome=0`; clean moveon/complete → `outcome=1`. Confidence from the
  per-signal table (switch 0.90 … complete 0.55).
- **Independence constraint (critical).** Heuristics are only a useful second opinion
  if independent of the judge. If the simulated user just parrots the agent's
  self-reported success, heuristics and judge both key off the agent's prose and
  consensus is worthless. The sim-user's reaction MUST be grounded in something
  objective (a command exit code, a quick check it runs) — a constraint on DATA_PLAN
  Phase 3.

---

## 6. Consensus + calibration — the payoff of the mixed corpus

### 6.1 Calibration anchor (oracle-bearing sample)

On a representative sample of oracle-bearing sessions we have BOTH `executed` truth
and judge/heuristic labels. Compute agreement (precision/recall/accuracy of
judge-vs-executed and implicit-vs-executed). This is `RESULTS.md`'s Pillar-1c report,
but **finally meaningful on real responses** (DECISIONS D6 noted it was hollow on
templated stubs). It tells us how much to trust each weak labeler on oracle-less logs
and sets `label_confidence`. Write it to `calibration/report.json`.

### 6.2 Fusion (judge ⊕ heuristics on oracle-less logs) — judge-primary

Both branches emit an outcome + confidence. The **judge is primary**: it sees full
context (the evidence pack — changed-file contents + the agent's real verification
runs), whereas heuristics are a noisy behavioral proxy read off the sim-user's
reaction. So the rule is judge-primary, heuristics-as-confirmation:

- **Agree** → that outcome, **high** confidence.
- **Disagree** → **take the JUDGE's outcome**, stamp **low** confidence, and flag the
  session for escalation (k=3 judge / richer evidence pack / human audit). Heuristics
  never override the judge — they modulate confidence and surface the hard cases.
- **Even the strongest heuristic (a `switch`) doesn't override:** for an oracle-less
  log the sim-user's escalation is derived from the same in-session cues the judge
  already sees, so it is not independent ground truth and can't outrank the
  fuller-context judge.
- **Calibrate, don't guess.** Use the oracle-bearing sample (executed truth) to
  CONFIRM the judge-primary prior and tune the confidence mapping — MEASURE
  judge-alone vs a weighted fusion against executed truth and adopt whichever predicts
  truth best, keeping judge-primary as the default. Re-derive as oracle data accrues.

This matches the schema strength order (`judge > implicit`): heuristics inform
confidence and calibration; the judge sets the outcome.

### 6.3 Optional (v2)

A few hundred human-audited `(prompt, response, verdict)` triples to check the judge
against human ground truth where no oracle exists (the brainstorm's inter-rater
reliability). Defer.

---

## 7. Phases (build order)

Each has a clean acceptance gate. Build against the DATA_PLAN artifacts; if those
don't exist yet, a handful of hand-made logs suffice to develop against.

### Phase O1 — Storage + LabelRecord + resolve + manifest wiring

Define `LabelRecord` (Go struct + JSONL), the `labels/*.jsonl` layout, and
`resolve_labels` (strongest-per-`(task,model)` using `schema.LabelStrength`). Read
`split_manifest.json` + run records to route logs.

- **Acceptance:** resolve produces canonical outcomes from hand-written label files;
  strength ordering respected; all records retained; a test asserts eval-label ≥
  train-label strength.

### Phase O2 — Executed branch (Python)

`agentic/runner/grade_offline.py`: for `has_executable_oracle=true` runs, reconstruct
`(base repo + patch + test_patch)` and grade via the task's `grader`
(`docker_pytest` | `swebench`). Emit `executed` LabelRecords.

- **Acceptance:** re-grading the existing SWE instance + a curated task reproduces
  their known outcomes; `executed` records land with per-node evidence; no grading
  ran during generation (it's here, offline).

### Phase O3 — Evidence-pack reader + LLM-judge (Go)

`BuildEvidencePack` (pure, §4.2) + the judge over it (§4.4), targeted k=3, cached.
Firewall guard: judge path cannot read `_oracle/`.

- **Acceptance:** a pack is built deterministically from `events.json` + patch + base
  repo (byte-identical on re-run); judge emits structured verdicts; k=3 fires only on
  the sample + low-confidence cases; caching makes re-runs free.

### Phase O4 — Heuristics branch (Go)

Extend `signals.go` to mine sim-user turns → session rollup; LLM-assist for ambiguous
residue only. Multi-turn only.

- **Acceptance:** implicit LabelRecords produced from a multi-turn log with a real
  escalation; single-shot logs correctly produce none.

### Phase O5 — Calibration + fusion

Compute the calibration report (§6.1) on the oracle sample; implement agreement-gate
fusion (§6.2) with weights set from the calibration. Emit calibrated
`label_confidence`.

- **Acceptance:** `calibration/report.json` shows judge-vs-executed and
  implicit-vs-executed precision/recall; fusion labels carry a confidence that
  tracks measured accuracy; disagreements are flagged for escalation.

### Phase O6 — Materialize datasets + hand to the existing eval — ✅ BUILT

`internal/materialize` + `cmd/materialize` (`make agentic-materialize`). Reads
`labels/resolved.jsonl`, joins task issues (features + best-effort embeddings) and
per-session tokens (cost), and writes `pointwise.jsonl` / `pairwise.jsonl` /
`gold.jsonl` + `gold_meta.json` for the UNCHANGED `internal/eval` harness.
SUPERSEDES `internal/agentic.BuildGold` (which read a `resolved` field the
log-first runner no longer emits → the all-zero-gold landmine).

Discipline enforced (see package doc):

- **train vs eval split** — pointwise/pairwise from TRAIN tasks; gold ONLY from
  HOLDOUT tasks. No task in both → no leakage.
- **executed-only gold** — a gold row needs BOTH arms `executed`; oracle-bearing
  sessions whose canonical label is judge/implicit (the oracle never ran) are
  QUARANTINED, never materialized as truth. Counts land in `gold_meta.json`.
- **firewall** — gold is `executed` (strongest), dominating any weak train label;
  a belt-and-suspenders check warns on violation.

- **Acceptance:** `make agentic-materialize` writes the datasets;
  `AIL_DATA_DIR=data_agentic make train`/`agentic-eval` run over them. On the
  current corpus: 4 executed train pointwise, 2 pairwise, 0 gold (no holdout
  executed dual-arm yet), 4 oracle-ungraded SWE sessions quarantined — honest and
  correct until the generation batch + swebench grading land. Retiring BuildGold
  (repoint `cmd/agentic`, delete `BuildGold`) is the last step, deferred until the
  data-plan branch's BuildGold guard merges to avoid a conflict.

---

## 8. Sequencing

```text
O1 (storage/resolve) ──► O2 (executed)  ─┐
                     ──► O3 (judge)      ─┼─► O5 (calibration + fusion) ──► O6 (materialize → eval)
                     ──► O4 (heuristics) ─┘
```

O2/O3/O4 are independent once O1 lands; O5 needs O2 (the executed anchor) plus at
least one of O3/O4; O6 is last.

## 9. Open decisions to record in DECISIONS.md

- Calibration sample size + selection (how many oracle-bearing logs to judge).
- Evidence-pack budgets: char caps, hunk window size, verification-command allowlist.
- Judge rubric wording + `rubric_version` policy; single-vs-k=3 escalation threshold.
- Fusion rule + per-branch weights (set from calibration, but record the chosen form).
- Heuristic signal→outcome rollup rules and the sim-user independence mechanism.
- Firewall guard for the judge/heuristics load path.

## 10. What "solid outcomes" means when this is done

- Oracle-bearing sessions carry non-circular `executed` labels; gold uses them.
- Oracle-less sessions carry judge (+ heuristics) labels whose confidence is
  **calibrated against executed truth**, not asserted.
- Every label is retained and provenance-tagged; disagreements are surfaced, not
  hidden; the whole thing re-resolves without re-labeling.
- The materialized datasets flow into the existing eval harness with no circularity.
