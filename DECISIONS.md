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

<!-- More decisions appended as the build proceeds. -->
