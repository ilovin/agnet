#!/usr/bin/env bash
# TunnelHub server script — start/stop/restart the WebSocket relay
#
# Usage:
#   ./scripts/tunnelhub.sh start           # start tunnelhub
#   ./scripts/tunnelhub.sh start --cloudflared  # start with cloudflared tunnel
#   ./scripts/tunnelhub.sh stop            # stop tunnelhub
#   ./scripts/tunnelhub.sh restart         # restart tunnelhub
#   ./scripts/tunnelhub.sh status          # show process status
#   ./scripts/tunnelhub.sh logs            # tail logs
#   ./scripts/tunnelhub.sh url             # show cloudflared tunnel URL
#
set -euo pipefail

cd "$(dirname "$0")/.."

TUNNELHUB_DIR="./tunnelhub"
PORT="${PORT:-7374}"
USERS="${USERS:-""}"
CLOUDFLARED="${CLOUDFLARED:-false}"
LOG_FILE="/tmp/tunnelhub.log"
CF_LOG_FILE="/tmp/cloudflared.log"

show_help() {
  cat <<'EOF'
Usage: ./scripts/tunnelhub.sh <COMMAND> [OPTIONS]

COMMANDS:
  start      Start tunnelhub (and optionally cloudflared)
  stop       Stop tunnelhub and cloudflared
  restart    Restart tunnelhub and cloudflared
  status     Show running status
  logs       Tail tunnelhub logs
  url        Show current cloudflared tunnel URL
  help       Show this help message

OPTIONS (for start/restart):
  --users USER:PASS;...   Set allowed users (default: env $USERS)
  --cloudflared           Also start cloudflared tunnel

EXAMPLES:
  ./scripts/tunnelhub.sh start
  USERS="alice:token1;bob:token2" ./scripts/tunnelhub.sh start --cloudflared
  ./scripts/tunnelhub.sh restart --cloudflared
  ./scripts/tunnelhub.sh status
  ./scripts/tunnelhub.sh logs
EOF
}

find_tunnelhub_bin() {
  local candidates=(
    "$TUNNELHUB_DIR/tunnelhub"
    "./tunnelhub"
    "./tunnelhub/tunnelhub"
  )
  for c in "${candidates[@]}"; do
    if [[ -x "$c" ]]; then
      echo "$c"
      return 0
    fi
  done
  return 1
}

start_tunnelhub() {
  local users_arg="${1:-}"
  if pgrep -f "tunnelhub" > /dev/null 2>&1; then
    echo "[tunnelhub] already running"
    return 0
  fi

  if [[ -n "$users_arg" ]]; then
    export USERS="$users_arg"
  fi

  local bin
  if bin=$(find_tunnelhub_bin); then
    echo "[tunnelhub] Starting with binary: $bin (port $PORT)..."
    nohup "$bin" > "$LOG_FILE" 2>&1 &
  else
    echo "[tunnelhub] Binary not found, using 'go run' (port $PORT)..."
    cd "$TUNNELHUB_DIR"
    nohup go run ./cmd/tunnelhub > "$LOG_FILE" 2>&1 &
    cd - > /dev/null
  fi

  sleep 1
  if pgrep -f "tunnelhub" > /dev/null 2>&1; then
    echo "[tunnelhub] started (PID $(pgrep -f 'tunnelhub' | head -1))"
    echo "[tunnelhub] Logs: tail -f $LOG_FILE"
  else
    echo "[tunnelhub] ERROR: failed to start"
    tail -5 "$LOG_FILE"
    return 1
  fi
}

stop_tunnelhub() {
  echo "[tunnelhub] Stopping tunnelhub..."
  pkill -f "go run ./cmd/tunnelhub" 2>/dev/null || true
  pkill -f "tunnelhub" 2>/dev/null || true
  sleep 1
  echo "[tunnelhub] Stopped."
}

start_cloudflared() {
  if pgrep -f "cloudflared tunnel" > /dev/null 2>&1; then
    echo "[cloudflared] already running"
    return 0
  fi
  echo "[cloudflared] Starting tunnel (port $PORT)..."
  nohup cloudflared tunnel --url "http://127.0.0.1:$PORT" > "$CF_LOG_FILE" 2>&1 &
  echo "[cloudflared] starting in background..."
  echo "[cloudflared] Waiting for tunnel URL (this may take 5-10s)..."
  sleep 5
  show_url
}

stop_cloudflared() {
  echo "[cloudflared] Stopping..."
  pkill -f "cloudflared tunnel" 2>/dev/null || true
  sleep 1
  echo "[cloudflared] Stopped."
}

show_status() {
  if pgrep -f "tunnelhub" > /dev/null 2>&1; then
    echo "[tunnelhub] running (PID $(pgrep -f 'tunnelhub' | head -1))"
  else
    echo "[tunnelhub] not running"
  fi

  if pgrep -f "cloudflared tunnel" > /dev/null 2>&1; then
    echo "[cloudflared] running (PID $(pgrep -f 'cloudflared tunnel' | head -1))"
  else
    echo "[cloudflared] not running"
  fi
}

show_logs() {
  if [[ -f "$LOG_FILE" ]]; then
    tail -f "$LOG_FILE"
  else
    echo "[tunnelhub] log file not found: $LOG_FILE"
  fi
}

show_url() {
  if ! pgrep -f "cloudflared tunnel" > /dev/null 2>&1; then
    echo "[cloudflared] not running."
    return 1
  fi
  local url
  url=$(grep -oE 'https://[a-zA-Z0-9_-]+\.trycloudflare\.com' "$CF_LOG_FILE" | tail -1 || true)
  if [[ -n "$url" ]]; then
    echo "[cloudflared] Tunnel URL: $url"
    echo ""
    echo "Agentgw connection examples:"
    echo "  agentgw start --hub \"${url}\" --qr"
    echo "  App WS URL: wss://${url#https://}/api.v1.AgentService/Stream/<username>"
  else
    echo "[cloudflared] Tunnel URL not yet available. Check: tail -f $CF_LOG_FILE"
  fi
}

# Parse command
COMMAND="${1:-}"
shift || true

# Parse remaining options
USERS_ARG=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --users)
      shift
      USERS_ARG="${1:-}"
      ;;
    --cloudflared)
      CLOUDFLARED=true
      ;;
    --help|-h)
      show_help
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      show_help
      exit 1
      ;;
  esac
  shift
done

case "$COMMAND" in
  start)
    start_tunnelhub "$USERS_ARG"
    if $CLOUDFLARED; then
      start_cloudflared
    fi
    ;;
  stop)
    stop_cloudflared
    stop_tunnelhub
    ;;
  restart)
    stop_cloudflared
    stop_tunnelhub
    sleep 1
    start_tunnelhub "$USERS_ARG"
    if $CLOUDFLARED; then
      start_cloudflared
    fi
    ;;
  status)
    show_status
    ;;
  logs)
    show_logs
    ;;
  url)
    show_url
    ;;
  help|--help|-h|"")
    show_help
    ;;
  *)
    echo "Unknown command: $COMMAND"
    show_help
    exit 1
    ;;
esac
