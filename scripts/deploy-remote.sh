#!/bin/bash
set -e

# Deploy agentd to a remote machine via SSH
# Usage: ./scripts/deploy-remote.sh <ssh-host> [token]

HOST="${1:?Usage: $0 <ssh-host> [token]}"
TOKEN="${2:-$(openssl rand -hex 16)}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BINARY="$PROJECT_DIR/agentd/agentd-linux"

if [ ! -f "$BINARY" ]; then
    echo "Error: agentd-linux not found. Run scripts/build.sh first."
    exit 1
fi

echo "=== Deploying agentd to $HOST ==="

# Upload binary
echo "[1/3] Uploading binary..."
scp "$BINARY" "$HOST:/tmp/agentd-new"

# Install and start
echo "[2/3] Installing..."
ssh "$HOST" "sudo pkill -f 'agentd start' 2>/dev/null || true; \
    sleep 1; \
    sudo cp /tmp/agentd-new /usr/local/bin/agentd; \
    sudo chmod +x /usr/local/bin/agentd; \
    rm /tmp/agentd-new"

echo "[3/3] Starting agentd..."
ssh "$HOST" "sudo nohup /usr/local/bin/agentd start --token '$TOKEN' > /var/log/agentd.log 2>&1 &"

# Verify
sleep 2
if ssh "$HOST" "pgrep -f 'agentd start'" > /dev/null 2>&1; then
    echo ""
    echo "=== Deploy Complete ==="
    echo "Host: $HOST"
    echo "Port: 7373"
    echo "Token: $TOKEN"
else
    echo "ERROR: agentd failed to start. Check /var/log/agentd.log on $HOST"
    exit 1
fi
