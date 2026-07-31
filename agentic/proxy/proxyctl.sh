#!/usr/bin/env bash
# Start/stop/health the Anthropic->Ollama proxy for the local arm.
# Usage: proxyctl.sh {start|stop|health}
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORT="${PROXY_PORT:-8790}"
URL="http://127.0.0.1:${PORT}"
PIDFILE="/tmp/agentic_proxy.pid"
export PROXY_PORT="$PORT"
export PROXY_LOG="${PROXY_LOG:-/tmp/agentic_proxy.log}"
export PROXY_NUM_CTX="${PROXY_NUM_CTX:-8192}"
export PROXY_NUM_PREDICT="${PROXY_NUM_PREDICT:-1024}"
export PROXY_KEEP_ALIVE="${PROXY_KEEP_ALIVE:-60m}"
export PROXY_OLLAMA_MODEL="${PROXY_OLLAMA_MODEL:-qwen2.5-coder:14b}"

health() { curl -sf "${URL}/health" >/dev/null 2>&1; }

case "${1:-}" in
  start)
    if health; then echo "[proxyctl] already healthy at ${URL}"; exit 0; fi
    echo "[proxyctl] starting proxy on :${PORT} (model=${PROXY_OLLAMA_MODEL} ctx=${PROXY_NUM_CTX})"
    nohup python3 "${HERE}/ollama_anthropic_proxy.py" > /tmp/agentic_proxy.out 2>&1 &
    echo $! > "$PIDFILE"
    for _ in $(seq 1 20); do health && { echo "[proxyctl] up"; exit 0; }; sleep 1; done
    echo "[proxyctl] FAILED to become healthy; see /tmp/agentic_proxy.out" >&2; exit 1
    ;;
  stop)
    [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
    pkill -f ollama_anthropic_proxy 2>/dev/null || true
    rm -f "$PIDFILE"; echo "[proxyctl] stopped"
    ;;
  health)
    if health; then echo "healthy"; else echo "down"; exit 1; fi
    ;;
  *) echo "usage: $0 {start|stop|health}" >&2; exit 2 ;;
esac
