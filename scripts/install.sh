#!/usr/bin/env bash
# phone-talk one-click installer
#
# Scans SSH config for remote nodes, deploys agentd, starts agentgw,
# generates token, opens browser dashboard, and displays QR code.
#
# Usage:
#   ./install.sh                    # interactive install
#   ./install.sh --token mytoken    # pre-set token
#   ./install.sh --local-only       # only setup agentgw, no remote
#   ./install.sh --no-browser       # don't open browser
#   ./install.sh restart            # restart local agentgw + agentd
#   ./install.sh stop               # stop local agentgw + agentd
#   ./install.sh status             # show local service status
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
INSTALL_DIR="$HOME/.agentgw"
RUNTIME_ENV_FILE="$INSTALL_DIR/runtime.env"
AGENTD_REMOTE_DIR="~/bin"
AGENTD_PORT=7373
GW_PORT=7374
DEFAULT_HUB_URL="wss://ilovin.xyz:8443"
TOKEN=""
LOCAL_ONLY=false
OPEN_BROWSER=true
HUB_URL="$DEFAULT_HUB_URL"
TUNNEL_URL=""
APP_URL=""
REGISTERED_USER=""
REGISTERED_TOKEN=""

show_help() {
  cat <<'EOF'
Usage: ./install.sh [OPTIONS] | [COMMAND]

COMMANDS:
  install   (default) Install / start agentgw and optionally deploy remote agentd
  restart   Restart local agentgw and local agentd
  stop      Stop local agentgw and local agentd
  status    Check whether local agentgw and agentd are running
  help      Show this help message

OPTIONS (for install):
  --token TOKEN      Pre-set authentication token
  --hub URL          Tunnelhub base URL (e.g. wss://domain:8443)
  --tunnel-url URL   Full tunnel register URL (overrides --hub)
  --app-url URL      App-facing remote URL for QR/websocket
  --local-only       Only setup local agentgw, skip remote deployment
  --no-browser       Don't open browser after installation
  -h, --help         Show this help message and exit

ENVIRONMENT:
  AGENTGW_HUB           Optional tunnelhub base URL (default: wss://ilovin.xyz:8443)
  AGENTGW_TUNNEL_URL    Optional full tunnel register URL (overrides AGENTGW_HUB)
  AGENTGW_APP_URL       Optional app-facing remote URL (e.g. wss://hub.example.com:8443/ws)
  AGENTGW_TUNNEL_TOKEN  Optional tunnel auth token

EXAMPLES:
  ./install.sh
  ./install.sh --token mytoken --local-only
  ./install.sh --no-browser
  ./install.sh restart
  ./install.sh stop
  ./install.sh status
  ./install.sh --help
EOF
}

# Sub-command handling
SUBCMD="${1:-}"
case "$SUBCMD" in
  restart|stop|status|help|--help|-h)
    # Subcommands bypass normal flag parsing
    ;;
  *)
    SUBCMD=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --token=*) TOKEN="${1#--token=}" ;;
        --token)   shift; TOKEN="${1:-}" ;;
        --hub=*) HUB_URL="${1#--hub=}" ;;
        --hub)   shift; HUB_URL="${1:-}" ;;
        --tunnel-url=*) TUNNEL_URL="${1#--tunnel-url=}" ;;
        --tunnel-url)   shift; TUNNEL_URL="${1:-}" ;;
        --app-url=*) APP_URL="${1#--app-url=}" ;;
        --app-url)   shift; APP_URL="${1:-}" ;;
        --local-only) LOCAL_ONLY=true ;;
        --no-browser) OPEN_BROWSER=false ;;
        --help|-h)
          show_help
          exit 0
          ;;
      esac
      shift
    done
    ;;
esac

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; RED='\033[0;31m'; NC='\033[0m'
info()  { echo -e "${GREEN}✅ $*${NC}"; }
warn()  { echo -e "${YELLOW}⚠️  $*${NC}"; }
step()  { echo -e "${CYAN}⚙️  $*${NC}"; }
err()   { echo -e "${RED}❌ $*${NC}"; }

# ── Service helpers ────────────────────────────────────────────────
local_agentd_pid() {
  lsof -nP -tiTCP:"$AGENTD_PORT" -sTCP:LISTEN 2>/dev/null || true
}

gateway_pid() {
  pgrep -f "agentgw start" 2>/dev/null || true
}

