# Results

End-to-end run on the small default config (seed=42, 2 local models + frontier `claude-sonnet-5`).

_Absolute cost/quality numbers come from the dual-arm gold set only; backtests rank routers; off-policy estimates the counterfactual reward from logged propensities._

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
| always-local | 0.263 | 0.500 | 1.000 | 0.800 | 0.000 | 0.000 | 0.615 | 0.200 | 0.250 |
| always-frontier | 0.263 | 0.500 | 12.946 | 0.200 | 1.000 | 0.200 | 1.000 | 0.325 | 0.000 |
| routellm-logistic | 0.218 | 0.852 | 12.946 | 0.134 | 1.000 | 0.200 | 1.000 | 0.325 | 0.000 |
| irt-1pl | 0.229 | 0.922 | 12.280 | 0.074 | 0.750 | 0.050 | 0.923 | 0.300 | 0.100 |
| knn | 0.229 | 1.000 | 12.840 | 0.155 | 0.950 | 0.150 | 1.154 | 0.375 | 0.000 |
| encoder-mlp(stub) | 0.241 | 0.938 | 6.775 | 0.586 | 0.075 | 0.000 | 0.615 | 0.200 | 0.250 |
| slm-head(stub) | 0.243 | 0.969 | 6.775 | 0.579 | 0.075 | 0.000 | 0.615 | 0.200 | 0.250 |

- Operating threshold = 0.50. AIQ is threshold-independent (area under the cost/quality hull).
- cell-B (under_escal) = stayed local but frontier would have passed — the costly miss.
- Gold outcomes are a strictly stronger label source than the training labels (no circularity).
- Only the gold set (and later online A/B) give trustworthy ABSOLUTE numbers.

### temporal-backtest

| router | acc@thr | auc | ece |
|---|--:|--:|--:|
| always-local | 0.000 | 0.500 | 1.000 |
| always-frontier | 1.000 | 0.500 | 0.000 |
| routellm-logistic | 0.667 | 0.500 | 0.291 |
| irt-1pl | 0.667 | 0.500 | 0.347 |
| knn | 0.556 | 0.500 | 0.398 |
| encoder-mlp(stub) | 0.000 | 0.500 | 0.814 |
| slm-head(stub) | 0.000 | 0.500 | 0.804 |

- Split by session+time at unix 1784654719: 42 train / 18 eval sessions; train_rows=143 eval_rows=9 (eval source="judge").
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

### policy layer (deployed router: slm-head(stub), gold AIQ=0.243)

| calibration | threshold | escalation | quality_retention | cost_vs_local | under_escal(cellB) |
|---|--:|--:|--:|--:|--:|
| target escalation 30% | 0.308 | 0.450 | 0.769 | 9.79 | 0.200 |
| target quality 95% | 0.163 | 0.700 | 1.077 | 12.17 | 0.100 |

Quota gate (threshold 0.308, cap 20%): escalated 7/40 = 17.5% of traffic.

> Target-escalation-rate calibration is safe on logs; target-QUALITY calibration is only trustworthy on the dual-arm gold set (or online A/B).


---

See DECISIONS.md for choices and README.md to reproduce (`make all`).
