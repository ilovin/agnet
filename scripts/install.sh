#!/usr/bin/env bash
# shellcheck disable=SC2001  # sed used for complex regex stripping; ${var//pat/rep} can't replicate all patterns
# shellcheck disable=SC2012  # ls used for file-size display; no clean find equivalent
# shellcheck disable=SC2034  # REPO_ROOT/AGENTD_REMOTE_DIR set as discovery results; may be used by sourcing callers
# shellcheck disable=SC2088  # AGENTD_REMOTE_DIR="~/bin": tilde is intentionally unexpanded (expands on remote via SSH)
# shellcheck disable=SC1012  # \n inside Python heredoc passed over SSH — it's Python source, not a bash escape
# shellcheck disable=SC2140  # Same heredoc: "A"B"C" form is part of the shell-quoted Python string literal
# shellcheck disable=SC2015  # A && B || C is intentional; C (warn) should always run on failure path
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
set -e
#set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PACKAGE_ROOT="$SCRIPT_DIR"
# Search upwards for repo root containing agentgw/, agentd/, agentapp/ so that
# install.sh works from both repo root and release subdirectories.
REPO_ROOT=""
_dir="$SCRIPT_DIR"
while [[ "$_dir" != "/" && "$_dir" != "." ]]; do
  _parent="$(cd "$_dir/.." && pwd)"
  if [[ -d "$_parent/agentgw" && -d "$_parent/agentd" && -d "$_parent/agentapp" ]]; then
    REPO_ROOT="$_parent"
    break
  fi
  _dir="$_parent"
done
BIN_DIR="$PACKAGE_ROOT/bin"
# OUT_DIR removed: install.sh now only resolves from standard layout paths
INSTALL_DIR="$HOME/.agentgw"
CACHE_DIR="$INSTALL_DIR/cache"
RUNTIME_ENV_FILE="$INSTALL_DIR/runtime.env"
AGENTD_REMOTE_DIR="~/bin"
AGENTD_PORT=7373
GW_PORT=7374
# Domain defaults injected at build time via -ldflags, fallback to tunnel.ilovin.xyz
DEFAULT_HUB_DOMAIN="${DEFAULT_HUB_DOMAIN:-tunnel.ilovin.xyz}"
DEFAULT_HUB_URL="https://${DEFAULT_HUB_DOMAIN}"
SCRIPT_UPDATE_URL="${SCRIPT_UPDATE_URL:-https://api.ilovin.xyz/v1/install.sh}"
TOKEN=""
LOCAL_ONLY=false
OPEN_BROWSER=true
HUB_URL="$DEFAULT_HUB_URL"
TUNNEL_URL=""
APP_URL=""
REGISTERED_USER=""

# REALITY defaults (matching tunnelhub server config)
REALITY_PUB="${AGENTGW_REALITY_PUB:-}"
REALITY_SID="${AGENTGW_REALITY_SID:-}"
REALITY_SNI="${AGENTGW_REALITY_SNI:-www.google.com}"

show_help() {
  cat <<'EOF'
Usage: ./install.sh [OPTIONS] | [COMMAND]

COMMANDS:
  install   (default) Install / start agentgw and optionally deploy remote agentd
  start     Start (or restart) local agentgw and local agentd — idempotent
  restart   Alias for start
  stop      Stop local agentgw and local agentd
  status    Check whether local agentgw and agentd are running
  purge     Remove all local + remote agentgw/agentd data (requires confirmation)
  update    Self-update install script from remote, then re-exec
  help      Show this help message

OPTIONS (for install):
  --token TOKEN      Pre-set authentication token
  --hub URL          Tunnelhub base URL (e.g. https://${DEFAULT_HUB_DOMAIN})
  --tunnel-url URL   Full tunnel URL (overrides --hub)
  --app-url URL      App-facing remote URL for QR/websocket
  --local-only       Only setup local agentgw, skip remote deployment
  --no-browser       Don't open browser after installation
  -h, --help         Show this help message and exit

ENVIRONMENT:
  AGENTGW_HUB              Tunnelhub base URL (default: https://${DEFAULT_HUB_DOMAIN})
  AGENTGW_TUNNEL_URL       Full tunnel URL (overrides AGENTGW_HUB)
  AGENTGW_APP_URL          App-facing remote URL
  AGENTGW_REALITY_PUB      REALITY public key (base64)
  AGENTGW_REALITY_SID      REALITY short ID (hex)
  AGENTGW_REALITY_SNI      REALITY server name (default: www.google.com)

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
  start|restart|stop|status|purge|update|help|--help|-h)
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
info()  { echo -e "${GREEN}✅ $*${NC}" >&2; }
warn()  { echo -e "${YELLOW}⚠️  $*${NC}" >&2; }
step()  { echo -e "${CYAN}⚙️  $*${NC}" >&2; }
err()   { echo -e "${RED}❌ $*${NC}" >&2; }

# Read a line interactively. Falls back to default when no TTY is available.
INTERACTIVE=true
if [[ ! -t 0 ]] && ! (exec < /dev/tty; test -t 0) 2>/dev/null; then
  INTERACTIVE=false
fi

read_tty() {
  local _prompt="$1" _default="$2" _value=""
  if ! $INTERACTIVE; then
    info "非交互模式，使用默认值: ${_default:-<空>}" >&2
    _value="$_default"
  elif [[ -t 0 ]]; then
    [[ -n "$_prompt" ]] && echo -n "$_prompt" >&2
    IFS= read -r _value || true
  else
    [[ -n "$_prompt" ]] && echo -n "$_prompt" > /dev/tty
    IFS= read -r _value < /dev/tty || true
  fi
  printf '%s\n' "$_value"
}

# ── Platform detection ─────────────────────────────────────────────
normalize_linux_arch() {
  case "$1" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) echo "" ;;
  esac
}

detect_platform() {
  local os arch linux_arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "$os" in
    Darwin)
      case "$arch" in
        arm64|aarch64) echo "darwin-arm64" ;;
        x86_64|amd64) echo "darwin-amd64" ;;
        *) echo "unsupported" ;;
      esac
      ;;
    Linux)
      linux_arch="$(normalize_linux_arch "$arch")"
      if [[ -n "$linux_arch" ]]; then
        echo "linux-$linux_arch"
      else
        echo "unsupported"
      fi
      ;;
    *) echo "unsupported" ;;
  esac
}