stop_services() {
  local pid
  pid="$(gateway_pid)"
  if [[ -n "$pid" ]]; then
    step "停止 agentgw (PID $pid)..."
    kill "$pid" 2>/dev/null || true
    sleep 1
  fi

  pid="$(local_agentd_pid)"
  if [[ -n "$pid" ]]; then
    step "停止 agentd (PID $pid)..."
    kill "$pid" 2>/dev/null || true
    sleep 1
    pid="$(local_agentd_pid)"
    if [[ -n "$pid" ]]; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
}

# Detect agentgw binary for current platform
detect_binary() {
  case "$(uname -s):$(uname -m)" in
    Darwin:*)  echo "$BIN_DIR/agentgw-macos-arm64" ;;
    Linux:x86_64)  echo "$BIN_DIR/agentgw-linux" ;;
    Linux:aarch64) echo "$BIN_DIR/agentgw-linux" ;;
    *) echo "" ;;
  esac
}

# Detect local agentd binary (in agentd/ dir, sibling of scripts/)
detect_local_agentd() {
  local agentd_dir="$SCRIPT_DIR/../agentd"
  case "$(uname -s):$(uname -m)" in
    Darwin:arm64|Darwin:x86_64)
      if [[ -f "$agentd_dir/agentd-darwin" ]]; then
        echo "$agentd_dir/agentd-darwin"
      elif [[ -f "$agentd_dir/agentd" ]]; then
        echo "$agentd_dir/agentd"
      fi
      ;;
    Linux:x86_64)
      if [[ -f "$agentd_dir/agentd-linux-amd64" ]]; then
        echo "$agentd_dir/agentd-linux-amd64"
      elif [[ -f "$agentd_dir/agentd-linux" ]]; then
        echo "$agentd_dir/agentd-linux"
      elif [[ -f "$agentd_dir/agentd" ]]; then
        echo "$agentd_dir/agentd"
      fi
      ;;
    Linux:aarch64)
      if [[ -f "$agentd_dir/agentd-linux" ]]; then
        echo "$agentd_dir/agentd-linux"
      elif [[ -f "$agentd_dir/agentd" ]]; then
        echo "$agentd_dir/agentd"
      fi
      ;;
    *)
      # Fallback: any agentd binary
      if [[ -f "$agentd_dir/agentd" ]]; then
        echo "$agentd_dir/agentd"
      fi
      ;;
  esac
}

restart_services() {
  stop_services

  local gw_bin
  gw_bin="$INSTALL_DIR/agentgw"
  if [[ ! -f "$gw_bin" ]]; then
    gw_bin="$(detect_binary)"
    if [[ -z "$gw_bin" || ! -f "$gw_bin" ]]; then
      err "找不到 agentgw 二进制文件"
      exit 1
    fi
    cp "$gw_bin" "$INSTALL_DIR/agentgw"
    chmod +x "$INSTALL_DIR/agentgw"
  fi

  local local_bin
  local_bin="$(detect_local_agentd)"
  if [[ -z "$local_bin" || ! -f "$local_bin" ]]; then
    warn "未找到本地 agentd 二进制文件，跳过 agentd 启动"
    warn "预期位置: $SCRIPT_DIR/../agentd/agentd[-darwin|-linux|-linux-amd64]"
  else
    step "启动本地 agentd (${local_bin##*/})..."
    nohup "$local_bin" start > /tmp/agentd-local.log 2>&1 &
    sleep 2
    if lsof -nP -iTCP:"$AGENTD_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      info "agentd 已启动 (PID $(local_agentd_pid))"
    else
      warn "agentd 启动失败，查看日志: tail -f /tmp/agentd-local.log"
    fi
  fi

  step "启动 agentgw..."
  local -a env_args=()
  if [[ -f "$RUNTIME_ENV_FILE" ]]; then
    env_args+=(env)
    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      env_args+=("$line")
    done < "$RUNTIME_ENV_FILE"
  fi
  nohup "${env_args[@]}" "$gw_bin" start --qr > /tmp/agentgw.log 2>&1 &
  sleep 2
  local gwpid
  gwpid="$(gateway_pid)"
  if [[ -n "$gwpid" ]]; then
    info "agentgw 已启动 (PID $gwpid, http://localhost:$GW_PORT)"
  else
    err "agentgw 启动失败:"
    tail -5 /tmp/agentgw.log
    exit 1
  fi
}

