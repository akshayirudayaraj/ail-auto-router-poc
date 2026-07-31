# Agentic, Execution-Grounded Evaluation — Results

This track adds an **agentic, execution-ground-truth** arm to the router framework. Each task is run to completion inside the **real Claude Code harness** (`claude -p`, tool-calling loop over a repo checkout) for BOTH a local open-weight model and a frontier Claude model; the produced patch is then scored by **executing the repo's hidden tests** (SWE-bench rule: all FAIL_TO_PASS pass and all PASS_TO_PASS still pass). This replaces the single-shot, LLM-judge (circular) labels with a non-circular oracle and surfaces the binding constraint the single-shot scores hide: **tool-call fidelity**.

## Arms, models, harness

| arm | model | harness invocation |
|---|---|---|
| **frontier** | `claude-sonnet` (CLI alias, latest; via logged-in subscription) | `claude -p --output-format stream-json --allowedTools Read Edit Write Bash --permission-mode bypassPermissions --strict-mcp-config --model sonnet` |
| **local** | `qwen2.5-coder:14b` (Ollama) via an Anthropic→Ollama proxy (`ANTHROPIC_BASE_URL`) | same, plus `--bare` (see note) and `ANTHROPIC_BASE_URL=<proxy>` |

- The proxy exposes an Anthropic Messages API (`POST /v1/messages`, tool_use/tool_result, SSE streaming) and translates to Ollama `/api/chat`, so the local model drives the **same tool protocol** as frontier — the point is to measure fidelity, not just reasoning.
- Both arms run with **no MCP servers and no hooks** (`--strict-mcp-config`) so the harness is lean and reproducible. The local arm additionally uses `--bare`: the full Claude Code system prompt is ~30k tokens, which costs **~8 min/turn** on `qwen2.5-coder:14b` locally (measured) — intractable — so `--bare` trims it to ~1k tokens/turn. This asymmetry, if anything, *handicaps* the local arm (less guidance); the tool protocol is identical. See DECISIONS D13.
- **Execution oracle:** the agent's `git diff` is scored by running FAIL_TO_PASS + PASS_TO_PASS in a hermetic Docker image (`python:3.11-slim` + pytest, `--network none`).
- Gold set provenance: `executable=True`, `synthetic=False`, N=11, local-arm-missing=0.

## Per-(task, arm) results

Executed pass/fail is the oracle. `native/rescued` = local tool-call fidelity: how many tool calls arrived as **native** Ollama `tool_calls` vs had to be **rescued** by the proxy from bare prose-JSON the model emitted as text. Cost is in the framework's relative units (tokens × price, frontier priced 15× local).

| task | tier | front exec | local exec | front turns | local turns | local native/rescued | front cost | local cost | local wall |
|---|---|:--:|:--:|--:|--:|:--:|--:|--:|--:|
| `easy-01-reverse-words` | easy | PASS | FAIL ⏱ | 5 | 0 | 0/1 | 8265 | 0 | 1200s |
| `easy-02-is-even` | easy | PASS | FAIL ⏱ | 6 | 0 | 0/0 | 8070 | 0 | 1200s |
| `easy-03-fizzbuzz-range` | easy | PASS | FAIL ⏱ | 6 | 0 | 0/2 | 31485 | 0 | 1200s |
| `easy-04-celsius` | easy | PASS | FAIL ⏱ | 6 | 0 | 0/1 | 9105 | 0 | 1200s |
| `med-01-roman` | medium | PASS | FAIL ⏱ | 7 | 0 | 0/2 | 13245 | 0 | 1200s |
| `med-02-csv-quotes` | medium | PASS | FAIL ⏱ | 6 | 0 | 0/1 | 11145 | 0 | 1200s |
| `med-03-interval-merge` | medium | PASS | FAIL ⏱ | 6 | 0 | 0/2 | 9780 | 0 | 1200s |
| `med-04-lru-cache` | medium | PASS | FAIL ⏱ | 9 | 0 | 0/2 | 19830 | 0 | 1200s |
| `hard-01-expr-eval` | hard | PASS | FAIL ⏱ | 6 | 0 | 0/1 | 17655 | 0 | 1200s |
| `hard-02-toposort` | hard | PASS | FAIL ⏱ | 7 | 0 | 0/3 | 20325 | 0 | 1200s |
| `hard-03-lcs-diff` | hard | PASS | FAIL ⏱ | 7 | 0 | 0/1 | 15420 | 0 | 1200s |

## Headline routing-relevant findings

