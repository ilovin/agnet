#!/usr/bin/env bash
# R-012: launch a `claude` CLI bound to a sandbox HOME so that
# ~/.claude/projects/, ~/.claude/agents/, and other state files
# land inside the sandbox dir, not the user's real ~/.claude/.
#
# Usage:
#   scripts/sandbox-claude.sh <sandbox-id> [claude-args...]
#
# The sandbox must have been started first via:
#   scripts/deploy.sh sandbox <sandbox-id>

set -euo pipefail

cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"

if [[ $# -lt 1 ]]; then
    echo "Usage: $0 <sandbox-id> [claude-args...]" >&2
    exit 2
fi

SANDBOX_ID="$1"
shift

SANDBOX_DIR="$REPO_ROOT/.sandbox/$SANDBOX_ID"
SANDBOX_HOME="$SANDBOX_DIR/home"

if [[ ! -d "$SANDBOX_HOME" ]]; then
    echo "ERROR: sandbox '$SANDBOX_ID' not found at $SANDBOX_HOME" >&2
    echo "Start it first: scripts/deploy.sh sandbox $SANDBOX_ID" >&2
    exit 1
fi

if ! command -v claude >/dev/null 2>&1; then
    echo "ERROR: 'claude' CLI not found in PATH" >&2
    exit 1
fi

echo "[sandbox-claude] HOME=$SANDBOX_HOME"
exec env HOME="$SANDBOX_HOME" claude "$@"
