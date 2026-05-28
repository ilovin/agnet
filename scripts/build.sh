#!/usr/bin/env bash
# Build all Agent Manager components with incremental caching
#
# Usage:
#   ./scripts/build.sh [TARGET]
#   DOMAIN=ilovim.xyz ./scripts/build.sh go
#
# TARGETS:
#   all              Build everything (agentd + agentgw + APK + IPA + Web) [default]
#   go               Build all Go binaries (agentd + agentgw for all platforms)
#   agentd           Build agentd for current platform
#   agentd-linux     Build agentd for Linux amd64
#   agentd-linux-arm64 Build agentd for Linux arm64
#   agentgw          Build agentgw for current platform
#   agentgw-linux    Build agentgw for Linux amd64
#   agentgw-linux-arm64 Build agentgw for Linux arm64
#   apk              Build Android APK
#   ipa              Build iOS IPA
#   web              Build Flutter Web
#   help             Show this help message
#
# ENVIRONMENT:
#   DOMAIN           Root domain for build-time injection (default: ilovin.xyz)
#                    Subdomains are automatically derived: api.<DOMAIN>, download.<DOMAIN>
#   BUILD_VERSION    Version string to embed in binaries

# shellcheck disable=SC2012  # ls -lh|awk '{print $5}' is the idiomatic file-size display; no clean find equivalent
# shellcheck disable=SC2034  # Alias variables (LINUX_BIN, GW_LINUX_BIN, OUT_DIR_LINUX) exported for callers that source this script
set -euo pipefail
cd "$(dirname "$0")/.."

AGENTD_DIR="./agentd"
AGENTGW_DIR="./agentgw"
AGENTAPP_DIR="./agentapp"
OUT_DIR="./out"
OUT_DIR_DARWIN="$OUT_DIR/darwin-arm64"
OUT_DIR_LINUX_AMD64="$OUT_DIR/linux-amd64"
OUT_DIR_LINUX_ARM64="$OUT_DIR/linux-arm64"
OUT_DIR_LINUX="$OUT_DIR_LINUX_AMD64"
OUT_DIR_ANDROID="$OUT_DIR/android"
OUT_DIR_IOS="$OUT_DIR/ios"
LOCAL_BIN="$OUT_DIR_DARWIN/agentd"
LINUX_AMD64_BIN="$OUT_DIR_LINUX_AMD64/agentd"
LINUX_ARM64_BIN="$OUT_DIR_LINUX_ARM64/agentd"
LINUX_BIN="$LINUX_AMD64_BIN"
GW_BIN="$OUT_DIR_DARWIN/agentgw"
GW_LINUX_AMD64_BIN="$OUT_DIR_LINUX_AMD64/agentgw"
GW_LINUX_ARM64_BIN="$OUT_DIR_LINUX_ARM64/agentgw"
GW_LINUX_BIN="$GW_LINUX_AMD64_BIN"
APK_OUTPUT="$OUT_DIR_ANDROID/agentapp.apk"
IPA_OUTPUT="$OUT_DIR_IOS/agentapp.ipa"
WEB_STATIC_DIR="$OUT_DIR/static"
WEB_HASH_FILE="$OUT_DIR/static.buildhash"
BUILD_VERSION="${BUILD_VERSION:-}"
DOMAIN="${DOMAIN:-ilovin.xyz}"

# Derive subdomains from root domain
HUB_DOMAIN="tunnel.${DOMAIN}"
API_DOMAIN="api.${DOMAIN}"
DOWNLOAD_DOMAIN="download.${DOMAIN}"

mkdir -p "$OUT_DIR_DARWIN" "$OUT_DIR_LINUX_AMD64" "$OUT_DIR_LINUX_ARM64" "$OUT_DIR_ANDROID" "$OUT_DIR_IOS"

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

version_hash_file() {
    local output="$1"
    echo "${output}.buildversion"
}

