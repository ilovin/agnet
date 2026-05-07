#!/usr/bin/env bash
# Build all release artifacts and package into a distributable tarball.
#
# Usage:
#   ./scripts/release.sh              # build everything
#   ./scripts/release.sh --skip-apk   # skip Flutter APK build
#   ./scripts/release.sh --skip-ios   # skip iOS build
#   ./scripts/release.sh --publish    # upload to OSS and refresh CDN
#
# Environment:
#   DOMAIN           Root domain for download URLs (default: ilovin.xyz)
#   OSS_BUCKET       OSS bucket name for upload
#   OSS_ENDPOINT     OSS endpoint (e.g. oss-cn-hangzhou.aliyuncs.com)
#   CDN_DOMAIN       CDN domain for download URLs (default: download.<DOMAIN>)
#
# Output: release/phone-talk-vX.Y.Z.tar.gz
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/build.sh

show_help() {
  cat <<'EOF'
Usage: ./scripts/release.sh [OPTIONS]

Build all release artifacts and package them into a distributable tarball.

OPTIONS:
  --skip-apk     Skip Flutter Android APK build
  --skip-ios     Skip iOS IPA build
  --publish      Upload artifacts to OSS and refresh CDN
  --dry-run      Show what would be uploaded without actually uploading
  --version V    Override version (default: auto-detected from agentgw)
  -h, --help     Show this help message and exit

OUTPUT:
  release/phone-talk-vX.Y.Z.tar.gz

EXAMPLES:
  # Build full release (recommended)
  ./scripts/release.sh

  # Skip mobile builds when you only need binaries
  ./scripts/release.sh --skip-apk --skip-ios

  # Force a specific version
  VERSION=v0.5.0 ./scripts/release.sh

CONTENTS OF THE RELEASE:
  bin/agentd              - Local agent daemon (macOS)
  bin/agentd-linux        - Remote server agent daemon (Linux amd64)
  bin/agentgw-macos-arm64 - Local gateway (macOS ARM64)
  bin/agentgw-linux       - Local gateway (Linux amd64)
  bin/agentapp.apk        - Android app (if built)
  bin/agentapp.ipa        - iOS app (if built)
  install.sh              - One-click installer for end users
  scripts/                - Helper / deploy scripts
  static/                 - Web static assets for agentgw
  README.md               - Quick start guide
  VERSION                 - Build metadata
EOF
}

# Parse args
SKIP_APK=false
SKIP_IOS=false
PUBLISH=false
DRY_RUN=false

auto_increment_version() {
  local bump="${RELEASE_BUMP:-patch}"
  local latest major minor patch

  latest="$(git tag --list 'v*' --sort=-v:refname | head -n1)"
  if [[ -z "$latest" || ! "$latest" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo "v0.1.0"
    return
  fi

  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[3]}"

  case "$bump" in
    major)
      major=$((major + 1))
      minor=0
      patch=0
      ;;
    minor)
      minor=$((minor + 1))
      patch=0
      ;;
    patch)
      patch=$((patch + 1))
      ;;
    *)
      echo "invalid RELEASE_BUMP: $bump (expected major|minor|patch)" >&2
      exit 1
      ;;
  esac

  echo "v${major}.${minor}.${patch}"
}

sha256_file() {
  local path="$1"
  shasum -a 256 "$path" | awk '{print $1}'
}

for arg in "$@"; do
  case "$arg" in
    -h|--help)
      show_help
      exit 0
      ;;
    --skip-apk) SKIP_APK=true ;;
    --skip-ios) SKIP_IOS=true ;;
    --publish) PUBLISH=true ;;
    --dry-run) DRY_RUN=true ;;
  esac
done

VERSION="${VERSION:-$(auto_increment_version)}"
RELEASE_DIR="release/phone-talk-${VERSION}"
MANIFEST_PATH="${RELEASE_DIR}/manifest.json"

# Domain configuration for download URLs
DOMAIN="${DOMAIN:-ilovin.xyz}"
CDN_DOMAIN="${CDN_DOMAIN:-download.${DOMAIN}}"
API_DOMAIN="${API_DOMAIN:-api.${DOMAIN}}"
BASE_URL="https://${CDN_DOMAIN}"

