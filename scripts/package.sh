#!/usr/bin/env bash
# Package built artifacts into standard distributable layout.
#
# Usage:
#   ./scripts/package.sh
#
# Input:  out/  (built by build.sh)
# Output: dist/ + dist/manifest.json + dist/phone-talk-vX.Y.Z.tar.gz
#
# Standard layout:
#   dist/platform/<os-arch>/{agentd,agentgw}
#   dist/bin/{agentapp.apk,agentapp.ipa}
#   dist/static/
#   dist/install.sh
#   dist/scripts/
#   dist/manifest.json

# shellcheck disable=SC2034  # os/arch loop variables used for naming context; kept for readability
set -euo pipefail
cd "$(dirname "$0")/.."

OUT_DIR="./out"
DIST_DIR="./dist"

# ── Auto-increment version ─────────────────────────────────────────────
auto_increment_version() {
  local latest="0.0.0"
  for d in release/phone-talk-v*/; do
    [[ -d "$d" ]] || continue
    local v
    v="$(basename "$d")"
    v="${v#phone-talk-v}"
    # Skip non-semver versions (e.g. vv0.99.0)
    if ! [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      continue
    fi
    if [[ "$(printf '%s\n' "$latest" "$v" | sort -V | tail -n1)" == "$v" ]]; then
      latest="$v"
    fi
  done

  local major minor patch
  major="${latest%%.*}"
  minor="${latest#*.}"
  minor="${minor%%.*}"
  patch="${latest##*.}"
  patch="${patch:-0}"
  patch=$((patch + 1))

  echo "${major}.${minor}.${patch}"
}

# ── Compute SHA256 ─────────────────────────────────────────────────────
sha256_file() {
  if command -v sha256sum &>/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum &>/dev/null; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "unknown"
  fi
}

# ── Main ───────────────────────────────────────────────────────────────

VERSION="${VERSION:-$(auto_increment_version)}"
# Normalize: strip leading 'v' if present, we'll add it consistently
VERSION="${VERSION#v}"

echo "[package] Packaging phone-talk v${VERSION}..."

# Clean and create dist/
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# Platform binaries — only create platform/<pair>/ if at least one binary exists,
# so tar never encounters an empty skeleton directory and fails traversal.
for pair in darwin-arm64 linux-amd64 linux-arm64; do
  os="${pair%-*}"
  arch="${pair#*-}"
  src="$OUT_DIR/$pair"
  dst="$DIST_DIR/platform/$pair"

  # Skip entirely if no binaries built for this platform
  if [[ ! -f "$src/agentd" && ! -f "$src/agentgw" ]]; then
    echo "[package] Skipping $pair (no binaries in $src)"
    continue
  fi

  mkdir -p "$dst"
  if [[ -f "$src/agentd" ]]; then
    cp "$src/agentd" "$dst/agentd"
    chmod +x "$dst/agentd"
  fi
  if [[ -f "$src/agentgw" ]]; then
    cp "$src/agentgw" "$dst/agentgw"
    chmod +x "$dst/agentgw"
  fi
done

# Mobile binaries
mkdir -p "$DIST_DIR/bin"
if [[ -f "$OUT_DIR/android/agentapp.apk" ]]; then
  cp "$OUT_DIR/android/agentapp.apk" "$DIST_DIR/bin/agentapp.apk"
fi
if [[ -f "$OUT_DIR/ios/agentapp.ipa" ]]; then
  cp "$OUT_DIR/ios/agentapp.ipa" "$DIST_DIR/bin/agentapp.ipa"
fi

# Static files
if [[ -d "$OUT_DIR/static" ]]; then
  cp -r "$OUT_DIR/static" "$DIST_DIR/static"
fi

# install.sh
if [[ -f "scripts/install.sh" ]]; then
  cp "scripts/install.sh" "$DIST_DIR/install.sh"
  chmod +x "$DIST_DIR/install.sh"
fi

# scripts/
mkdir -p "$DIST_DIR/scripts"
for script in build.sh deploy.sh package.sh install.sh bump-version.sh; do
  if [[ -f "scripts/$script" ]]; then
    cp "scripts/$script" "$DIST_DIR/scripts/$script"
  fi
done

# ── Generate manifest.json ─────────────────────────────────────────────
#
# Schema is intentionally hybrid: it carries BOTH
#   (a) old-style fields (schemaVersion / version with `v` prefix /
#       downloadBaseUrl / artifacts[]) consumed by the public download
#       portal (portal/index.html — uses
#       `manifest.artifacts.find(a => a.name === 'agentapp').apk.url`),
#   (b) new-style fields (platforms[] / mobile / static / install) used
#       by install.sh and other internal tooling.
# Both must be emitted; do not drop either side without coordinating
# with portal + install consumers.

DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-https://download.ilovin.xyz}"
VERSION_TAG="v${VERSION}"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if ! command -v jq &>/dev/null; then
  echo "[package] ERROR: jq is required to build manifest.json"
  exit 1
fi

# File size in bytes (portable across macOS/Linux).
file_size() {
  if stat -f%z "$1" &>/dev/null; then
    stat -f%z "$1"
  else
    stat -c%s "$1"
  fi
}

# Build platforms[] (new schema): one entry per os-arch with the
# binaries that exist in out/.
platforms_json='[]'
for pair in darwin-arm64 linux-amd64 linux-arm64; do
  os="${pair%-*}"
  arch="${pair#*-}"
  if [[ ! -f "$OUT_DIR/$pair/agentd" && ! -f "$OUT_DIR/$pair/agentgw" ]]; then
    continue
  fi
  binaries='{}'
  for bin in agentd agentgw; do
    if [[ -f "$OUT_DIR/$pair/$bin" ]]; then
      hash=$(sha256_file "$OUT_DIR/$pair/$bin")
      binaries=$(jq -n \
        --argjson cur "$binaries" \
        --arg name "$bin" \
        --arg sha "$hash" \
        --arg path "platform/$pair/$bin" \
        '$cur + {($name): {sha256: $sha, path: $path}}')
    fi
  done
  platforms_json=$(jq -n \
    --argjson cur "$platforms_json" \
    --arg os "$os" \
    --arg arch "$arch" \
    --argjson bins "$binaries" \
    '$cur + [{os: $os, arch: $arch, binaries: $bins}]')
done

# Build mobile{} (new schema).
mobile_json='{}'
if [[ -f "$OUT_DIR/android/agentapp.apk" ]]; then
  hash=$(sha256_file "$OUT_DIR/android/agentapp.apk")
  mobile_json=$(jq -n \
    --argjson cur "$mobile_json" \
    --arg sha "$hash" \
    --arg path "bin/agentapp.apk" \
    '$cur + {apk: {sha256: $sha, path: $path}}')
fi
if [[ -f "$OUT_DIR/ios/agentapp.ipa" ]]; then
  hash=$(sha256_file "$OUT_DIR/ios/agentapp.ipa")
  mobile_json=$(jq -n \
    --argjson cur "$mobile_json" \
    --arg sha "$hash" \
    --arg path "bin/agentapp.ipa" \
    '$cur + {ipa: {sha256: $sha, path: $path}}')
fi

# Build artifacts[] (old schema) — what the download portal expects.
# Each binary artifact (agentgw, agentd) has platforms[] with absolute
# urls; agentapp carries apk{url,sha256,size}.
artifacts_json='[]'
for bin in agentgw agentd; do
  description=""
  case "$bin" in
    agentgw) description="Gateway daemon" ;;
    agentd)  description="Agent daemon" ;;
  esac
  art_platforms='[]'
  for pair in darwin-arm64 linux-amd64 linux-arm64; do
    os="${pair%-*}"
    arch="${pair#*-}"
    if [[ ! -f "$OUT_DIR/$pair/$bin" ]]; then
      continue
    fi
    hash=$(sha256_file "$OUT_DIR/$pair/$bin")
    size=$(file_size "$OUT_DIR/$pair/$bin")
    rel_path="platform/$pair/$bin"
    url="${DOWNLOAD_BASE_URL}/${VERSION_TAG}/${rel_path}"
    art_platforms=$(jq -n \
      --argjson cur "$art_platforms" \
      --arg os "$os" \
      --arg arch "$arch" \
      --arg path "$rel_path" \
      --arg url "$url" \
      --arg sha "$hash" \
      --argjson size "$size" \
      '$cur + [{os: $os, arch: $arch, path: $path, url: $url, sha256: $sha, size: $size}]')
  done
  # Only emit the artifact if at least one platform was built.
  if [[ "$(echo "$art_platforms" | jq 'length')" -gt 0 ]]; then
    artifacts_json=$(jq -n \
      --argjson cur "$artifacts_json" \
      --arg name "$bin" \
      --arg desc "$description" \
      --argjson platforms "$art_platforms" \
      '$cur + [{name: $name, description: $desc, platforms: $platforms}]')
  fi