# ── Manifest helpers ───────────────────────────────────────────────
download_manifest() {
  local manifest_url="${1:-https://api.ilovin.xyz/v1/release/latest}"
  mkdir -p "$CACHE_DIR"
  local cached="$CACHE_DIR/manifest.json"
  local tmpfile="$CACHE_DIR/manifest.json.tmp"
  if command -v wget >/dev/null 2>&1; then
    if wget -q --no-check-certificate "$manifest_url" -O "$tmpfile" 2>/dev/null; then
      mv "$tmpfile" "$cached"
      echo "$cached"
      return 0
    fi
  fi
  if command -v curl >/dev/null 2>&1; then
    if curl -fsSL "$manifest_url" -o "$tmpfile" 2>/dev/null; then
      mv "$tmpfile" "$cached"
      echo "$cached"
      return 0
    fi
  fi
  if command -v python3 >/dev/null 2>&1; then
    if python3 -c "import urllib.request; urllib.request.urlretrieve('$manifest_url', '$tmpfile')" 2>/dev/null; then
      mv "$tmpfile" "$cached"
      echo "$cached"
      return 0
    fi
  fi
  rm -f "$tmpfile"
  # Fallback to stale cached manifest
  if [[ -f "$cached" ]]; then
    warn "无法下载 manifest，使用本地缓存"
    echo "$cached"
    return 0
  fi
  err "需要 curl、wget 或 python3 来下载 manifest (https://api.ilovin.xyz/v1/release/latest)"
  return 1
}

# Compute sha256 of a file. Returns empty string on failure.
file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" 2>/dev/null | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" 2>/dev/null | awk '{print $1}'
  fi
}

# Resolve artifact info (url, sha256) from manifest via Python.
# Usage: resolve_manifest_info <manifest_file> <name> <platform>
# Prints two lines: url and sha256 (either may be empty).
resolve_manifest_info() {
  python3 - "$1" "$2" "$3" <<'PY'
import json, sys
m_path, name, plat = sys.argv[1], sys.argv[2], sys.argv[3]
with open(m_path) as f:
    m = json.load(f)
for a in m.get('artifacts', []):
    if a.get('name') != name:
        continue
    for p in a.get('platforms', []):
        if f"{p['os']}-{p['arch']}" == plat:
            print(p.get('url', ''))
            print(p.get('sha256', ''))
            sys.exit(0)
PY
}

# Download a URL to a file. Tries curl, wget, python3 in order.
_download_file() {
  local url="$1" dest="$2"
  local download_ok=false
  if command -v wget >/dev/null 2>&1; then
    local wget_err
    wget_err="$(wget -q --no-check-certificate "$url" -O "$dest" 2>&1)" && download_ok=true
    if [[ "$download_ok" != true ]]; then
      warn "wget 失败: $wget_err"
    fi
  fi
  if [[ "$download_ok" != true ]] && command -v curl >/dev/null 2>&1; then
    local curl_err
    curl_err="$(curl -fsSL "$url" -o "$dest" 2>&1)" && download_ok=true
    if [[ "$download_ok" != true ]]; then
      warn "curl 失败: $curl_err"
    fi
  fi
  if [[ "$download_ok" != true ]] && command -v python3 >/dev/null 2>&1; then
    local py_err
    py_err="$(python3 -c "import urllib.request; urllib.request.urlretrieve('$url', '$dest')" 2>&1)" && download_ok=true
    if [[ "$download_ok" != true ]]; then
      warn "python3 失败: $py_err"
    fi
  fi
  if [[ "$download_ok" != true ]]; then
    rm -f "$dest"
    return 1
  fi
  return 0
}

# Download an artifact from manifest by name. Saves to $2.
# Optional $3: platform override (e.g. "linux-amd64").
# Uses $CACHE_DIR for caching; skips download if cached file matches sha256.
download_artifact() {
  local name="$1" dest="$2" platform="${3:-}"
  if [[ -z "$platform" ]]; then
    platform="$(detect_platform)"
    if [[ "$platform" == "unsupported" ]]; then
      err "不支持的平台: $(uname -s) $(uname -m)"
      return 1
    fi
  fi

  local manifest_file
  manifest_file="$(download_manifest)" || return 1

  # Resolve url + sha256 from manifest
  local info
  info="$(resolve_manifest_info "$manifest_file" "$name" "$platform")" || true
  local artifact_url artifact_sha
  artifact_url="$(echo "$info" | head -1)"
  artifact_sha="$(echo "$info" | tail -1)"

  if [[ -z "$artifact_url" ]]; then
    warn "未找到 $name ($platform) 的下载包"
    return 1
  fi

  # Check cache: if file exists with matching sha256, reuse it
  mkdir -p "$CACHE_DIR"
  local cache_key="${name}-${platform}"
  local cached_file="$CACHE_DIR/$cache_key"
  if [[ -f "$cached_file" && -n "$artifact_sha" ]]; then
    local cached_sha
    cached_sha="$(file_sha256 "$cached_file")"
    if [[ "$cached_sha" == "$artifact_sha" ]]; then
      mkdir -p "$(dirname "$dest")"
      cp "$cached_file" "${dest}.new"
      mv "${dest}.new" "$dest"
      chmod +x "$dest"
      info "$name ($platform) 使用缓存"
      return 0
    fi
    # sha mismatch — stale cache, re-download
    warn "$name ($platform) 缓存失效，重新下载"
  fi

  # Download to cache first
  step "下载 $name ($platform): ${artifact_url##*/}"
  if ! _download_file "$artifact_url" "$cached_file.tmp"; then
    err "下载失败: $artifact_url"
    return 1
  fi

  # Verify checksum
  if [[ -n "$artifact_sha" ]]; then
    local actual_sha
    actual_sha="$(file_sha256 "$cached_file.tmp")"
    if [[ -n "$actual_sha" && "$actual_sha" != "$artifact_sha" ]]; then
      err "$name 校验失败！expected $artifact_sha, got $actual_sha"
      rm -f "$cached_file.tmp"
      return 1
    fi
  fi

  mv "$cached_file.tmp" "$cached_file"
  chmod +x "$cached_file"

  # Copy to destination
  mkdir -p "$(dirname "$dest")"
  cp "$cached_file" "${dest}.new"
  mv "${dest}.new" "$dest"
  chmod +x "$dest"
  info "$name ($platform) 下载完成"
}