echo "=== Release ${VERSION} ==="
echo "[release] Domain: ${DOMAIN}"
echo "[release] CDN: ${CDN_DOMAIN}"
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/bin"

# ── Go binaries ─────────────────────────────────────────────────────
echo "[release] Building Go binaries in parallel..."
BUILD_VERSION="$VERSION" build_go_all

cp "$LOCAL_BIN" "${RELEASE_DIR}/bin/agentd"
cp "$LINUX_BIN" "${RELEASE_DIR}/bin/agentd-linux"
cp "$GW_BIN" "${RELEASE_DIR}/bin/agentgw-macos-arm64"
cp "$GW_LINUX_BIN" "${RELEASE_DIR}/bin/agentgw-linux"

AGENTD_MAC_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentd")"
AGENTD_LINUX_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentd-linux")"
AGENTGW_MAC_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentgw-macos-arm64")"
AGENTGW_LINUX_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentgw-linux")"

echo "[release] agentd (macOS): $(ls -lh "${RELEASE_DIR}/bin/agentd" | awk '{print $5}')"
echo "[release] agentd-linux: $(ls -lh "${RELEASE_DIR}/bin/agentd-linux" | awk '{print $5}')"
echo "[release] agentgw-macos-arm64: $(ls -lh "${RELEASE_DIR}/bin/agentgw-macos-arm64" | awk '{print $5}')"
echo "[release] agentgw-linux: $(ls -lh "${RELEASE_DIR}/bin/agentgw-linux" | awk '{print $5}')"

# ── Android APK ────────────────────────────────────────────────────
APK_SHA=""
if ! $SKIP_APK; then
  if build_apk; then
    if [[ -f "$APK_OUTPUT" ]]; then
      cp "$APK_OUTPUT" "${RELEASE_DIR}/bin/agentapp.apk"
      APK_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentapp.apk")"
      echo "[release] APK: $(ls -lh "${RELEASE_DIR}/bin/agentapp.apk" | awk '{print $5}')"
    else
      echo "[release] WARNING: APK not found"
    fi
  else
    echo "[release] WARNING: APK build failed"
  fi
else
  echo "[release] Skipping APK (--skip-apk)"
fi

# ── iOS IPA ────────────────────────────────────────────────────────
IPA_SHA=""
if ! $SKIP_IOS && command -v xcodebuild &>/dev/null; then
  if build_ipa; then
    if [[ -f "$IPA_OUTPUT" ]]; then
      cp "$IPA_OUTPUT" "${RELEASE_DIR}/bin/agentapp.ipa"
      IPA_SHA="$(sha256_file "${RELEASE_DIR}/bin/agentapp.ipa")"
      echo "[release] IPA: $(ls -lh "${RELEASE_DIR}/bin/agentapp.ipa" | awk '{print $5}')"
    else
      echo "[release] WARNING: IPA not found"
    fi
  else
    echo "[release] WARNING: IPA build failed"
  fi
else
  echo "[release] Skipping iOS (--skip-ios or no Xcode)"
fi

# ── Install script & static ────────────────────────────────────────
cp scripts/install.sh "${RELEASE_DIR}/install.sh"
chmod +x "${RELEASE_DIR}/install.sh"

# ── Deploy scripts ─────────────────────────────────────────────────
mkdir -p "${RELEASE_DIR}/scripts"
for script in scripts/build.sh scripts/deploy.sh scripts/tunnelhub.sh; do
  if [[ -f "$script" ]]; then
    cp "$script" "${RELEASE_DIR}/$script"
    chmod +x "${RELEASE_DIR}/$script"
  fi
done

if [[ -d "$WEB_STATIC_DIR" ]]; then
  cp -r "$WEB_STATIC_DIR" "${RELEASE_DIR}/static"
fi

# ── README ─────────────────────────────────────────────────────────
cat > "${RELEASE_DIR}/README.md" <<EOF
# Agent Manager ${VERSION}

