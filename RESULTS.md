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

## Pillar 1 — label engine

## Extractor quality (Pillar 1c)

Graded against planted ground truth over 201 assistant turns. Positive class = *inadequate* (the answer that needed escalation).

| Labeler | N | Accuracy | Precision | Recall | F1 |
|---|--:|--:|--:|--:|--:|
| implicit heuristics | 201 | 0.761 | 1.000 | 0.655 | 0.791 |
| frontier judge (sample) | 40 | 0.550 | 0.550 | 1.000 | 0.710 |

### Per-signal precision (implicit)

| signal | n | correct | precision |
|---|--:|--:|--:|
| complete | 60 | 12 | 0.200 |
| moveon | 50 | 50 | 1.000 |
| negative | 17 | 17 | 1.000 |
| paste_error | 28 | 28 | 1.000 |
| retry | 18 | 18 | 1.000 |
| switch | 28 | 28 | 1.000 |

> Implicit signals are NOISY FEATURES anchored by the judge, not clean labels. Positive class = 'inadequate' (needed escalation). CAVEAT: on SYNTHETIC data the judge row grades templated stub responses; a strict judge correctly flags a planted-'adequate' stub as inadequate, so judge-vs-planted-truth here mostly measures template realism, not judge quality. The judge path is validated for wiring; on REAL responses judge-vs-truth is the meaningful anchor. The implicit metric is meaningful either way (it grades signal mining).


## Pillar 2 — routers

## Router training summary

Fit on 241 pointwise / 204 pairwise rows (train source = implicit).

Routers: always-local, always-frontier, routellm-logistic, irt-1pl, knn, encoder-mlp(stub), slm-head(stub)

### IRT ability recovery (θ, reference-centered)

| model | planted θ | recovered θ |
|---|--:|--:|
| llama3.1:8b | +0.00 | +0.00 |
| qwen2.5-coder:14b | +1.20 | +0.20 |
| claude-sonnet-5 | +2.60 | +0.96 |

> Recovery is approximate (noisy implicit labels, small data); the ordering and sign of the ability gaps are what matter for routing.


## Pillar 3 — evaluation harness

Gold set meta:
```json
{
  "synthetic": false,
  "local_model": "llama3.1:8b",
  "frontier_model": "claude-sonnet-5",
  "n": 40
}
```

### dual-arm-gold

| router | aiq | auc | cost_vs_local | ece | escalation@thr | over_escalation | qual_retention | quality@thr | under_escal_cellB |
|---|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| always-local | 0.412 | 0.500 | 1.000 | 0.625 | 0.000 | 0.000 | 0.833 | 0.375 | 0.350 |
| always-frontier | 0.412 | 0.500 | 6.841 | 0.375 | 1.000 | 0.375 | 1.000 | 0.450 | 0.000 |
| routellm-logistic | 0.458 | 0.824 | 6.841 | 0.241 | 1.000 | 0.375 | 1.000 | 0.450 | 0.000 |
| irt-1pl | 0.468 | 0.765 | 5.167 | 0.084 | 0.500 | 0.075 | 1.000 | 0.450 | 0.200 |
| knn | 0.511 | 1.000 | 6.658 | 0.161 | 0.925 | 0.300 | 1.167 | 0.525 | 0.000 |
| encoder-mlp(stub) | 0.477 | 0.840 | 1.000 | 0.520 | 0.000 | 0.000 | 0.833 | 0.375 | 0.350 |
| slm-head(stub) | 0.506 | 0.936 | 1.000 | 0.505 | 0.000 | 0.000 | 0.833 | 0.375 | 0.350 |

- Operating threshold = 0.50. AIQ is threshold-independent (area under the cost/quality hull).
- cell-B (under_escal) = stayed local but frontier would have passed — the costly miss.
- Gold outcomes are a strictly stronger label source than the training labels (no circularity).
- Only the gold set (and later online A/B) give trustworthy ABSOLUTE numbers.

### temporal-backtest

> ⚠️ held-out strong-label eval set is single-class (9/9 positive) — AUC is uninformative (0.5). Scale AIL_JUDGE_SAMPLE (or add executed labels) so the held-out split has both classes.

| router | acc@thr | auc | ece |
|---|--:|--:|--:|
| always-local | 0.000 | 0.500 | 1.000 |
| always-frontier | 1.000 | 0.500 | 0.000 |
| routellm-logistic | 0.667 | 0.500 | 0.291 |
| irt-1pl | 0.667 | 0.500 | 0.347 |
| knn | 0.556 | 0.500 | 0.398 |
| encoder-mlp(stub) | 0.000 | 0.500 | 0.814 |
| slm-head(stub) | 0.000 | 0.500 | 0.804 |

- Split by session+time at unix 1784656478: 42 train / 18 eval sessions; train_rows=143 eval_rows=9 (eval source="judge").
- Backtests only RANK routers — they inherit the label heuristic's blind spots and log censoring. Absolute numbers come from the gold set only.

### off-policy-ips-dr

| router | ess | uplift_dr | v_dr | v_ips |
|---|--:|--:|--:|--:|
| always-local | 155.000 | -0.068 | 0.479 | 0.431 |
| always-frontier | 13.462 | 0.221 | 0.768 | 0.692 |
| routellm-logistic | 19.042 | 0.257 | 0.805 | 0.790 |
| irt-1pl | 28.599 | 0.174 | 0.721 | 0.776 |
| knn | 21.575 | 0.252 | 0.799 | 0.832 |
| encoder-mlp(stub) | 152.000 | -0.066 | 0.482 | 0.431 |
| slm-head(stub) | 152.000 | -0.066 | 0.482 | 0.431 |

- Logging-policy observed reward = 0.547 (on-policy baseline). uplift_dr = V_DR - baseline.
- V_IPS is unbiased but high-variance (watch ESS); V_DR uses the IRT Q-model to cut variance.
- Rewards are the implicit outcome labels, so estimates inherit that label's noise (documented).

### guardrail-suite

| router | difficulty_monotonicity | topic_flip_rate |
|---|--:|--:|
| always-local | 0.000 | 0.000 |
| always-frontier | 0.000 | 0.000 |
| routellm-logistic | 0.400 | 0.200 |
| irt-1pl | 1.000 | 0.000 |
| knn | 0.800 | 0.100 |
| encoder-mlp(stub) | 1.000 | 0.000 |
| slm-head(stub) | 1.000 | 0.000 |

- difficulty_monotonicity: fraction of easy/hard pairs where score rises with difficulty (want 1.0).
- topic_flip_rate: fraction of off-topic keyword injections that flipped the decision (want 0.0 — the topic-collapse guardrail).

### policy layer (deployed router: knn, gold AIQ=0.511)

| calibration | threshold | escalation | quality_retention | cost_vs_local | under_escal(cellB) |
|---|--:|--:|--:|--:|--:|
| target escalation 30% | 0.652 | 0.375 | 1.056 | 4.41 | 0.250 |
| target quality 95% | 0.652 | 0.375 | 1.056 | 4.41 | 0.250 |

Quota gate (threshold 0.652, cap 20%): escalated 7/40 = 17.5% of traffic.

> Target-escalation-rate calibration is safe on logs; target-QUALITY calibration is only trustworthy on the dual-arm gold set (or online A/B).


---

See DECISIONS.md for choices and README.md to reproduce (`make all`).