show_status() {
  local apid gwpid
  apid="$(local_agentd_pid)"
  gwpid="$(gateway_pid)"
  echo ""
  echo -e "${CYAN}服务状态:${NC}"
  if [[ -n "$apid" ]]; then
    echo -e "  agentd  ${GREEN}运行中${NC} (PID $apid, port $AGENTD_PORT)"
  else
    echo -e "  agentd  ${YELLOW}未运行${NC}"
  fi
  if [[ -n "$gwpid" ]]; then
    echo -e "  agentgw ${GREEN}运行中${NC} (PID $gwpid, port $GW_PORT)"
  else
    echo -e "  agentgw ${YELLOW}未运行${NC}"
  fi
  echo ""
  echo -e "${CYAN}日志位置:${NC}"
  echo "  agentd:  tail -f /tmp/agentd-local.log"
  echo "  agentgw: tail -f /tmp/agentgw.log"
  echo ""
}

# ── Early subcommands ──────────────────────────────────────────────
case "$SUBCMD" in
  restart)
    restart_services
    exit 0
    ;;
  stop)
    stop_services
    info "服务已停止"
    exit 0
    ;;
  status)
    show_status
    exit 0
    ;;
  help|--help|-h)
    show_help
    exit 0
    ;;
esac

# ═══════════════════════════════════════════════════════════════════
# Normal install flow below
# ═══════════════════════════════════════════════════════════════════

# ── 0. Read existing config (port etc.) ────────────────────────────
mkdir -p "$INSTALL_DIR"
if [[ -f "$INSTALL_DIR/config.yaml" ]]; then
  EXISTING_PORT="$(grep '^port:' "$INSTALL_DIR/config.yaml" 2>/dev/null | head -1 | sed 's/.*port:[[:space:]]*//')"
  if [[ -n "$EXISTING_PORT" ]]; then
    GW_PORT="$EXISTING_PORT"
  fi
fi

# ── Scan SSH config ────────────────────────────────────────────────
scan_ssh_nodes() {
  local cfg="$HOME/.ssh/config"
  [[ ! -f "$cfg" ]] && return
  local host="" hostname="" port="22"
  while IFS= read -r line; do
    line="$(echo "$line" | sed 's/^[[:space:]]*//')"
    case "$line" in
      Host\ *)
        [[ -n "$host" && -n "$hostname" ]] && echo "${host}|${hostname}|${port}"
        host="$(echo "$line" | sed 's/^Host[[:space:]]*//')"
        hostname=""; port="22"
        ;;
      HostName\ *) hostname="$(echo "$line" | sed 's/^HostName[[:space:]]*//')" ;;
      Port\ *)      port="$(echo "$line" | sed 's/^Port[[:space:]]*//')" ;;
    esac
  done < "$cfg"
  [[ -n "$host" && -n "$hostname" ]] && echo "${host}|${hostname}|${port}"
}

# ── Deploy agentd to remote ────────────────────────────────────────
deploy_agentd() {
  local target="$1" port="${2:-22}"
  step "部署 agentd → ${target}..."

  if ! ssh -o ConnectTimeout=5 -o BatchMode=yes -p "$port" "$target" "echo ok" &>/dev/null; then
    err "SSH 连接失败: ${target}（需要免密登录）"
    return 1
  fi

  ssh -o ConnectTimeout=5 -p "$port" "$target" "mkdir -p ~/bin" || return 1
  scp -o ConnectTimeout=5 -P "$port" "$BIN_DIR/agentd-linux" "${target}:~/bin/agentd-new" || return 1

  ssh -o ConnectTimeout=5 -p "$port" "$target" \
    "pkill -f 'agentd start' 2>/dev/null; sleep 1" || true

  ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 -p "$port" "$target" \
    "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && \
     bash -c 'nohup ~/bin/agentd start > /tmp/agentd.log 2>&1 </dev/null & disown; exit 0'" || true
  sleep 3

  if ssh -o ConnectTimeout=5 -p "$port" "$target" "pgrep -f 'agentd start'" &>/dev/null; then
    info "agentd 已启动: ${target}"
  else
    warn "agentd 可能未启动，检查: ssh ${target} tail /tmp/agentd.log"
  fi
}