done

# agentapp artifact (apk + optional ipa).
if [[ -f "$OUT_DIR/android/agentapp.apk" || -f "$OUT_DIR/ios/agentapp.ipa" ]]; then
  apk_obj='null'
  ipa_obj='null'
  if [[ -f "$OUT_DIR/android/agentapp.apk" ]]; then
    hash=$(sha256_file "$OUT_DIR/android/agentapp.apk")
    size=$(file_size "$OUT_DIR/android/agentapp.apk")
    rel_path="bin/agentapp.apk"
    url="${DOWNLOAD_BASE_URL}/${VERSION_TAG}/${rel_path}"
    apk_obj=$(jq -n \
      --arg path "$rel_path" \
      --arg url "$url" \
      --arg sha "$hash" \
      --argjson size "$size" \
      '{path: $path, url: $url, sha256: $sha, size: $size}')
  fi
  if [[ -f "$OUT_DIR/ios/agentapp.ipa" ]]; then
    hash=$(sha256_file "$OUT_DIR/ios/agentapp.ipa")
    size=$(file_size "$OUT_DIR/ios/agentapp.ipa")
    rel_path="bin/agentapp.ipa"
    url="${DOWNLOAD_BASE_URL}/${VERSION_TAG}/${rel_path}"
    ipa_obj=$(jq -n \
      --arg path "$rel_path" \
      --arg url "$url" \
      --arg sha "$hash" \
      --argjson size "$size" \
      '{path: $path, url: $url, sha256: $sha, size: $size}')
  fi
  artifacts_json=$(jq -n \
    --argjson cur "$artifacts_json" \
    --argjson apk "$apk_obj" \
    --argjson ipa "$ipa_obj" \
    '$cur + [{name: "agentapp", description: "Mobile app", apk: $apk, ipa: $ipa}]')
