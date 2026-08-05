# Decisions

Every non-obvious choice, and every place the brief was ambiguous, is recorded
here with its rationale. Newest sections may be appended over the build.

## D1 — Language & dependency policy
- **Portable core is Go, stdlib-only.** No third-party Go modules in the core
  (schema, config, backend, log parsing, extraction, numerics, routers, IRT,
  eval, metrics, policy). Numerics (dot/cosine, logistic regression via
  gradient descent, IRT via Newton) are hand-rolled.
- **`go 1.23`** in `go.mod` (installed toolchain is 1.26.3). Pinned lower for
  portability into other environments; nothing here needs >1.23.
- **Python is optional and quarantined under `python/`** — used only for the
  two pieces Go genuinely can't carry: fine-tuning a neural encoder and a small
  LM router head. The Go side ships these as interface + stub.

## D2 — Model roster (driven by what's installed locally)
- Brief suggested `all-minilm` for embeddings; the machine has
  **`nomic-embed-text`** pulled (768-dim) and not all-minilm. Chose the
  installed model to avoid a pull; it is a config knob (`AIL_EMBED_MODEL`).
- **Local rung = `llama3.1:8b` + `qwen2.5-coder:14b`** (both already pulled).
  Two local models — one general, one code-specialized — is deliberate: it
  makes model-relative adequacy non-degenerate (the whole point of the router),
  and it exercises the multi-model IRT ability estimates.
- **Frontier + judge = `claude-sonnet-5`** via the `claude` CLI subprocess
  (uses the logged-in subscription; no API key, no per-token billing). Direct
  HTTP with `ANTHROPIC_API_KEY` is the auto-detected fallback.

## D3 — Anthropic auth
- Prefer the `claude` CLI subprocess (subscription). The Go backend must pass
  `HOME`, `PATH`, `USER`, `LOGNAME` through to the subprocess or the CLI
  misreports a Keychain-read failure as "Credit balance is too low". Handled in
  the backend; nothing for the operator to configure.

## D4 — Spend discipline
- Small defaults (≈60 sessions, 40 gold rows, judge only a 40-pair sample) so a
  full `make all` completes overnight with a bounded, logged number of paid
  (subscription) calls. Hard caps (`AIL_MAX_*`) make the backend refuse calls
  past a ceiling. The disk cache makes reruns free.

## D5 — Hidden ground-truth convention
- The synthetic generator plants outcome truth in underscore-prefixed JSON
  fields (`_true_adequate`, ...). Extraction is always run on turns passed
  through `RawTurn.StripHidden()`, and a unit test guards that extraction never
  sees a populated hidden field. This keeps Pillar 1c an honest grader.

## D6 — Extraction: implicit signals as noisy features, judge as anchor
- Weak outcome labels are mined from the NEXT user turn. The local→frontier
  **switch** is detected structurally from known cost tiers (not wording) and is
  the highest-confidence failure signal (0.90). Confidences descend:
  paste_error 0.80, negative 0.75, retry 0.65; success: moveon 0.70, clean
  session-end 0.55, ambiguous 0.50.
- The **judge** grades a sampled subset and is emitted as separate
  judge-sourced rows (same prompt_id). This gives a mixed-source dataset so the
  eval harness can train on `implicit` and evaluate on the strictly-stronger
  `judge` — no label circularity.
- **Ground-truth firewall:** extraction loads logs through `StripHidden()` and
  asserts no `_`-field survives; the quality report reads truth from a separate
  un-stripped load and re-derives predictions. Verified: implicit labeler
  P=1.00 / R=0.66 on the small config.
- **Judge metric on synthetic data is not meaningful** and is labeled as such:
  planted-"adequate" responses are templated stubs a strict judge correctly
  flags, so judge-vs-truth measures template realism. On real responses it
  becomes the meaningful anchor. (See report Note.)

## D7 — Pillar 2 data shapes & derivations
- **RouteLLM (pairwise):** logistic regression over the 10 structural features
  (NOT the 768-d embedding — too few samples, keeps it portable). Trains on
  real local-vs-frontier pairwise rows **plus** pointwise local-served rows as
  pseudo-pairs (local inadequate ⇒ "frontier preferred"). This is the
  pointwise→pairwise derivation the brief asks to document.