# Update agentgw from manifest
self_update() {
  step "检查更新..."
  local manifest_file
  manifest_file="$(download_manifest)" || true
  if [[ -x "$INSTALL_DIR/agentgw" && -n "$manifest_file" ]]; then
    local current_version latest_version
    current_version="$("$INSTALL_DIR/agentgw" version 2>/dev/null | awk '{print $2}' || true)"
    latest_version="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('version',''))" "$manifest_file" 2>/dev/null || true)"
    if [[ -n "$current_version" && "$current_version" == "$latest_version" ]]; then
      info "当前已是最新版本: $current_version"
      return 0
    fi
  fi
  download_artifact agentgw "$INSTALL_DIR/agentgw"
}

# Download latest install.sh from remote, replace current script, and re-exec.
update_script_and_reexec() {
  local script_path
  script_path="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"

  step "更新安装脚本..."
  mkdir -p "$CACHE_DIR"
  local tmpfile="$CACHE_DIR/install.sh.tmp"

  if ! _download_file "$SCRIPT_UPDATE_URL" "$tmpfile"; then
    warn "脚本下载失败: $SCRIPT_UPDATE_URL"
    return 1
  fi

  if [[ ! -s "$tmpfile" ]] || ! head -1 "$tmpfile" | grep -q "^#!/"; then
    warn "下载的文件不是有效的脚本"
    rm -f "$tmpfile"
    return 1
  fi

  cp "$tmpfile" "$script_path"
  chmod +x "$script_path"
  rm -f "$tmpfile"
  info "安装脚本已更新"

  # Strip "update" from args, pass the rest to new version
  local -a reexec_args=()
  for arg in "$@"; do
    [[ "$arg" == "update" ]] && continue
    reexec_args+=("$arg")
  done
  exec "$script_path" "${reexec_args[@]}"
}

# Download agentd for the current platform from manifest
download_agentd() {
  download_artifact agentd "$INSTALL_DIR/agentd"
}

# ── Service helpers ────────────────────────────────────────────────
local_agentd_pid() {
  if command -v lsof &>/dev/null; then
    lsof -nP -tiTCP:"$AGENTD_PORT" -sTCP:LISTEN 2>/dev/null || true
    return
  fi
  if command -v ss &>/dev/null; then
    ss -tlnp 2>/dev/null | awk -v port=":$AGENTD_PORT" '$4 ~ port {split($7,p,","); for(i=1;i<=length(p);i++) if(p[i]~/pid=/) {sub(/pid=/,"",p[i]); print p[i]; exit}}' || true
    return
  fi
  if command -v netstat &>/dev/null; then
    netstat -tlnp 2>/dev/null | awk -v port=":$AGENTD_PORT" '$4 ~ port {split($7,p,"/"); print p[1]}' | head -1 || true
    return
  fi
  true
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

# Resolve packaged-release artifacts from standard layout paths: ./platform/ and ../dist/platform/
resolve_artifact() {
  local kind="$1"
  shift
  local candidates=()
  local name

  # Standard layout: ./platform/<os-arch>/ (current directory, e.g. dist/ or release/)
  for name in "$@"; do
    candidates+=("$PACKAGE_ROOT/platform/$name")
  done
  # Standard layout: ../dist/platform/<os-arch>/ (when running from dist/scripts/)
  for name in "$@"; do
    candidates+=("$PACKAGE_ROOT/../dist/platform/$name")
  done
  # Legacy bin/ directory (for backward compatibility)
  for name in "$@"; do
    candidates+=("$BIN_DIR/$name")
  done

  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -f "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done

  err "找不到 ${kind} 二进制文件"
  echo "  搜索路径: ${candidates[*]}"
  return 1
}

# Detect agentgw binary for current platform
detect_binary() {
  case "$(uname -s):$(uname -m)" in
    Darwin:*)
      resolve_artifact "agentgw" darwin-arm64/agentgw agentgw agentgw-macos-arm64
      ;;
    Linux:*)
      local linux_arch
      linux_arch="$(normalize_linux_arch "$(uname -m)")"
      case "$linux_arch" in
        amd64) resolve_artifact "agentgw" linux-amd64/agentgw agentgw-linux agentgw ;;
        arm64) resolve_artifact "agentgw" linux-arm64/agentgw agentgw-linux-arm64 agentgw ;;
        *) return 1 ;;
      esac
      ;;
    *) return 1 ;;
  esac
}

# Detect local agentgw binary for current platform
sync_local_agentgw_binary() {
  mkdir -p "$INSTALL_DIR"
  local bin
  bin="$(detect_binary)" || exit 1
  cp "$bin" "$INSTALL_DIR/agentgw"
  chmod +x "$INSTALL_DIR/agentgw"
}

sync_web_static() {
  local static_src=""
  local out_static="${OUT_DIR:-}"
  if [[ -n "$out_static" ]]; then
    out_static="$out_static/static"
  fi
  for candidate in "$out_static" "$SCRIPT_DIR/static" "$PACKAGE_ROOT/static"; do
    [[ -z "$candidate" ]] && continue
    if [[ -d "$candidate" ]]; then
      static_src="$candidate"
      break
    fi
  done
  if [[ -n "$static_src" ]]; then
    rm -rf "$INSTALL_DIR/static"
    mkdir -p "$INSTALL_DIR/static"
    cp -R "$static_src/." "$INSTALL_DIR/static/"
    return
  fi
  # If ~/.agentgw/static/ already has content and INSTALL_USE_CACHED_WEB is not
  # explicitly set to "1", skip any cache/download step to avoid overwriting a
  # freshly deployed web build (e.g. one written by `deploy.sh local --with-web`).
  if [[ "${INSTALL_USE_CACHED_WEB:-0}" != "1" && -d "$INSTALL_DIR/static" && -n "$(ls -A "$INSTALL_DIR/static" 2>/dev/null)" ]]; then
    info "Web 控制台已存在，跳过缓存覆盖 (INSTALL_USE_CACHED_WEB=1 可强制更新)"
    return
  fi
  # Check cache — skip download if already extracted
  local cache_key="static-tarball"
  local cached_tar="$CACHE_DIR/$cache_key"
  local stamp_file="$CACHE_DIR/$cache_key.stamp"
  local manifest_file
  manifest_file="$CACHE_DIR/manifest.json"
  if [[ -f "$manifest_file" ]]; then
    local version
    version="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('version',''))" "$manifest_file" 2>/dev/null || true)"
    if [[ -f "$stamp_file" && "$(cat "$stamp_file")" == "$version" && -d "$INSTALL_DIR/static" && -n "$(ls -A "$INSTALL_DIR/static" 2>/dev/null)" ]]; then
      info "Web 控制台使用缓存 (v${version})"
      return
    fi
  fi
  # Download static tarball
  step "下载 Web 控制台..."
  if [[ -z "$version" ]]; then
    download_manifest >/dev/null || return
    version="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('version',''))" "$manifest_file" 2>/dev/null || true)"
  fi
  if [[ -z "$version" ]]; then
    warn "无法获取版本号，跳过 Web 控制台下载"
    return
  fi
  local static_url="https://download.ilovin.xyz/${version}/static.tar.gz"
  if ! _download_file "$static_url" "$cached_tar.tmp"; then
    warn "Web 控制台下载失败，跳过"
    return
  fi
  rm -rf "$INSTALL_DIR/static"
  mkdir -p "$INSTALL_DIR/static"
  tar xzf "$cached_tar.tmp" -C "$INSTALL_DIR/static" --strip-components=1 2>/dev/null || {
    rm -rf "$INSTALL_DIR/static"
    warn "Web 控制台格式异常，跳过"
    rm -f "$cached_tar.tmp"
    return
  }
  mv "$cached_tar.tmp" "$cached_tar"
  echo "$version" > "$stamp_file"
  info "Web 控制台已下载 (v${version})"
}

