#!/usr/bin/env bash
# Overnight finalizer: as the (slow, GPU-contended) local arm lands results,
# re-assemble the executed gold set, re-run the existing eval harness, refresh
# RESULTS_AGENTIC.md, and commit+push when it materially changes. Exits once the
# local run finishes (or after a bounded number of cycles).
set -uo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export AIL_MAX_EMBED_CALLS=0   # embeddings need the contended GPU; skip them

refresh() {
  ./bin/agentic -data-dir data_agentic -train-src data -no-embed >/dev/null 2>&1 || return 1
  ( cd data_agentic && AIL_DATA_DIR=. ../bin/eval >/dev/null 2>&1 ) || true
  python3 agentic/runner/report.py >/dev/null 2>&1 || true
}

commit_if_changed() {
  git add RESULTS_AGENTIC.md data_agentic agentic/results 2>/dev/null || true
  if ! git diff --cached --quiet; then
    local n
    n=$(ls agentic/results/*local* 2>/dev/null | wc -l | tr -d ' ')
    git commit -q -m "agentic: refresh executed results (local arm: ${n}/11 landed)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012izakea5v4JqfR8MnvG2mi"
    git push -q origin HEAD 2>/dev/null || true
    echo "[finalizer] committed refresh (local ${n}/11)"
  fi
}

for i in $(seq 1 30); do        # up to ~10h at 20-min cycles
  refresh
  commit_if_changed
  pgrep -f "run_agentic.py --arms local" >/dev/null 2>&1 || { echo "[finalizer] local run done; final refresh"; refresh; commit_if_changed; break; }
  sleep 1200
done
echo "[finalizer] exiting"