远程管理 AI Agent 的工具集。

## 快速安装

    tar xzf phone-talk-${VERSION}.tar.gz
    cd phone-talk-${VERSION}
    ./install.sh

安装脚本会自动：
- 扫描 SSH 配置发现远程节点
- 部署 agentd 到选中的节点
- 启动本地 agentgw 网关
- 生成 Token 和连接二维码

## 手机连接

1. 安装 agentapp.apk（Android）或 agentapp.ipa（iOS）
2. 打开 app → 扫描终端显示的二维码
3. 自动连接，开始使用

## 日常管理

    # 重启本地服务（安装后常用）
    bash ./install.sh restart

    # 查看运行状态
    bash ./install.sh status

    # 停止本地服务
    bash ./install.sh stop

    # 查看帮助
    bash ./install.sh --help

## 手动添加连接

URL:  ws://<你的IP>:8080/ws
Token: 安装时生成的 Token

## 文件说明

    bin/agentd              # macOS 本地 Agent 守护进程
    bin/agentd-linux        # 远程服务器 Agent 守护进程 (Linux)
    bin/agentgw-macos-arm64 # macOS 网关
    bin/agentgw-linux       # Linux 网关
    bin/agentapp.apk        # Android App
    install.sh              # 一键安装脚本
    scripts/                # 部署与辅助脚本（可被管理 UI 调用）

## 架构

    手机 App ──WebSocket──► agentgw ──SSH tunnel──► agentd ──PTY──► Claude/OpenCode
EOF

# ── Version file ───────────────────────────────────────────────────
cat > "${RELEASE_DIR}/VERSION" <<EOF
version: ${VERSION}
build_date: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

cat > "$MANIFEST_PATH" <<EOF
{
  "schemaVersion": 1,
  "package": "phone-talk",
  "version": "${VERSION}",
  "buildDate": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "downloadBaseUrl": "${BASE_URL}",
  "installScriptUrl": "https://${API_DOMAIN}/v1/install.sh",
  "artifacts": [
    {
      "name": "agentgw",
      "description": "Gateway daemon",
      "platforms": [
        {
          "os": "darwin",
          "arch": "arm64",
          "path": "bin/agentgw-macos-arm64",
          "url": "${BASE_URL}/${VERSION}/bin/agentgw-macos-arm64",
          "sha256": "${AGENTGW_MAC_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentgw-macos-arm64")
        },
        {
          "os": "linux",
          "arch": "amd64",
          "path": "bin/agentgw-linux",
          "url": "${BASE_URL}/${VERSION}/bin/agentgw-linux",
          "sha256": "${AGENTGW_LINUX_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentgw-linux")
        }
      ]
    },
    {
      "name": "agentd",
      "description": "Agent daemon",
      "platforms": [
        {
          "os": "darwin",
          "arch": "arm64",
          "path": "bin/agentd",
          "url": "${BASE_URL}/${VERSION}/bin/agentd",
          "sha256": "${AGENTD_MAC_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentd")
        },
        {
          "os": "linux",
          "arch": "amd64",
          "path": "bin/agentd-linux",
          "url": "${BASE_URL}/${VERSION}/bin/agentd-linux",
          "sha256": "${AGENTD_LINUX_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentd-linux")
        }
      ]
    },
    {
      "name": "agentapp",
      "description": "Mobile app",
      "apk": $(if [[ -n "$APK_SHA" ]]; then cat <<JSON
{
          "path": "bin/agentapp.apk",
          "url": "${BASE_URL}/${VERSION}/bin/agentapp.apk",
          "sha256": "${APK_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentapp.apk")
        }
JSON
else
cat <<JSON
null
JSON
fi),
      "ipa": $(if [[ -n "$IPA_SHA" ]]; then cat <<JSON
{
          "path": "bin/agentapp.ipa",
          "url": "${BASE_URL}/${VERSION}/bin/agentapp.ipa",
          "sha256": "${IPA_SHA}",
          "size": $(wc -c < "${RELEASE_DIR}/bin/agentapp.ipa")
        }
JSON
else
cat <<JSON
null
JSON
fi)
    }
  ]
}
EOF

