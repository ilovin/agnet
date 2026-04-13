#!/usr/bin/env bash
# Deploy agentd + build APK
#
# Lessons learned (baked into the script):
#   1. Remote agentd must run as the normal user (NOT sudo).
#      sudo causes os.UserHomeDir() to return /root instead of the actual user,
#      so watcher can't find ~/.claude/projects session files.
#   2. After restarting agentd, agentgw's proxy connections break.
#      The script auto-restarts agentgw to re-establish WS tunnels.
#   3. Can't SCP over a running binary — upload to temp name first, stop, then mv.
#
# Usage:
#   ./scripts/deploy.sh          # build all + deploy local + ws + apk + restart agentgw
#   ./scripts/deploy.sh local    # build mac + restart local agentd + restart agentgw
#   ./scripts/deploy.sh ws       # build linux + deploy to ws + restart agentgw
#   ./scripts/deploy.sh build    # build agentd only (no deploy)
#   ./scripts/deploy.sh apk      # build APK only
#   ./scripts/deploy.sh gw       # restart agentgw only

set -euo pipefail
cd "$(dirname "$0")/.."

AGENTD_DIR="./agentd"
AGENTGW_DIR="./agentgw"
AGENTAPP_DIR="./agentapp"
LOCAL_BIN="$AGENTD_DIR/agentd"
LINUX_BIN="$AGENTD_DIR/agentd-linux"
APK_OUTPUT="$AGENTGW_DIR/agentapp.apk"
REMOTE_HOST="${REMOTE_HOST:-ws}"
REMOTE_LOG="/tmp/agentd.log"

build_mac() {
    echo "[deploy] Building agentd for macOS..."
    (cd "$AGENTD_DIR" && go build -o agentd ./cmd/agentd/)
    echo "[deploy] macOS binary: $(ls -lh "$LOCAL_BIN" | awk '{print $5}')"
}

build_linux() {
    echo "[deploy] Building agentd for Linux amd64..."
    (cd "$AGENTD_DIR" && GOOS=linux GOARCH=amd64 go build -o agentd-linux ./cmd/agentd/)
    echo "[deploy] Linux binary: $(ls -lh "$LINUX_BIN" | awk '{print $5}')"
}

build_apk() {
    echo "[deploy] Building APK..."
    (cd "$AGENTAPP_DIR" && flutter build apk --release)
    local apk="$AGENTAPP_DIR/build/app/outputs/flutter-apk/app-release.apk"
    if [[ -f "$apk" ]]; then
        cp "$apk" "$APK_OUTPUT"
        echo "[deploy] APK: $(ls -lh "$APK_OUTPUT" | awk '{print $5}')"
    else
        echo "[deploy] ERROR: APK not found at $apk"
        return 1
    fi
}

build_all() {
    # Build macOS, Linux, and APK in parallel
    local mac_pid linux_pid apk_pid mac_ok=0 linux_ok=0 apk_ok=0
    build_mac & mac_pid=$!
    build_linux & linux_pid=$!
    build_apk & apk_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_pid" && linux_ok=1 || true
    wait "$apk_pid" && apk_ok=1 || true
    echo "[deploy] Build results: mac=$mac_ok linux=$linux_ok apk=$apk_ok"
    [[ $mac_ok -eq 1 && $linux_ok -eq 1 && $apk_ok -eq 1 ]]
}

deploy_local() {
    echo "[deploy] Restarting local agentd..."
    pkill -f "./agentd start" 2>/dev/null || true
    pkill -f "$AGENTD_DIR/agentd start" 2>/dev/null || true
    sleep 1
    nohup "$LOCAL_BIN" start > /tmp/agentd-local.log 2>&1 &
    sleep 2
    if pgrep -f "agentd start" > /dev/null; then
        echo "[deploy] Local agentd started (PID $(pgrep -f "agentd start"))"
    else
        echo "[deploy] ERROR: local agentd failed to start"
        tail -5 /tmp/agentd-local.log
        return 1
    fi
    tail -3 /tmp/agentd-local.log
}

deploy_remote() {
    echo "[deploy] Deploying to $REMOTE_HOST (as user, NOT sudo)..."
    # Upload to temp name (can't overwrite running binary)
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p ~/bin" || return 1
    scp -o ConnectTimeout=5 "$LINUX_BIN" "$REMOTE_HOST:~/bin/agentd-new" || return 1

    # Stop old agentd (try user-owned first, then root-owned as fallback)
    echo "[deploy] Stopping remote agentd..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "pkill -f 'agentd start' 2>/dev/null; sudo pkill -f 'agentd start' 2>/dev/null; sleep 1" || true

    # Replace old binary and start as normal user
    echo "[deploy] Replacing binary and starting remote agentd..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && nohup ~/bin/agentd start > $REMOTE_LOG 2>&1 &"
    sleep 3
    echo "[deploy] Checking remote status..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "if pgrep -u \$(whoami) -f 'agentd start' > /dev/null; then echo 'OK (running as user)'; else echo 'WARN: may be running as root'; fi; tail -3 $REMOTE_LOG" || true
}

deploy_all() {
    # Deploy local and remote in parallel
    local local_pid remote_pid local_ok=0 remote_ok=0
    deploy_local & local_pid=$!
    deploy_remote & remote_pid=$!
    wait "$local_pid" && local_ok=1 || true
    wait "$remote_pid" && remote_ok=1 || true
    [[ $local_ok -eq 1 && $remote_ok -eq 1 ]]
}

restart_agentgw() {
    echo "[deploy] Restarting agentgw (to reconnect WS tunnels to agentd)..."
    pkill -f "agentgw start" 2>/dev/null || true
    sleep 1
    nohup "$AGENTGW_DIR/agentgw" start > /tmp/agentgw.log 2>&1 &
    sleep 2
    if pgrep -f "agentgw start" > /dev/null; then
        echo "[deploy] agentgw started (PID $(pgrep -f "agentgw start"))"
    else
        echo "[deploy] ERROR: agentgw failed to start"
        tail -5 /tmp/agentgw.log
        return 1
    fi
}

TARGET="${1:-all}"

case "$TARGET" in
    build)
        build_all
        ;;
    apk)
        build_apk
        ;;
    local)
        build_mac
        deploy_local
        restart_agentgw
        ;;
    ws)
        build_linux
        deploy_remote
        restart_agentgw
        ;;
    gw)
        restart_agentgw
        ;;
    all)
        build_all
        deploy_all
        restart_agentgw
        ;;
    *)
        echo "Usage: $0 [build|local|ws|gw|apk|all]"
        exit 1
        ;;
esac
