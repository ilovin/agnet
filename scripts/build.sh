#!/usr/bin/env bash
# Build all Agent Manager components with incremental caching
#
# Usage:
#   ./scripts/build.sh [TARGET]
#
# TARGETS:
#   all              Build everything (agentd + agentgw + APK + IPA + Web) [default]
#   go               Build all Go binaries (agentd + agentgw for all platforms)
#   agentd           Build agentd for current platform
#   agentd-linux     Build agentd for Linux amd64
#   agentgw          Build agentgw for current platform
#   agentgw-linux    Build agentgw for Linux amd64
#   apk              Build Android APK
#   ipa              Build iOS IPA
#   web              Build Flutter Web
#   help             Show this help message

set -euo pipefail
cd "$(dirname "$0")/.."

AGENTD_DIR="./agentd"
AGENTGW_DIR="./agentgw"
AGENTAPP_DIR="./agentapp"
OUT_DIR="./out"
LOCAL_BIN="$OUT_DIR/agentd"
LINUX_BIN="$OUT_DIR/agentd-linux"
GW_BIN="$OUT_DIR/agentgw"
GW_LINUX_BIN="$OUT_DIR/agentgw-linux"
GW_MACOS_BIN="$OUT_DIR/agentgw-macos-arm64"
APK_OUTPUT="$OUT_DIR/agentapp.apk"
IPA_OUTPUT="$OUT_DIR/agentapp.ipa"
WEB_STATIC_DIR="$OUT_DIR/static"

mkdir -p "$OUT_DIR"

# Symlink legacy paths to out/ for backward compatibility
link_legacy() {
    local src="$1" dest="$2"
    if [[ -L "$dest" ]]; then
        return
    fi
    if [[ -e "$dest" && ! -L "$dest" ]]; then
        rm -rf "$dest"
    fi
    local relpath
    relpath="$(python3 -c "import os; print(os.path.relpath('$src', '$(dirname "$dest")'))")"
    ln -sf "$relpath" "$dest"
}

# ── Hash-based incremental build helpers ──────────────────────────────

binary_hash_file() {
    local output="$1"
    echo "${output}.buildhash"
}

compute_source_hash() {
    python3 - "$@" <<'PY'
import hashlib
import pathlib
import sys

paths = [pathlib.Path(p) for p in sys.argv[1:]]
files = []
for root in paths:
    if root.is_file():
        files.append(root)
        continue
    if root.is_dir():
        for path in root.rglob('*'):
            if path.is_file() and path.suffix in {'.go'}:
                files.append(path)
for path in sorted(set(files), key=lambda p: str(p)):
    rel = str(path).replace('\\', '/')
    sys.stdout.write(rel + '\n')
    with path.open('rb') as f:
        sys.stdout.flush()
        data = f.read()
    sys.stdout.buffer.write(data)
PY
}

binary_up_to_date() {
    local output="$1"
    shift
    [[ -f "$output" ]] || return 1
    local hash_file expected actual
    hash_file="$(binary_hash_file "$output")"
    [[ -f "$hash_file" ]] || return 1
    expected="$(<"$hash_file")"
    actual="$(compute_source_hash "$@" | shasum -a 256 | awk '{print $1}')"
    [[ -n "$expected" && "$expected" == "$actual" ]]
}

record_binary_hash() {
    local output="$1"
    shift
    compute_source_hash "$@" | shasum -a 256 | awk '{print $1}' > "$(binary_hash_file "$output")"
}

# Check if an output file is up-to-date versus its source files.
# Usage: up_to_date <output> <find_args...>
up_to_date() {
    local output="$1"
    shift
    [[ ! -f "$output" ]] && return 1
    local newer
    newer=$(find "$@" -newer "$output" -print 2>/dev/null | head -1)
    [[ -z "$newer" ]]
}

# ── Build functions ────────────────────────────────────────────────────

build_agentd_mac() {
    if binary_up_to_date "$LOCAL_BIN" agentd agentd/go.mod agentd/go.sum; then
        echo "[build] agentd (macOS) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentd for macOS..."
    (cd "$AGENTD_DIR" && go build -o "../$LOCAL_BIN" ./cmd/agentd/)
    record_binary_hash "$LOCAL_BIN" agentd agentd/go.mod agentd/go.sum
    link_legacy "$LOCAL_BIN" "$AGENTD_DIR/agentd"
    echo "[build] agentd (macOS): $(ls -lh "$LOCAL_BIN" | awk '{print $5}')"
}

build_agentd_linux() {
    if binary_up_to_date "$LINUX_BIN" agentd agentd/go.mod agentd/go.sum; then
        echo "[build] agentd (Linux) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentd for Linux amd64..."
    (cd "$AGENTD_DIR" && GOOS=linux GOARCH=amd64 go build -o "../$LINUX_BIN" ./cmd/agentd/)
    record_binary_hash "$LINUX_BIN" agentd agentd/go.mod agentd/go.sum
    link_legacy "$LINUX_BIN" "$AGENTD_DIR/agentd-linux"
    echo "[build] agentd (Linux): $(ls -lh "$LINUX_BIN" | awk '{print $5}')"
}