# Sync agentgw token into local agentd config so they stay consistent.
sync_local_agentd_token() {
  local token
  token="$(python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(d.get('token',''))" "$INSTALL_DIR/config.json" 2>/dev/null || true)"
  if [[ -z "$token" ]]; then
    return
  fi
  mkdir -p ~/.agentd
  python3 -c "
import json, os
path = os.path.expanduser('~/.agentd/config.json')
cfg = {'port': 7373, 'data_dir': os.path.expanduser('~/.agentd/data')}
if os.path.exists(path):
    with open(path) as f:
        cfg = json.load(f)
cfg['token'] = '$token'
with open(path, 'w') as f:
    json.dump(cfg, f, indent=2)
    f.write('\n')
"
}

# Detect local agentd binary (in agentd/ dir, sibling of scripts/)
detect_local_agentd() {
  local bin=""
  case "$(uname -s):$(uname -m)" in
    Darwin:*)
      bin="$(resolve_artifact "agentd" darwin-arm64/agentd agentd agentd-darwin)" || return 1
      ;;
    Linux:*)
      local linux_arch
      linux_arch="$(normalize_linux_arch "$(uname -m)")"
      case "$linux_arch" in
        amd64)
          bin="$(resolve_artifact "agentd" linux-amd64/agentd agentd-linux agentd)" || return 1
          ;;
        arm64)
          bin="$(resolve_artifact "agentd" linux-arm64/agentd agentd-linux-arm64 agentd)" || return 1
          ;;
        *)
          return 1
          ;;
      esac
      ;;
    *)
      return 1
      ;;
  esac
  echo "$bin"
}

# Read tunnel URL config from config.json as a fallback when runtime.env is missing.
# Does NOT read token/user_id — those live in local_auth.json / oauth.json only.
read_tunnel_from_config() {
  local cfg="$INSTALL_DIR/config.json"
  [[ -f "$cfg" ]] || return
  python3 - "$cfg" <<'PY'
import sys
with open(sys.argv[1]) as f:
    in_tunnel = False
    for line in f:
        stripped = line.lstrip()
        if stripped.startswith('tunnel:'):
            in_tunnel = True
            continue
        if in_tunnel:
            if stripped and not line.startswith('    '):
                break
            if ':' in stripped:
                k, v = stripped.split(':', 1)
                k = k.strip()
                if k in ('hub_url', 'app_url', 'reality_sni'):
                    print(f'{k}={v.strip()}')
PY
}

