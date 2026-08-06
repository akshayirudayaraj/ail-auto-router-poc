> **ARCHIVED — delivered & superseded.** Step 1 (one real SWE-bench execution-grounded dual-arm gold row) is done; the execution-grounded track is now covered by `DATA_PLAN.md` and the agentic runner. Kept for history.

# Step 1 Plan — the execution-grounded keystone

> Handoff doc for the next agent/session. Written 2026-08-04. Owner: Akshay.
> Read this top-to-bottom before touching code. It is scoped deliberately small.

## Context: what this repo already is

`ail-routing-test` is a **complete v1 skeleton** of the §6 POC harness from
`ROUTER_BRAINSTORM.md` (predictive auto-router: for every Claude Code call, pick
the cheapest model that's still adequate; local by default, burst to frontier).
All three pillars are wired end-to-end and one full overnight run exists
(`RESULTS.md`):

- **Pillar 1 — label engine:** synthetic generator, session reconstruction,
  implicit-signal mining, offline judge, ground-truth firewall. Working.
- **Pillar 2 — routers:** RouteLLM logistic, IRT 1PL, kNN all *real*;
  encoder-MLP + SLM-head are Go stubs loading `python/artifacts/*.json`.
- **Pillar 3 — eval:** dual-arm gold, temporal backtest, off-policy IPS+DR,
  guardrail/topic-collapse suite, threshold+quota policy layer. Working.

**The honest catch:** *every input today is templated synthetic data.* The
plumbing is verified; the numbers are not yet trustworthy. Closing that is the
whole remaining job.

## The four real gaps (do NOT try to close them all)

| # | Gap | Status |
|---|-----|--------|
| **G1** | **Execution-grounded track** — real CC loop on local+frontier, graded on *hidden tests* | **Missing.** Generator emits templated stubs; gold `Executable=false` everywhere. **← Step 1 targets this.** |
| **G2** | **Source 2** benchmark-seeded (SWE-bench Verified) at scale | Missing (only fully-synthetic Source 3 exists). Step 2. |
| **G3** | Python routers (encoder-MLP / SLM-head) trained for real | `python/artifacts/` empty → they're placeholders. Step 3. |
| **G4** | Gateway integration (local via `gateway/pkg` / running gateway) | Direct Ollama shim instead. Step 4 — **deliberately deferred, see below.** |

Source 1 (real prod logs) is correctly deferred — blocked on prod logging;
requirements already documented in `README.md`.

## Build sequence (crawl → walk → run)

The key leverage: **the harness already consumes gold rows and
pointwise/pairwise data.** You don't rebuild anything — you replace one
*synthetic input* at a time with a *real* one.

1. **Step 1 (crawl — THIS DOC):** one real execution-grounded gold row from one
   SWE-bench Verified instance. Prove the plumbing.
2. **Step 2 (walk):** batch Step 1 over 20–50 instances → replace the synthetic
   gold set → absolute AIQ/quality numbers become trustworthy (G2).
3. **Step 3 (walk):** train the two Python routers for real → fill
   `python/artifacts/` (G3).
4. **Step 4 (run):** point the agentic track at the real AIL gateway + the
   RTX-6000 Gemma box (G4). One endpoint swap on a proven harness.
5. **Step 5 (run):** scale data volume to fix the small-sample CIs the run flags.

---

## Two serving paths — critical, do not unify them

There are **two different jobs**, and they need **two different local-serving
mechanisms**. Keep them separate.

- **Job A — labeling/judging** (Pillars 1 & 3 today): single-shot
  `messages → text`. The existing `internal/backend` Ollama path
  (`ollamaGenerate`, `/api/generate`) is exactly right. **No tool-use needed.**
- **Job B — the execution-grounded agentic track** (Step 1): the *real* Claude
  Code agent drives a multi-turn **tool loop** (read → edit → run tests) against
  the local model. This requires the local model behind an **Anthropic-Messages-
  compatible endpoint that speaks `tool_use`/`tool_result`**, so the `claude`
  CLI can point at it and drive it through the *identical* protocol as frontier.
  **The existing shim CANNOT do this** — it's one-shot text, no tools.

> A later "cleanup" that unifies these two paths will silently break the agentic
> track. They are intentionally separate. (Record this in `DECISIONS.md`.)

## Step 1 — exact scope

Produce **one** dual-arm gold row with `Executable=true` from **one** SWE-bench
Verified instance, by running the real Claude Code loop on a local model and on
frontier, and grading each on the instance's hidden tests.

### Serving setup (throwaway path — do NOT touch the sensitive RTX-6000 env)

- **Local model = Ollama `qwen2.5-coder` (or any pulled Qwen)** on any
  non-sensitive dev box. The goal of Step 1 is to prove *plumbing*, not to get a
  meaningful local score — Qwen is fine.
- **Tool-capable proxy (Job B):** start with an **off-the-shelf** Anthropic→Ollama
  tool-translating proxy (e.g. `claude-code-router`, or LiteLLM's Anthropic
  passthrough). It must faithfully translate `tool_use`/`tool_result`. If the
  off-the-shelf option can't drive the CC tool loop cleanly, fall back to a
  **minimal stdlib Anthropic-Messages→Ollama shim** in the repo — that shim also
  becomes the seam that Step 4 swaps for the gateway. Report which you chose and
  why in `DECISIONS.md`.
- **Frontier:** the existing `claude` CLI subscription path.
- **The RTX-6000 / Gemma box is out of scope for Step 1.** It comes in only at
  Step 4, deliberately, on a harness already proven. Do not use it here.

### The oracle (SWE-bench Verified mechanics)

A SWE-bench instance = `{repo, base_commit, problem_statement, test_patch
(hidden tests), gold_patch (reference solution), FAIL_TO_PASS, PASS_TO_PASS}`.

- **Firewall (maps to the repo's existing ground-truth-firewall convention):**
  the agent sees ONLY `problem_statement` + the repo checked out at
  `base_commit`. It must NEVER see `test_patch` or `gold_patch`. Grade by
  applying `test_patch` *after* the agent finishes.
- **Grade:** apply the agent's produced diff to `repo@base_commit`, then apply
  `test_patch`, then run the tests. **Pass = all `FAIL_TO_PASS` now pass AND all
  `PASS_TO_PASS` still pass.** That boolean is `outcome_{local,frontier}`.
- Use the official SWE-bench evaluation harness / per-instance Docker image for
  grading if practical; a hand-rolled `pytest` runner against a pinned checkout
  is an acceptable start for a single instance.

### Flow

1. Check out `repo@base_commit` into a clean working dir (one copy per arm).
2. Run `claude` headless (`-p`, non-interactive) with `problem_statement` as the
   task, `ANTHROPIC_BASE_URL` pointed at:
   - the **local proxy** (Qwen) for the local arm,
   - the **real frontier** for the frontier arm.
3. Capture each arm's resulting git diff + token/cost usage.
4. Grade each diff via the hidden-test oracle above.
5. Emit a gold row `{prompt_id, prompt_text (=problem_statement), outcome_local,
   outcome_frontier, cost_local, cost_frontier, executable: true}` into the gold
   dataset, alongside (not overwriting) the synthetic gold, tagged so
   `gold_meta.json` reflects the real/executable provenance.

### Bonus (free, but don't scope-creep)

Each real CC run also produces a genuine **session log** (the CC turns). That's
a real Source-2 session Pillar 1 extraction can consume later. Log it in the raw
JSONL format if cheap; otherwise just note the seam for Step 2. Do not build the
Source-2 batch pipeline in Step 1.

## Acceptance criteria (Step 1 is done when…)

- [ ] One SWE-bench Verified instance runs end-to-end through the real CC loop on
      **both** a local (Qwen via tool-capable proxy) and frontier arm.
- [ ] Each arm is graded on the instance's **hidden tests** (FAIL_TO_PASS +
      PASS_TO_PASS), producing a real boolean outcome per arm.
- [ ] One gold row with `Executable=true` lands in the gold dataset and
      `gold_meta.json` reflects real provenance.
- [ ] `make eval` runs over the mixed (synthetic + 1 real) gold set without
      choking on the `Executable=true` row.
- [ ] The firewall holds: a check/test confirms the agent never received
      `test_patch`/`gold_patch`.
- [ ] `DECISIONS.md` records: the proxy choice + tradeoff, the two-serving-paths
      separation, and that the RTX-6000 env was intentionally not used.
- [ ] The sensitive RTX-6000 / Gemma environment was **not touched.**

## Explicit non-goals for Step 1

- No scaling to many instances (that's Step 2).
- No training the Python routers (Step 3).
- No real gateway / Gemma integration (Step 4).
- No changes to the portable Go core's dependency policy — any external dep must
  live only in the throwaway Job-B proxy path, never in the stdlib-only core, and
  be justified in `DECISIONS.md`.