generate_token() {
  [[ -n "$TOKEN" ]] && { echo "$TOKEN"; return; }
  openssl rand -hex 8 2>/dev/null || python3 -c "import secrets; print(secrets.token_hex(8))" 2>/dev/null || echo "agent-$(date +%s)"
}

extract_json_field() {
  local path="$1" field="$2"
  python3 - "$path" "$field" <<'PY'
import json, sys
path, field = sys.argv[1], sys.argv[2]
try:
    with open(path, 'r', encoding='utf-8') as f:
        data = json.load(f)
    value = data.get(field, "")
    if value is None:
        value = ""
    print(value)
except Exception:
    pass
PY
}

build_remote_ws_url() {
  local register_url="$1" app_url="$2"
  [[ -z "$register_url" ]] && return
  python3 - "$register_url" "$app_url" <<'PY'
from urllib.parse import urlparse, parse_qs
import sys
register_url, app_url = sys.argv[1], sys.argv[2]
base = app_url or register_url
u = urlparse(base)
if not u.scheme or not u.netloc:
    sys.exit(0)
user_id = parse_qs(urlparse(register_url).query).get('userId', ['default'])[0] or 'default'
scheme = 'wss' if u.scheme == 'wss' else 'ws'
print(f"{scheme}://{u.netloc}/ws/{user_id}")
PY
}

# Save userId + token to local_auth.json
save_local_auth() {
  local user="$1" tok="$2" path="$INSTALL_DIR/local_auth.json"
  python3 - "$user" "$tok" "$path" <<'PY'
import json, sys, os
user, tok, path = sys.argv[1], sys.argv[2], sys.argv[3]
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, 'w') as f:
    json.dump({"userId": user, "token": tok}, f)
PY
}

register_with_hub() {
  local gw_bin="$1"
  local hub_url="$2"
  [[ -z "$hub_url" ]] && return 0
  step "注册 agentgw 到 tunnelhub..."
  if "$gw_bin" login --hub "$hub_url"; then
    return 0
  fi
  return 1
}

# ── QR code generation ─────────────────────────────────────────────
generate_qr() {
  local data="$1"
  # Try qrencode first
  if command -v qrencode &>/dev/null; then
    qrencode -t ANSIUTF8 "$data" 2>/dev/null && return
  fi
  # Try python qrcode
  if python3 -c "import qrcode" 2>/dev/null; then
    python3 -c "
import qrcode, sys
qr = qrcode.QRCode(box_size=1, border=1)
qr.add_data(sys.argv[1])
qr.make(fit=True)
qr.print_ascii(tty=sys.stdout.isatty())
" "$data" 2>/dev/null && return
  fi
  # Auto-install qrencode via brew
  step "安装 qrencode 以生成二维码..."
  if command -v brew &>/dev/null; then
    brew install qrencode 2>/dev/null && qrencode -t ANSIUTF8 "$data" && return
  fi
  # Fallback: pip install qrcode
  step "通过 pip 安装 qrcode..."
  pip3 install --user qrcode 2>/dev/null && python3 -c "
import qrcode, sys
qr = qrcode.QRCode(box_size=1, border=1)
qr.add_data(sys.argv[1])
qr.make(fit=True)
qr.print_ascii(tty=sys.stdout.isatty())
" "$data" 2>/dev/null && return
  # Last resort: show URL + token as text
  echo "  (无法生成二维码，请手动输入以下信息连接)"
}

# ═══════════════════════════════════════════════════════════════════
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   Agent Manager — One-Click Installer   ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── 1. Detect binary ──────────────────────────────────────────────
GW_BIN="$(detect_binary)"
if [[ -z "$GW_BIN" || ! -f "$GW_BIN" ]]; then
  err "找不到适合当前平台的 agentgw"
  echo "  平台: $(uname -s)/$(uname -m)"
  exit 1
fi
info "平台: $(uname -s)/$(uname -m)"

# ── 2. Scan & deploy remote nodes ─────────────────────────────────
NODES=() NODE_HOSTS=() NODE_PORTS=()
SELECTION=""