domain_hash_file() {
    local output="$1"
    echo "${output}.builddomain"
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
            if path.is_file() and path.suffix in {'.go', '.dart'}:
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
    local hash_file expected actual version_file recorded_version
    hash_file="$(binary_hash_file "$output")"
    [[ -f "$hash_file" ]] || return 1
    expected="$(<"$hash_file")"
    actual="$(compute_source_hash "$@" | shasum -a 256 | awk '{print $1}')"
    [[ -n "$expected" && "$expected" == "$actual" ]] || return 1
    if [[ -n "$BUILD_VERSION" ]]; then
        version_file="$(version_hash_file "$output")"
        [[ -f "$version_file" ]] || return 1
        recorded_version="$(<"$version_file")"
        [[ "$recorded_version" == "$BUILD_VERSION" ]] || return 1
    fi
    # Check if DOMAIN changed
    local domain_file recorded_domain
    domain_file="$(domain_hash_file "$output")"
    [[ -f "$domain_file" ]] || return 1
    recorded_domain="$(<"$domain_file")"
    [[ "$recorded_domain" == "${DOMAIN:-}" ]] || return 1
    return 0
}

record_binary_hash() {
    local output="$1"
    shift
    compute_source_hash "$@" | shasum -a 256 | awk '{print $1}' > "$(binary_hash_file "$output")"
    if [[ -n "$BUILD_VERSION" ]]; then
        printf '%s' "$BUILD_VERSION" > "$(version_hash_file "$output")"
    else
        rm -f "$(version_hash_file "$output")"
    fi
    printf '%s' "${DOMAIN:-}" > "$(domain_hash_file "$output")"
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
    (cd "$AGENTD_DIR" && go build -ldflags "-s -w" -o "../$LOCAL_BIN" ./cmd/agentd/)
    record_binary_hash "$LOCAL_BIN" agentd agentd/go.mod agentd/go.sum
    link_legacy "$LOCAL_BIN" "$AGENTD_DIR/agentd"
    echo "[build] agentd (macOS): $(ls -lh "$LOCAL_BIN" | awk '{print $5}')"
}

build_agentd_linux_amd64() {
    if binary_up_to_date "$LINUX_AMD64_BIN" agentd agentd/go.mod agentd/go.sum; then
        echo "[build] agentd (Linux amd64) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentd for Linux amd64..."
    (cd "$AGENTD_DIR" && GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o "../$LINUX_AMD64_BIN" ./cmd/agentd/)
    record_binary_hash "$LINUX_AMD64_BIN" agentd agentd/go.mod agentd/go.sum
    link_legacy "$LINUX_AMD64_BIN" "$AGENTD_DIR/agentd-linux"
    echo "[build] agentd (Linux amd64): $(ls -lh "$LINUX_AMD64_BIN" | awk '{print $5}')"
}

build_agentd_linux_arm64() {
    if binary_up_to_date "$LINUX_ARM64_BIN" agentd agentd/go.mod agentd/go.sum; then
        echo "[build] agentd (Linux arm64) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentd for Linux arm64..."
    (cd "$AGENTD_DIR" && GOOS=linux GOARCH=arm64 go build -o "../$LINUX_ARM64_BIN" ./cmd/agentd/)
    record_binary_hash "$LINUX_ARM64_BIN" agentd agentd/go.mod agentd/go.sum
    link_legacy "$LINUX_ARM64_BIN" "$AGENTD_DIR/agentd-linux-arm64"
    echo "[build] agentd (Linux arm64): $(ls -lh "$LINUX_ARM64_BIN" | awk '{print $5}')"
}

build_agentd_linux() {
    build_agentd_linux_amd64
}


build_agentgw_mac() {
    if binary_up_to_date "$GW_BIN" agentgw agentgw/go.mod agentgw/go.sum; then
        echo "[build] agentgw (macOS) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentgw for macOS..."
    local ldflags="-s -w"
    if [[ -n "$BUILD_VERSION" ]]; then
        ldflags="$ldflags -X main.Version=$BUILD_VERSION"
    fi
    if [[ -n "$DOMAIN" ]]; then
        ldflags="$ldflags -X main.DefaultHubDomain=$HUB_DOMAIN -X main.DefaultAPIDomain=$API_DOMAIN -X main.DefaultDownloadDomain=$DOWNLOAD_DOMAIN"
    fi
    (cd "$AGENTGW_DIR" && go build -ldflags "$ldflags" -o "../$GW_BIN" ./cmd/agentgw/)
    record_binary_hash "$GW_BIN" agentgw agentgw/go.mod agentgw/go.sum
    link_legacy "$GW_BIN" "$AGENTGW_DIR/agentgw"
    echo "[build] agentgw (macOS): $(ls -lh "$GW_BIN" | awk '{print $5}')"
}