restart_services() {
  if [[ "${CHECK_ONLY:-}" == "1" ]]; then
    step "[install] CHECK_ONLY=1: 仅校验二进制路径，跳过实际重启"
    detect_local_agentd >/dev/null 2>&1 || true
    detect_binary >/dev/null || { err "[install] CHECK_ONLY=1: 找不到 agentgw 二进制，校验失败"; return 1; }
    step "[install] CHECK_ONLY=1: 二进制路径解析 OK"
    return 0
  fi

  stop_services

  sync_local_agentgw_binary
  sync_web_static
  local gw_bin="$INSTALL_DIR/agentgw"

  local local_bin
  local_bin="$(detect_local_agentd 2>/dev/null)" || true
  if [[ -z "$local_bin" || ! -f "$local_bin" ]]; then
    if [[ -x "$INSTALL_DIR/agentd" ]]; then
      local_bin="$INSTALL_DIR/agentd"
    else
      step "本地未找到 agentd，尝试从远程下载..."
      if download_agentd; then
        local_bin="$INSTALL_DIR/agentd"
      else
        warn "agentd 下载失败，跳过 agentd 启动"
        local_bin=""
      fi
    fi
  fi
  if [[ -n "$local_bin" && -f "$local_bin" ]]; then
    sync_local_agentd_token
    step "启动本地 agentd (${local_bin##*/})..."
    nohup "$local_bin" start > /tmp/agentd-local.log 2>&1 &
    sleep 2
    if lsof -nP -iTCP:"$AGENTD_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      info "agentd 已启动 (PID $(local_agentd_pid))"
    else
      warn "agentd 启动失败，查看日志: tail -f /tmp/agentd-local.log"
    fi
  fi

  # Rebuild agentgw args from runtime env (same as first install),
  # falling back to config.json tunnel section if runtime.env is missing.
  local restart_hub="" restart_tunnel="" restart_app=""
  local restart_reality_pub="" restart_reality_sid="" restart_reality_sni=""
  if [[ -f "$RUNTIME_ENV_FILE" ]]; then
    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      case "$line" in
        AGENTGW_HUB=*) restart_hub="${line#AGENTGW_HUB=}" ;;
        AGENTGW_TUNNEL_URL=*) restart_tunnel="${line#AGENTGW_TUNNEL_URL=}" ;;
        AGENTGW_APP_URL=*) restart_app="${line#AGENTGW_APP_URL=}" ;;
        AGENTGW_REALITY_PUB=*) restart_reality_pub="${line#AGENTGW_REALITY_PUB=}" ;;
        AGENTGW_REALITY_SID=*) restart_reality_sid="${line#AGENTGW_REALITY_SID=}" ;;
        AGENTGW_REALITY_SNI=*) restart_reality_sni="${line#AGENTGW_REALITY_SNI=}" ;;
      esac
    done < "$RUNTIME_ENV_FILE"
  else
    while IFS='=' read -r k v; do
      [[ -z "$k" ]] && continue
      case "$k" in
        hub_url) restart_hub="$v" ;;
        app_url) restart_app="$v" ;;
        reality_sni) restart_reality_sni="$v" ;;
      esac
    done < <(read_tunnel_from_config)
  fi

  step "启动 agentgw..."
  local -a gw_args=(start --qr)
  if [[ -n "$restart_hub" ]]; then
    gw_args+=(--hub "$restart_hub")
  fi
  if [[ -n "$restart_tunnel" ]]; then
    gw_args+=(--tunnel-url "$restart_tunnel")
  fi
  if [[ -n "$restart_app" ]]; then
    gw_args+=(--app-url "$restart_app")
  fi
  if [[ -n "$restart_reality_pub" && -n "$restart_reality_sni" ]]; then
    gw_args+=(--reality-pub "$restart_reality_pub" --reality-sni "$restart_reality_sni")
    if [[ -n "$restart_reality_sid" ]]; then
      gw_args+=(--reality-sid "$restart_reality_sid")
    fi
  fi

  local -a env_args=()
  if [[ -f "$RUNTIME_ENV_FILE" ]]; then
    env_args+=(env)
    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      env_args+=("$line")
    done < "$RUNTIME_ENV_FILE"
    nohup "${env_args[@]}" "$gw_bin" "${gw_args[@]}" > /tmp/agentgw.log 2>&1 &
  else
    nohup "$gw_bin" "${gw_args[@]}" > /tmp/agentgw.log 2>&1 &
  fi
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
  if [[ -n "$apid" ]]; then
    local local_bin
    local_bin="$(detect_local_agentd 2>/dev/null || true)"
    if [[ -n "$local_bin" && -x "$local_bin" ]]; then
      echo -e "${CYAN}agentd 详情:${NC}"
      "$local_bin" status 2>/dev/null || true
      echo ""
    fi
  fi
  if [[ -n "$gwpid" && -x "$INSTALL_DIR/agentgw" ]]; then
    echo -e "${CYAN}agentgw 详情:${NC}"
    "$INSTALL_DIR/agentgw" status 2>/dev/null || true
    echo ""
  fi
}

# ── Early subcommands ──────────────────────────────────────────────
case "$SUBCMD" in
  start|restart)
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
  purge)
    echo ""
    echo -e "${RED}⚠️  即将删除 agentgw / agentd 的所有本地和远程数据:${NC}"
    echo "  本地: $INSTALL_DIR ~/.agentd /tmp/agentgw.log /tmp/agentd-local.log"
    echo "  远程: ~/bin/agentd ~/.agentd (config.json 中的所有节点)"
    echo ""
    CONFIRM=$(read_tty "确认删除？输入 YES 继续: " "")
    if [[ "$CONFIRM" != "YES" ]]; then
      info "已取消"
      exit 0
    fi

    # Stop local services first
    stop_services

    # Purge remote nodes from config.json
    CFG="$INSTALL_DIR/config.json"
    if [[ -f "$CFG" ]]; then
      REMOTE_NODES="$(python3 - "$CFG" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    cfg = json.load(f)
for n in cfg.get("nodes", []):
    h = n.get("host", "")
    if h and h not in ("localhost", "127.0.0.1", "::1"):
        alias = n.get("ssh_alias", h)
        port = n.get("ssh_port", 22)
        print(f"{alias}|{h}|{port}")
PY
)"
      if [[ -n "$REMOTE_NODES" ]]; then
        echo ""
        while IFS='|' read -r alias host port; do
          [[ -z "$host" ]] && continue
          ssh_opts=(-o ConnectTimeout=10 -o BatchMode=yes)
          step "清理远程节点: ${alias} (${host})..."
          ssh "${ssh_opts[@]}" -p "$port" "$alias" "
            pkill -f 'agentd start' 2>/dev/null; sleep 1
            rm -f ~/bin/agentd
            rm -rf ~/.agentd
          " 2>/dev/null && info "已清理: ${alias}" || warn "无法连接: ${alias}"
        done <<< "$REMOTE_NODES"
      fi
    fi

    # Purge local
    step "清理本地数据..."
    rm -rf "$INSTALL_DIR"
    rm -rf ~/.agentd
    rm -f /tmp/agentgw.log /tmp/agentd-local.log
    info "本地数据已清除"

    echo ""
    info "purge 完成"
    exit 0
    ;;
  update)
    update_script_and_reexec "$@"
    # If script update failed, still update agentgw binary
    self_update
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
if [[ -f "$INSTALL_DIR/config.json" ]]; then
  EXISTING_PORT="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('port',''))" "$INSTALL_DIR/config.json" 2>/dev/null || true)"
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
        local host_val first_h="" token
        host_val="$(echo "$line" | sed 's/^Host[[:space:]]*//')"
        while [[ -n "$host_val" ]]; do
          token="${host_val%% *}"
          [[ "$token" == "$host_val" ]] && host_val="" || host_val="${host_val#* }"
          case "$token" in
            *[*?]* | !*) continue ;;
            *) first_h="$token"; break ;;
          esac
        done
        host="$first_h"
        hostname="" port="22"
        ;;
      HostName\ *) hostname="$(echo "$line" | sed 's/^HostName[[:space:]]*//')" ;;
      Port\ *)      port="$(echo "$line" | sed 's/^Port[[:space:]]*//')" ;;
    esac
  done < "$cfg"
  [[ -n "$host" && -n "$hostname" ]] && echo "${host}|${hostname}|${port}"
  return 0
}

