#!/usr/bin/env python3
"""Train the (non-portable) small-LM (SLM) router head.

Fine-tunes a small pretrained sentence encoder on the RAW prompt text to
predict "the local rung was inadequate" (escalate). This is the genuinely
non-portable router: Go cannot run transformer inference without a Python/ONNX
runtime, so in production this model would be served behind an endpoint or
exported to ONNX for the gateway to call.

For the demonstration seam, we export a LINEAR probe over the encoder's pooled
sentence embeddings as `artifacts/slm_head.json` = {"w": [...], "b": ...}. Note
this probe is over the SLM encoder's OWN embedding space, so to actually use it
from Go you must embed prompts with the SAME encoder (endpoint/ONNX) rather than
nomic — hence "non-portable". The full model is saved too.

Usage:
    python train_slm_head.py [--data ../data/pointwise.jsonl] [--model all-MiniLM-L6-v2]
"""
import argparse
import json
import os
import sys

import numpy as np


def load_rows(path):
    texts, y, w = [], [], []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            r = json.loads(line)
            if r.get("label_source") != "implicit":
                continue
            if r.get("model", "").startswith("claude"):
                continue
            t = r.get("prompt_text", "")
            if not t:
                continue
            texts.append(t)
            y.append(0 if r.get("outcome", 0) == 1 else 1)
            w.append(float(r.get("label_confidence", 0.5)) or 0.5)
    return texts, np.array(y), np.array(w, dtype=np.float32)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default=os.path.join(os.path.dirname(__file__), "..", "data", "pointwise.jsonl"))
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "artifacts", "slm_head.json"))
    ap.add_argument("--model", default="all-MiniLM-L6-v2")
    args = ap.parse_args()

    texts, y, w = load_rows(args.data)
    if len(texts) < 10:
        print(f"not enough rows ({len(texts)}); run `make extract` first", file=sys.stderr)
        sys.exit(1)
    print(f"loaded {len(texts)} rows, positive rate={y.mean():.3f}")

    try:
        from sentence_transformers import SentenceTransformer
    except ImportError:
        print("sentence-transformers not installed; see python/requirements.txt", file=sys.stderr)
        sys.exit(1)

    enc = SentenceTransformer(args.model)
    emb = enc.encode(texts, normalize_embeddings=True, show_progress_bar=False)

    from sklearn.linear_model import LogisticRegression
    lin = LogisticRegression(max_iter=1000, C=1.0)
    lin.fit(emb, y, sample_weight=w)
    acc = lin.score(emb, y, sample_weight=w)
    print(f"train weighted accuracy = {acc:.3f}")

    art = {"w": lin.coef_.ravel().astype(float).tolist(), "b": float(lin.intercept_[0]), "encoder": args.model}
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(art, f)
    print(f"wrote {args.out} (linear probe over {args.model}, dim={len(art['w'])})")
    print("NOTE: to use from Go, embed prompts with the SAME encoder (endpoint/ONNX), not nomic.")


if __name__ == "__main__":
    main()
