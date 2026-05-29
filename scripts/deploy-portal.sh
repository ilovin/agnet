#!/usr/bin/env bash
# Dev → Prod Portal Release Script
#
# Usage:
#   ./scripts/deploy-portal.sh dev     # Deploy to dev environment for testing
#   ./scripts/deploy-portal.sh prod    # Deploy to production after dev testing passes
#   ./scripts/deploy-portal.sh test    # Run tests against dev environment
#
# Environment:
#   REMOTE_HOST    SSH alias for the server (default: tx)

set -euo pipefail
cd "$(dirname "$0")/.."

REMOTE_HOST="${REMOTE_HOST:-tx}"
PORTAL_SRC="./portal"
DEV_DIR="/opt/phonetalk/portal-dev"
PROD_DIR="/opt/phonetalk/portal"

show_help() {
    cat <<'EOF'
Usage: ./scripts/deploy-portal.sh [COMMAND]

Commands:
  dev       Deploy portal to dev environment (dev.download.ilovin.xyz)
  test      Run automated tests against dev environment
  prod      Deploy portal to production (download.ilovin.xyz)
  verify    Verify both dev and prod environments are accessible
  help      Show this help message

Examples:
  # 1. Deploy to dev for testing
  ./scripts/deploy-portal.sh dev

  # 2. Run tests on dev
  ./scripts/deploy-portal.sh test

  # 3. If tests pass, deploy to prod
  ./scripts/deploy-portal.sh prod

  # 4. Verify both environments
  ./scripts/deploy-portal.sh verify
EOF
}

deploy_to_dev() {
    echo "[deploy-portal] Deploying to dev environment..."
    echo "[deploy-portal] Source: $PORTAL_SRC"
    echo "[deploy-portal] Target: $REMOTE_HOST:$DEV_DIR"
    
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p '$DEV_DIR'" || {
        echo "[deploy-portal] ERROR: Cannot connect to $REMOTE_HOST"
        exit 1
    }
    
    scp -o ConnectTimeout=5 -r "$PORTAL_SRC/"* "$REMOTE_HOST:$DEV_DIR/" || {
        echo "[deploy-portal] ERROR: SCP failed"
        exit 1
    }
    
    echo "[deploy-portal] Dev deployment complete"
    echo "[deploy-portal] Test URL: https://dev.download.ilovin.xyz"
    echo ""
    echo "[deploy-portal] Next steps:"
    echo "  1. Open https://dev.download.ilovin.xyz in browser"
    echo "  2. Click '下载 APK' button"
    echo "  3. Verify download starts and file is correct"
    echo "  4. Run: ./scripts/deploy-portal.sh test"
}

deploy_to_prod() {
    echo "[deploy-portal] Deploying to production..."
    echo "[deploy-portal] Source: $PORTAL_SRC"
    echo "[deploy-portal] Target: $REMOTE_HOST:$PROD_DIR"
    
    # Safety check: ensure dev has been tested
    echo "[deploy-portal] WARNING: This will update the production portal!"
    read -p "[deploy-portal] Have you tested on dev? (yes/no): " confirm
    if [[ "$confirm" != "yes" ]]; then
        echo "[deploy-portal] Aborted. Please test on dev first:"
        echo "  ./scripts/deploy-portal.sh dev"
        echo "  ./scripts/deploy-portal.sh test"
        exit 1
    fi
    
    # Verify connectivity and ensure prod dir exists (uses sudo since /opt/phonetalk/portal is root-owned)
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "sudo -n mkdir -p '$PROD_DIR'" || {
        echo "[deploy-portal] ERROR: Cannot connect to $REMOTE_HOST or sudo requires password"
        echo "[deploy-portal] Hint: ensure passwordless sudo is configured for the deploy user on $REMOTE_HOST"
        exit 1
    }

    # Backup current prod (sibling .bak.<ts> directory, sudo so root-owned files copy cleanly)
    local backup_ts
    backup_ts="$(date +%Y%m%d_%H%M%S)"
    echo "[deploy-portal] Backing up current production to ${PROD_DIR}.bak.${backup_ts}..."
    ssh "$REMOTE_HOST" "sudo -n cp -r '$PROD_DIR' '${PROD_DIR}.bak.${backup_ts}'" || {
        echo "[deploy-portal] WARNING: Backup step failed (continuing)"
    }

    # /opt/phonetalk/portal is root-owned; ubuntu cannot scp directly.
    # Workaround: scp to /tmp staging area, then sudo cp into place.
    local stage_ts stage_path
    stage_ts="$(date +%s)"
    stage_path="/tmp/portal_index_${stage_ts}.html"

    echo "[deploy-portal] Staging index.html to $REMOTE_HOST:$stage_path ..."
    scp -o ConnectTimeout=5 "$PORTAL_SRC/index.html" "$REMOTE_HOST:$stage_path" || {
        echo "[deploy-portal] ERROR: SCP to staging path failed"
        exit 1
    }

    echo "[deploy-portal] Installing index.html to $PROD_DIR via sudo ..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "sudo -n cp '$stage_path' '$PROD_DIR/index.html' && sudo -n chmod 644 '$PROD_DIR/index.html' && rm -f '$stage_path'" || {
        echo "[deploy-portal] ERROR: sudo cp into $PROD_DIR failed"
        echo "[deploy-portal] Hint: passwordless sudo is required for the deploy user on $REMOTE_HOST"
        echo "[deploy-portal] Staging file left at $REMOTE_HOST:$stage_path for manual recovery"
        exit 1
    }

    echo "[deploy-portal] Production deployment complete"
    echo "[deploy-portal] URL: https://download.ilovin.xyz"
    echo ""
    echo "[deploy-portal] Rollback command (if needed):"
    echo "  ssh $REMOTE_HOST 'sudo cp -r ${PROD_DIR}.bak.${backup_ts}/index.html $PROD_DIR/index.html'"
}