# ── Deploy agentd to remote ────────────────────────────────────────
ensure_agentd_linux_binary() {
  local arch="${1:-amd64}"
  local platform="linux-$arch"
  local install_bin="$INSTALL_DIR/agentd-linux-$arch"
  local bin=""

  case "$arch" in
    amd64)
      bin="$(resolve_artifact "agentd-linux-$arch" linux-amd64/agentd agentd-linux agentd-linux-amd64 2>/dev/null)" && { echo "$bin"; return 0; }
      ;;
    arm64)
      bin="$(resolve_artifact "agentd-linux-$arch" linux-arm64/agentd agentd-linux-arm64 2>/dev/null)" && { echo "$bin"; return 0; }
      ;;
    *)
      warn "不支持的 Linux 架构: $arch"
      return 1
      ;;
  esac

  download_artifact agentd "$install_bin" "$platform" && { echo "$install_bin"; return 0; }
  return 1
}

resolve_remote_linux_arch() {
  local target="$1" port="${2:-22}"
  local ssh_opts=(-o ConnectTimeout=10 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=3)
  local raw
  raw="$(ssh "${ssh_opts[@]}" -p "$port" "$target" "uname -m" 2>/dev/null | tr -d '[:space:]' || true)"
  normalize_linux_arch "$raw"
}

