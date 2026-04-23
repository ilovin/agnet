#!/bin/bash
# Monitor agentd thread/goroutine growth and lsof child processes.
# Usage: ./monitor_threads.sh &
# Logs to: agentd/scripts/debug/thread_monitor_YYYYMMDD_HHMMSS.log

set -euo pipefail

AGENTD_PID=""
LOG_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_FILE="$LOG_DIR/thread_monitor_$(date +%Y%m%d_%H%M%S).log"
INTERVAL_SEC=${INTERVAL_SEC:-30}

log() {
    local ts
    ts=$(date '+%Y-%m-%d %H:%M:%S')
    echo "$ts $1" | tee -a "$LOG_FILE"
}

find_agentd() {
    pgrep -x agentd | head -1 || true
}

sample_agentd() {
    local pid=$1
    local threads goroutines lsof_children cpu mem
    threads=$(ps -M "$pid" 2>/dev/null | wc -l | tr -d ' ')
    threads=$((threads - 1))  # subtract header
    lsof_children=$(pgrep -P "$pid" -x lsof 2>/dev/null | wc -l | tr -d ' ')
    cpu=$(ps -p "$pid" -o %cpu= 2>/dev/null | tr -d ' ' || echo "N/A")
    mem=$(ps -p "$pid" -o %mem= 2>/dev/null | tr -d ' ' || echo "N/A")

    # Try to read goroutine count from agentd log tail
    goroutines="N/A"
    if [[ -f /tmp/agentd-local.log ]]; then
        local line
        line=$(grep '\[Runtime\]' /tmp/agentd-local.log | tail -1 || true)
        if [[ -n "$line" ]]; then
            goroutines=$(echo "$line" | grep -oE 'goroutines=[0-9]+' | cut -d= -f2 || echo "N/A")
        fi
    fi

    log "pid=$pid threads=$threads goroutines=$goroutines lsof_children=$lsof_children cpu=$cpu% mem=$mem%"
}

log "Starting agentd thread monitor (interval=${INTERVAL_SEC}s, log=$LOG_FILE)"

while true; do
    AGENTD_PID=$(find_agentd)
    if [[ -z "$AGENTD_PID" ]]; then
        log "agentd not running, waiting..."
        sleep "$INTERVAL_SEC"
        continue
    fi

    sample_agentd "$AGENTD_PID"
    sleep "$INTERVAL_SEC"
done