build_agentgw_linux_amd64() {
    if binary_up_to_date "$GW_LINUX_AMD64_BIN" agentgw agentgw/go.mod agentgw/go.sum; then
        echo "[build] agentgw (Linux amd64) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentgw for Linux amd64..."
    local ldflags="-s -w"
    if [[ -n "$BUILD_VERSION" ]]; then
        ldflags="$ldflags -X main.Version=$BUILD_VERSION"
    fi
    if [[ -n "$DOMAIN" ]]; then
        ldflags="$ldflags -X main.DefaultHubDomain=$HUB_DOMAIN -X main.DefaultAPIDomain=$API_DOMAIN -X main.DefaultDownloadDomain=$DOWNLOAD_DOMAIN"
    fi
    (cd "$AGENTGW_DIR" && GOOS=linux GOARCH=amd64 go build -ldflags "$ldflags" -o "../$GW_LINUX_AMD64_BIN" ./cmd/agentgw/)
    record_binary_hash "$GW_LINUX_AMD64_BIN" agentgw agentgw/go.mod agentgw/go.sum
    link_legacy "$GW_LINUX_AMD64_BIN" "$AGENTGW_DIR/agentgw-linux"
    echo "[build] agentgw (Linux amd64): $(ls -lh "$GW_LINUX_AMD64_BIN" | awk '{print $5}')"
}

build_agentgw_linux_arm64() {
    if binary_up_to_date "$GW_LINUX_ARM64_BIN" agentgw agentgw/go.mod agentgw/go.sum; then
        echo "[build] agentgw (Linux arm64) up-to-date, skipping"
        return 0
    fi
    echo "[build] Building agentgw for Linux arm64..."
    local ldflags=""
    if [[ -n "$BUILD_VERSION" ]]; then
        ldflags="-X main.Version=$BUILD_VERSION"
    fi
    if [[ -n "$DOMAIN" ]]; then
        ldflags="${ldflags:+$ldflags }-X main.DefaultHubDomain=$HUB_DOMAIN -X main.DefaultAPIDomain=$API_DOMAIN -X main.DefaultDownloadDomain=$DOWNLOAD_DOMAIN"
    fi
    if [[ -n "$ldflags" ]]; then
        (cd "$AGENTGW_DIR" && GOOS=linux GOARCH=arm64 go build -ldflags "$ldflags" -o "../$GW_LINUX_ARM64_BIN" ./cmd/agentgw/)
    else
        (cd "$AGENTGW_DIR" && GOOS=linux GOARCH=arm64 go build -o "../$GW_LINUX_ARM64_BIN" ./cmd/agentgw/)
    fi
    record_binary_hash "$GW_LINUX_ARM64_BIN" agentgw agentgw/go.mod agentgw/go.sum
    link_legacy "$GW_LINUX_ARM64_BIN" "$AGENTGW_DIR/agentgw-linux-arm64"
    echo "[build] agentgw (Linux arm64): $(ls -lh "$GW_LINUX_ARM64_BIN" | awk '{print $5}')"
}