deploy_agentd() {
  local target="$1" port="${2:-22}" token="${3:-}"
  local ssh_opts=(-o ConnectTimeout=10 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=3)
  step "部署 agentd → ${target}..."

  if ! ssh "${ssh_opts[@]}" -p "$port" "$target" "echo ok" &>/dev/null; then
    err "SSH 连接失败: ${target}（需要免密登录）"
    return 1
  fi

  local remote_arch agentd_linux_bin
  remote_arch="$(resolve_remote_linux_arch "$target" "$port")"
  if [[ -z "$remote_arch" ]]; then
    err "无法识别远程 Linux 架构: ${target}"
    return 1
  fi
  info "远程架构: ${target} -> linux-${remote_arch}"

  agentd_linux_bin="$(ensure_agentd_linux_binary "$remote_arch")" || {
    err "找不到 agentd linux-${remote_arch} 二进制文件"
    return 1
  }

  local file_size
  file_size="$(ls -lh "$agentd_linux_bin" 2>/dev/null | awk '{print $5}' || true)"
  info "传输文件: ${agentd_linux_bin##*/} (${file_size})"

  ssh "${ssh_opts[@]}" -p "$port" "$target" "mkdir -p ~/bin" || return 1
  step "传输 ${agentd_linux_bin##*/} → ${target}..."
  if ! scp "${ssh_opts[@]}" -P "$port" "$agentd_linux_bin" "${target}:~/bin/agentd-new" 2>/dev/null; then
    step "SCP 不可用，使用 SSH 管道传输..."
    cat "$agentd_linux_bin" | ssh "${ssh_opts[@]}" -p "$port" "$target" "cat > ~/bin/agentd-new" || {
      err "传输失败: ${target} — 请检查网络连接和远程磁盘空间"
      return 1
    }
  fi

  ssh "${ssh_opts[@]}" -p "$port" "$target" \
    "pkill -f 'agentd start' 2>/dev/null; sleep 1" || true

  # Write remote config.json with the correct token so agentgw can authenticate
  if [[ -n "$token" ]]; then
    ssh "${ssh_opts[@]}" -p "$port" "$target" "mkdir -p ~/.agentd"
    ssh "${ssh_opts[@]}" -p "$port" "$target" "python3 -c '
import json, os
path = os.path.expanduser(\"~/.agentd/config.json\")
cfg = {\"port\": 7373, \"data_dir\": os.path.expanduser(\"~/.agentd/data\")}
if os.path.exists(path):
    with open(path) as f:
        cfg = json.load(f)
cfg[\"token\"] = \"$token\"
with open(path, \"w\") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
'"
  fi

  ssh "${ssh_opts[@]}" -p "$port" "$target" \
    "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && \
     bash -c 'nohup ~/bin/agentd start > /tmp/agentd.log 2>&1 </dev/null & disown; exit 0'" || true
  sleep 3

  if ssh "${ssh_opts[@]}" -p "$port" "$target" "pgrep -f 'agentd start'" &>/dev/null; then
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
  local hub_url="$1" app_url="$2" user_id="$3"
  [[ -z "$hub_url" ]] && return
  python3 - "$hub_url" "$app_url" "$user_id" <<'PY'
from urllib.parse import urlparse
import sys
hub_url, app_url, user_id = sys.argv[1], sys.argv[2], sys.argv[3]
base = app_url or hub_url
u = urlparse(base)
if not u.scheme or not u.hostname:
    sys.exit(0)
if not user_id:
    user_id = 'default'
scheme = 'wss' if u.scheme in ('wss', 'https') else 'ws'
# Strip port so the app connects on default HTTPS port (443) through Caddy,
# not directly to the tunnel port (e.g. 8443).
print(f"{scheme}://{u.hostname}/api.v1.AgentService/Stream/{user_id}")
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
  if ! $INTERACTIVE; then
    warn "非交互模式，跳过 tunnelhub 注册"
    return 1
  fi
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
  echo "    ${data}"
}

# ═══════════════════════════════════════════════════════════════════
echo ""
echo -e "${CYAN}╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║   Agent Manager — One-Click Installer   ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── 1. Detect binary ──────────────────────────────────────────────
GW_BIN="$(detect_binary)" || true
if [[ ! -f "$GW_BIN" && -x "$INSTALL_DIR/agentgw" ]]; then
  GW_BIN="$INSTALL_DIR/agentgw"
  info "使用已安装的 agentgw"
fi
if [[ ! -f "$GW_BIN" ]]; then
  step "本地未找到 agentgw，尝试从远程下载..."
  if self_update; then
    GW_BIN="$INSTALL_DIR/agentgw"
  fi
fi
if [[ ! -f "$GW_BIN" ]]; then
  err "找不到适合当前平台的 agentgw，且自动更新失败"
  echo "  平台: $(uname -s)/$(uname -m)"
  echo "  请手动下载: https://api.ilovin.xyz/v1/release/latest"
  exit 1
fi
info "平台: $(uname -s)/$(uname -m)"
info "agentgw 来源: $GW_BIN"

# ── 2. Resolve network mode + token ────────────────────────────────
# Decide tunnel vs local FIRST, then derive a single token that all
# components (agentd, agentgw, QR codes) will share.

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
  MODE_CHOICE=$(read_tty "选择 (默认 1): " "1")

  if [[ "${MODE_CHOICE:-1}" == "2" ]]; then
    info "使用本地模式"
    HUB_URL=""
    TUNNEL_URL=""
    APP_URL=""
  fi
fi

# Tunnel mode: handle credentials → token comes from hub
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
    CHOICE=$(read_tty "请选择 (默认 1): " "1")
    CHOICE="${CHOICE:-1}"

    if [[ "$CHOICE" -ge 1 && "$CHOICE" -le "$n" ]]; then
      idx=$((CHOICE - 1))
      REGISTERED_USER="${CRED_USERS[idx]}"
      TOKEN="${CRED_TOKENS[idx]}"
      info "使用本地凭据: $REGISTERED_USER"
    elif [[ "$CHOICE" == "$((n+1))" ]]; then
      if register_with_hub "$GW_BIN" "$HUB_URL"; then
        REGISTERED_USER="$(extract_json_field "$local_auth_path" userId)"
        TOKEN="$(extract_json_field "$local_auth_path" token)"
      else
        echo ""
        warn "重新注册失败（userId 可能已在 hub 上存在）"
        RECOVERY_TOKEN=$(read_tty "输入已有 token 恢复凭据（或按 Enter 切换到本地模式）: " "")
        if [[ -n "$RECOVERY_TOKEN" ]]; then
          RECOVERY_USER=$(read_tty "输入 userId: " "")
          if [[ -n "$RECOVERY_USER" ]]; then
            save_local_auth "$RECOVERY_USER" "$RECOVERY_TOKEN"
            REGISTERED_USER="$RECOVERY_USER"
            TOKEN="$RECOVERY_TOKEN"
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
      TOKEN="$(extract_json_field "$local_auth_path" token)"
    else
      warn "注册失败（userId 可能已在 hub 上存在）"
      RECOVERY_TOKEN=$(read_tty "输入已有 token 恢复凭据（或按 Enter 切换到本地模式）: " "")
      if [[ -n "$RECOVERY_TOKEN" ]]; then
        RECOVERY_USER=$(read_tty "输入 userId: " "")
        if [[ -n "$RECOVERY_USER" ]]; then
          save_local_auth "$RECOVERY_USER" "$RECOVERY_TOKEN"
          REGISTERED_USER="$RECOVERY_USER"
          TOKEN="$RECOVERY_TOKEN"
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

  if [[ -z "$APP_URL" && -n "$HUB_URL" ]]; then
    APP_URL="${HUB_URL%/}"
  fi
fi

# Local mode (or tunnel registration failed → fell back): resolve token
# from existing config or generate a fresh one.
if [[ -z "$TOKEN" && -f "$INSTALL_DIR/config.json" ]]; then
  EXISTING_TOKEN="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('token',''))" "$INSTALL_DIR/config.json" 2>/dev/null || true)"
  if [[ -n "$EXISTING_TOKEN" ]]; then
    TOKEN="$EXISTING_TOKEN"
    info "沿用已有 Token: ${TOKEN:0:12}..."
  fi
fi
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(generate_token)"
  info "生成新 Token: ${TOKEN:0:12}..."
fi

# ── 3. Scan & deploy remote nodes ─────────────────────────────────
NODES=() NODE_HOSTS=() NODE_PORTS=()
SELECTION=""
DEPLOYED_NODES=()

if ! $LOCAL_ONLY; then
  step "扫描 SSH 配置..."
  FOUND="$(scan_ssh_nodes)"

  if [[ -n "$FOUND" ]]; then
    echo ""
    echo -e "${CYAN}📱 发现远程节点:${NC}"
    IDX=1
    while IFS='|' read -r alias host port; do
      [[ -z "$alias" || -z "$host" || "$alias" == *[*?]* || "$alias" == !* || "$host" == *[*?]* || "$host" == "127.0.0.1" || "$host" == "localhost" ]] && continue
      echo "  [$IDX] ${alias} (${host}:${port})"
      NODES+=("$alias"); NODE_HOSTS+=("$host"); NODE_PORTS+=("$port")
      IDX=$((IDX + 1))
    done <<< "$FOUND"
    echo ""
    SELECTION=$(read_tty "选择要部署的节点（逗号分隔, 0=跳过）: " "")
  else
    warn "未在 ~/.ssh/config 发现远程节点"
  fi

  # Collect selected nodes into SELECTED_NODES regardless of deploy success
  SELECTED_NODES=()
  if [[ -n "$SELECTION" && "$SELECTION" != "0" ]]; then
    for idx in $(echo "$SELECTION" | tr ',' ' '); do
      idx="$(echo "$idx" | tr -d ' ')"
      if [[ "$idx" -ge 1 && "$idx" -le "${#NODES[@]}" ]]; then
        SELECTED_NODES+=("${NODES[$((idx-1))]}|${NODE_HOSTS[$((idx-1))]}|${NODE_PORTS[$((idx-1))]}")
      fi
    done
  fi

  # Deploy to selected nodes
  if [[ ${#SELECTED_NODES[@]} -gt 0 ]]; then
    for node_info in "${SELECTED_NODES[@]}"; do
      IFS='|' read -r alias host port <<< "$node_info"
      deploy_agentd "$alias" "$port" "$TOKEN" || warn "部署 ${alias} 失败，但节点仍会添加到配置"
    done
  fi

  # All selected nodes go into config.json
  DEPLOYED_NODES=("${SELECTED_NODES[@]}")
fi

# ── 5. Config ─────────────────────────────────────────────────────
# Write deployed nodes to a temp file for Python to read
DEPLOYED_TMP="$(mktemp)"
if [[ ${#DEPLOYED_NODES[@]} -gt 0 ]]; then
  printf '%s\n' "${DEPLOYED_NODES[@]}" > "$DEPLOYED_TMP"
fi

# Sync config.json: refresh token and nodes idempotently via Python.
python3 - "$INSTALL_DIR/config.json" "$TOKEN" "$GW_PORT" "$AGENTD_PORT" "$DEPLOYED_TMP" <<'PY'
import sys, os, uuid, json

path, token, gw_port, agentd_port, deployed_tmp = sys.argv[1:6]
gw_port = int(gw_port)
agentd_port = int(agentd_port)

# Parse deployed nodes from temp file (alias|host|ssh_port per line)
deployed = []
if os.path.exists(deployed_tmp):
    with open(deployed_tmp) as f:
        for line in f:
            line = line.strip()
            if '|' in line:
                parts = line.split('|')
                if len(parts) >= 3:
                    deployed.append({'alias': parts[0], 'host': parts[1], 'port': int(parts[2])})

# Load existing config or start fresh
cfg = {}
if os.path.exists(path):
    try:
        with open(path) as f:
            cfg = json.load(f)
    except Exception:
        pass

# Ensure base fields
cfg.setdefault('token', token)
cfg.setdefault('port', gw_port)
if cfg.get('token') != token:
    cfg['token'] = token

# Build node map keyed by (host, ssh_alias) for dedup
node_map = {}
for n in cfg.get('nodes', []) or []:
    key = (n.get('host', ''), n.get('ssh_alias', ''))
    node_map[key] = n

# Local node
local_key = ('localhost', '')
if local_key not in node_map:
    node_map[local_key] = {
        'id': str(uuid.uuid4()),
        'name': 'local',
        'host': 'localhost',
        'agentd_port': agentd_port,
        'token': token,
        'ssh_alias': '',
    }
else:
    node_map[local_key]['token'] = token

# Deployed / refreshed nodes
for d in deployed:
    key = (d['host'], d['alias'])
    node_map[key] = {
        'id': str(uuid.uuid4()),
        'name': d['alias'],
        'host': d['host'],
        'ssh_port': d['port'],
        'agentd_port': agentd_port,
        'token': token,
        'ssh_alias': d['alias'],
    }

cfg['nodes'] = list(node_map.values())

# Ensure parent dir exists
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, 'w') as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
    f.write('\n')
PY

rm -f "$DEPLOYED_TMP"
info "配置: ${INSTALL_DIR}/config.json"

# ── 6. Persist runtime env ─────────────────────────────────────────
cat > "$RUNTIME_ENV_FILE" <<EOF
AGENTGW_HUB=${HUB_URL}
AGENTGW_TUNNEL_URL=${TUNNEL_URL}
AGENTGW_APP_URL=${APP_URL}
AGENTGW_REALITY_PUB=${REALITY_PUB}
AGENTGW_REALITY_SID=${REALITY_SID}
AGENTGW_REALITY_SNI=${REALITY_SNI}
EOF
chmod 600 "$RUNTIME_ENV_FILE"

# ── 7. Start local agentd ─────────────────────────────────────────
local_bin="$(detect_local_agentd 2>/dev/null)" || true
if [[ -z "$local_bin" || ! -f "$local_bin" ]]; then
  if [[ -x "$INSTALL_DIR/agentd" ]]; then
    local_bin="$INSTALL_DIR/agentd"
  else
    step "本地未找到 agentd，尝试从远程下载..."
    if download_agentd; then
      local_bin="$INSTALL_DIR/agentd"
    else
      warn "agentd 下载失败，跳过本地 agentd"
      local_bin=""
    fi
  fi
fi
if [[ -n "$local_bin" && -f "$local_bin" ]]; then
  sync_local_agentd_token
  step "启动本地 agentd (${local_bin##*/})..."
  nohup "$local_bin" start > /tmp/agentd-local.log 2>&1 &
  sleep 2
  if lsof -nP -iTCP:"$AGENTD_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    info "本地 agentd 已启动 (PID $(local_agentd_pid), port $AGENTD_PORT)"
  else
    warn "本地 agentd 启动失败，查看日志: tail -f /tmp/agentd-local.log"
  fi
fi

# ── 8. Start agentgw ──────────────────────────────────────────────
sync_local_agentgw_binary
sync_web_static

pkill -f "agentgw start" 2>/dev/null || true
sleep 1

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
if [[ -n "$REALITY_PUB" && -n "$REALITY_SNI" ]]; then
  AGENTGW_START_ARGS+=(--reality-pub "$REALITY_PUB" --reality-sni "$REALITY_SNI")
  if [[ -n "$REALITY_SID" ]]; then
    AGENTGW_START_ARGS+=(--reality-sid "$REALITY_SID")
  fi
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

# Detect LAN IP (platform-aware)
LAN_IP=""
if [[ "$(uname -s)" == "Darwin" ]]; then
  LAN_IP="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true)"
else
  LAN_IP="$(ip route get 1 2>/dev/null | awk '{print $7; exit}' || hostname -I 2>/dev/null | awk '{print $1}' || true)"
fi

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
  ip_choice=$(read_tty "选择 (默认 1): " "1")
  case "${ip_choice:-1}" in
    2) LOCAL_IP="$LAN_IP" ;;
    *) LOCAL_IP="$TAILSCALE_IP" ;;
  esac
fi

TUNNEL_USER="$REGISTERED_USER"
if [[ -z "$TUNNEL_USER" ]]; then
  # Try loading from local_auth.json
  TUNNEL_USER="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['userId'])" "$INSTALL_DIR/local_auth.json" 2>/dev/null || true)"
fi
if [[ -z "$TUNNEL_URL" ]]; then
  TUNNEL_URL="${AGENTGW_TUNNEL_URL:-}"
fi
if [[ -z "$APP_URL" ]]; then
  APP_URL="${AGENTGW_APP_URL:-}"
fi
REMOTE_WS_URL="$(build_remote_ws_url "$HUB_URL" "$APP_URL" "$TUNNEL_USER")"
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
echo "  URL:   ${LOCAL_WS_URL}"
echo "  Token: ${TOKEN}"

if [[ -n "$REMOTE_WS_URL" ]]; then
  echo ""
  echo -e "${CYAN}[远程] 跨网络使用:${NC}"
  generate_qr "${REMOTE_WS_URL}|${TOKEN}"
  echo "  URL:   ${REMOTE_WS_URL}"
  echo "  Token: ${TOKEN}"
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
echo "  📝 配置: ${INSTALL_DIR}/config.json"
echo "  🔐 凭据: ${INSTALL_DIR}/local_auth.json"
echo "  ⚙️  运行环境: ${RUNTIME_ENV_FILE}"
echo "  📋 日志: tail -f /tmp/agentgw.log"
echo "  🔄 重启: ./install.sh restart"
echo "  ⏹  停止: ./install.sh stop"
echo "  ℹ️  状态: ./install.sh status"
if [[ -n "$HUB_URL" ]]; then
  echo ""
  echo "  agentgw 已使用以下隧道配置："
  echo "    AGENTGW_HUB=${HUB_URL}"
  if [[ -n "$APP_URL" ]]; then
    echo "    AGENTGW_APP_URL=${APP_URL}"
  fi
  if [[ -n "$REALITY_PUB" ]]; then
    echo "    REALITY: SNI=${REALITY_SNI}"
  fi
fi
echo ""