- **IRT 1PL (pointwise):** `P(success|m,i)=sigmoid(theta_m - (w·x+c))`, fit
  jointly by weighted MLE (gradient descent), confidence-weighted. Reference
  model (local[0]) pinned θ=0 for identifiability. New-model onboarding freezes
  b_i and Newton-solves one scalar θ. Recovery verified in tests (θ gap and
  ordering recovered within tolerance).
- **kNN (pointwise, training-free):** cosine-weighted vote over local-served
  neighbors' outcomes; embedding-based, drift-friendly.
- **encoder-MLP / SLM-head:** Go exposes the `Router` interface + a functional
  baseline stub that loads `python/artifacts/*.json` if present. Real training
  is non-portable (Python). These are the only non-portable routers.
- Escalation score convention: **higher = local more likely inadequate**
  (P(escalate)); a threshold turns it into a decision. Baselines `always-local`
  / `always-frontier` anchor the eval.

## D8 — Gold set: real arms with synthetic fallback
- Dual-arm gold generation actually calls **both** arms (local = `LocalModels[0]`
  = the weaker general model, to make escalation value visible; frontier =
  Claude) and judges both. If Anthropic is unavailable or a cap is hit, the
  whole set falls back to synthetic 1PL-planted outcomes (flagged in
  `gold_meta.json`) so eval runs anywhere. `Executable=false` marks the seam for
  later real unit-test outcomes.
- Cost units: tokens × price weight, frontier priced 15× local (ratio is what
  matters, not scale).

## D9 — Eval harness guardrails (the four hard problems, made explicit)
- **Censoring:** observational logs serve one model per prompt, so the
  backtest and off-policy methods only use LOCAL-served rows to ask "did local
  fail?" and are documented as RANKING tools. Absolute cost/quality comes ONLY
  from the dual-arm gold set (both arms observed).
- **No oracle:** "adequate" is a proxy; the judge is fallible. The gold seam
  `Executable` marks where real unit-test pass/fail would replace judged
  outcomes.
- **Feedback loop:** a deployed router reshapes traffic — so absolute numbers
  are only claimed for the static gold set and (future) online A/B, never for
  backtests.
- **Label circularity:** the backtest ENFORCES eval-source > train-source
  (executed > human > judge > implicit) and WARNs if they match (self-agreement,
  not correctness).
- **Off-policy** REFUSES deterministic logs (needs stochastic propensities +
  overlap); the epsilon-greedy logging policy provides them. DR uses a fitted
  IRT as the Q-model.
- **AIQ** integrates the achievable-quality frontier over the GLOBAL cost range
  (shared across routers) so it actually ranks them; a per-router-range
  normalization (first attempt) collapsed the comparison and was fixed.
- **cell-B** = under-escalation (stayed local, local failed, frontier would have
  passed) — the costly miss; over-escalation (escalated, local would have
  passed) is reported alongside as waste.

## D10 — Python boundary is a demonstration seam
- The two non-portable routers export a **linear JSON head** the Go stubs load
  (`sigmoid(w·embedding+b)`). The encoder-MLP head is over the SAME nomic
  embeddings the Go dataset carries (consistent train/inference). A true
  fine-tuned encoder/SLM would be served via endpoint/ONNX in production — the
  JSON head demonstrates the integration seam, documented as such. Scripts are
  optional and gated on torch/sentence-transformers being installed.

## D11 — Gold difficulty mix reflects real traffic (headroom)
- The first full run exposed a subtlety worth keeping: routers predict "local
  will be inadequate" very well (gold AUC 0.85–1.00), but a uniform gold sample
  over the task bank was **40% one-shot-impossible tasks** (implement Raft /
  a lock-free queue in a single turn) where BOTH arms fail per a strict judge —
  no routing headroom, and escalating them just burns 15× cost for zero quality.
  That flattened AIQ and made "don't escalate" near-optimal.
