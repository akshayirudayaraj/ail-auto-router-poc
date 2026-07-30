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

<!-- More decisions appended as the build proceeds. -->
