#!/usr/bin/env python3
"""Train the (non-portable) encoder + MLP escalation scorer.

Reads the pointwise dataset produced by `make extract`, trains a small MLP on
the nomic embeddings already in the dataset to predict "the local rung was
inadequate" (escalate), and exports a LINEAR head artifact the Go stub loads:

    artifacts/encoder_mlp.json = {"w": [...768], "b": float}

We train a full MLP for quality, then distill it into the linear head that the
Go `router.EncoderMLP` contract consumes (a logistic layer fit to the MLP's
predicted probabilities). The full torch checkpoint is also saved. See
README.md for the portability rationale.

Usage:
    python train_encoder_mlp.py [--data ../data/pointwise.jsonl]
"""
import argparse
import json
import os
import sys

import numpy as np


def load_local_rows(path):
    """Return (X embeddings, y escalate-labels, w confidence) for local-served,
    implicit-labeled rows that carry an embedding."""
    X, y, w = [], [], []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            r = json.loads(line)
            if r.get("label_source") != "implicit":
                continue
            model = r.get("model", "")
            if model.startswith("claude"):  # skip frontier-served rows
                continue
            emb = r.get("embedding")
            if not emb:
                continue
            X.append(emb)
            y.append(0 if r.get("outcome", 0) == 1 else 1)  # inadequate -> escalate
            w.append(float(r.get("label_confidence", 0.5)) or 0.5)
    return np.array(X, dtype=np.float32), np.array(y, dtype=np.float32), np.array(w, dtype=np.float32)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default=os.path.join(os.path.dirname(__file__), "..", "data", "pointwise.jsonl"))
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "artifacts", "encoder_mlp.json"))
    ap.add_argument("--epochs", type=int, default=200)
    args = ap.parse_args()

    X, y, w = load_local_rows(args.data)
    if len(X) < 10:
        print(f"not enough local-served embedded rows ({len(X)}); run `make extract` first", file=sys.stderr)
        sys.exit(1)
    print(f"loaded {len(X)} rows, dim={X.shape[1]}, positive rate={y.mean():.3f}")

    try:
        import torch
        import torch.nn as nn
    except ImportError:
        print("torch not installed; see python/requirements.txt", file=sys.stderr)
        sys.exit(1)

    Xt = torch.tensor(X)
    yt = torch.tensor(y).unsqueeze(1)
    wt = torch.tensor(w).unsqueeze(1)

    d = X.shape[1]
    mlp = nn.Sequential(nn.Linear(d, 64), nn.ReLU(), nn.Dropout(0.1), nn.Linear(64, 1))
    opt = torch.optim.Adam(mlp.parameters(), lr=1e-3, weight_decay=1e-4)
    bce = nn.BCEWithLogitsLoss(reduction="none")
    for epoch in range(args.epochs):
        opt.zero_grad()
        logits = mlp(Xt)
        loss = (bce(logits, yt) * wt).mean()
        loss.backward()
        opt.step()
    print(f"final weighted BCE = {loss.item():.4f}")

    # distill the MLP into a linear head for the Go artifact contract
    with torch.no_grad():
        p = torch.sigmoid(mlp(Xt)).numpy().ravel()
    from sklearn.linear_model import LogisticRegression
    # target the MLP's hard predictions; fit linear probe over embeddings
    lin = LogisticRegression(max_iter=1000, C=1.0)
    lin.fit(X, (p >= 0.5).astype(int), sample_weight=w)

    art = {"w": lin.coef_.ravel().astype(float).tolist(), "b": float(lin.intercept_[0])}
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(art, f)
    torch.save(mlp.state_dict(), os.path.join(os.path.dirname(args.out), "encoder_mlp.pt"))
    print(f"wrote {args.out} (linear head, dim={len(art['w'])}) and encoder_mlp.pt")


if __name__ == "__main__":
    main()