if ! $LOCAL_ONLY; then
  step "扫描 SSH 配置..."
  FOUND="$(scan_ssh_nodes)"

  if [[ -n "$FOUND" ]]; then
    echo ""
    echo -e "${CYAN}📱 发现远程节点:${NC}"
    IDX=1
    while IFS='|' read -r alias host port; do
      [[ "$alias" == "*" || "$host" == "127.0.0.1" || "$host" == "localhost" ]] && continue
      echo "  [$IDX] ${alias} (${host}:${port})"
      NODES+=("$alias"); NODE_HOSTS+=("$host"); NODE_PORTS+=("$port")
      IDX=$((IDX + 1))
    done <<< "$FOUND"
    echo ""
    echo -n "选择要部署的节点（逗号分隔, 0=跳过）: "
    read -r SELECTION
  else
    warn "未在 ~/.ssh/config 发现远程节点"
  fi

  if [[ -n "$SELECTION" && "$SELECTION" != "0" ]]; then
    for idx in $(echo "$SELECTION" | tr ',' ' '); do
      idx="$(echo "$idx" | tr -d ' ')"
      [[ "$idx" -ge 1 && "$idx" -le "${#NODES[@]}" ]] && deploy_agentd "${NODES[$((idx-1))]}" "${NODE_PORTS[$((idx-1))]}"
    done
  fi
fi

# ── 3. Resolve tunnel settings / registration ──────────────────────
if [[ -z "$HUB_URL" ]]; then
  HUB_URL="${AGENTGW_HUB:-$DEFAULT_HUB_URL}"
fi
if [[ -z "$TUNNEL_URL" ]]; then
  TUNNEL_URL="${AGENTGW_TUNNEL_URL:-}"
fi
if [[ -z "$APP_URL" ]]; then
  APP_URL="${AGENTGW_APP_URL:-}"
fi

# Interactive mode selection
if [[ -z "$TUNNEL_URL" && -z "$APP_URL" ]]; then
  echo ""
  echo -e "${CYAN}网络模式选择:${NC}"
  echo "  [1] 隧道模式 — 通过 tunnelhub 远程访问（需要注册）"
  echo "  [2] 本地模式 — 仅在局域网内访问，不连接 tunnelhub"
  echo -n "选择 (默认 1): "
  read -r MODE_CHOICE

  if [[ "${MODE_CHOICE:-1}" == "2" ]]; then
    info "使用本地模式"
    HUB_URL=""
    TUNNEL_URL=""
    APP_URL=""
  fi
fi