run_tests() {
    echo "[deploy-portal] Running tests against dev environment..."
    
    local failed=0
    
    # Test 1: Check dev portal files exist
    echo "[test] Checking dev portal files..."
    if ssh "$REMOTE_HOST" "test -f '$DEV_DIR/index.html'"; then
        echo "  ✓ index.html exists"
    else
        echo "  ✗ index.html missing"
        failed=1
    fi
    
    # Test 2: Check APK link is versioned
    echo "[test] Checking APK link format..."
    if ssh "$REMOTE_HOST" "grep -q 'agentapp-v' '$DEV_DIR/index.html'"; then
        echo "  ✓ APK link contains version"
    else
        echo "  ✗ APK link missing version"
        failed=1
    fi
    
    # Test 3: Check manifest loading fallback
    echo "[test] Checking manifest loading logic..."
    if ssh "$REMOTE_HOST" "grep -q 'loadLatestVersion' '$DEV_DIR/index.html'"; then
        echo "  ✓ Dynamic manifest loading present"
    else
        echo "  ✗ Dynamic manifest loading missing"
        failed=1
    fi
    
    # Test 4: Check dev subdomain responds locally
    echo "[test] Checking dev subdomain response..."
    if ssh "$REMOTE_HOST" "curl -s -o /dev/null -w '%{http_code}' -H 'Host: dev.download.ilovin.xyz' http://localhost:80 | grep -q '200\\|308'"; then
        echo "  ✓ Dev subdomain responding"
    else
        echo "  ✗ Dev subdomain not responding"
        failed=1
    fi
    
    # Test 5: Verify manifest endpoint works
    echo "[test] Checking manifest endpoint..."
    if ssh "$REMOTE_HOST" "curl -s -o /dev/null -w '%{http_code}' http://localhost:80/v1/release/latest -H 'Host: dev.download.ilovin.xyz' | grep -q '200\\|308'"; then
        echo "  ✓ Manifest endpoint accessible"
    else
        echo "  ✗ Manifest endpoint failed"
        failed=1
    fi
    
    echo ""
    if [[ $failed -eq 0 ]]; then
        echo "[test] ✓ All tests passed! Ready for production deployment."
        echo "[test] Run: ./scripts/deploy-portal.sh prod"
    else
        echo "[test] ✗ Some tests failed. Please fix before deploying to production."
        exit 1
    fi
}

verify_environments() {
    echo "[verify] Checking both environments..."
    
    echo "[verify] Dev environment ($DEV_DIR):"
    ssh "$REMOTE_HOST" "ls -la '$DEV_DIR'" || echo "  ✗ Dev not accessible"
    
    echo ""
    echo "[verify] Production environment ($PROD_DIR):"
    ssh "$REMOTE_HOST" "ls -la '$PROD_DIR'" || echo "  ✗ Prod not accessible"
    
    echo ""
    echo "[verify] Dev URL: https://dev.download.ilovin.xyz"
    echo "[verify] Prod URL: https://download.ilovin.xyz"
}

case "${1:-help}" in
    dev)
        deploy_to_dev
        ;;
    prod)
        deploy_to_prod
        ;;
    test)
        run_tests
        ;;
    verify)
        verify_environments
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        echo "Unknown command: $1"
        show_help
        exit 1
        ;;
esac