- **Insight (documented in RESULTS):** predicting `local fails` ≠ predicting
  `frontier rescues`. The addressable band is where local fails AND frontier
  succeeds — empirically the *medium* tier (local 0.00 → frontier 0.50).
- **Fix:** gold prompts are now tier-weighted **easy 0.40 / medium 0.45 / hard
  0.15**, matching the difficulty mix real CC traffic actually shows (mostly
  small edits/questions, few one-shot-impossible tasks). This is more realistic,
  not a thumb on the scale — it removes no-headroom noise so the cost/quality
  curve is informative. Weights are a code constant (`goldTierWeights`).
- Judge noise remains visible (frontier occasionally judged worse than local on
  trivial tasks) — that's a real property of an LLM-judge proxy and exactly the
  "no oracle" hazard the harness is meant to surface, not hide.

<!-- More decisions appended as the build proceeds. -->

## D11-ag — Tool-call fidelity is the binding constraint (measured)
- `qwen2.5-coder:14b` served by Ollama's `/api/chat` with `tools` emits its tool
  calls as **bare prose-JSON in `message.content`** (e.g. `{"name":"Read",
  "arguments":{...}}`) instead of the `<tool_call>…</tool_call>` form its own
  chat template asks for — so Ollama never populates `message.tool_calls`
  (**0/5 native** over repeated trials, reproduced in-harness). A stock Claude
  Code harness therefore sees **zero valid tool calls** and the agent cannot
  act, exactly the fidelity gap the study predicts. The proxy records native vs
  rescued per response so this is a first-class metric, not an anecdote.

## D12 — Agentic track: Go/Python boundary preserved
- New portable-Go pieces: `GoldRow.Executable` (already seamed), the executed
  gold assembly + scoring integration (`internal/agentic`, `cmd/agentic`). These
  produce a gold set the **existing** eval harness consumes unchanged.
- Non-portable orchestration is quarantined under `agentic/` (Python + Docker):
  the `claude` CLI driver, the Anthropic→Ollama proxy, and Docker pytest
  execution. Justified: driving a subprocess harness and containerized test
  execution are not things the stdlib-Go gateway should carry.

## D13 — Harness config: symmetric where it matters, asymmetric where forced
- Both arms run the REAL `claude -p` harness with the SAME tools
  (Read/Edit/Write/Bash), same task prompt, same `--max-turns`, same
  `bypassPermissions`, and **no MCP/hooks** (`--strict-mcp-config`) for a lean,
  reproducible run. (Without it the inherited MCP servers balloon the context
  past 30k tokens and hang local startup.)
- The local arm additionally uses **`--bare`**. Two reasons: (1) the full CC
  system prompt is ~30k tokens → **~8 min/turn** on `qwen2.5-coder:14b`
  (measured 501 s for one 30k-token turn) → intractable; `--bare` trims it to
  ~1k tokens/turn. (2) `--bare` skips keychain reads, which the local arm (dummy
  env auth via the proxy) doesn't need — but which the frontier arm REQUIRES, so
  `--bare` cannot be used on frontier. This asymmetry (frontier gets the richer
  system prompt) if anything **handicaps** local; the **tool protocol is
  identical**, so tool-call fidelity — the primary metric — is measured on equal
  footing.
- Local KV context is capped at `num_ctx=8192` with `keep_alive=60m` (model
  stays resident so each task's first turn doesn't pay a ~5 min cold load).