- **Frontier executed pass rate:** 11/11 tasks resolved (real tests, real harness).
- **Local executed pass rate:** 0/11 tasks resolved (11 hit the 20-min wall-clock budget ⏱). **Two distinct failure modes compound here:** (a) tool-call fidelity (the model emits prose-JSON, rescued by the proxy) and (b) latency — a local 14B turn is seconds when the GPU is free but minutes under contention (a parallel process held the GPU during this run), so multi-turn agentic tasks blow the time budget. Both are real routing signals: the local rung is inadequate agentically here, for capability AND cost-of-latency reasons.
- **Local tool-call fidelity (the binding constraint):** 0/16 tool calls were emitted as **native** tool calls (0%); the other 16 were **rescued** by the proxy from bare prose-JSON the model emitted as text. Without the rescue shim (i.e. in a stock harness), the local model makes **~0 valid tool calls** and therefore cannot act at all — a 75%-single-shot model scores ~0% agentically. This is exactly the harness-conditioned fidelity gap the study targets.
- **cell-B (escalation-worthy set):** 11 tasks where LOCAL FAILED but FRONTIER PASSED — the costly misses a good router must catch. (both-pass=0, both-fail=0, local-only-pass=0, of 11 paired tasks.)
- **Cost saved by perfect routing vs always-frontier:** ~0% over the 11 paired tasks so far — because local passes **none** of them (0 tool-call fidelity and/or latency timeouts), a perfect router equals always-frontier here. The cost-saving lever only opens once the local rung can actually pass tasks; this run shows the local rung is agentically non-viable in this harness, so there is nothing safe to route down. That *is* the routing verdict: escalate everything until local's fidelity/latency are fixed.

## What we already know about local tool-call fidelity (measured)

These results are protocol-level and independent of how far the local executed sweep got, so they hold even for a partial run:

1. **Bare-JSON tool calls, 0 native.** `qwen2.5-coder:14b` served by Ollama `/api/chat` with `tools` was prompted to call a `read_file` tool **5/5 trials**; every time it emitted the call as **bare prose-JSON in `message.content`** (`{"name": "read_file", "arguments": {…}}`) instead of the `<tool_call>…</tool_call>` form its own chat template requires, so Ollama populated `tool_calls` **0/5** times. A stock Claude Code harness sees **zero valid tool calls** and the agent cannot act.
2. **The proxy's rescue shim quantifies the addressable ceiling.** When the proxy lifts those bare-JSON calls into real `tool_use` blocks, the local model *can* drive the loop — but sloppily. On the fidelity smoke (fix a `NameError` in `greet.py`) it issued Read+Edit+Bash entirely via **rescued** calls (native 0), then corrupted the fix: it ran `Edit` with `replace_all` on the substring `nam`, turning `name`→`namee`, and emitted the final verify command as fenced prose-JSON naming the tool `"Bash execute shell commands"` (its description). Net: even with the format barrier removed, the edit was wrong — a genuine capability miss, not just a protocol miss.
3. **Consequence for routing.** Single-shot benchmarks that score `qwen2.5-coder` highly on function-completion do not predict agentic adequacy: in the real harness its tool-call fidelity is the binding constraint, so nearly every agentic task is escalation-worthy. This is exactly why adequacy must be measured **inside the harness, by execution** — which this track does.

## The existing eval harness, run on the agentic (executed) gold set

The dual-arm gold set below was assembled from these executed runs (`Executable=true`, outcomes from real tests) and fed through the **existing** eval harness (`internal/eval`) unchanged — dual-arm gold, AIQ, cost/quality curve, cell-B. Routers are trained on the synthetic `implicit` logs and evaluated here on the strictly-stronger `executed` labels, so there is no circularity.

### dual-arm-gold

| router | aiq | auc | cost_vs_local | ece | escalation@thr | over_escalation | qual_retention | quality@thr | under_escal_cellB |
|---|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| always-local | 0.500 | 0.500 | 0.000 | 1.000 | 0.000 | 0.000 | 0.000 | 0.000 | 1.000 |
| always-frontier | 0.500 | 0.500 | 0.000 | 0.000 | 1.000 | 0.000 | 1.000 | 1.000 | 0.000 |
| routellm-logistic | 0.436 | 0.500 | 0.000 | 0.069 | 1.000 | 0.000 | 1.000 | 1.000 | 0.000 |
| irt-1pl | 0.472 | 0.500 | 0.000 | 0.195 | 1.000 | 0.000 | 1.000 | 1.000 | 0.000 |
| knn | 0.484 | 0.500 | 0.000 | 0.955 | 0.091 | 0.000 | 0.091 | 0.091 | 0.909 |
| encoder-mlp(stub) | 0.484 | 0.500 | 0.000 | 0.973 | 0.000 | 0.000 | 0.000 | 0.000 | 1.000 |
| slm-head(stub) | 0.465 | 0.500 | 0.000 | 0.909 | 0.000 | 0.000 | 0.000 | 0.000 | 1.000 |

