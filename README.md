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

## Source of truth & repo map

The authoritative design is **`docs/ROUTER_BRAINSTORM.md`** — this repo is the
predictive-branch **POC harness of its §6**. Design docs live under **`docs/`**
(`ROUTER_BRAINSTORM`, `DATA_PLAN`, `OFFLINE_ENGINE_PLAN`, `DECISIONS`); `RESULTS.md`
is generated at the repo root by `make eval`. The pipeline maps to the code:

| ROUTER_BRAINSTORM §6 stage | in this repo |
|---|---|
| ① Data sources (logs / semi-synthetic / synthetic) | `agentic/` (real execution-grounded generation) · `internal/generate` (templated CI fixture, never signal) |
| ② Parse → training labels (firewall, reconstruct, split, implicit/judge) | `internal/extract`, `agentic/runner/{firewall_gate,split}.py` |
| ③ Per-router shapes (pointwise / pairwise) | `internal/schema` |
| ④ Fit routers (RouteLLM / IRT / kNN / encoder-MLP·SLM) | `internal/router` (last two are Go stubs + `python/`) |
| ⑤ Non-circular eval (gold / backtest / off-policy / guardrail) | `internal/eval` |
| Gold lane (execution oracle, dual-arm) | `agentic/` + the deferred offline label engine |

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

## Web console

A web console over everything the pipeline produces. The **Go server is
stdlib-only** (`net/http` + a JSON API, no external Go deps); the frontend is a
**decoupled TypeScript + React app built with Vite**. The API and the UI deploy
independently — the Go binary no longer embeds the bundle, so `go build`/`make
serve` need no Node, and the UI is served by Vite (dev) or any static host.

```bash
make serve            # terminal 1: Go JSON API on :8080  (AIL_ADDR=:9000 to change port)
make console-dev      # terminal 2: Vite HMR on :5173 (proxies /api -> :8080) — open this

AIL_DATA_DIR=data_agentic make serve        # point the API at the agentic dataset
make console-preview                        # prod build served via `vite preview` (:4173, also proxies /api)
```

The UI always talks to the API on :8080 through the Vite proxy — :8080 itself is
API-only (a browser hit there returns a JSON pointer to the console).

Four tabs:
- **Data** — reconstructed sessions by source (Internal / Semi-synthetic /
  Synthetic); click a row to open its full trace as a **back-and-forth chat**
  (thinking, tool calls + results, patch, offline-engine labels, hidden-oracle
  reveal).
- **Training** — routing methods; IRT ability recovery (planted vs recovered θ);
  the **pointwise + pairwise** training rows.
- **Evals** — dual-arm gold leaderboard (AIQ, AUC, cell-B, …), routing
  distribution (local vs frontier per strategy), and the **gold rows** behind it.
- **Route** — type any prompt; it is embedded live (Ollama) and scored by every
  fitted router, showing per-router decision, consensus, and the model-free
  feature vector that drove it.

The console reads the same files the CLI stages write and calls the same
`router`/`eval`/`extract` code — a view, not a reimplementation. The UI needs
Node (`make frontend-install` once, then `make console-dev` for live edits or
`make frontend-build` to produce `frontend/dist`); the Go API does not. See
`frontend/README.md`.

---

## Commands

Everything is a `make` target — seeded and idempotent, and the backend
disk-caches every model call, so reruns are cheap and an interrupted run
resumes. Every target has a `##` doc line in the `Makefile`.

**Core pipeline** (synthetic default config)

| command | does |
|---|---|
| `make all` | `gen → extract → train → eval` end-to-end; writes `RESULTS.md` |
| `make gen` | generate synthetic CC session logs |
| `make extract` | raw logs → structured pointwise/pairwise/gold + extractor-quality report |
| `make train` | fit the candidate routers |
| `make eval` | run the eval harness → `RESULTS.md` + `eval_report.md` |
| `make test` | unit tests for the portable Go core |
| `make build` | compile all binaries into `./bin` |
| `make clean` / `distclean` | drop build + regenerable data / also drop the backend cache |

**Console**

| command | does |
|---|---|
| `make serve` | serve the JSON API on :8080 (`AIL_ADDR`, `AIL_DATA_DIR` env) |
| `make console-dev` | Vite dev server with hot reload on :5173 (proxies `/api` to `serve`) — the UI |
| `make console-preview` | build + serve the prod UI bundle via `vite preview` (:4173) |
| `make frontend-install` | install the console's npm deps (run once) |
| `make frontend-build` | rebuild the embedded UI bundle after editing `frontend/src` |

**Agentic, execution-grounded track** (real `claude -p` loop, dual-arm)

| command | does |
|---|---|
| `make agentic-smoke` | 1 task, both arms, tool-call fidelity smoke |
| `make agentic-swe SWE_N=20` | materialize N SWE-bench Verified instances, build images, run both arms in-container |
| `make agentic-generate` | run both arms over all materialized tasks (log-first, no grading) + write the split |
| `make agentic-proxy` / `agentic-proxy-stop` | start / stop the Anthropic→Ollama proxy for the local arm |

**Offline label engine** (turn log-first sessions into labels → datasets)