fi

# Compose final manifest: old schema keys + new schema keys (additive).
manifest=$(jq -n \
  --arg version "$VERSION_TAG" \
  --arg buildDate "$BUILD_DATE" \
  --arg downloadBaseUrl "$DOWNLOAD_BASE_URL" \
  --arg installScriptUrl "${DOWNLOAD_BASE_URL%/}/${VERSION_TAG}/install.sh" \
  --argjson artifacts "$artifacts_json" \
  --argjson platforms "$platforms_json" \
  --argjson mobile "$mobile_json" \
  '{
    schemaVersion: 1,
    package: "phone-talk",
    version: $version,
    buildDate: $buildDate,
    downloadBaseUrl: $downloadBaseUrl,
    installScriptUrl: $installScriptUrl,
    artifacts: $artifacts,
    platforms: $platforms,
    mobile: $mobile,
    static: {path: "static"},
    install: {script: "install.sh"}
  }')

echo "$manifest" > "$DIST_DIR/manifest.json"

# ── Create tarball ─────────────────────────────────────────────────────

RELEASE_DIR="release/phone-talk-v${VERSION}"
mkdir -p "$RELEASE_DIR"
tarball="$RELEASE_DIR/phone-talk-v${VERSION}.tar.gz"

cd "$DIST_DIR"
tar -czf "$(cd .. && pwd)/$tarball" .
cd - >/dev/null

# Also drop a standalone manifest.json next to the tarball so external
# consumers (portal, release-tools) can read it without untarring.
cp "$DIST_DIR/manifest.json" "$RELEASE_DIR/manifest.json"

echo "[package] Packaged to $DIST_DIR/"
echo "[package] Tarball: $tarball"
echo "[package] Manifest: $RELEASE_DIR/manifest.json"
echo "[package] Done."