build_agentgw_linux() {
    build_agentgw_linux_amd64
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
    local expected_hash actual_hash
    if [[ -f "$WEB_HASH_FILE" && -f "$WEB_STATIC_DIR/index.html" ]]; then
        expected_hash="$(<"$WEB_HASH_FILE")"
        actual_hash="$(compute_source_hash agentapp/lib agentapp/pubspec.yaml agentapp/pubspec.lock | shasum -a 256 | awk '{print $1}')"
        if [[ "$expected_hash" == "$actual_hash" ]]; then
            echo "[build] Web static up-to-date, skipping"
            return 0
        fi
    fi
    echo "[build] Building Flutter Web..."
    rm -rf "$AGENTAPP_DIR/build/web"
    (cd "$AGENTAPP_DIR" && flutter build web --release --no-tree-shake-icons --no-version-check)
    rm -rf "$WEB_STATIC_DIR"
    cp -r "$AGENTAPP_DIR/build/web" "$WEB_STATIC_DIR"
    link_legacy "$WEB_STATIC_DIR" "$AGENTGW_DIR/static"
    compute_source_hash agentapp/lib agentapp/pubspec.yaml agentapp/pubspec.lock | shasum -a 256 | awk '{print $1}' > "$WEB_HASH_FILE"
    echo "[build] Web static copied to $WEB_STATIC_DIR"
}

build_portal() {
    local portal_src="./portal"
    local portal_out="./out/portal"
    if [[ ! -d "$portal_src" ]]; then
        echo "[build] Portal source not found, skipping"
        return 0
    fi
    if [[ -d "$portal_out" && -f "$portal_out/index.html" ]]; then
        local src_hash out_hash
        src_hash="$(compute_source_hash "$portal_src" | shasum -a 256 | awk '{print $1}')"
        out_hash="$(compute_source_hash "$portal_out" | shasum -a 256 | awk '{print $1}')"
        if [[ "$src_hash" == "$out_hash" ]]; then
            echo "[build] Portal up-to-date, skipping"
            return 0
        fi
    fi
    echo "[build] Building download portal..."
    rm -rf "$portal_out"
    mkdir -p "$portal_out"
    cp -r "$portal_src/"* "$portal_out/"
    if [[ -n "$DOMAIN" ]]; then
        find "$portal_out" -name '*.html' -exec sed -i '' \
            -e "s/api\.ilovim\.xyz/api.${DOMAIN}/g" \
            -e "s/download\.ilovim\.xyz/download.${DOMAIN}/g" \
            -e "s/ilovim\.xyz/${DOMAIN}/g" {} \; 2>/dev/null || true
    fi
    echo "[build] Portal built at $portal_out"
}

build_go_all() {
    echo "[build] Building all Go binaries in parallel..."
    local mac_pid linux_amd64_pid linux_arm64_pid gw_mac_pid gw_linux_amd64_pid gw_linux_arm64_pid
    local mac_ok=0 linux_amd64_ok=0 linux_arm64_ok=0 gw_mac_ok=0 gw_linux_amd64_ok=0 gw_linux_arm64_ok=0
    build_agentd_mac & mac_pid=$!
    build_agentd_linux_amd64 & linux_amd64_pid=$!
    build_agentd_linux_arm64 & linux_arm64_pid=$!
    build_agentgw_mac & gw_mac_pid=$!
    build_agentgw_linux_amd64 & gw_linux_amd64_pid=$!
    build_agentgw_linux_arm64 & gw_linux_arm64_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_amd64_pid" && linux_amd64_ok=1 || true
    wait "$linux_arm64_pid" && linux_arm64_ok=1 || true
    wait "$gw_mac_pid" && gw_mac_ok=1 || true
    wait "$gw_linux_amd64_pid" && gw_linux_amd64_ok=1 || true
    wait "$gw_linux_arm64_pid" && gw_linux_arm64_ok=1 || true
    echo "[build] Go build results: agentd_mac=$mac_ok agentd_linux_amd64=$linux_amd64_ok agentd_linux_arm64=$linux_arm64_ok agentgw_mac=$gw_mac_ok agentgw_linux_amd64=$gw_linux_amd64_ok agentgw_linux_arm64=$gw_linux_arm64_ok"
    [[ $mac_ok -eq 1 && $linux_amd64_ok -eq 1 && $linux_arm64_ok -eq 1 && $gw_mac_ok -eq 1 && $gw_linux_amd64_ok -eq 1 && $gw_linux_arm64_ok -eq 1 ]]
}

