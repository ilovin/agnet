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
source scripts/deploy.sh

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

VERSION="${VERSION:-$(grep -m1 'agentgw v' agentgw/cmd/agentgw/main.go | sed -n 's/.*\(v[0-9][0-9.]*\).*/\1/p')}"
if [[ -z "$VERSION" ]]; then
  VERSION="v0.3.0"
fi

RELEASE_DIR="release/phone-talk-${VERSION}"

echo "=== Release ${VERSION} ==="
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/bin"

# ── Go binaries ─────────────────────────────────────────────────────
echo "[release] Building Go binaries in parallel..."

build_agentgw_macos() {
  local output="${RELEASE_DIR}/bin/agentgw-macos-arm64"
  if up_to_date "$output" agentgw -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \); then
    echo "[release] agentgw-macos-arm64 up-to-date, skipping build"
    return 0
  fi
  echo "[release] Building agentgw-macos-arm64..."
  (cd agentgw && CGO_ENABLED=0 go build -o "../${output}" ./cmd/agentgw/)
}
build_agentgw_linux() {
  local output="${RELEASE_DIR}/bin/agentgw-linux"
  if up_to_date "$output" agentgw -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \); then
    echo "[release] agentgw-linux up-to-date, skipping build"
    return 0
  fi
  echo "[release] Building agentgw-linux (amd64)..."
  (cd agentgw && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "../${output}" ./cmd/agentgw/)
}

build_linux & pid_linux=$!
build_agentgw_macos & pid_gw_mac=$!
build_agentgw_linux & pid_gw_linux=$!

ok_linux=0 ok_gw_mac=0 ok_gw_linux=0
wait "$pid_linux" && ok_linux=1 || true
wait "$pid_gw_mac" && ok_gw_mac=1 || true
wait "$pid_gw_linux" && ok_gw_linux=1 || true

if [[ $ok_linux -eq 1 && -f "$LINUX_BIN" ]]; then
  cp "$LINUX_BIN" "${RELEASE_DIR}/bin/agentd-linux"
  echo "[release] agentd-linux: $(ls -lh "${RELEASE_DIR}/bin/agentd-linux" | awk '{print $5}')"
else
  echo "[release] ERROR: agentd-linux build failed"
  exit 1
fi

if [[ $ok_gw_mac -eq 1 && -f "${RELEASE_DIR}/bin/agentgw-macos-arm64" ]]; then
  echo "[release] agentgw-macos-arm64: $(ls -lh "${RELEASE_DIR}/bin/agentgw-macos-arm64" | awk '{print $5}')"
else
  echo "[release] ERROR: agentgw-macos-arm64 build failed"
  exit 1
fi

if [[ $ok_gw_linux -eq 1 && -f "${RELEASE_DIR}/bin/agentgw-linux" ]]; then
  echo "[release] agentgw-linux: $(ls -lh "${RELEASE_DIR}/bin/agentgw-linux" | awk '{print $5}')"
else
  echo "[release] ERROR: agentgw-linux build failed"
  exit 1
fi

# ── Android APK ────────────────────────────────────────────────────
if ! $SKIP_APK; then
  if build_apk; then
    if [[ -f "$APK_OUTPUT" ]]; then
      cp "$APK_OUTPUT" "${RELEASE_DIR}/bin/agentapp.apk"
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
if ! $SKIP_IOS && command -v xcodebuild &>/dev/null; then
  if build_ipa; then
    if [[ -f "$IPA_OUTPUT" ]]; then
      cp "$IPA_OUTPUT" "${RELEASE_DIR}/bin/agentapp.ipa"
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
for script in scripts/deploy.sh scripts/deploy-remote.sh scripts/setup.sh scripts/build.sh; do
  if [[ -f "$script" ]]; then
    cp "$script" "${RELEASE_DIR}/$script"
    chmod +x "${RELEASE_DIR}/$script"
  fi
done

if [[ -d "agentgw/static" ]]; then
  cp -r agentgw/static "${RELEASE_DIR}/static"
fi

# ── README ─────────────────────────────────────────────────────────
cat > "${RELEASE_DIR}/README.md" <<EOF
# Agent Manager ${VERSION}

远程管理 AI Agent 的工具集。

## 快速安装

\`\`\`bash
tar xzf phone-talk-${VERSION}.tar.gz
cd phone-talk-${VERSION}
./install.sh
\`\`\`

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

```bash
# 重启本地服务（安装后常用）
./install.sh restart

# 查看运行状态
./install.sh status

# 停止本地服务
./install.sh stop

# 查看帮助
./install.sh --help
```

## 手动添加连接

URL:  ws://<你的IP>:8080/ws
Token: 安装时生成的 Token

## 文件说明

\`\`\`
bin/agentd-linux        # 远程服务器 Agent 守护进程
bin/agentgw-macos-arm64 # macOS 网关
bin/agentgw-linux       # Linux 网关
bin/agentapp.apk        # Android App
install.sh              # 一键安装脚本
scripts/                # 部署与辅助脚本（可被管理 UI 调用）
\`\`\`

## 架构

\`\`\`
手机 App ──WebSocket──► agentgw ──SSH tunnel──► agentd ──PTY──► Claude/OpenCode
\`\`\`
EOF

# ── Version file ───────────────────────────────────────────────────
cat > "${RELEASE_DIR}/VERSION" <<EOF
version: ${VERSION}
build_date: $(date -u +%Y-%m-%dT%H:%M:%SZ)
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
echo "    ./install.sh restart   # 重启服务"
echo "    ./install.sh status    # 查看状态"
echo "    ./install.sh stop      # 停止服务"
echo "    ./install.sh --help    # 查看帮助"