## D14 — Tasks: curated executable set (Docker available; SWE-bench-shaped)
- Docker IS available, so execution runs in containers (hermetic
  `python:3.11-slim` + pytest, `--network none`). Rather than a subset of
  upstream SWE-bench Verified, the set is **11 curated, self-contained Python
  bug-fix tasks** built the same way (issue + repo at a base commit +
  FAIL_TO_PASS/PASS_TO_PASS), spanning easy→hard. Rationale: (a) the routing
  signal needs a **controlled difficulty spread** (some tasks local can do, some
  it can't) which a random SWE-bench subset does not guarantee; (b) upstream
  images are large and running a slow local 14B agent over big real repos
  (django/sympy) for 12–20 tasks × 2 arms is not overnight-tractable; (c) each
  task is **auto-validated** fail-before / pass-after, so the executed labels are
  trustworthy. The runner and executor are SWE-bench-shaped: to point at real
  SWE-bench Verified, materialize a task dir with the same `task.json`
  (issue/FAIL_TO_PASS/PASS_TO_PASS/test_cmd) and repo checkout — no runner change.

## D15 — Cost, caching, spend discipline (agentic)
- Gold cost units follow the repo convention: `tokens × price`, frontier priced
  **15× local**. The runner also records the real frontier `total_cost_usd` from
  the CLI (subscription-equivalent) for the writeup; local has no $ cost so its
  cost is token-based only.
- Every `(task, arm, config-hash)` result is cached to `agentic/results/`, so an
  interrupted overnight run resumes without re-running or re-paying. The frontier
  arm is hard-capped by `MAX_FRONTIER_USD` (default $6); running totals logged.
- Environmental note: this machine had a **parallel process holding the GPU**
  (a separate `qwen3:14b` at 100% GPU) during the run, which serializes/slows
  the local arm. The frontier arm (API) is unaffected; the local arm is
  resumable and completes as GPU windows open. Partial-but-real results are the
  deliverable when a full local sweep is blocked (see the brief's Part 5).

## D16 — Step 1: one REAL SWE-bench Verified instance through the proven plumbing

The curated set (D14) proved the agentic loop + Docker grading; Step 1 closes the
last gap by driving **one genuine SWE-bench Verified instance** (`psf__requests-1142`)
end-to-end. What changed, and what deliberately did not:

- **Instance choice.** `psf__requests-1142` — F2P=1, P2P=5 (fastest grading), short
  381-char problem statement (clean firewall). Materialized by
  `agentic/runner/materialize_swe.py` into the SAME `tasks/<id>/` layout as D14
  (`repo/` at base_commit with `.git` stripped, `task.json`), with `test_patch` +
  `gold_patch` quarantined in `_oracle/` OUTSIDE `repo/`. "No runner change" (D14)
  held: `run_swe_arm.py` reuses the exact `claude -p` invocation.

- **TWO GRADING PATHS, kept separate (mirrors the two SERVING paths).** The curated
  tasks grade with the in-repo `python:3.11-slim` + pytest Docker executor. A real
  SWE-bench instance cannot be graded that way (its hidden F2P test lives in
  `test_patch`, applied only at grade time, and it needs the instance's own env).
  So the real arm grades with the **official swebench harness** (v4.1.0, reused from
  the sibling `ail-self-routing/.venv_swe`), fed the agent's diff as the prediction;
  the harness applies `test_patch` itself, so the ground-truth firewall holds by
  construction. `agentic/runner/test_firewall.py` independently asserts the hidden
  tests are absent from the agent-visible `repo/` and the gold fix is not pre-applied.
  A later "cleanup" that routes real instances through the curated executor would
  silently misgrade them — the two grading paths are intentionally separate.

- **Proxy: kept the in-repo shim, no off-the-shelf proxy needed.** The plan allowed
  starting with claude-code-router / LiteLLM and falling back to a minimal in-repo
  shim. The in-repo `ollama_anthropic_proxy.py` already translates `tool_use` /
  `tool_result` faithfully and — critically — reads NATIVE Ollama `message.tool_calls`
  first (`build_anthropic_blocks`), only rescuing prose-JSON when absent. So no
  external proxy was introduced; the external dependency footprint stays confined to
  the Job-B Python quarantine (proxy + swebench harness), never the stdlib-only Go core.

- **Local model swap: gpt-oss:20b, not qwen2.5-coder.** qwen2.5-coder was the binding
  constraint (~0% native tool-call fidelity; 100% prose-JSON rescued — D14 /
  RESULTS_AGENTIC). Probed and confirmed **gpt-oss:20b emits clean NATIVE
  `message.tool_calls`**; in the Step-1 run it drove the loop at **5/5 = 100% native
  fidelity, 0 rescued**. This resolves the harness-faithfulness gap. A minor deviation
  from the plan's "Ollama Qwen" letter, aligned with its intent (throwaway local, prove
  plumbing) — and it makes the plumbing proof stronger. Result: `outcome_local=0`
  (gpt-oss explored via 5 native tool calls but committed no edit → empty diff),
  `outcome_frontier=1` (resolved) — a discriminative dual-arm row (local fails /
  frontier succeeds). Getting a non-degenerate local *attempt* (turn/prompt tuning) is
  Step-2 territory, not Step 1.

- **Gold emission.** `agentic/runner/emit_gold.py` appends the single dual-arm
  `Executable=true` row to `data_agentic/gold.jsonl` (append-if-absent, 11→12 rows) and
  updates `gold_meta.json` (`real_swe_verified_instances`). Hand-emitted rather than via
  the Go assembler (`cmd/agentic`), which consumes the curated-runner format + Docker
  executor; batch SWE assembly through the Go path is a Step-2 item. `make agentic-eval`
  runs clean over the mixed 12-row set.

- **RTX-6000 / Gemma environment: NOT touched.** All local serving used throwaway Ollama
  (gpt-oss:20b) on the dev box. The sensitive box + gateway remain a later step (D14/Step 4).

## D17 — DATA_PLAN: log-first agentic generation (no grading during generation)

Executing DATA_PLAN.md. The agentic dual-arm runner becomes the ONE generation
engine and stops grading; grading/judging/scoring all move to a deferred offline
engine. Choices made in Phase 1:

- **One serving path, no grading during generation.** `run_agentic.py:run_one`
  no longer calls `score_checkout` (removed the `executor` import); `run_swe_arm.py`
  no longer calls `grade()` on the generation path (the swebench wrapper is parked,
  documented OFFLINE-ENGINE-ONLY). Run records carry NO `resolved`/`fail_to_pass_ok`/
  `per_node`. The base `repo/` + `_oracle/` are preserved so the offline executed
  oracle can grade later from (base repo + agent diff + test_patch).

- **Two log artifacts per (task, arm), plus the record.** `run_one` now emits a
  portable RawTurn `<...>.session.jsonl` (user prompt + concatenated assistant turn;
  `served_model` set; `propensity` null — every task runs on every rung
  deterministically, so there is no logging policy and no propensity) which
  `internal/extract` consumes unchanged, and the raw CC `<...>.events.jsonl` event
  stream (tool_use/tool_result, native/rescued, usage) for the UI trace + fidelity.

- **Configurable roster keyed on `served_model`.** `ARM_MODELS` is an ordered
  cheap→expensive list; frontier default swapped `sonnet`→**`opus`** (subscription),
  local = **gpt-oss:20b** via the proxy. Adding a mid-rung = adding an arm.

- **Latency ≠ capability.** `timed_out` / `hit_turn_cap` stay first-class on the
  record; a timeout is a routing signal, never a capability outcome.

- **Frontier Max rate-limit backstop.** A frontier usage-limit is a TRANSIENT: the
  `(task, arm)` is neither written nor cached (free resume), with bounded backoff.
  Detection is FRONTIER-ONLY (a local Ollama arm can never be Max-rate-limited) and
  scans only an ERRORED result's human-text fields + stderr — never the full raw
  stream, whose ever-present `anthropic-ratelimit-*` / `usage` metadata otherwise
  false-matches `429`/`rate_limit` on successful runs (a bug caught and fixed on the
  first Opus smoke). Verified: local gpt-oss:20b 9/9 native, 0 rescued, non-empty
  patch; Opus 18 turns, non-empty patch, no false positive.

- **Proxy hardening.** `call_ollama` now surfaces Ollama's actual error body (not the
  opaque HTTP status) and retries transient 5xx (a cold gpt-oss:20b load under GPU
  contention can 500 on the first request).

- **Cost under subscription.** `reported_cost_usd` (from the stream-json result event)
  is a THEORETICAL dollar figure the CLI reports even on the subscription — a useful
  throttle, but the real Max constraint is a rate limit, not $. Logged as such.
