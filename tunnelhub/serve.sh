#!/usr/bin/env bash
# tunnelhub serve script — one-click start tunnelhub + optional cloudflared tunnel
set -euo pipefail

cd "$(dirname "$0")"

PORT="${PORT:-7374}"
USERS="${USERS:-""}"
CLOUDFLARED="${CLOUDFLARED:-false}"

show_help() {
  cat <<'EOF'
Usage: ./serve.sh [OPTIONS]

Start tunnelhub (WebSocket relay for agentgw/agentapp).

OPTIONS:
  --users USER:PASS;...   Set allowed users (default: env $USERS)
  --cloudflared           Also start cloudflared tunnel to expose hub publicly
  --stop                  Stop running tunnelhub and cloudflared processes
  --url                   Show current cloudflared tunnel URL
  --help, -h              Show this help message

EXAMPLES:
  # Start tunnelhub locally
  ./serve.sh

  # Start with users and cloudflared
  ./serve.sh --users "yehong.yang:yehong.yang;fengming.xie:fengming.xie" --cloudflared

  # Use environment variable
  USERS="alice:alice" ./serve.sh --cloudflared

AGENTGW SETUP:
  1. Start agentgw with tunnel URL:
     agentgw start --tunnel-url wss://<your-domain>.trycloudflare.com/tunnel/register?userId=<username>

  2. Or use environment variable:
     export AGENTGW_TUNNEL_URL="wss://<your-domain>.trycloudflare.com/tunnel/register?userId=<username>"
     agentgw start

  3. Token for agentgw is the user password (by default same as username).

GET CURRENT TUNNEL URL:
  ./serve.sh --url
EOF
}

start_tunnelhub() {
  local users_arg="$1"
  if [[ -n "$users_arg" ]]; then
    export USERS="$users_arg"
  fi
  echo "[serve] Starting tunnelhub on port $PORT..."
  nohup go run ./cmd/tunnelhub > /tmp/tunnelhub.log 2>&1 &
  sleep 1
  if pgrep -f "tunnelhub" > /dev/null; then
    echo "[serve] tunnelhub started (PID $(pgrep -f 'go run ./cmd/tunnelhub' | head -1))"
    echo "[serve] Logs: tail -f /tmp/tunnelhub.log"
  else
    echo "[serve] ERROR: tunnelhub failed to start"
    tail -5 /tmp/tunnelhub.log
    return 1
  fi
}

start_cloudflared() {
  echo "[serve] Starting cloudflared tunnel (port $PORT)..."
  nohup cloudflared tunnel --url "http://127.0.0.1:$PORT" > /tmp/cloudflared.log 2>&1 &
  echo "[serve] cloudflared starting in background..."
  echo "[serve] Waiting for tunnel URL (this may take 5-10s)..."
  sleep 5
  show_url
}

stop_all() {
  echo "[serve] Stopping tunnelhub and cloudflared..."
  pkill -f "go run ./cmd/tunnelhub" 2>/dev/null || true
  pkill -f "cloudflared tunnel" 2>/dev/null || true
  sleep 1
  echo "[serve] Stopped."
}

show_url() {
  if ! pgrep -f "cloudflared tunnel" > /dev/null; then
    echo "[serve] cloudflared is not running."
    return 1
  fi
  local url
  url=$(grep -oE 'https://[a-zA-Z0-9_-]+\.trycloudflare\.com' /tmp/cloudflared.log | tail -1 || true)
  if [[ -n "$url" ]]; then
    echo "[serve] Current tunnel URL: $url"
    echo ""
    echo "Agentgw connection URLs:"
    echo "  yehong.yang  -> ${url}/tunnel/register?userId=yehong.yang"
    echo "  fengming.xie -> ${url}/tunnel/register?userId=fengming.xie"
  else
    echo "[serve] Tunnel URL not yet available. Check: tail -f /tmp/cloudflared.log"
  fi
}

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --users)
      shift
      USERS="${1:-}"
      ;;
    --cloudflared)
      CLOUDFLARED=true
      ;;
    --stop)
      stop_all
      exit 0
      ;;
    --url)
      show_url
      exit 0
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

# Main flow
stop_all 2>/dev/null || true
sleep 1
start_tunnelhub "$USERS"
if $CLOUDFLARED; then
  start_cloudflared
fi

echo ""
echo "Tunnelhub is running on http://localhost:$PORT"
if $CLOUDFLARED; then
  show_url
fi
echo ""
echo "To connect agentgw:"
echo '  agentgw start --tunnel-url "wss://<domain>.trycloudflare.com/tunnel/register?userId=<username>"'
echo ""
echo "To update tunnel URL without restarting agentgw:"
echo '  curl -X POST http://localhost:8383/config/tunnel \\'
echo '    -H "Authorization: Bearer <agentgw-token>" \\'
echo "    -d '{ \"url\": \"wss://<new-domain>.trycloudflare.com/tunnel/register?userId=<username>\", \"token\": \"<user-password>\" }'"
