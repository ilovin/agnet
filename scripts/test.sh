#!/usr/bin/env bash
# Unified test entry point for the Agent Manager project.
#
# Usage:
#   ./scripts/test.sh [SUBCOMMAND]
#
# SUBCOMMANDS:
#   unit     Run all Go unit tests (non-integration) across agentd/ and agentgw/
#   e2e      Run E2E integration tests (agentd + agentgw session lifecycle)
#   flutter  Run Flutter tests (unit or integration)
#   smoke    Run deployment smoke tests (build + local deploy + health checks)
#   help     Show this help message

set -euo pipefail
cd "$(dirname "$0")/.."

AGENTD_DIR="./agentd"
AGENTGW_DIR="./agentgw"
AGENTAPP_DIR="./agentapp"
AGENTCLI_DIR="./agentcli"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

show_help() {
  cat <<EOF
Usage: ./scripts/test.sh [SUBCOMMAND] [OPTIONS]

Unified test entry point for the Agent Manager project.

SUBCOMMANDS:
  unit     Run all Go unit tests (non-integration) across agentd/ and agentgw/
  e2e      Run E2E session lifecycle integration tests (TEST-003)
  flutter  Run Flutter tests (unit or integration)
  smoke    Run deployment smoke tests (build + local deploy + health checks)
  help     Show this help message

EXAMPLES:
  # Run all Go unit tests
  ./scripts/test.sh unit

  # Run E2E integration tests
  ./scripts/test.sh e2e

  # Run Flutter unit tests
  ./scripts/test.sh flutter

  # Run Flutter integration tests in Chrome
  ./scripts/test.sh flutter -d chrome

  # Run deployment smoke tests
  ./scripts/test.sh smoke

  # Show this help message
  ./scripts/test.sh help

NOTES:
  - Integration tests (tagged with //go:build integration) are excluded from 'unit'.
  - The 'e2e' subcommand requires the agentd binary to be built first.
  - The script exits with a non-zero status if any test fails.
  - A consolidated pass/fail summary is printed at the end.
EOF
}

run_unit_tests() {
  local agentd_ok=0 agentgw_ok=0 agentcli_ok=0
  local agentd_output="" agentgw_output="" agentcli_output=""

  echo "[test] Running Go unit tests..."
  echo ""

  # Run agentd unit tests (exclude integration tests)
  echo "[test] agentd: go test ./..."
  if agentd_output=$(cd "$AGENTD_DIR" && go test ./... -tags="!integration" 2>&1); then
    agentd_ok=1
    echo -e "${GREEN}[test] agentd: PASS${NC}"
  else
    echo -e "${RED}[test] agentd: FAIL${NC}"
    echo "$agentd_output"
  fi
  echo ""

  # Run agentgw unit tests (exclude integration tests)
  echo "[test] agentgw: go test ./..."
  if agentgw_output=$(cd "$AGENTGW_DIR" && go test ./... -tags="!integration" 2>&1); then
    agentgw_ok=1
    echo -e "${GREEN}[test] agentgw: PASS${NC}"
  else
    echo -e "${RED}[test] agentgw: FAIL${NC}"
    echo "$agentgw_output"
  fi
  echo ""

  echo "[test] agentcli: go test ./..."
  if agentcli_output=$(cd "$AGENTCLI_DIR" && go test ./... 2>&1); then
    agentcli_ok=1
    echo -e "${GREEN}[test] agentcli: PASS${NC}"
  else
    echo -e "${RED}[test] agentcli: FAIL${NC}"
    echo "$agentcli_output"
  fi
  echo ""

  # Consolidated summary
  echo "========================================"
  echo "           TEST SUMMARY"
  echo "========================================"
  if [[ $agentd_ok -eq 1 ]]; then
    echo -e "  agentd  : ${GREEN}PASS${NC}"
  else
    echo -e "  agentd  : ${RED}FAIL${NC}"
  fi

  if [[ $agentgw_ok -eq 1 ]]; then
    echo -e "  agentgw : ${GREEN}PASS${NC}"
  else
    echo -e "  agentgw : ${RED}FAIL${NC}"
  fi

  if [[ $agentcli_ok -eq 1 ]]; then
    echo -e "  agentcli: ${GREEN}PASS${NC}"
  else
    echo -e "  agentcli: ${RED}FAIL${NC}"
  fi
  echo "========================================"

  if [[ $agentd_ok -eq 1 && $agentgw_ok -eq 1 && $agentcli_ok -eq 1 ]]; then
    echo -e "${GREEN}All tests passed.${NC}"
    return 0
  else
    echo -e "${RED}Some tests failed.${NC}"
    return 1
  fi
}

run_e2e_tests() {
  local agentd_ok=0 agentgw_ok=0
  local agentd_output="" agentgw_output=""

  echo "[test] Running E2E integration tests (TEST-003)..."
  echo ""

  # Check that agentd binary exists
  local agentd_bin=""
  for candidate in "$AGENTD_DIR/agentd" "$AGENTD_DIR/agentd-darwin" "$AGENTD_DIR/agentd-linux"; do
    if [[ -x "$candidate" ]]; then
      agentd_bin="$candidate"
      break
    fi
  done

  if [[ -z "$agentd_bin" ]]; then
    echo -e "${YELLOW}[test] agentd binary not found. Building first...${NC}"
    if ./scripts/build.sh agentd; then
      echo -e "${GREEN}[test] agentd built successfully${NC}"
    else
      echo -e "${RED}[test] agentd build failed${NC}"
      return 1
    fi
  else
    echo "[test] Found agentd binary: $agentd_bin"
  fi

  # Run agentd integration tests
  echo "[test] agentd: go test -tags=integration ./..."
  if agentd_output=$(cd "$AGENTD_DIR" && go test -tags=integration -v -run 'TestSessionLifecycle|TestEndToEnd' ./... 2>&1); then
    agentd_ok=1
    echo -e "${GREEN}[test] agentd integration: PASS${NC}"
  else
    echo -e "${RED}[test] agentd integration: FAIL${NC}"
    echo "$agentd_output"
  fi
  echo ""

  # Run agentgw integration tests
  echo "[test] agentgw: go test -tags=integration ./..."
  if agentgw_output=$(cd "$AGENTGW_DIR" && go test -tags=integration -v -run 'TestAgentgwAgentdHandshake|TestEndToEndSessionLifecycle' ./... 2>&1); then
    agentgw_ok=1
    echo -e "${GREEN}[test] agentgw integration: PASS${NC}"
  else
    echo -e "${RED}[test] agentgw integration: FAIL${NC}"
    echo "$agentgw_output"
  fi
  echo ""

  # Consolidated summary
  echo "========================================"
  echo "         E2E TEST SUMMARY"
  echo "========================================"
  if [[ $agentd_ok -eq 1 ]]; then
    echo -e "  agentd  : ${GREEN}PASS${NC}"
  else
    echo -e "  agentd  : ${RED}FAIL${NC}"
  fi

  if [[ $agentgw_ok -eq 1 ]]; then
    echo -e "  agentgw : ${GREEN}PASS${NC}"
  else
    echo -e "  agentgw : ${RED}FAIL${NC}"
  fi
  echo "========================================"

  if [[ $agentd_ok -eq 1 && $agentgw_ok -eq 1 ]]; then
    echo -e "${GREEN}All E2E tests passed.${NC}"
    return 0
  else
    echo -e "${RED}Some E2E tests failed.${NC}"
    return 1
  fi
}

run_flutter_tests() {
  local device=""
  local integration_mode=false

  # Parse arguments after 'flutter' subcommand
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -d|--device)
        if [[ -n "${2:-}" ]]; then
          device="$2"
          shift 2
        else
          echo "Error: -d requires a device argument"
          exit 1
        fi
        ;;
      chrome)
        device="chrome"
        shift
        ;;
      *)
        echo "Unknown flutter option: $1"
        echo "Usage: ./scripts/test.sh flutter [-d chrome]"
        exit 1
        ;;
    esac
  done

  if [[ "$device" == "chrome" ]]; then
    integration_mode=true
  fi

  cd "$AGENTAPP_DIR"

  if [[ "$integration_mode" == true ]]; then
    echo "[test] Running Flutter integration tests against existing Chrome tab..."

    # TEST-004: Reuse existing Flutter web app; never launch new Chrome.
    # 1. Detect a running `flutter run -d chrome` process.
    # 2. Extract its VM service URL from the command line.
    # 3. Use `flutter drive --use-existing-app=<url>` to connect.
    local flutter_run_pid vm_service_url
    flutter_run_pid=$(pgrep -f "flutter run .*-d chrome" || true)

    if [[ -z "$flutter_run_pid" ]]; then
      echo -e "${YELLOW}[test] No running Flutter web app found.${NC}"
      echo ""
      echo "To run integration tests, first start the app in a terminal:"
      echo ""
      echo "  cd agentapp && flutter run -d chrome --web-port 8080"
      echo ""
      echo "Then, in another terminal, run:"
      echo ""
      echo "  ./scripts/test.sh flutter -d chrome"
      echo ""
      return 1
    fi

    # Extract the VM service URL from the flutter run process arguments.
    # The URL looks like: http://127.0.0.1:<port>/<token>=/
    vm_service_url=$(ps -p "$flutter_run_pid" -o args= | grep -oE 'http://127\.0\.0\.1:[0-9]+/[^ ]+=' || true)

    if [[ -z "$vm_service_url" ]]; then
      echo -e "${YELLOW}[test] Found flutter run process (PID $flutter_run_pid) but could not extract VM service URL.${NC}"
      echo "Make sure the app has finished starting and the VM service URL is printed in the terminal."
      return 1
    fi

    echo "[test] Found existing Flutter web app (PID $flutter_run_pid)"
    echo "[test] VM service URL: $vm_service_url"
    echo "[test] Connecting via flutter drive --use-existing-app ..."
    echo ""

    if flutter drive \
         --use-existing-app="$vm_service_url" \
         --driver=test_driver/integration_test.dart \
         --target=integration_test/app_test.dart; then
      echo -e "${GREEN}[test] Flutter integration tests: PASS${NC}"
      return 0
    else
      echo -e "${RED}[test] Flutter integration tests: FAIL${NC}"
      return 1
    fi
  else
    echo "[test] Running Flutter unit tests..."

    if flutter test; then
      echo -e "${GREEN}[test] Flutter unit tests: PASS${NC}"
      return 0
    else
      echo -e "${RED}[test] Flutter unit tests: FAIL${NC}"
      return 1
    fi
  fi
}