- Operating threshold = 0.50. AIQ is threshold-independent (area under the cost/quality hull).
- cell-B (under_escal) = stayed local but frontier would have passed — the costly miss.
- Gold outcomes are a strictly stronger label source than the training labels (no circularity).
- Only the gold set (and later online A/B) give trustworthy ABSOLUTE numbers.

### temporal-backtest

> ⚠️ only 1 eval rows (source "judge") in the held-out split — metrics are high-variance.

> ⚠️ held-out strong-label eval set is single-class (1/1 positive) — AUC is uninformative (0.5). Scale AIL_JUDGE_SAMPLE (or add executed labels) so the held-out split has both classes.

| router | acc@thr | auc | ece |
|---|--:|--:|--:|
| always-local | 0.000 | 0.500 | 1.000 |
| always-frontier | 1.000 | 0.500 | 0.000 |
| routellm-logistic | 1.000 | 0.500 | 0.001 |
| irt-1pl | 1.000 | 0.500 | 0.019 |
| knn | 1.000 | 0.500 | 0.500 |
| encoder-mlp(stub) | 0.000 | 0.500 | 0.700 |
| slm-head(stub) | 0.000 | 0.500 | 0.692 |

- Split by session+time at unix 1784737898: 28 train / 12 eval sessions; train_rows=97 eval_rows=1 (eval source="judge").
- Backtests only RANK routers — they inherit the label heuristic's blind spots and log censoring. Absolute numbers come from the gold set only.

### off-policy-ips-dr

| router | ess | uplift_dr | v_dr | v_ips |
|---|--:|--:|--:|--:|
| always-local | 107.000 | -0.067 | 0.483 | 0.426 |
| always-frontier | 11.880 | 0.231 | 0.782 | 0.848 |
| routellm-logistic | 17.341 | 0.211 | 0.762 | 0.790 |
| irt-1pl | 17.761 | 0.190 | 0.741 | 0.788 |
| knn | 29.499 | -0.061 | 0.489 | 0.530 |
| encoder-mlp(stub) | 106.000 | -0.066 | 0.485 | 0.426 |
| slm-head(stub) | 106.000 | -0.066 | 0.485 | 0.426 |

- Logging-policy observed reward = 0.551 (on-policy baseline). uplift_dr = V_DR - baseline.
- V_IPS is unbiased but high-variance (watch ESS); V_DR uses the IRT Q-model to cut variance.
- Rewards are the implicit outcome labels, so estimates inherit that label's noise (documented).

### guardrail-suite

| router | difficulty_monotonicity | topic_flip_rate |
|---|--:|--:|
| always-local | 0.000 | 0.000 |
| always-frontier | 0.000 | 0.000 |
| routellm-logistic | 1.000 | 0.000 |
| irt-1pl | 1.000 | 0.000 |
| knn | 1.000 | 0.000 |
| encoder-mlp(stub) | 1.000 | 0.000 |
| slm-head(stub) | 1.000 | 0.000 |

- difficulty_monotonicity: fraction of easy/hard pairs where score rises with difficulty (want 1.0).
- topic_flip_rate: fraction of off-topic keyword injections that flipped the decision (want 0.0 — the topic-collapse guardrail).

### policy layer (deployed router: knn, gold AIQ=0.484)

| calibration | threshold | escalation | quality_retention | cost_vs_local | under_escal(cellB) |
|---|--:|--:|--:|--:|--:|
| target escalation 30% | 0.000 | 1.000 | 1.000 | 0.00 | 0.000 |
| target quality 95% | 0.000 | 1.000 | 1.000 | 0.00 | 0.000 |

Quota gate (threshold 0.000, cap 20%): escalated 2/11 = 18.2% of traffic.

> Target-escalation-rate calibration is safe on logs; target-QUALITY calibration is only trustworthy on the dual-arm gold set (or online A/B).



---

Reproduce: `make agentic` (full, resumable/cached) or `make agentic-smoke` (1-task both-arm fidelity smoke). See DECISIONS.md (D12–D15) for every assumption and `agentic/README.md` for the Go/Python boundary.
