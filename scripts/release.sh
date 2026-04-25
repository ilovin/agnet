#!/usr/bin/env bash
# Build all release artifacts and package into a distributable tarball.
#
# Usage:
#   ./scripts/release.sh              # build everything
#   ./scripts/release.sh --skip-apk   # skip Flutter APK build
#   ./scripts/release.sh --skip-ios   # skip iOS build
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
  esac
done

VERSION="${VERSION:-$(auto_increment_version)}"
RELEASE_DIR="release/phone-talk-${VERSION}"
MANIFEST_PATH="${RELEASE_DIR}/manifest.json"

echo "=== Release ${VERSION} ==="
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
  "package": "phone-talk",
  "version": "${VERSION}",
  "buildDate": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "components": {
    "agentgw": [
      {
        "os": "darwin",
        "arch": "arm64",
        "path": "bin/agentgw-macos-arm64",
        "sha256": "${AGENTGW_MAC_SHA}",
        "size": $(wc -c < "${RELEASE_DIR}/bin/agentgw-macos-arm64")
      },
      {
        "os": "linux",
        "arch": "amd64",
        "path": "bin/agentgw-linux",
        "sha256": "${AGENTGW_LINUX_SHA}",
        "size": $(wc -c < "${RELEASE_DIR}/bin/agentgw-linux")
      }
    ],
    "agentd": [
      {
        "os": "darwin",
        "arch": "arm64",
        "path": "bin/agentd",
        "sha256": "${AGENTD_MAC_SHA}",
        "size": $(wc -c < "${RELEASE_DIR}/bin/agentd")
      },
      {
        "os": "linux",
        "arch": "amd64",
        "path": "bin/agentd-linux",
        "sha256": "${AGENTD_LINUX_SHA}",
        "size": $(wc -c < "${RELEASE_DIR}/bin/agentd-linux")
      }
    ],
    "agentapp": {
      "apk": $(if [[ -n "$APK_SHA" ]]; then cat <<JSON
{
        "path": "bin/agentapp.apk",
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
  }
}
EOF

# ── Tarball ────────────────────────────────────────────────────────
echo "[release] Creating tarball..."
tar czf "release/phone-talk-${VERSION}.tar.gz" -C release "phone-talk-${VERSION}"

echo ""
echo "=== Release ${VERSION} complete ==="
echo ""
ls -lh "${RELEASE_DIR}/bin/"
echo ""
echo "  Tarball: release/phone-talk-${VERSION}.tar.gz"
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
