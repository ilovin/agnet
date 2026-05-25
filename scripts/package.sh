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

# Platform binaries
for pair in darwin-arm64 linux-amd64 linux-arm64; do
  os="${pair%-*}"
  arch="${pair#*-}"
  src="$OUT_DIR/$pair"
  dst="$DIST_DIR/platform/$pair"
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

# Build manifest JSON
manifest='{
  "version": "'$VERSION'",
  "platforms": ['

first=true
for pair in darwin-arm64 linux-amd64 linux-arm64; do
  if [[ -f "$OUT_DIR/$pair/agentd" || -f "$OUT_DIR/$pair/agentgw" ]]; then
    if [[ "$first" == true ]]; then
      first=false
    else
      manifest+=','
    fi
    manifest+='
    {
      "os": "'${pair%-*}'",
      "arch": "'${pair#*-}'",
      "binaries": {'
    bin_first=true
    for bin in agentd agentgw; do
      if [[ -f "$OUT_DIR/$pair/$bin" ]]; then
        if [[ "$bin_first" == true ]]; then
          bin_first=false
        else
          manifest+=','
        fi
        hash=$(sha256_file "$OUT_DIR/$pair/$bin")
        manifest+='
        "'$bin'": {
          "sha256": "'$hash'",
          "path": "platform/'$pair'/'$bin'"
        }'
      fi
    done
    manifest+='
      }
    }'
  fi
done

manifest+='
  ],
  "mobile": {'
mobile_first=true
if [[ -f "$OUT_DIR/android/agentapp.apk" ]]; then
  hash=$(sha256_file "$OUT_DIR/android/agentapp.apk")
  manifest+='
    "apk": {
      "sha256": "'$hash'",
      "path": "bin/agentapp.apk"
    }'
  mobile_first=false
fi
if [[ -f "$OUT_DIR/ios/agentapp.ipa" ]]; then
  if [[ "$mobile_first" == false ]]; then
    manifest+=','
  fi
  hash=$(sha256_file "$OUT_DIR/ios/agentapp.ipa")
  manifest+='
    "ipa": {
      "sha256": "'$hash'",
      "path": "bin/agentapp.ipa"
    }'
fi
manifest+='
  },
  "static": {
    "path": "static"
  },
  "install": {
    "script": "install.sh"
  }
}'

echo "$manifest" > "$DIST_DIR/manifest.json"

# ── Create tarball ─────────────────────────────────────────────────────

RELEASE_DIR="release/phone-talk-v${VERSION}"
mkdir -p "$RELEASE_DIR"
tarball="$RELEASE_DIR/phone-talk-v${VERSION}.tar.gz"

cd "$DIST_DIR"
tar -czf "$(cd .. && pwd)/$tarball" .
cd - >/dev/null

echo "[package] Packaged to $DIST_DIR/"
echo "[package] Tarball: $tarball"
echo "[package] Done."