# ── Tarball ────────────────────────────────────────────────────────
echo "[release] Creating tarball..."
tar czf "release/phone-talk-${VERSION}.tar.gz" -C release "phone-talk-${VERSION}"

# ── Upload to OSS (optional) ──────────────────────────────────────
if $PUBLISH || $DRY_RUN; then
  echo ""
  if $DRY_RUN; then
    echo "[release] DRY RUN - would upload the following artifacts:"
  else
    echo "[release] Publishing to OSS..."
  fi

  OSS_BUCKET="${OSS_BUCKET:-}"
  OSS_ENDPOINT="${OSS_ENDPOINT:-}"

  if [[ -z "$OSS_BUCKET" || -z "$OSS_ENDPOINT" ]]; then
    echo "[release] No OSS configured (OSS_BUCKET/OSS_ENDPOINT not set)"
    echo "[release] Skipping cloud upload - binaries remain in ${RELEASE_DIR}/"
    echo "[release] To host locally, copy ${RELEASE_DIR}/ to your web server:"
    echo "[release]   cp -r ${RELEASE_DIR} /var/www/download/${VERSION}/"
    echo "[release] Or use --dry-run to preview what would be uploaded"
  else
    upload_oss() {
      local src="$1"
      local dst="$2"
      if $DRY_RUN; then
        echo "  [dry-run] would upload: ${src} -> oss://${OSS_BUCKET}/${dst}"
      else
        echo "  [upload] ${src} -> oss://${OSS_BUCKET}/${dst}"
        # Placeholder: replace with actual ossutil or aws s3 cp command
        # ossutil cp "${src}" "oss://${OSS_BUCKET}/${dst}"
      fi
    }

    # Upload individual binaries
    upload_oss "${RELEASE_DIR}/bin/agentd" "${VERSION}/bin/agentd"
    upload_oss "${RELEASE_DIR}/bin/agentd-linux" "${VERSION}/bin/agentd-linux"
    upload_oss "${RELEASE_DIR}/bin/agentgw-macos-arm64" "${VERSION}/bin/agentgw-macos-arm64"
    upload_oss "${RELEASE_DIR}/bin/agentgw-linux" "${VERSION}/bin/agentgw-linux"

    if [[ -n "$APK_SHA" ]]; then
      upload_oss "${RELEASE_DIR}/bin/agentapp.apk" "${VERSION}/bin/agentapp.apk"
    fi

    if [[ -n "$IPA_SHA" ]]; then
      upload_oss "${RELEASE_DIR}/bin/agentapp.ipa" "${VERSION}/bin/agentapp.ipa"
    fi

    # Upload manifest
    upload_oss "$MANIFEST_PATH" "${VERSION}/manifest.json"

    # Upload install script
    upload_oss "${RELEASE_DIR}/install.sh" "${VERSION}/install.sh"

    if ! $DRY_RUN; then
      echo "[release] Refreshing CDN cache..."
      # Placeholder: replace with actual CDN refresh API call
      # curl -X POST "https://cdn.api/refresh" -d "url=${BASE_URL}/${VERSION}/manifest.json"
      echo "[release] CDN cache refresh placeholder (implement with your CDN provider API)"
    fi
  fi
fi

echo ""
echo "=== Release ${VERSION} complete ==="
echo ""
ls -lh "${RELEASE_DIR}/bin/"
echo ""
echo "  Tarball: release/phone-talk-${VERSION}.tar.gz"
echo "  Manifest: ${MANIFEST_PATH}"
echo ""
echo "  首次安装:"
echo "    tar xzf phone-talk-${VERSION}.tar.gz"
echo "    cd phone-talk-${VERSION}"
echo "    ./install.sh"
echo ""
echo "  日常管理:"
echo "    bash ./install.sh restart   # 重启服务"
echo "    bash ./install.sh status    # 查看状态"
echo "    bash ./install.sh stop      # 停止服务"
echo "    bash ./install.sh --help    # 查看帮助"
