# Results

End-to-end run on the small default config (seed=42, 2 local models + frontier `claude-sonnet-5`).

_Absolute cost/quality numbers come from the dual-arm gold set only; backtests rank routers; off-policy estimates the counterfactual reward from logged propensities._

## How to read this report

- **Pillar 1 (label engine).** `implicit` precision/recall of catching the *inadequate* answers that need escalation, graded vs planted truth. High precision + partial recall is the intended profile: implicit signals are trustworthy when present but miss quietly-abandoned failures.
- **Pillar 2 (routers).** IRT ability recovery: recovered θ ordering/sign should match planted (magnitudes compress under noisy labels — that's fine, routing only needs the ordering).
- **dual-arm-gold** is the only ABSOLUTE anchor. Read it as: **AIQ** (higher = more quality per unit cost; a good learned router beats both baselines), **qual_retention** vs **cost_vs_local** (e.g. matching frontier quality at a fraction of frontier cost is the win), and **under_escal_cellB** (the costly miss — lower is better).
- **temporal-backtest** only RANKS (observational censoring). It enforces eval labels be a strictly-stronger source than train; at this tiny scale the held-out judge set can be single-class, making AUC uninformative (see its warning) — that is a data-scale limit, not a router verdict.
- **off-policy-ips-dr** estimates the reward of *deploying* each router from logs with propensities; `uplift_dr` > 0 means it beats the logging policy. Watch **ess** (low ⇒ high-variance IPS).
- **guardrail-suite**: `difficulty_monotonicity` should be ~1.0 and `topic_flip_rate` ~0.0 (routes on difficulty, not topic). Baselines score 0 monotonicity by design (constant scores).
- **policy layer** shows a deployable threshold calibrated on the best-AIQ router, plus a frontier quota gate.

---

## Pillar 3 — evaluation harness

Gold set meta:
```json
{
  "synthetic": false,
  "executable": true,
  "local_model": "qwen2.5-coder:14b (via Anthropic-\u003eOllama proxy)",
  "frontier_model": "claude-sonnet (CLI alias; via subscription)",
  "n": 11,
  "local_arm_missing": 6
}
```

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

See DECISIONS.md for choices and README.md to reproduce (`make all`).
