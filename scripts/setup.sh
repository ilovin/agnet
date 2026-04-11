#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Agent Manager Setup ==="

# Generate token
TOKEN=$(openssl rand -hex 16)
PORT=8080

# Create config directory
CONFIG_DIR="$HOME/.agentgw"
mkdir -p "$CONFIG_DIR"

# Generate config
cat > "$CONFIG_DIR/config.yaml" << YAML
port: $PORT
token: $TOKEN
nodes_file: $CONFIG_DIR/nodes.yaml
YAML

# Create empty nodes file
if [ ! -f "$CONFIG_DIR/nodes.yaml" ]; then
    echo "[]" > "$CONFIG_DIR/nodes.yaml"
fi

echo ""
echo "=== Setup Complete ==="
echo "Config: $CONFIG_DIR/config.yaml"
echo "Token: $TOKEN"
echo "Port: $PORT"
echo ""
echo "Next steps:"
echo "  1. Build:  ./scripts/build.sh"
echo "  2. Start:  ./agentgw/agentgw start"
echo "  3. Open:   http://localhost:$PORT/?token=$TOKEN"
echo ""
echo "To add a remote node:"
echo "  ./scripts/deploy-remote.sh <ssh-host> <token>"