build_agentgw_mac() {
    if binary_up_to_date "$GW_BIN" agentgw agentgw/go.mod agentgw/go.sum; then
        echo "[build] agentgw (macOS) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentgw for macOS..."
    (cd "$AGENTGW_DIR" && go build -o "../$GW_BIN" ./cmd/agentgw/)
    record_binary_hash "$GW_BIN" agentgw agentgw/go.mod agentgw/go.sum
    link_legacy "$GW_BIN" "$AGENTGW_DIR/agentgw"
    echo "[build] agentgw (macOS): $(ls -lh "$GW_BIN" | awk '{print $5}')"
}

build_agentgw_linux() {
    if binary_up_to_date "$GW_LINUX_BIN" agentgw agentgw/go.mod agentgw/go.sum; then
        echo "[build] agentgw (Linux) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentgw for Linux amd64..."
    (cd "$AGENTGW_DIR" && GOOS=linux GOARCH=amd64 go build -o "../$GW_LINUX_BIN" ./cmd/agentgw/)
    record_binary_hash "$GW_LINUX_BIN" agentgw agentgw/go.mod agentgw/go.sum
    link_legacy "$GW_LINUX_BIN" "$AGENTGW_DIR/agentgw-linux"
    echo "[build] agentgw (Linux): $(ls -lh "$GW_LINUX_BIN" | awk '{print $5}')"
}

build_apk() {
    local needs_build=false
    if ! up_to_date "$APK_OUTPUT" agentapp/lib -type f -name '*.dart'; then
        needs_build=true
    fi
    if ! up_to_date "$APK_OUTPUT" agentapp/pubspec.yaml; then
        needs_build=true
    fi
    if ! up_to_date "$APK_OUTPUT" agentapp/pubspec.lock; then
        needs_build=true
    fi
    if [[ "$needs_build" == false ]]; then
        echo "[build] APK up-to-date, skipping"
        return 0
    fi
    echo "[build] Building APK..."
    (cd "$AGENTAPP_DIR" && flutter build apk --release --no-tree-shake-icons)
    local apk="$AGENTAPP_DIR/build/app/outputs/flutter-apk/app-release.apk"
    if [[ -f "$apk" ]]; then
        cp "$apk" "$APK_OUTPUT"
        link_legacy "$APK_OUTPUT" "$AGENTGW_DIR/agentapp.apk"
        echo "[build] APK: $(ls -lh "$APK_OUTPUT" | awk '{print $5}')"
    else
        echo "[build] ERROR: APK not found at $apk"
        return 1
    fi
}

build_ipa() {
    if ! command -v xcodebuild &>/dev/null; then
        echo "[build] Skipping IPA (Xcode not found)"
        return 0
    fi
    if up_to_date "$IPA_OUTPUT" agentapp/lib -type f -name '*.dart' agentapp/pubspec.yaml agentapp/pubspec.lock; then
        echo "[build] IPA up-to-date, skipping"
        return 0
    fi
    echo "[build] Building iOS IPA..."
    (cd "$AGENTAPP_DIR" && flutter build ipa --release --export-method ad-hoc 2>/dev/null) || {
        echo "[build] WARNING: iOS IPA build failed (needs Apple Developer account / provisioning profile)"
        return 0
    }
    local ipa
    ipa=$(ls -t "$AGENTAPP_DIR/build/ios/ipa/"*.ipa 2>/dev/null | head -1)
    if [[ -n "$ipa" && -f "$ipa" ]]; then
        cp "$ipa" "$IPA_OUTPUT"
        link_legacy "$IPA_OUTPUT" "$AGENTGW_DIR/agentapp.ipa"
        echo "[build] IPA: $(ls -lh "$IPA_OUTPUT" | awk '{print $5}')"
    else
        echo "[build] WARNING: IPA not found after build"
    fi
}

build_web() {
    if up_to_date "$WEB_STATIC_DIR/index.html" agentapp/lib -type f -name '*.dart' agentapp/pubspec.yaml agentapp/pubspec.lock; then
        echo "[build] Web static up-to-date, skipping"
        return 0
    fi
    echo "[build] Building Flutter Web..."
    (cd "$AGENTAPP_DIR" && flutter build web --release --no-tree-shake-icons)
    rm -rf "$WEB_STATIC_DIR"
    cp -r "$AGENTAPP_DIR/build/web" "$WEB_STATIC_DIR"
    link_legacy "$WEB_STATIC_DIR" "$AGENTGW_DIR/static"
    echo "[build] Web static copied to $WEB_STATIC_DIR"
}