run_smoke_tests() {
  local build_ok=0 deploy_ok=0 health_ok=0 cleanup_ok=0
  local agentd_port=7373
  local agentgw_port=7374
  local services_were_running=false
  local agentd_pid_before="" agentgw_pid_before=""

  echo ""
  echo "========================================"
  echo "       DEPLOYMENT SMOKE TESTS"
  echo "========================================"
  echo ""

  # ── Prerequisites check ──────────────────────────────────────────────
  echo "[smoke] Checking prerequisites..."

  # Check for port conflicts
  local port_conflict=false
  if lsof -nP -iTCP:"$agentd_port" -sTCP:LISTEN >/dev/null 2>&1; then
    agentd_pid_before="$(lsof -nP -iTCP:"$agentd_port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $2}')"
    echo -e "${YELLOW}[smoke] WARNING: Port $agentd_port is already in use (PID $agentd_pid_before)${NC}"
    services_were_running=true
  fi
  if lsof -nP -iTCP:"$agentgw_port" -sTCP:LISTEN >/dev/null 2>&1; then
    agentgw_pid_before="$(lsof -nP -iTCP:"$agentgw_port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $2}')"
    echo -e "${YELLOW}[smoke] WARNING: Port $agentgw_port is already in use (PID $agentgw_pid_before)${NC}"
    services_were_running=true
  fi

  if [[ "$services_were_running" == true ]]; then
    echo -e "${YELLOW}[smoke] Existing services detected. Smoke test will verify they are healthy${NC}"
    echo -e "${YELLOW}[smoke] but will NOT stop them (to avoid disrupting your dev environment).${NC}"
    echo ""
  fi

  # Check required tools
  if ! command -v go &>/dev/null; then
    echo -e "${RED}[smoke] FAIL: Go is not installed${NC}"
    return 1
  fi
  if ! command -v curl &>/dev/null; then
    echo -e "${RED}[smoke] FAIL: curl is not installed${NC}"
    return 1
  fi
  echo -e "${GREEN}[smoke] Prerequisites OK${NC}"
  echo ""

  # ── Build smoke test ─────────────────────────────────────────────────
  echo "[smoke] TEST 1/3: Build smoke test"
  echo "[smoke] Running: scripts/build.sh go"
  if bash scripts/build.sh go >/dev/null 2>&1; then
    build_ok=1
    echo -e "${GREEN}[smoke] Build: PASS${NC}"
  else
    echo -e "${RED}[smoke] Build: FAIL${NC}"
  fi
  echo ""

  # Verify artifacts exist
  local artifact_check=true
  for artifact in out/darwin-arm64/agentd out/darwin-arm64/agentgw out/linux-amd64/agentd out/linux-amd64/agentgw; do
    if [[ ! -f "$artifact" ]]; then
      echo -e "${RED}[smoke] Missing artifact: $artifact${NC}"
      artifact_check=false
    fi
  done
  if [[ "$artifact_check" == true && $build_ok -eq 1 ]]; then
    echo -e "${GREEN}[smoke] Artifacts verified in out/${NC}"
  elif [[ "$artifact_check" == false ]]; then
    build_ok=0
  fi
  echo ""

  # ── Local deploy smoke test ──────────────────────────────────────────
  echo "[smoke] TEST 2/3: Local deploy smoke test"

  if [[ "$services_were_running" == true ]]; then
    echo -e "${YELLOW}[smoke] Skipping deploy start (services already running)${NC}"
    deploy_ok=1
  else
    echo "[smoke] Running: scripts/deploy.sh local"
    if bash scripts/deploy.sh local >/dev/null 2>&1; then
      deploy_ok=1
      echo -e "${GREEN}[smoke] Local deploy: PASS${NC}"
    else
      echo -e "${RED}[smoke] Local deploy: FAIL${NC}"
    fi
  fi
  echo ""

  # ── Health check smoke test ──────────────────────────────────────────
  echo "[smoke] TEST 3/3: Health check smoke test"

  # Check agentd health
  local agentd_healthy=0
  local agentd_response
  agentd_response=$(curl -s "http://localhost:$agentd_port/status" 2>/dev/null || true)
  if [[ -n "$agentd_response" ]]; then
    if echo "$agentd_response" | python3 -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if 'version' in d else 1)" 2>/dev/null; then
      agentd_healthy=1
      local agentd_version
      agentd_version=$(echo "$agentd_response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('version','unknown'))" 2>/dev/null || echo "unknown")
      echo -e "${GREEN}[smoke] agentd health (port $agentd_port): PASS (version: $agentd_version)${NC}"
    else
      echo -e "${RED}[smoke] agentd health (port $agentd_port): FAIL (invalid response)${NC}"
    fi
  else
    echo -e "${RED}[smoke] agentd health (port $agentd_port): FAIL (no response)${NC}"
  fi

  # Check agentgw health
  local agentgw_healthy=0
  local agentgw_response
  local agentgw_token=""
  if [[ -f "$HOME/.agentgw/config.json" ]]; then
    agentgw_token=$(python3 -c "import json; print(json.load(open('$HOME/.agentgw/config.json')).get('token',''))" 2>/dev/null || true)
  fi

  agentgw_response=$(curl -s "http://localhost:$agentgw_port/status" -H "Authorization: Bearer ${agentgw_token}" 2>/dev/null || true)
  if [[ -n "$agentgw_response" ]]; then
    if echo "$agentgw_response" | python3 -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if 'version' in d else 1)" 2>/dev/null; then
      agentgw_healthy=1
      local agentgw_version
      agentgw_version=$(echo "$agentgw_response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('version','unknown'))" 2>/dev/null || echo "unknown")
      echo -e "${GREEN}[smoke] agentgw health (port $agentgw_port): PASS (version: $agentgw_version)${NC}"
    else
      echo -e "${RED}[smoke] agentgw health (port $agentgw_port): FAIL (invalid response)${NC}"
    fi
  else
    echo -e "${RED}[smoke] agentgw health (port $agentgw_port): FAIL (no response)${NC}"
  fi

  # Check agentgw serves static files
  local agentgw_static_ok=0
  if curl -s "http://localhost:$agentgw_port/" 2>/dev/null | grep -q "html"; then
    agentgw_static_ok=1
    echo -e "${GREEN}[smoke] agentgw static files: PASS${NC}"
  else
    echo -e "${RED}[smoke] agentgw static files: FAIL${NC}"
  fi

  if [[ $agentd_healthy -eq 1 && $agentgw_healthy -eq 1 && $agentgw_static_ok -eq 1 ]]; then
    health_ok=1
  fi
  echo ""

  # ── Cleanup (only if we started services) ────────────────────────────
  echo "[smoke] Cleanup"
  if [[ "$services_were_running" == true ]]; then
    echo -e "${YELLOW}[smoke] Skipping cleanup (services were running before test)${NC}"
    cleanup_ok=1
  else
    echo "[smoke] Stopping services..."
    bash scripts/install.sh stop >/dev/null 2>&1 || true
    sleep 1

    # Verify ports are freed
    local ports_freed=true
    if lsof -nP -iTCP:"$agentd_port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo -e "${RED}[smoke] agentd still listening on port $agentd_port${NC}"
      ports_freed=false
    fi
    if lsof -nP -iTCP:"$agentgw_port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo -e "${RED}[smoke] agentgw still listening on port $agentgw_port${NC}"
      ports_freed=false
    fi
    if [[ "$ports_freed" == true ]]; then
      cleanup_ok=1
      echo -e "${GREEN}[smoke] Cleanup: PASS (ports freed)${NC}"
    else
      echo -e "${RED}[smoke] Cleanup: FAIL (ports still in use)${NC}"
    fi
  fi
  echo ""

  # ── Summary ──────────────────────────────────────────────────────────
  echo "========================================"
  echo "         SMOKE TEST SUMMARY"
  echo "========================================"
  if [[ $build_ok -eq 1 ]]; then
    echo -e "  Build          : ${GREEN}PASS${NC}"
  else
    echo -e "  Build          : ${RED}FAIL${NC}"
  fi
  if [[ $deploy_ok -eq 1 ]]; then
    echo -e "  Local Deploy   : ${GREEN}PASS${NC}"
  else
    echo -e "  Local Deploy   : ${RED}FAIL${NC}"
  fi
  if [[ $health_ok -eq 1 ]]; then
    echo -e "  Health Checks  : ${GREEN}PASS${NC}"
  else
    echo -e "  Health Checks  : ${RED}FAIL${NC}"
  fi
  if [[ $cleanup_ok -eq 1 ]]; then
    echo -e "  Cleanup        : ${GREEN}PASS${NC}"
  else
    echo -e "  Cleanup        : ${RED}FAIL${NC}"
  fi
  echo "========================================"

  if [[ $build_ok -eq 1 && $deploy_ok -eq 1 && $health_ok -eq 1 && $cleanup_ok -eq 1 ]]; then
    echo -e "${GREEN}All smoke tests passed.${NC}"
    return 0
  else
    echo -e "${RED}Some smoke tests failed.${NC}"
    return 1
  fi
}

# ── Main ───────────────────────────────────────────────────────────────

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  SUBCOMMAND="${1:-help}"

  case "$SUBCOMMAND" in
    help|--help|-h)
      show_help
      exit 0
      ;;
    unit)
      run_unit_tests
      ;;
    e2e)
      run_e2e_tests
      ;;
    flutter)
      shift || true
      run_flutter_tests "$@"
      ;;
    smoke)
      run_smoke_tests
      ;;
    *)
      echo "Unknown subcommand: $SUBCOMMAND"
      echo "Run '$0 help' for usage."
      exit 1
      ;;
  esac
fi