build_all() {
    echo "[build] Building all components in parallel..."
    local mac_pid linux_amd64_pid linux_arm64_pid gw_mac_pid gw_linux_amd64_pid gw_linux_arm64_pid apk_pid ipa_pid web_pid portal_pid
    local mac_ok=0 linux_amd64_ok=0 linux_arm64_ok=0 gw_mac_ok=0 gw_linux_amd64_ok=0 gw_linux_arm64_ok=0 apk_ok=0 ipa_ok=0 web_ok=0 portal_ok=0
    build_agentd_mac & mac_pid=$!
    build_agentd_linux_amd64 & linux_amd64_pid=$!
    build_agentd_linux_arm64 & linux_arm64_pid=$!
    build_agentgw_mac & gw_mac_pid=$!
    build_agentgw_linux_amd64 & gw_linux_amd64_pid=$!
    build_agentgw_linux_arm64 & gw_linux_arm64_pid=$!
    build_apk & apk_pid=$!
    build_ipa & ipa_pid=$!
    build_web & web_pid=$!
    build_portal & portal_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_amd64_pid" && linux_amd64_ok=1 || true
    wait "$linux_arm64_pid" && linux_arm64_ok=1 || true
    wait "$gw_mac_pid" && gw_mac_ok=1 || true
    wait "$gw_linux_amd64_pid" && gw_linux_amd64_ok=1 || true
    wait "$gw_linux_arm64_pid" && gw_linux_arm64_ok=1 || true
    wait "$apk_pid" && apk_ok=1 || true
    wait "$ipa_pid" && ipa_ok=1 || true
    wait "$web_pid" && web_ok=1 || true
    wait "$portal_pid" && portal_ok=1 || true
    echo "[build] Build results: agentd_mac=$mac_ok agentd_linux_amd64=$linux_amd64_ok agentd_linux_arm64=$linux_arm64_ok agentgw_mac=$gw_mac_ok agentgw_linux_amd64=$gw_linux_amd64_ok agentgw_linux_arm64=$gw_linux_arm64_ok apk=$apk_ok ipa=$ipa_ok web=$web_ok portal=$portal_ok"
    [[ $mac_ok -eq 1 && $linux_amd64_ok -eq 1 && $linux_arm64_ok -eq 1 && $gw_mac_ok -eq 1 && $gw_linux_amd64_ok -eq 1 && $gw_linux_arm64_ok -eq 1 && $apk_ok -eq 1 ]]
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
  agentd-linux-arm64 Build agentd for Linux arm64
  agentgw          Build agentgw for current platform
  agentgw-linux    Build agentgw for Linux amd64
  agentgw-linux-arm64 Build agentgw for Linux arm64
  apk              Build Android APK
  ipa              Build iOS IPA
  web              Build Flutter Web
  portal           Build download portal static site
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
  out/darwin-arm64/agentd  — macOS daemon
  out/darwin-arm64/agentgw — macOS gateway
  out/linux-amd64/agentd   — Linux daemon (amd64)
  out/linux-amd64/agentgw  — Linux gateway (amd64)
  out/linux-arm64/agentd   — Linux daemon (arm64)
  out/linux-arm64/agentgw  — Linux gateway (arm64)
  out/android/agentapp.apk — Android APK (canonical)
  out/ios/agentapp.ipa     — iOS IPA (canonical)
  out/static/              — Web static assets

  Legacy (symlinked → canonical, do not edit):
  agentgw/agentapp.apk     — symlink → ../out/android/agentapp.apk
  agentgw/agentapp.ipa     — symlink → ../out/ios/agentapp.ipa
  agentgw/agentgw          — symlink → ../out/darwin-arm64/agentgw
  agentd/agentd            — symlink → ../out/darwin-arm64/agentd
  agentgw/static/          — symlink → ../out/static
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
        agentd-linux-arm64)
            build_agentd_linux_arm64
            ;;
        agentgw)
            build_agentgw_mac
            ;;
        agentgw-linux)
            build_agentgw_linux
            ;;
        agentgw-linux-arm64)
            build_agentgw_linux_arm64
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
        portal)
            build_portal
            ;;
        *)
            echo "Unknown target: $TARGET"
            echo "Run '$0 help' for usage."
            exit 1
            ;;
    esac
fi
