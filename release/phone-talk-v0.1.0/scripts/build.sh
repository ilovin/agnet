#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Building Agent Manager ==="

# Build agentd (local platform)
echo "[1/4] Building agentd..."
cd "$PROJECT_DIR/agentd"
go build -o agentd ./cmd/agentd/
echo "  → agentd/agentd"

# Build agentd for Linux (remote deployment)
echo "[2/4] Building agentd-linux..."
GOOS=linux GOARCH=amd64 go build -o agentd-linux ./cmd/agentd/
echo "  → agentd/agentd-linux"

# Build Flutter Web
echo "[3/4] Building Flutter Web..."
cd "$PROJECT_DIR/agentapp"
flutter build web --release
echo "  → agentapp/build/web/"

# Build agentgw (with embedded web UI)
echo "[4/4] Building agentgw..."
cd "$PROJECT_DIR/agentgw"
# Copy web build to static dir
mkdir -p static
cp -r "$PROJECT_DIR/agentapp/build/web/"* static/
go build -o agentgw ./cmd/agentgw/
echo "  → agentgw/agentgw"

echo ""
echo "=== Build Complete ==="
echo "Artifacts:"
echo "  agentd/agentd        — local daemon"
echo "  agentd/agentd-linux  — remote daemon (Linux amd64)"
echo "  agentgw/agentgw      — gateway + web UI"