# Tunnel mode: handle credentials
if [[ -n "$HUB_URL" ]]; then
  local_auth_path="$INSTALL_DIR/local_auth.json"
  oauth_path="$INSTALL_DIR/oauth.json"
  CRED_USERS=()
  CRED_TOKENS=()
  CRED_SOURCES=()

  if [[ -f "$local_auth_path" ]]; then
    u="$(extract_json_field "$local_auth_path" userId)"
    t="$(extract_json_field "$local_auth_path" token)"
    if [[ -n "$u" && -n "$t" ]]; then
      CRED_USERS+=("$u")
      CRED_TOKENS+=("$t")
      CRED_SOURCES+=("local_auth.json")
    fi
  fi

  if [[ -f "$oauth_path" ]]; then
    u="$(extract_json_field "$oauth_path" userId)"
    t="$(extract_json_field "$oauth_path" accessToken)"
    if [[ -n "$u" && -n "$t" ]]; then
      CRED_USERS+=("$u")
      CRED_TOKENS+=("$t")
      CRED_SOURCES+=("oauth.json")
    fi
  fi

  if [[ ${#CRED_USERS[@]} -gt 0 ]]; then
    echo ""
    echo -e "${CYAN}检测到本地已保存的凭据:${NC}"
    for i in "${!CRED_USERS[@]}"; do
      idx=$((i + 1))
      echo "  [$idx] User: ${CRED_USERS[i]} (来源: ${CRED_SOURCES[i]})"
    done
    n=${#CRED_USERS[@]}
    echo "  [$((n+1))] 重新注册（覆盖现有，生成新 token）"
    echo "  [$((n+2))] 本地模式（仅局域网，不连接 tunnelhub）"
    echo -n "请选择 (默认 1): "
    read -r CHOICE
    CHOICE="${CHOICE:-1}"

    if [[ "$CHOICE" -ge 1 && "$CHOICE" -le "$n" ]]; then
      idx=$((CHOICE - 1))
      REGISTERED_USER="${CRED_USERS[idx]}"
      REGISTERED_TOKEN="${CRED_TOKENS[idx]}"
      info "使用本地凭据: $REGISTERED_USER"
    elif [[ "$CHOICE" == "$((n+1))" ]]; then
      if register_with_hub "$GW_BIN" "$HUB_URL"; then
        REGISTERED_USER="$(extract_json_field "$local_auth_path" userId)"
        REGISTERED_TOKEN="$(extract_json_field "$local_auth_path" token)"
      else
        echo ""
        warn "重新注册失败（userId 可能已在 hub 上存在）"
        echo -n "输入已有 token 恢复凭据（或按 Enter 切换到本地模式）: "
        read -r RECOVERY_TOKEN
        if [[ -n "$RECOVERY_TOKEN" ]]; then
          echo -n "输入 userId: "
          read -r RECOVERY_USER
          if [[ -n "$RECOVERY_USER" ]]; then
            save_local_auth "$RECOVERY_USER" "$RECOVERY_TOKEN"
            REGISTERED_USER="$RECOVERY_USER"
            REGISTERED_TOKEN="$RECOVERY_TOKEN"
            info "已恢复凭据: $RECOVERY_USER"
          fi
        else
          HUB_URL=""
          TUNNEL_URL=""
          APP_URL=""
          warn "切换到本地模式"
        fi
      fi
    else
      HUB_URL=""
      TUNNEL_URL=""
      APP_URL=""
      info "切换到本地模式"
    fi
  else
    if register_with_hub "$GW_BIN" "$HUB_URL"; then
      REGISTERED_USER="$(extract_json_field "$local_auth_path" userId)"
      REGISTERED_TOKEN="$(extract_json_field "$local_auth_path" token)"
    else
      warn "注册失败（userId 可能已在 hub 上存在）"
      echo -n "输入已有 token 恢复凭据（或按 Enter 切换到本地模式）: "
      read -r RECOVERY_TOKEN
      if [[ -n "$RECOVERY_TOKEN" ]]; then
        echo -n "输入 userId: "
        read -r RECOVERY_USER
        if [[ -n "$RECOVERY_USER" ]]; then
          save_local_auth "$RECOVERY_USER" "$RECOVERY_TOKEN"
          REGISTERED_USER="$RECOVERY_USER"
          REGISTERED_TOKEN="$RECOVERY_TOKEN"
          info "已恢复凭据: $RECOVERY_USER"
        fi
      else
        HUB_URL=""
        TUNNEL_URL=""
        APP_URL=""
        warn "切换到本地模式"
      fi
    fi
  fi

  if [[ -z "$TUNNEL_URL" && -n "$HUB_URL" && -n "$REGISTERED_USER" ]]; then
    TUNNEL_URL="${HUB_URL%/}/tunnel/register?userId=${REGISTERED_USER}"
  fi
  if [[ -z "$APP_URL" && -n "$HUB_URL" ]]; then
    APP_URL="${HUB_URL%/}/ws"
  fi
  if [[ -z "$TOKEN" && -n "$REGISTERED_TOKEN" ]]; then
    TOKEN="$REGISTERED_TOKEN"
  fi
fi

# ── 4. Token ──────────────────────────────────────────────────────
# Reuse existing token if config already exists
if [[ -z "$TOKEN" && -f "$INSTALL_DIR/config.yaml" ]]; then
  EXISTING_TOKEN="$(grep 'token:' "$INSTALL_DIR/config.yaml" 2>/dev/null | head -1 | sed 's/.*token:[[:space:]]*//' | tr -d '"' | tr -d "'")"
  if [[ -n "$EXISTING_TOKEN" ]]; then
    TOKEN="$EXISTING_TOKEN"
    info "沿用已有 Token: ${TOKEN}"
  fi
fi
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(generate_token)"
  info "生成新 Token: ${TOKEN}"
fi

# ── 5. Config ─────────────────────────────────────────────────────
# Only write config.yaml if it doesn't exist or token changed
if [[ ! -f "$INSTALL_DIR/config.yaml" ]]; then
  cat > "$INSTALL_DIR/config.yaml" <<EOF
token: "${TOKEN}"
port: ${GW_PORT}
nodes:
  - name: "local"
    host: "localhost"
    agentd_port: ${AGENTD_PORT}
EOF
  info "配置: ${INSTALL_DIR}/config.yaml"
else
  # Update token if changed, preserve rest
  EXISTING_TOKEN_LINE="$(grep '^token:' "$INSTALL_DIR/config.yaml" | head -1)"
  NEW_TOKEN_LINE="token: \"${TOKEN}\""
  if [[ "$EXISTING_TOKEN_LINE" != "$NEW_TOKEN_LINE" ]]; then
    sed -i.bak "s|^token:.*|${NEW_TOKEN_LINE}|" "$INSTALL_DIR/config.yaml"
    rm -f "$INSTALL_DIR/config.yaml.bak"
  fi
  info "配置已存在，已更新: ${INSTALL_DIR}/config.yaml"
fi

# ── 6. Persist runtime env ─────────────────────────────────────────
# Embed tunnel token directly into the URL so URL + token travel as one unit
if [[ -n "$TUNNEL_URL" && -n "$REGISTERED_TOKEN" ]]; then
  if [[ "$TUNNEL_URL" != *"token="* ]]; then
    sep="?"
    [[ "$TUNNEL_URL" == *"?"* ]] && sep="&"
    TUNNEL_URL="${TUNNEL_URL}${sep}token=${REGISTERED_TOKEN}"
  fi
fi

cat > "$RUNTIME_ENV_FILE" <<EOF
AGENTGW_HUB=${HUB_URL}
AGENTGW_TUNNEL_URL=${TUNNEL_URL}
AGENTGW_APP_URL=${APP_URL}
EOF
chmod 600 "$RUNTIME_ENV_FILE"

# ── 7. Start agentgw ──────────────────────────────────────────────
cp "$GW_BIN" "$INSTALL_DIR/agentgw"
chmod +x "$INSTALL_DIR/agentgw"

pkill -f "agentgw start" 2>/dev/null || true
sleep 1

# Copy static files if available
for static_src in "$SCRIPT_DIR/static" "$SCRIPT_DIR/../agentgw/static"; do
  if [[ -d "$static_src" ]]; then
    mkdir -p "$INSTALL_DIR/static"
    cp -r "$static_src/"* "$INSTALL_DIR/static/" 2>/dev/null || true
    break
  fi
done

AGENTGW_START_ARGS=(start --qr)
if [[ -n "$HUB_URL" ]]; then
  AGENTGW_START_ARGS+=(--hub "$HUB_URL")
fi
if [[ -n "$TUNNEL_URL" ]]; then
  AGENTGW_START_ARGS+=(--tunnel-url "$TUNNEL_URL")
fi
if [[ -n "$APP_URL" ]]; then
  AGENTGW_START_ARGS+=(--app-url "$APP_URL")
fi

nohup "$INSTALL_DIR/agentgw" "${AGENTGW_START_ARGS[@]}" > /tmp/agentgw.log 2>&1 &
GW_PID=$!
sleep 2

if kill -0 "$GW_PID" 2>/dev/null; then
  info "agentgw 已启动 (PID ${GW_PID}, http://localhost:${GW_PORT})"
else
  err "agentgw 启动失败:"
  tail -5 /tmp/agentgw.log
  exit 1
fi

# ── 8. Connection info + QR ──────────────────────────────────────
# Detect all available IPs
LOCAL_IP=""
TAILSCALE_IP=""

# Detect Tailscale IP
if command -v tailscale &>/dev/null; then
  TAILSCALE_IP="$(tailscale ip -4 2>/dev/null || true)"
fi

# Detect LAN IP
LAN_IP="$(ipconfig getifaddr en0 2>/dev/null || ip route get 1 2>/dev/null | awk '{print $7; exit}' || hostname -I 2>/dev/null | awk '{print $1}' || true)"

# Prefer Tailscale, fallback to LAN
if [[ -n "$TAILSCALE_IP" ]]; then
  LOCAL_IP="$TAILSCALE_IP"
  info "检测到 Tailscale IP: ${TAILSCALE_IP}"
elif [[ -n "$LAN_IP" ]]; then
  LOCAL_IP="$LAN_IP"
  info "检测到 LAN IP: ${LAN_IP}"
else
  LOCAL_IP="127.0.0.1"
  warn "无法检测 IP，使用 127.0.0.1"
fi

# Show all available IPs
if [[ -n "$TAILSCALE_IP" && -n "$LAN_IP" && "$TAILSCALE_IP" != "$LAN_IP" ]]; then
  echo ""
  echo -e "${CYAN}可用 IP 地址:${NC}"
  echo "  [1] Tailscale: ${TAILSCALE_IP} (推荐，跨网络可用)"
  echo "  [2] LAN:       ${LAN_IP} (仅同网络)"
  echo -n "选择 (默认 1): "
  read -r ip_choice
  case "${ip_choice:-1}" in
    2) LOCAL_IP="$LAN_IP" ;;
    *) LOCAL_IP="$TAILSCALE_IP" ;;
  esac
fi

TUNNEL_USER="$REGISTERED_USER"
if [[ -z "$TUNNEL_URL" ]]; then
  TUNNEL_URL="${AGENTGW_TUNNEL_URL:-}"
fi
if [[ -z "$APP_URL" ]]; then
  APP_URL="${AGENTGW_APP_URL:-}"
fi
if [[ -z "$TUNNEL_USER" && -n "$TUNNEL_URL" ]]; then
  # Extract userId from query string if present
  TUNNEL_USER="$(echo "$TUNNEL_URL" | sed -n 's/.*[?&]userId=\([^&]*\).*/\1/p')"
fi
REMOTE_WS_URL="$(build_remote_ws_url "$TUNNEL_URL" "$APP_URL")"
LOCAL_WS_URL="ws://${LOCAL_IP}:${GW_PORT}/ws"

echo ""
echo -e "${GREEN}╔════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║   🎉 安装完成！                            ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════╝${NC}"
echo ""

# Always show local connection info
echo -e "${CYAN}📱 本地连接（同局域网）:${NC}"
echo "  URL:   ${LOCAL_WS_URL}"
echo "  Token: ${TOKEN}"
echo ""

# Show remote tunnel info if available
if [[ -n "$REMOTE_WS_URL" ]]; then
  echo -e "${CYAN}🌐 远程连接（通过 tunnelhub）:${NC}"
  echo "  URL:   ${REMOTE_WS_URL}"
  echo "  Token: ${TOKEN}"
  if [[ -n "$TUNNEL_USER" ]]; then
    echo "  User:  ${TUNNEL_USER}"
  fi
  echo ""
fi

echo -e "${CYAN}💻 本地 Web 控制台:${NC}"
echo "  http://localhost:${GW_PORT}"
echo ""

# QR codes — always show local; also show remote if available
step "二维码（手机扫描即可连接）:"
echo ""
echo -e "${CYAN}[本地] 同局域网使用:${NC}"
generate_qr "${LOCAL_WS_URL}|${TOKEN}"

if [[ -n "$REMOTE_WS_URL" ]]; then
  echo ""
  echo -e "${CYAN}[远程] 跨网络使用:${NC}"
  generate_qr "${REMOTE_WS_URL}|${TOKEN}"
fi

# Open browser
if $OPEN_BROWSER; then
  echo ""
  step "打开控制台..."
  open "http://localhost:${GW_PORT}" 2>/dev/null || xdg-open "http://localhost:${GW_PORT}" 2>/dev/null || true
fi

echo ""
echo -e "${CYAN}💡 提示:${NC}"
echo "  📱 手机安装 agentapp.apk → 扫描上方二维码"
echo "  🌐 浏览器: http://localhost:${GW_PORT}"
if [[ -n "$REMOTE_WS_URL" ]]; then
  echo "  🔗 远程连接: ${REMOTE_WS_URL}"
fi
if [[ -n "$TUNNEL_USER" ]]; then
  echo "  🧾 注册用户: ${TUNNEL_USER}"
fi
echo "  📝 配置: ${INSTALL_DIR}/config.yaml"
echo "  🔐 凭据: ${INSTALL_DIR}/local_auth.json"
echo "  ⚙️  运行环境: ${RUNTIME_ENV_FILE}"
echo "  📋 日志: tail -f /tmp/agentgw.log"
echo "  🔄 重启: ./install.sh restart"
echo "  ⏹  停止: ./install.sh stop"
echo "  ℹ️  状态: ./install.sh status"
if [[ -n "$TUNNEL_URL" ]]; then
  echo ""
  echo "  agentgw 已使用以下隧道配置："
  echo "    AGENTGW_TUNNEL_URL=${TUNNEL_URL}"
  if [[ -n "$APP_URL" ]]; then
    echo "    AGENTGW_APP_URL=${APP_URL}"
  fi
fi
echo ""