| command | does | needs |
|---|---|---|
| `make agentic-heuristics` | mine implicit labels from sim-user reactions | — (deterministic) |
| `make agentic-grade` | executed-oracle: run hidden tests on each patch → `executed.jsonl` | Docker / swebench venv |
| `make agentic-calibrate` | score weak labels vs executed truth + judge-primary fuse → `resolved.jsonl` | — |
| `make agentic-materialize` | fused labels → `pointwise/pairwise/gold` for train + eval | — |
| `make agentic-train` / `agentic-eval` / `agentic-fit-eval` | fit / eval / (materialize→fit→eval) on the agentic corpus | — |

The offline engine runs **after** generation, in this order:

```bash
make agentic-heuristics    # weak implicit labels (no model, no grading)
make agentic-grade         # executed truth where the oracle env exists (Docker/swebench)
make agentic-calibrate     # fuse weak + executed -> labels/resolved.jsonl (judge-primary)
make agentic-materialize   # resolved.jsonl -> data_agentic/{pointwise,pairwise,gold}.jsonl
AIL_DATA_DIR=data_agentic make serve   # inspect the result in the console
```

`materialize` reads `resolved.jsonl`, so `agentic-calibrate` must run before it;
gold rows require **both arms executed** on a holdout task, so oracle-bearing
sessions that were never graded are **quarantined** (never fabricated as truth).

## Agentic, execution-grounded track

The base pipeline labels each task with a single-shot LLM-judge verdict — which
is **not agentic** (real Claude Code drives a multi-turn tool loop over a repo)
and **circular** (the judge that trains also grades). The `agentic/` track fixes
both: it runs each task to completion **inside the real Claude Code harness**
(`claude -p`, tool-calling loop over a repo checkout) for a **local** open-weight
model and a **frontier** Claude model, then labels the outcome by **executing
the repo's hidden tests** (a non-circular oracle) — filling the
`GoldRow.Executable` seam. It measures the constraint single-shot scores hide:
**tool-call fidelity**.

```bash
make agentic-smoke   # 1 task, BOTH arms, tool-call fidelity smoke
make agentic         # full pipeline (resumable/cached) -> data_agentic/RESULTS.md
```

The local arm reaches the harness through an **Anthropic→Ollama proxy**
(`agentic/proxy/`) so it drives the exact same `tool_use`/`tool_result` protocol
as frontier. See `agentic/README.md`, `data_agentic/RESULTS.md`, and DECISIONS
D11-ag/D12–D15. This track keeps the Go/Python boundary: the schema, gold
assembly (`internal/agentic`, `cmd/agentic`) and eval harness stay portable Go;
the harness-driving glue and Docker execution live under `agentic/` (Python).

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
| `internal/generate` | **QUARANTINED** templated log generator — CI/plumbing fixture only (`provenance=templated`, never signal) |
| `internal/extract` | Pillar 1b extraction + 1c quality report |
| `internal/router` | Pillar 2 routers + interface |
| `internal/eval` | Pillar 3 harness, metrics, policy |
| `internal/server` | console backend: stdlib `net/http` JSON API only (the React UI is the separate `frontend/`) |
| `cmd/{gen,extract,train,eval}` | stage entrypoints |
| `cmd/serve` | web console server (`make serve`) |
| `cmd/{label,materialize}` | offline label engine + O6 dataset materializer |
| `frontend/` | TypeScript + React + Vite console source (builds to `frontend/dist`, served independently) |
| `agentic/` | **non-portable** log-first generation: `claude -p` dual-arm runner + Anthropic→Ollama proxy + SWE materializer + synth gate + sim-user |
| `python/` | **non-portable** encoder + SLM-head training |
| `docs/` | design docs: `ROUTER_BRAINSTORM`, `DATA_PLAN`, `OFFLINE_ENGINE_PLAN`, `DECISIONS` (+ `archive/`) |

### Data sources & the one serving path (DATA_PLAN)

All trustworthy data now comes from **one serving path** — an agentic `claude -p`
run across both rungs (`local=gpt-oss:20b` via the proxy, `frontier=opus` via the
subscription), producing a log-first artifact set (RawTurn `.session.jsonl` + raw
`.events.jsonl` + `.patch` + a grade-free run record). **No grading happens during
generation** — outcomes are the deferred offline engine's job.

| Source | Origin | Runner | Grading |
|---|---|---|---|
| **2 — semi-synthetic** | SWE-bench Verified (given) | `run_agentic.py` (`make agentic-swe`) | offline (swebench harness) |
| **3 — generated** | authored in a Claude session, OSS-grounded; gated by `agentic/synth/validate_task.py` | `run_agentic.py` | offline (docker pytest) |
| ~~templated~~ | `internal/generate` | — | **quarantined; never signal** |

The corpus is deterministically split into train/held-out (`agentic/runner/split.py`
→ `split_manifest.json`) *before* any labeling. Off-policy propensities stay null by
construction (every task runs on every rung deterministically — no logging policy).

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

Any non-stdlib Go dependency would be isolated and justified in
`docs/DECISIONS.md`. There are currently **none**.

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

See `docs/DECISIONS.md` for every choice, and `RESULTS.md` for the latest run.
