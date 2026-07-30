# ail-routing-test

A framework for building and honestly evaluating a **predictive auto-router**
for Claude Code (CC): for every LLM call CC makes, pick the *cheapest* model
that will still give an *adequate* answer, from a two-rung ladder
`{local open-weight model, frontier model}`. Default to local; **burst** to the
frontier only when local would be inadequate *and* the user's frontier quota
allows.

This repo is the **predictive branch** (decide from the prompt *before*
generating) plus its data/eval tooling. It does **not** build the cascade
branch (generate-then-check). Because real production logs aren't flowing yet,
the framework **manufactures** realistic logs, extracts structured training
data from them, trains candidate routers, and evaluates them. When real logs
arrive, only the generator is swapped out — **the extraction / train / eval
code is the real deliverable.**

> **Why not an off-the-shelf router?** Our traffic is overwhelmingly code, so
> topic classifiers collapse to "it's code → frontier" and are useless. Fixed
> hard/medium/easy difficulty tiers break because *difficulty is
> model-relative* and the model set drifts. The principled fix, implemented
> here, is to **learn model-relative adequacy from our own traffic** — the
> decision axis is capability/difficulty, not topic.

---

## Quick start

```bash
# Prerequisites (see "Backends" below): Go >=1.23, Ollama running with an
# embedding + a local gen model pulled, and either the `claude` CLI logged in
# (preferred) or ANTHROPIC_API_KEY set.

make all        # gen -> extract -> train -> eval on the small default config
cat RESULTS.md  # tables the run produced
make test       # unit tests for the portable core
```

Individual stages: `make gen`, `make extract`, `make train`, `make eval`.
All stages are seeded and the backend caches every model call to disk, so
reruns are free and an interrupted overnight run resumes cheaply.

---

## Architecture

```
                 ┌─────────────────────────────────────────────────────┐
                 │  Backend (internal/backend): Embed / Generate / Judge│
                 │  content-hash disk cache · bounded concurrency ·      │
                 │  retry+backoff · spend caps · CLI-or-API auto-detect  │
                 └───────────────┬──────────────────────┬───────────────┘
                                 │                      │
  PILLAR 1  (label engine)       │                      │
  ┌───────────────┐  raw JSONL   │                      │
  │ gen (1a)      │─────────────►│  extract (1b/1c)     │
  │ synthetic CC  │              │  session reconstruct │
  │ session logs  │              │  implicit-signal     │
  │ + hidden      │              │  heuristics -> weak  │
  │ ground truth  │              │  labels; judge sample│
  └───────────────┘              │  -> STRUCTURED DATA  │
                                 │  + extractor-quality │
                                 │    report vs hidden  │
                                 └──────────┬───────────┘
                                            │ pointwise / pairwise / gold
             PILLAR 2 (routers)             ▼
     ┌───────────────────────────────────────────────────────┐
     │ Router iface: Fit / Score / Decide / Name             │
     │  · RouteLLM-style logistic (pairwise)                 │
     │  · IRT 1PL (pointwise) + new-model onboarding         │
     │  · kNN (pointwise, training-free)                     │
     │  · encoder+MLP / SLM-head  (Go stub + python/ impl)   │
     └───────────────────────────────┬───────────────────────┘
                                     │
             PILLAR 3 (eval)          ▼
     ┌───────────────────────────────────────────────────────┐
     │ EvalMethod iface -> Report                             │
     │  · dual-arm gold set (AIQ, convex hull, cell-B rate)  │
     │  · temporal backtest (session+time split, label-      │
     │    source ordering guardrail)                         │
     │  · off-policy IPS + doubly-robust (needs propensities)│
     │  · guardrail/perturbation suite (topic-collapse test) │
     │  · metrics: AUC, ECE, escalation, quality retention…  │
     │  · policy: threshold calibration + quota gate         │
     └───────────────────────────────────────────────────────┘
```

Package layout:

| Path | Role |
|---|---|
| `internal/schema` | Data contracts (raw log, pointwise, pairwise, gold) |
| `internal/config` | All knobs; env + file; seeded |
| `internal/backend` | Embed / Generate / Judge, cache, concurrency, caps |
| `internal/numerics` | dot/cosine, logistic regression (GD), Newton helpers |
| `internal/feature` | prompt → structural `Features` |
| `internal/generate` | Pillar 1a synthetic log generator |
| `internal/extract` | Pillar 1b extraction + 1c quality report |
| `internal/router` | Pillar 2 routers + interface |
| `internal/eval` | Pillar 3 harness, metrics, policy |
| `cmd/{gen,extract,train,eval}` | stage entrypoints |
| `python/` | **non-portable** encoder + SLM-head training |

---

## The Go / Python portability boundary

The portable core will be ported into a **stdlib-only Go gateway**, so:

- **Go, stdlib-only (portable):** schema, config, log parsing, session
  reconstruction, signal extraction, feature extraction, kNN, logistic-
  regression router, 1PL IRT fit + new-model onboarding, threshold/policy
  layer, the entire eval harness and all metrics. Numerics are hand-rolled.
- **Python, optional (`python/`, NOT portable):** fine-tuning a neural encoder
  (sentence-transformers) and fine-tuning a small LM router head (torch).
  These need a training stack Go can't reasonably carry. The Go side exposes
  them as a `Router` interface with a working stub; `python/` holds the real
  training and writes artifacts the Go side can consume.

