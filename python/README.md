# python/ — the non-portable routers (optional)

Everything in this directory is **optional** and **not** part of the
stdlib-only Go core that ports into the gateway. It exists for the two router
families Go genuinely can't carry: a **fine-tuned neural encoder + MLP scorer**
and a **small-LM (SLM) router head**. Both need a deep-learning stack
(PyTorch / sentence-transformers).

## Why these are non-portable

The Go gateway can compute Ollama embeddings and run linear/tree models, IRT,
and kNN — all stdlib-friendly. It **cannot** fine-tune a transformer or run
transformer inference without a Python/ONNX runtime. So:

| Router | Portable? | Where it lives |
|---|---|---|
| RouteLLM logistic, IRT 1PL, kNN | ✅ Go, stdlib-only | `internal/router` |
| encoder + MLP scorer | ❌ training in Python | `train_encoder_mlp.py` |
| SLM router head | ❌ training in Python | `train_slm_head.py` |

## Artifact contract (how Go consumes these)

Each script writes a JSON artifact the Go stub loads if present:

- `artifacts/encoder_mlp.json` → `{"w": [...], "b": ...}` — a linear head such
  that `P(escalate) = sigmoid(w·embedding + b)` over the **same nomic
  embeddings the Go dataset already carries** (so train- and inference-time
  features match exactly). The Go stub `router.EncoderMLP` picks this up
  automatically; otherwise it runs a transparent feature-based baseline.
- `artifacts/slm_head.json` → same shape, a linear probe over the SLM's pooled
  representation, as a demonstration stand-in.

> Production note: a genuinely fine-tuned **encoder** or **SLM** would be served
> behind an endpoint or exported to ONNX for the Go gateway to call — the linear
> JSON head here is a portable demonstration of the integration seam, not the
> full model. The scripts save the full torch checkpoints too.

## Setup & run

```bash
cd python
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# needs data/pointwise.jsonl from `make extract`
python train_encoder_mlp.py   # -> artifacts/encoder_mlp.json
python train_slm_head.py       # -> artifacts/slm_head.json  (heavier)
```

Re-run the Go eval (`make eval`) afterward and the encoder-mlp / slm-head
routers will report as trained (name drops the `(stub)` suffix).

## Label used

Both train the binary target **"the local rung was inadequate"** (escalate),
derived from the pointwise dataset (`outcome == 0` on local-served rows,
confidence-weighted). This mirrors the Go routers so results are comparable.