build_go_all() {
    echo "[build] Building all Go binaries in parallel..."
    local mac_pid linux_pid gw_mac_pid gw_linux_pid
    local mac_ok=0 linux_ok=0 gw_mac_ok=0 gw_linux_ok=0
    build_agentd_mac & mac_pid=$!
    build_agentd_linux & linux_pid=$!
    build_agentgw_mac & gw_mac_pid=$!
    build_agentgw_linux & gw_linux_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_pid" && linux_ok=1 || true
    wait "$gw_mac_pid" && gw_mac_ok=1 || true
    wait "$gw_linux_pid" && gw_linux_ok=1 || true
    echo "[build] Go build results: agentd_mac=$mac_ok agentd_linux=$linux_ok agentgw_mac=$gw_mac_ok agentgw_linux=$gw_linux_ok"
    [[ $mac_ok -eq 1 && $linux_ok -eq 1 && $gw_mac_ok -eq 1 && $gw_linux_ok -eq 1 ]]
}

build_all() {
    echo "[build] Building all components in parallel..."
    local mac_pid linux_pid gw_mac_pid gw_linux_pid apk_pid ipa_pid web_pid
    local mac_ok=0 linux_ok=0 gw_mac_ok=0 gw_linux_ok=0 apk_ok=0 ipa_ok=0 web_ok=0
    build_agentd_mac & mac_pid=$!
    build_agentd_linux & linux_pid=$!
    build_agentgw_mac & gw_mac_pid=$!
    build_agentgw_linux & gw_linux_pid=$!
    build_apk & apk_pid=$!
    build_ipa & ipa_pid=$!
    build_web & web_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_pid" && linux_ok=1 || true
    wait "$gw_mac_pid" && gw_mac_ok=1 || true
    wait "$gw_linux_pid" && gw_linux_ok=1 || true
    wait "$apk_pid" && apk_ok=1 || true
    wait "$ipa_pid" && ipa_ok=1 || true
    wait "$web_pid" && web_ok=1 || true
    echo "[build] Build results: agentd_mac=$mac_ok agentd_linux=$linux_ok agentgw_mac=$gw_mac_ok agentgw_linux=$gw_linux_ok apk=$apk_ok ipa=$ipa_ok web=$web_ok"
    [[ $mac_ok -eq 1 && $linux_ok -eq 1 && $gw_mac_ok -eq 1 && $gw_linux_ok -eq 1 && $apk_ok -eq 1 ]]
}

show_help() {
  cat <<EOF
Usage: ./scripts/build.sh [TARGET]

Build Agent Manager components with incremental caching.
Go binaries are rebuilt only when source-content hash changes.
Flutter artifacts use timestamp-based incremental builds.

TARGETS:
  all              Build everything (agentd + agentgw + APK + IPA + Web) [default]
  go               Build all Go binaries (agentd + agentgw for all platforms)
  agentd           Build agentd for current platform
  agentd-linux     Build agentd for Linux amd64
  agentgw          Build agentgw for current platform
  agentgw-linux    Build agentgw for Linux amd64
  apk              Build Android APK
  ipa              Build iOS IPA
  web              Build Flutter Web
  help             Show this help message

EXAMPLES:
  # Build everything (default)
  ./scripts/build.sh

  # Build only Go binaries
  ./scripts/build.sh go

  # Build only APK
  ./scripts/build.sh apk

  # Build agentgw for Linux
  ./scripts/build.sh agentgw-linux

INCREMENTAL BUILDS:
  Go binaries use content-hash caching (.buildhash files).
  Flutter artifacts use timestamp-based checks.
  Builds are skipped when sources haven't changed.

OUTPUT LOCATIONS:
  agentd/agentd              — macOS daemon
  agentd/agentd-linux        — Linux daemon (amd64)
  agentgw/agentgw            — macOS gateway
  agentgw/agentgw-linux      — Linux gateway (amd64)
  agentgw/agentapp.apk       — Android APK
  agentgw/agentapp.ipa       — iOS IPA
  agentgw/static/            — Web static assets
EOF
}

# ── Main ───────────────────────────────────────────────────────────────

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    TARGET="${1:-all}"

    case "$TARGET" in
        help|--help|-h)
            show_help
            exit 0
            ;;
        all)
            build_all
            ;;
        go)
            build_go_all
            ;;
        agentd)
            build_agentd_mac
            ;;
        agentd-linux)
            build_agentd_linux
            ;;
        agentgw)
            build_agentgw_mac
            ;;
        agentgw-linux)
            build_agentgw_linux
            ;;
        apk)
            build_apk
            ;;
        ipa)
            build_ipa
            ;;
        web)
            build_web
            ;;
        *)
            echo "Unknown target: $TARGET"
            echo "Run '$0 help' for usage."
            exit 1
            ;;
    esac
fi