Any non-stdlib Go dependency would be isolated and justified in DECISIONS.md.
There are currently **none**.

---

## Backends

`internal/backend` exposes three capabilities behind one interface, each with
content-hash disk caching, bounded concurrency, retry-with-backoff, and hard
spend caps:

- **`Embed(text) → []float32`** — local Ollama
  (`POST {OLLAMA_URL}/api/embeddings`, default model `nomic-embed-text`,
  `OLLAMA_URL` default `http://localhost:11434`).
- **`Generate(model, messages) → text`** — local via Ollama; frontier via
  Anthropic.
- **`Judge(prompt, response) → {adequate, score, rationale}`** — frontier-as-
  judge via Anthropic.

**Anthropic auth is auto-detected**, preferring the subscription path:
1. **`claude` CLI subprocess** (preferred) — uses your logged-in Claude
   subscription; no API key, no per-token billing. The backend forwards
   `HOME/PATH/USER/LOGNAME` so the CLI can read its Keychain credential.
2. **Direct HTTP with `ANTHROPIC_API_KEY`** — fallback if the CLI isn't found.

The active path is logged at startup. Frontier model defaults to the latest
Claude Sonnet.

### Setup
```bash
# Ollama
ollama serve &                 # if not already running
ollama pull nomic-embed-text
ollama pull llama3.1:8b
ollama pull qwen2.5-coder:14b

# Anthropic (pick one)
claude   # log in once; the framework will subprocess it     (preferred)
export ANTHROPIC_API_KEY=sk-...                              # or this
```

---

## Data schemas

Defined once in `internal/schema` (Go structs, JSON-tagged). Summary:

- **Raw log record** (JSONL): `session_id, turn_index, timestamp, role,
  content, served_model` (assistant turns), `propensity` (nullable), plus
  hidden `_true_*` ground-truth fields used only to grade extraction.
- **Structured pointwise row:** `prompt_id, prompt_text, features, embedding,
  model, outcome{0,1}, label_source, label_confidence, session_id, turn_index,
  timestamp, propensity?`.
- **Structured pairwise row:** `prompt_id, model_a, model_b, preferred{a,b,tie},
  source` (+ features/embedding).
- **Gold row (dual-arm):** `prompt_id, prompt_text, outcome_local,
  outcome_frontier, cost_local, cost_frontier, executable`.

`label_source` is ordered **executed > human > judge > implicit**; the eval
harness enforces that eval labels are strictly stronger than training labels.

---

## Real production logging requirements

For this pipeline to run on **real** CC traffic instead of synthetic logs, the
production logger must emit, per LLM call:

1. **Session + ordering:** a stable `session_id`, a monotonic `turn_index` (or
   timestamp precise enough to order within a session), and role.
2. **The served model** on every assistant turn (exact model id/version).
3. **The full prompt** (or enough to recompute `Features` and an embedding) and
   the **response text**.
4. **The next user turn** must be recoverable (it carries the implicit success/
   failure signal). This is automatic if turns are logged in order.
5. **Logging propensities** — *if* off-policy/counterfactual evaluation is
   wanted, the serving policy must be **stochastic** (e.g. epsilon-greedy) and
   record `propensity` = P(served model | state) at decision time. Deterministic
   logs cannot support IPS/DR and the harness will refuse them.
6. **(Optional but ideal) executable outcomes** — when a turn's code is run
   against tests, log pass/fail as an `executed` label. This is the strongest
   label source and unlocks trustworthy absolute numbers.

Everything else (features, embeddings, weak labels, judge labels) is derived
offline by this framework from the above.

---

## Evaluation methods (Pillar 3)

Each is an `EvalMethod` producing a structured `Report`; the harness encodes the
four things that make router eval hard as explicit guardrails.

| Method | What it measures | Guardrail it encodes |
|---|---|---|
| **dual-arm gold** | absolute cost/quality curve, convex hull, **AIQ**, under-/over-escalation (**cell-B**), AUC/ECE | both arms observed → no censoring; the only trustworthy absolute numbers |
| **temporal backtest** | ranks routers on held-out future (AUC/ECE/acc) | split by **session+time** (no leakage); **eval label source must be strictly stronger than train** or it WARNs (label circularity) |
| **off-policy IPS + DR** | counterfactual reward of deploying a router, from logs | **REFUSES** deterministic logs (needs stochastic propensities + overlap); reports ESS |
| **guardrail suite** | difficulty-monotonicity + **topic-collapse** (keyword injection) | score must move with difficulty; decision must NOT flip on off-topic words |

Plus a **policy layer**: calibrate a threshold to a target escalation rate (safe
on logs) or a target quality (gold only), and a frontier **quota gate**.

## What is trustworthy

- **Absolute** cost/quality numbers come only from the **dual-arm gold set**
  (both arms observed) and, later, **online A/B**. Everything else is relative.
- **Backtests only *rank* routers** — they inherit the training label source's
  blind spots (label circularity) and the observational censoring of real logs.
- Implicit signals are **noisy features anchored by the judge**, not clean
  labels. See Pillar 1c.

See `DECISIONS.md` for every choice, and `RESULTS.md` for the latest run.
