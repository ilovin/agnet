#!/usr/bin/env bash
# Deploy agentd + agentgw + mobile apps to local/remote targets
#
# Lessons learned (baked into the script):
#   1. Remote agentd must run as the normal user (NOT sudo).
#      sudo causes os.UserHomeDir() to return /root instead of the actual user,
#      so watcher can't find ~/.claude/projects session files.
#   2. After restarting agentd, agentgw's proxy connections break.
#      The script auto-restarts agentgw to re-establish WS tunnels.
#   3. Can't SCP over a running binary — upload to temp name first, stop, then mv.

# shellcheck disable=SC2012  # ls -t|head-1 is the idiomatic "latest file" pattern; no find equivalent
# shellcheck disable=SC2168  # 'local' used outside a function (real bug, out of R-008 scope; bash silently accepts it)
# shellcheck disable=SC2153  # ${VERSION} is injected by the caller's environment, not a typo
set -euo pipefail
cd "$(dirname "$0")/.."

REPO_ROOT="$(pwd)"
INSTALL_SCRIPT="$REPO_ROOT/scripts/install.sh"

# Source build functions (incremental caching, parallel builds)
source scripts/build.sh

# After sourcing build.sh, these variables point to out/ directory:
# LOCAL_BIN, LINUX_BIN, GW_BIN, APK_OUTPUT, etc.

REMOTE_HOST="${REMOTE_HOST:-ws}"
AGENTGW_CONFIG="${AGENTGW_CONFIG:-$HOME/.agentgw/config.json}"
REMOTE_LOG="/tmp/agentd.log"

# ── Idempotency: compare manifest sha256 values ───────────────────────
# Compare current binary SHA256s against last manifest.json.
# Returns 0 if all match (skip release), 1 otherwise.
is_release_up_to_date() {
    local latest_manifest
    latest_manifest=$(ls -t release/phone-talk-v*/manifest.json 2>/dev/null | head -1)
    [[ -f "$latest_manifest" ]] || return 1

    local current_hash expected_hash bin_path
    for pair in darwin-arm64 linux-amd64 linux-arm64; do
        for bin in agentd agentgw; do
            bin_path="out/$pair/$bin"
            [[ -f "$bin_path" ]] || continue
            if command -v sha256sum &>/dev/null; then
                current_hash=$(sha256sum "$bin_path" | awk '{print $1}')
            elif command -v shasum &>/dev/null; then
                current_hash=$(shasum -a 256 "$bin_path" | awk '{print $1}')
            else
                return 1
            fi
            expected_hash=$(python3 -c "
import json,sys
m=json.load(open('$latest_manifest'))
for p in m.get('platforms',[]):
    if p['os']=='${pair%-*}' and p['arch']=='${pair#*-}':
        print(p.get('binaries',{}).get('$bin',{}).get('sha256',''))
" 2>/dev/null || echo "")
            if [[ "$current_hash" != "$expected_hash" ]]; then
                return 1
            fi
        done
    done
    return 0
}

# ── Release deployment config ─────────────────────────────────────────
RELEASE_REMOTE_HOST="${RELEASE_REMOTE_HOST:-}"
RELEASE_REMOTE_DIR="${RELEASE_REMOTE_DIR:-/opt/phonetalk/releases}"
API_REMOTE_HOST="${API_REMOTE_HOST:-}"
API_REMOTE_DIR="${API_REMOTE_DIR:-/opt/phonetalk/api}"
PORTAL_REMOTE_DIR="${PORTAL_REMOTE_DIR:-/opt/phonetalk/portal}"

# ── Device detection ──────────────────────────────────────────────────

detect_android_devices() {
    if ! command -v adb &>/dev/null; then
        return
    fi
    adb devices 2>/dev/null | grep -v "List" | grep "device$" | awk '{print $1}'
}

detect_ios_devices() {
    if command -v ios-deploy &>/dev/null; then
        ios-deploy -c 2>/dev/null | grep -oE '^[0-9a-f-]{36,}' || true
        return
    fi
    if command -v idevice_id &>/dev/null; then
        idevice_id -l 2>/dev/null || true
        return
    fi
    if command -v cfgutil &>/dev/null; then
        cfgutil list 2>/dev/null | grep -E "ECID:" | awk '{print $2}' || true
    fi
}

detect_devices() {
    local android_devs ios_devs
    android_devs=$(detect_android_devices)
    ios_devs=$(detect_ios_devices)

    local count=0
    if [[ -n "$android_devs" ]]; then
        local n
        n=$(echo "$android_devs" | wc -l | tr -d ' ')
        echo "[deploy] Android devices: $n"
        echo "$android_devs" | while read -r d; do echo "  - $d"; done
        count=$((count + n))
    fi
    if [[ -n "$ios_devs" ]]; then
        local n
        n=$(echo "$ios_devs" | wc -l | tr -d ' ')
        echo "[deploy] iOS devices: $n"
        echo "$ios_devs" | while read -r d; do echo "  - $d"; done
        count=$((count + n))
    fi
    if [[ $count -eq 0 ]]; then
        echo "[deploy] No mobile devices detected"
    fi
}

# ── Mobile install functions ──────────────────────────────────────────

install_android() {
    if ! command -v adb &>/dev/null; then
        echo "[deploy] adb not found, trying flutter install..."
        install_android_flutter
        return
    fi
    local devices
    devices=$(detect_android_devices)
    if [[ -z "$devices" ]]; then
        echo "[deploy] No Android device connected via adb, trying flutter install..."
        install_android_flutter
        return
    fi
    local apk="$AGENTAPP_DIR/build/app/outputs/flutter-apk/app-release.apk"
    if [[ ! -f "$apk" ]]; then
        apk="$APK_OUTPUT"
    fi
    if [[ ! -f "$apk" ]]; then
        echo "[deploy] ERROR: APK not found for install"
        return 1
    fi
    local ok=0
    echo "$devices" | while read -r device; do
        echo "[deploy] Installing APK to Android device $device ..."
        if adb -s "$device" install -r "$apk"; then
            ok=1
        else
            echo "[deploy] WARNING: adb install failed on $device"
        fi
    done
    if [[ $ok -eq 0 ]]; then
        echo "[deploy] All adb installs failed, trying flutter install..."
        install_android_flutter
    fi
}

install_android_flutter() {
    if ! command -v flutter &>/dev/null; then
        echo "[deploy] flutter not found, skipping Android install"
        return 1
    fi
    echo "[deploy] Running flutter install for Android..."
    (cd "$AGENTAPP_DIR" && flutter install) || echo "[deploy] WARNING: flutter install failed"
}

install_ios() {
    local ipa="$IPA_OUTPUT"
    if [[ ! -f "$ipa" ]]; then
        ipa=$(ls -t "$AGENTAPP_DIR/build/ios/ipa/"*.ipa 2>/dev/null | head -1)
    fi
    if [[ ! -f "$ipa" ]]; then
        echo "[deploy] ERROR: IPA not found for install"
        return 1
    fi

    local has_ios_deploy=false has_idevice=false has_cfgutil=false
    command -v ios-deploy &>/dev/null && has_ios_deploy=true
    command -v ideviceinstaller &>/dev/null && has_idevice=true
    command -v cfgutil &>/dev/null && has_cfgutil=true

    local devices=""
    if $has_ios_deploy; then
        devices=$(ios-deploy -c | grep -E "^[0-9a-f-]{36,}" | awk '{print $1}')
    elif command -v idevice_id &>/dev/null; then
        devices=$(idevice_id -l)
    fi

    if [[ -n "$devices" ]]; then
        echo "$devices" | while read -r udid; do
            if [[ -z "$udid" ]]; then continue; fi
            echo "[deploy] Installing IPA to iOS device $udid ..."
            if $has_ios_deploy; then
                ios-deploy --id "$udid" --ipa "$ipa" --justlaunch || echo "[deploy] WARNING: ios-deploy failed on $udid"
            elif $has_idevice; then
                ideviceinstaller -u "$udid" -i "$ipa" || echo "[deploy] WARNING: ideviceinstaller failed on $udid"
            fi
        done
        return
    fi

    if $has_cfgutil; then
        echo "[deploy] No device from ios-deploy, trying cfgutil (Apple Configurator 2)..."
        local ecids
        ecids=$(cfgutil list 2>/dev/null | grep -E "ECID:" | awk '{print $2}')
        if [[ -n "$ecids" ]]; then
            echo "$ecids" | while read -r ecid; do
                if [[ -z "$ecid" ]]; then continue; fi
                echo "[deploy] Installing IPA via cfgutil to ECID $ecid ..."
                cfgutil --ecid "$ecid" install-app "$ipa" || echo "[deploy] WARNING: cfgutil failed on $ecid"
            done
            return
        fi
    fi

    echo "[deploy] No iOS device connected via ios-deploy/ideviceinstaller/cfgutil, trying flutter install..."
    install_ios_flutter
}

install_ios_flutter() {
    if ! command -v flutter &>/dev/null; then
        echo "[deploy] flutter not found, skipping iOS install"
        return 1
    fi
    echo "[deploy] Building iOS for device..."
    (cd "$AGENTAPP_DIR" && flutter build ios) || {
        echo "[deploy] WARNING: flutter build ios failed"
        return 1
    }
    echo "[deploy] Running flutter install for iOS..."
    (cd "$AGENTAPP_DIR" && flutter install) || echo "[deploy] WARNING: flutter install failed"
}

install_ios_cfgutil() {
    if ! command -v cfgutil &>/dev/null; then
        echo "[deploy] cfgutil (Apple Configurator 2) not found"
        echo "[deploy] Install Apple Configurator 2 from Mac App Store, then run:"
        echo "  sudo ln -s /Applications/Apple\ Configurator\ 2.app/Contents/MacOS/cfgutil /usr/local/bin/cfgutil"
        return 1
    fi
    local ipa="$IPA_OUTPUT"
    if [[ ! -f "$ipa" ]]; then
        ipa=$(ls -t "$AGENTAPP_DIR/build/ios/ipa/"*.ipa 2>/dev/null | head -1)
    fi
    if [[ ! -f "$ipa" ]]; then
        echo "[deploy] ERROR: IPA not found for install"
        return 1
    fi
    local ecids
    ecids=$(cfgutil list 2>/dev/null | grep -E "ECID:" | awk '{print $2}')
    if [[ -z "$ecids" ]]; then
        echo "[deploy] No iOS device found via cfgutil"
        return 1
    fi
    echo "$ecids" | while read -r ecid; do
        if [[ -z "$ecid" ]]; then continue; fi
        echo "[deploy] Installing IPA via cfgutil to ECID $ecid ..."
        cfgutil --ecid "$ecid" install-app "$ipa" || echo "[deploy] WARNING: cfgutil failed on $ecid"
    done
}

install_ios_simulator() {
    if ! command -v xcrun &>/dev/null; then
        echo "[deploy] xcrun not found (requires Xcode)"
        return 1
    fi
    echo "[deploy] Building iOS app for Simulator..."
    (cd "$AGENTAPP_DIR" && flutter build ios --simulator) || {
        echo "[deploy] ERROR: flutter build ios --simulator failed"
        return 1
    }

    local app_path
    app_path=$(find "$AGENTAPP_DIR/build/ios/iphonesimulator" -name "Runner.app" -maxdepth 1 | head -1)
    if [[ ! -d "$app_path" ]]; then
        echo "[deploy] ERROR: Runner.app not found in build/ios/iphonesimulator"
        return 1
    fi

    local booted_sim
    booted_sim=$(xcrun simctl list devices booted | grep -E "\(.*\) *\(Booted\)" | head -1 | sed -E 's/.*\(([A-Z0-9-]+)\).*/\1/')
    if [[ -z "$booted_sim" ]]; then
        echo "[deploy] No booted iOS Simulator found. Trying to boot the first available..."
        local first_sim
        first_sim=$(xcrun simctl list devices available | grep -E "iPhone [0-9]+.*\(.*\)" | head -1 | sed -E 's/.*\(([A-Z0-9-]+)\).*/\1/')
        if [[ -z "$first_sim" ]]; then
            echo "[deploy] ERROR: No available iOS Simulator found"
            return 1
        fi
        xcrun simctl boot "$first_sim"
        booted_sim="$first_sim"
        echo "[deploy] Booted simulator $booted_sim"
    fi

    local bundle_id
    bundle_id=$(/usr/libexec/PlistBuddy -c "Print :CFBundleIdentifier" "$app_path/Info.plist" 2>/dev/null || echo "com.example.agentapp")

    echo "[deploy] Installing to simulator $booted_sim ..."
    xcrun simctl install "$booted_sim" "$app_path"
    echo "[deploy] Launching app in simulator..."
    xcrun simctl launch "$booted_sim" "$bundle_id"
}

deploy_mobile() {
    install_android
    install_ios
}

# ── Server deploy functions ───────────────────────────────────────────

normalize_linux_arch() {
    case "$1" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) echo "" ;;
    esac
}

is_local_target() {
    local host="${1:-}"
    local host_lc
    host_lc="$(echo "$host" | tr '[:upper:]' '[:lower:]')"
    [[ -z "$host_lc" || "$host_lc" == "localhost" || "$host" == "127.0.0.1" || "$host" == "::1" ]]
}

resolve_remote_targets() {
    local config_path="$AGENTGW_CONFIG"
    local -a targets=()
    local raw_targets=""
    local host

    if [[ -f "$config_path" ]]; then
        raw_targets="$(python3 - "$config_path" <<'PY' 2>/dev/null || true
import json, sys
path = sys.argv[1]
try:
    with open(path, 'r', encoding='utf-8') as f:
        cfg = json.load(f)
except Exception:
    sys.exit(0)

nodes = cfg.get('nodes', [])
if not isinstance(nodes, list):
    sys.exit(0)

for node in nodes:
    if not isinstance(node, dict):
        continue

    ssh_alias = node.get('ssh_alias')
    host = node.get('host')
    ssh_alias = ssh_alias.strip() if isinstance(ssh_alias, str) else ''
    host = host.strip() if isinstance(host, str) else ''

    if (ssh_alias and ssh_alias.lower() in {'localhost', '127.0.0.1', '::1'}) or (host and host.lower() in {'localhost', '127.0.0.1', '::1'}):
        continue

    target = ssh_alias or host
    if target:
        print(target)
PY
)"

        while IFS= read -r host; do
            if [[ -n "$host" ]] && ! is_local_target "$host"; then
                targets+=("$host")
            fi
        done <<< "$raw_targets"
    fi

    if [[ ${#targets[@]} -eq 0 ]]; then
        echo "$REMOTE_HOST"
        return
    fi

    local seen="|"
    local target
    for target in "${targets[@]}"; do
        if [[ "$seen" != *"|$target|"* ]]; then
            echo "$target"
            seen+="$target|"
        fi
    done
}

print_remote_targets() {
    local -a targets=()
    local t
    while IFS= read -r t; do
        targets+=("$t")
    done < <(resolve_remote_targets)
    echo "[deploy] Resolved remote targets (${#targets[@]}):"
    for t in "${targets[@]}"; do
        echo "  - $t"
    done
}

resolve_remote_agentd_binary() {
    local remote_arch="$1"
    local remote_bin=""
    case "$remote_arch" in
        amd64)
            build_agentd_linux >&2
            remote_bin="$LINUX_BIN"
            ;;
        arm64)
            build_agentd_linux_arm64 >&2
            remote_bin="$LINUX_ARM64_BIN"
            ;;
        *)
            return 1
            ;;
    esac
    [[ -f "$remote_bin" ]] || return 1
    echo "$remote_bin"
}

deploy_local_runtime() {
    echo "[deploy] Refreshing local runtime via install.sh restart..."
    bash "$INSTALL_SCRIPT" restart
}

deploy_local() {
    echo "[deploy] Verifying local agentd build output..."
    if [[ ! -f "$LOCAL_BIN" ]]; then
        echo "[deploy] ERROR: local agentd binary not found at $LOCAL_BIN"
        return 1
    fi
    echo "[deploy] Local runtime artifacts ready"
}

# R-012 T5: refuse to run real deploy from a worktree subdirectory.
# A worktree's binary would replace the main runtime and silently revert
# fixes already merged to main. Sandbox subcommand is exempt — it runs
# entirely under $WORKTREE/.sandbox/ with redirected HOME and ports.
guard_no_worktree_for_real_deploy() {
    if [[ "$REPO_ROOT" == *"/.claude/worktrees/"* ]]; then
        echo "ERROR: deploy.sh '$1' must run from main worktree root, not a worktree subdirectory" >&2
        echo "Current REPO_ROOT: $REPO_ROOT" >&2
        echo "If you want to test a worktree's code, use 'scripts/deploy.sh sandbox <id>' instead." >&2
        exit 1
    fi
}

deploy_remote_host() {
    local target_host="$1"
    echo "[deploy] Deploying to $target_host (as user, NOT sudo)..."

    local remote_arch_raw remote_arch remote_bin
    remote_arch_raw="$(ssh -o ConnectTimeout=5 "$target_host" "uname -m" 2>/dev/null | tr -d '[:space:]' || true)"
    remote_arch="$(normalize_linux_arch "$remote_arch_raw")"
    if [[ -z "$remote_arch" ]]; then
        echo "[deploy] ERROR: Unsupported remote architecture '$remote_arch_raw' on $target_host"
        return 1
    fi
    echo "[deploy] Remote architecture on $target_host: $remote_arch_raw -> $remote_arch"
    remote_bin="$(resolve_remote_agentd_binary "$remote_arch")" || {
        echo "[deploy] ERROR: Failed to prepare agentd binary for linux-$remote_arch"
        return 1
    }

    # Sync token from agentgw config to remote agentd so auth stays consistent
    local token
    token="$(python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(d.get('token',''))" "$AGENTGW_CONFIG" 2>/dev/null || true)"
    if [[ -n "$token" ]]; then
        echo "[deploy] Syncing token to remote agentd config on $target_host..."
        ssh -o ConnectTimeout=5 "$target_host" "mkdir -p ~/.agentd && python3 -c \"
import json, os
path = os.path.expanduser('~/.agentd/config.json')
cfg = {'port': 7373, 'data_dir': os.path.expanduser('~/.agentd/data')}
if os.path.exists(path):
    with open(path) as f:
        cfg = json.load(f)
cfg['token'] = '$token'
with open(path, 'w') as f:
    json.dump(cfg, f, indent=2)
    f.write('\\n')
\"" || true
    fi

    ssh -o ConnectTimeout=5 "$target_host" "mkdir -p ~/bin" || return 1
    scp -o ConnectTimeout=5 "$remote_bin" "$target_host:~/bin/agentd-new" || return 1

    echo "[deploy] Stopping remote agentd on $target_host..."
    ssh -o ConnectTimeout=5 "$target_host" "pkill -f '[a]gentd start' 2>/dev/null; sudo pkill -f '[a]gentd start' 2>/dev/null; sleep 1" || true

    echo "[deploy] Replacing binary and starting remote agentd on $target_host..."
    ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 "$target_host" \
        "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && \
         bash -c 'nohup ~/bin/agentd start > $REMOTE_LOG 2>&1 </dev/null & \
                   disown; exit 0'" || true
    sleep 3
    echo "[deploy] Checking remote status on $target_host..."
    ssh -o ConnectTimeout=5 "$target_host" "if pgrep -u \$(whoami) -f 'agentd start' > /dev/null; then echo 'OK (running as user)'; else echo 'WARN: may be running as root'; fi; tail -3 $REMOTE_LOG" || true
}

deploy_remote() {
    deploy_remote_host "$REMOTE_HOST"
}

deploy_remote_targets_parallel() {
    local -a targets=() pids=()
    local target
    while IFS= read -r target; do
        targets+=("$target")
    done < <(resolve_remote_targets)

    for target in "${targets[@]}"; do
        deploy_remote_host "$target" &
        pids+=("$!")
    done

    local failed=0 pid
    for pid in "${pids[@]}"; do
        wait "$pid" || failed=1
    done
    [[ $failed -eq 0 ]]
}


deploy_all() {
    local local_pid remote_pid runtime_pid local_ok=0 remote_ok=0 runtime_ok=0
    deploy_local & local_pid=$!
    deploy_remote & remote_pid=$!
    wait "$local_pid" && local_ok=1 || true
    wait "$remote_pid" && remote_ok=1 || true
    if [[ $local_ok -eq 1 ]]; then
        deploy_local_runtime & runtime_pid=$!
        wait "$runtime_pid" && runtime_ok=1 || true
    fi
    [[ $local_ok -eq 1 && $remote_ok -eq 1 && $runtime_ok -eq 1 ]]
}


deploy_server_bin() {
    local local_pid remote_pid runtime_pid local_ok=0 remote_ok=0 runtime_ok=0
    deploy_local & local_pid=$!
    deploy_remote_targets_parallel & remote_pid=$!
    wait "$local_pid" && local_ok=1 || true
    wait "$remote_pid" && remote_ok=1 || true
    if [[ $local_ok -eq 1 ]]; then
        deploy_local_runtime & runtime_pid=$!
        wait "$runtime_pid" && runtime_ok=1 || true
    fi
    [[ $local_ok -eq 1 && $remote_ok -eq 1 && $runtime_ok -eq 1 ]]
}

# ── Release deployment functions ──────────────────────────────────────

deploy_release_artifacts() {
    if [[ -z "$RELEASE_REMOTE_HOST" ]]; then
        echo "[deploy-release] RELEASE_REMOTE_HOST not set, skipping artifact upload"
        echo "[deploy-release] Set RELEASE_REMOTE_HOST to enable remote release deployment"
        return 0
    fi

    echo "[deploy-release] Deploying release artifacts to $RELEASE_REMOTE_HOST..."

    # Find latest release tarball
    local latest_release
    latest_release=$(ls -t release/phone-talk-v*.tar.gz 2>/dev/null | head -1)
    if [[ -z "$latest_release" ]]; then
        echo "[deploy-release] ERROR: No release tarball found"
        return 1
    fi

    echo "[deploy-release] Uploading $latest_release..."
    ssh -o ConnectTimeout=5 "$RELEASE_REMOTE_HOST" "mkdir -p '$RELEASE_REMOTE_DIR'" || return 1
    scp -o ConnectTimeout=5 "$latest_release" "$RELEASE_REMOTE_HOST:$RELEASE_REMOTE_DIR/" || return 1

    # Extract version from tarball name
    local version
    version=$(basename "$latest_release" .tar.gz | sed 's/phone-talk-//')

    echo "[deploy-release] Extracting release $version on remote..."
    ssh -o ConnectTimeout=5 "$RELEASE_REMOTE_HOST" "
        cd '$RELEASE_REMOTE_DIR' && \
        rm -rf '$version' && \
        tar xzf 'phone-talk-$version.tar.gz' && \
        mv 'phone-talk-$version' '$version' && \
        ln -sfn '$version' latest
    " || return 1

    echo "[deploy-release] Release $version deployed to $RELEASE_REMOTE_HOST:$RELEASE_REMOTE_DIR/"
}

deploy_portal() {
    if [[ -z "$RELEASE_REMOTE_HOST" ]]; then
        echo "[deploy-portal] RELEASE_REMOTE_HOST not set, skipping portal deployment"
        return 0
    fi

    local portal_src="./out/portal"
    # Always rebuild portal to avoid stale cached output
    rm -rf "$portal_src"
    build_portal || return 1

    echo "[deploy-portal] Deploying portal to $RELEASE_REMOTE_HOST:$PORTAL_REMOTE_DIR..."
    # Upload to tmp — PORTAL_REMOTE_DIR may be owned by root
    local tmpdir="/tmp/phone-talk-portal-$$"
    ssh -o ConnectTimeout=5 "$RELEASE_REMOTE_HOST" "rm -rf '$tmpdir' && mkdir -p '$tmpdir'" || return 1
    scp -o ConnectTimeout=5 -r "$portal_src/"* "$RELEASE_REMOTE_HOST:$tmpdir/" || return 1
    ssh -o ConnectTimeout=5 "$RELEASE_REMOTE_HOST" "
        sudo rm -rf '$PORTAL_REMOTE_DIR' && sudo mkdir -p '$PORTAL_REMOTE_DIR' && \
        sudo cp -r '$tmpdir/'* '$PORTAL_REMOTE_DIR/' && rm -rf '$tmpdir'
    " || return 1

    echo "[deploy-portal] Portal deployed successfully"
}

deploy_api_service() {
    if [[ -z "$API_REMOTE_HOST" ]]; then
        echo "[deploy-api] API_REMOTE_HOST not set, skipping API deployment"
        echo "[deploy-api] Set API_REMOTE_HOST to enable API service deployment"
        return 0
    fi

    echo "[deploy-api] Building API service..."
    (cd api && GOOS=linux GOARCH=amd64 go build -o api-server ./cmd/api/) || {
        echo "[deploy-api] ERROR: API service build failed"
        return 1
    }

    echo "[deploy-api] Deploying API service to $API_REMOTE_HOST:$API_REMOTE_DIR..."
    ssh -o ConnectTimeout=5 "$API_REMOTE_HOST" "mkdir -p '$API_REMOTE_DIR'" || return 1
    # Upload to temp first — API_REMOTE_DIR may be owned by root (systemd User=root)
    scp -o ConnectTimeout=5 "./api/api-server" "$API_REMOTE_HOST:/tmp/api-server" || return 1
    ssh -o ConnectTimeout=5 "$API_REMOTE_HOST" "sudo mv /tmp/api-server '$API_REMOTE_DIR/api-server' && sudo chmod +x '$API_REMOTE_DIR/api-server'" || return 1

    # Create systemd service file
    local service_file="/tmp/phonetalk-api.service"
    cat > "$service_file" <<EOF
[Unit]
Description=PhoneTalk API Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$API_REMOTE_DIR
ExecStart=$API_REMOTE_DIR/api-server
Restart=always
RestartSec=5
Environment="PORT=8080"
Environment="MANIFEST_PATH=$RELEASE_REMOTE_DIR/latest/manifest.json"

[Install]
WantedBy=multi-user.target
EOF

    scp -o ConnectTimeout=5 "$service_file" "$API_REMOTE_HOST:/tmp/phonetalk-api.service" || return 1
    ssh -o ConnectTimeout=5 "$API_REMOTE_HOST" "
        sudo mv /tmp/phonetalk-api.service /etc/systemd/system/ && \
        sudo systemctl daemon-reload && \
        sudo systemctl enable phonetalk-api && \
        sudo systemctl restart phonetalk-api
    " || return 1

    rm -f "$service_file"
    echo "[deploy-api] API service deployed and started"
}

# ── Gateway management ────────────────────────────────────────────────

restart_agentgw() {
    if [[ "${DEPLOY_DRY_RUN:-0}" == "1" ]]; then
        echo "[deploy] DEPLOY_DRY_RUN=1: skipping actual restart, running CHECK_ONLY validation..."
        CHECK_ONLY=1 bash "$INSTALL_SCRIPT" restart
    else
        echo "[deploy] Refreshing local gateway runtime via install.sh restart..."
        bash "$INSTALL_SCRIPT" restart
    fi
}

# Handle --with-web flag for `deploy.sh local`: rebuild Flutter Web and copy to ~/.agentgw/static/.
# Default (no flag): skip web rebuild to keep the fast local iteration cycle.
# Usage: deploy_local_with_web_flag [--with-web] [other-args...]
deploy_local_with_web_flag() {
    local with_web=false
    for arg in "$@"; do
        [[ "$arg" == "--with-web" ]] && with_web=true
    done
    if [[ "$with_web" == true ]]; then
        echo "[deploy] --with-web: building Flutter Web (no CDN)..."
        bash "$REPO_ROOT/agentapp/build_web.sh"
        echo "[deploy] Copying agentapp/build/web -> ~/.agentgw/static/ ..."
        rm -rf "$HOME/.agentgw/static"
        mkdir -p "$HOME/.agentgw/static"
        cp -r "$REPO_ROOT/agentapp/build/web/." "$HOME/.agentgw/static/"
        echo "[deploy] Web static updated in ~/.agentgw/static/"
    fi
}

# ── Sandbox (R-012) ───────────────────────────────────────────────────
# Spawn an isolated agentd+agentgw under $REPO_ROOT/.sandbox/<id>/ with
# redirected HOME, random ports, and separate dist. No Go code changes;
# pure HOME + config injection. Safe to run from worktree subdirectories.

SANDBOX_ROOT="$REPO_ROOT/.sandbox"
SANDBOX_PORT_LO=17000
SANDBOX_PORT_HI=19999

sandbox_dir() { echo "$SANDBOX_ROOT/$1"; }

# Find an unused TCP port in [SANDBOX_PORT_LO..SANDBOX_PORT_HI].
# Avoid colliding with the caller's previously picked port via $exclude.
sandbox_pick_port() {
    local exclude="${1:-0}"
    local tries=0 candidate
    while [[ $tries -lt 200 ]]; do
        candidate=$((SANDBOX_PORT_LO + RANDOM % (SANDBOX_PORT_HI - SANDBOX_PORT_LO + 1)))
        if [[ "$candidate" == "$exclude" ]]; then
            tries=$((tries + 1))
            continue
        fi
        if ! lsof -iTCP:"$candidate" -sTCP:LISTEN >/dev/null 2>&1; then
            echo "$candidate"
            return 0
        fi
        tries=$((tries + 1))
    done
    echo "[sandbox] ERROR: failed to find an unused port in $SANDBOX_PORT_LO-$SANDBOX_PORT_HI" >&2
    return 1
}

sandbox_random_token() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        # Fallback for systems without openssl; uses /dev/urandom.
        head -c 32 /dev/urandom | xxd -p -c 64
    fi
}

# Build sandbox-only agentd+agentgw binaries to $SBX_DIR/dist/.
# Does NOT touch $REPO_ROOT/out/ (main build cache) — sandbox has its own.
sandbox_build_binaries() {
    local sbx_dir="$1"
    local dist="$sbx_dir/dist"
    mkdir -p "$dist"
    echo "[sandbox] Compiling agentd -> $dist/agentd"
    (cd "$REPO_ROOT/agentd" && go build -ldflags "-s -w" -o "$dist/agentd" ./cmd/agentd/) || {
        echo "[sandbox] ERROR: agentd build failed" >&2
        return 1
    }
    echo "[sandbox] Compiling agentgw -> $dist/agentgw"
    (cd "$REPO_ROOT/agentgw" && go build -ldflags "-s -w" -o "$dist/agentgw" ./cmd/agentgw/) || {
        echo "[sandbox] ERROR: agentgw build failed" >&2
        return 1
    }
}

# Write sandbox configs: agentd config.json, agentgw config.json.
sandbox_write_configs() {
    local sandbox_home="$1" agentd_port="$2" agentgw_port="$3" token="$4"
    mkdir -p "$sandbox_home/.agentd/data" "$sandbox_home/.agentgw"
    cat >"$sandbox_home/.agentd/config.json" <<EOF
{
  "port": $agentd_port,
  "data_dir": "$sandbox_home/.agentd/data",
  "token": "$token",
  "node_id": "sandbox"
}
EOF
    cat >"$sandbox_home/.agentgw/config.json" <<EOF
{
  "port": $agentgw_port,
  "token": "$token",
  "nodes_file": "",
  "ssh_key": "",
  "tunnel": {
    "hub_url": "",
    "app_url": "",
    "reality_sni": ""
  },
  "upgrade": {
    "manifest_url": ""
  },
  "nodes": [
    {
      "id": "sandbox-local",
      "name": "sandbox-local",
      "host": "localhost",
      "agentd_port": $agentd_port,
      "token": "$token",
      "ssh_alias": ""
    }
  ]
}
EOF
}

# Place static directory: ln -sfn to real $HOME/.agentgw/static, or rebuild on --with-web.
sandbox_setup_static() {
    local sandbox_home="$1" with_web="$2"
    local target="$sandbox_home/.agentgw/static"
    rm -rf "$target"
    if [[ "$with_web" == "true" ]]; then
        echo "[sandbox] --with-web: building Flutter Web (no CDN)..."
        bash "$REPO_ROOT/agentapp/build_web.sh"
        mkdir -p "$target"
        cp -r "$REPO_ROOT/agentapp/build/web/." "$target/"
        echo "[sandbox] Web static built into $target"
    else
        local real_static="${REAL_HOME:-$HOME}/.agentgw/static"
        if [[ -d "$real_static" ]]; then
            ln -sfn "$real_static" "$target"
            echo "[sandbox] Linked static -> $real_static"
        else
            mkdir -p "$target"
            echo "[sandbox] WARNING: $real_static missing; created empty static dir (web UI will 404)"
        fi
    fi
}

deploy_sandbox() {
    local sandbox_id="${1:-}"
    if [[ -z "$sandbox_id" ]]; then
        echo "Usage: scripts/deploy.sh sandbox <id> [--with-web]" >&2
        return 2
    fi
    shift || true
    local with_web=false
    for arg in "$@"; do
        [[ "$arg" == "--with-web" ]] && with_web=true
    done

    # REAL_HOME captures the user's actual HOME *before* we redirect it
    # for the sandbox children. Used by sandbox_setup_static to find the
    # main static dir to symlink.
    local real_home="${HOME}"

    local sbx_dir
    sbx_dir="$(sandbox_dir "$sandbox_id")"
    if [[ -d "$sbx_dir" ]]; then
        if [[ -f "$sbx_dir/pid/agentd.pid" ]] && kill -0 "$(cat "$sbx_dir/pid/agentd.pid" 2>/dev/null)" 2>/dev/null; then
            echo "[sandbox] Sandbox '$sandbox_id' is already running. Use 'sandbox-stop $sandbox_id' first." >&2
            return 1
        fi
        echo "[sandbox] Reusing existing dir $sbx_dir (stale; cleaning runtime state)..."
        rm -rf "$sbx_dir/pid" "$sbx_dir/logs" "$sbx_dir/sandbox.env"
    fi
    local sandbox_home="$sbx_dir/home"
    mkdir -p "$sandbox_home" "$sbx_dir/logs" "$sbx_dir/pid"

    local agentd_port agentgw_port
    agentd_port="$(sandbox_pick_port)" || return 1
    agentgw_port="$(sandbox_pick_port "$agentd_port")" || return 1
    local token
    token="$(sandbox_random_token)"

    cat >"$sbx_dir/sandbox.env" <<EOF
SANDBOX_ID=$sandbox_id
SANDBOX_HOME=$sandbox_home
SANDBOX_DIR=$sbx_dir
AGENTD_PORT=$agentd_port
AGENTGW_PORT=$agentgw_port
SANDBOX_TOKEN=$token
REAL_HOME=$real_home
EOF
    chmod 600 "$sbx_dir/sandbox.env"

    sandbox_write_configs "$sandbox_home" "$agentd_port" "$agentgw_port" "$token" || return 1
    REAL_HOME="$real_home" sandbox_setup_static "$sandbox_home" "$with_web" || return 1
    sandbox_build_binaries "$sbx_dir" || return 1

    # Start agentd
    echo "[sandbox] Starting agentd (HOME=$sandbox_home, port=$agentd_port)..."
    (HOME="$sandbox_home" nohup "$sbx_dir/dist/agentd" start \
        >"$sbx_dir/logs/agentd.log" 2>&1 </dev/null & echo $! >"$sbx_dir/pid/agentd.pid")
    sleep 1

    # Start agentgw — must run from $sandbox_home/.agentgw so that the relative
    # ./static lookup falls back into the sandbox dir, not the main repo.
    # Use `cd` inside a subshell for the nohup target only ($! captures the
    # nohup'd binary, not the cd compound).
    echo "[sandbox] Starting agentgw (HOME=$sandbox_home, port=$agentgw_port)..."
    (
        cd "$sandbox_home/.agentgw"
        HOME="$sandbox_home" nohup "$sbx_dir/dist/agentgw" start \
            >"$sbx_dir/logs/agentgw.log" 2>&1 </dev/null &
        echo $! >"$sbx_dir/pid/agentgw.pid"
    )

    # Verify
    local agentd_pid agentgw_pid
    agentd_pid="$(cat "$sbx_dir/pid/agentd.pid")"
    agentgw_pid="$(cat "$sbx_dir/pid/agentgw.pid")"
    sleep 4
    local ok=true
    if ! kill -0 "$agentd_pid" 2>/dev/null; then
        echo "[sandbox] ERROR: agentd ($agentd_pid) died — see $sbx_dir/logs/agentd.log" >&2
        ok=false
    fi
    if ! kill -0 "$agentgw_pid" 2>/dev/null; then
        echo "[sandbox] ERROR: agentgw ($agentgw_pid) died — see $sbx_dir/logs/agentgw.log" >&2
        ok=false
    fi

    local http_code=""
    if $ok; then
        http_code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://localhost:$agentgw_port/" || echo "000")"
        if [[ "$http_code" != "200" ]]; then
            echo "[sandbox] WARNING: agentgw HTTP responded with $http_code (expected 200)"
        fi
    fi

    if ! $ok; then
        return 1
    fi

    cat <<EOF
✓ Sandbox $sandbox_id started
  agentd PID:  $agentd_pid  port: $agentd_port
  agentgw PID: $agentgw_pid port: $agentgw_port
  Web:         http://localhost:$agentgw_port/  (HTTP $http_code)
  Token:       $token
  Sandbox dir: $sbx_dir
  Stop:        scripts/deploy.sh sandbox-stop $sandbox_id
EOF
}

deploy_sandbox_stop() {
    local sandbox_id="${1:-}"
    if [[ -z "$sandbox_id" ]]; then
        echo "Usage: scripts/deploy.sh sandbox-stop <id>" >&2
        return 2
    fi
    local sbx_dir
    sbx_dir="$(sandbox_dir "$sandbox_id")"
    if [[ ! -d "$sbx_dir" ]]; then
        echo "[sandbox] No sandbox dir at $sbx_dir" >&2
        return 1
    fi
    local pid
    for proc in agentd agentgw; do
        if [[ -f "$sbx_dir/pid/$proc.pid" ]]; then
            pid="$(cat "$sbx_dir/pid/$proc.pid")"
            if kill -0 "$pid" 2>/dev/null; then
                echo "[sandbox] Killing $proc (PID $pid)..."
                kill "$pid" 2>/dev/null || true
                sleep 1
                kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
            else
                echo "[sandbox] $proc PID $pid not running"
            fi
        fi
    done
    rm -rf "$sbx_dir"
    echo "[sandbox] Removed $sbx_dir"
}

deploy_sandbox_list() {
    if [[ ! -d "$SANDBOX_ROOT" ]]; then
        echo "[sandbox] No sandboxes (root $SANDBOX_ROOT does not exist)"
        return 0
    fi
    local found=false
    local d sandbox_id env_file agentd_pid agentgw_pid agentd_port agentgw_port
    for d in "$SANDBOX_ROOT"/*; do
        [[ -d "$d" ]] || continue
        sandbox_id="$(basename "$d")"
        env_file="$d/sandbox.env"
        if [[ ! -f "$env_file" ]]; then
            continue
        fi
        # shellcheck disable=SC1090
        source "$env_file"
        agentd_pid="$(cat "$d/pid/agentd.pid" 2>/dev/null || echo "?")"
        agentgw_pid="$(cat "$d/pid/agentgw.pid" 2>/dev/null || echo "?")"
        local agentd_status="dead" agentgw_status="dead"
        kill -0 "$agentd_pid" 2>/dev/null && agentd_status="alive"
        kill -0 "$agentgw_pid" 2>/dev/null && agentgw_status="alive"
        printf "%-24s agentd=%s(%s,p=%s) agentgw=%s(%s,p=%s)\n" \
            "$sandbox_id" "$agentd_pid" "$agentd_status" "${AGENTD_PORT:-?}" \
            "$agentgw_pid" "$agentgw_status" "${AGENTGW_PORT:-?}"
        found=true
    done
    if ! $found; then
        echo "[sandbox] No sandboxes registered under $SANDBOX_ROOT"
    fi
}

# ── Help ──────────────────────────────────────────────────────────────

show_deploy_help() {
  cat <<EOF
Usage: ./scripts/deploy.sh [TARGET]

Unified deployment entry point. Idempotent: skips build/release if
binaries have not changed (compared against last manifest.json).

TARGETS:
  local       Build + deploy local runtime + remote servers + auto-install mobile
              Options: --with-web  (rebuild Flutter Web + refresh ~/.agentgw/static/;
                                    default OFF to keep fast iteration cycle)
  web         Build + package + OSS publish + portal deploy + API deploy
              Options: --oss-only, --portal-only, --api-only
  npm         Build + package + npm publish
  tunnelhub   Trigger tunnelhub build and deploy
  all         Run local + web + npm + tunnelhub (full release cycle)
  sandbox <id> [--with-web]
              R-012: spawn an isolated agentd+agentgw under \$REPO_ROOT/.sandbox/<id>/
              with redirected HOME, random ports, separate dist. Does not touch the
              main runtime — safe to run from a worktree subdirectory.
  sandbox-stop <id>
              Kill sandbox processes and remove its directory.
  sandbox-list
              List currently running sandboxes (under \$REPO_ROOT/.sandbox/).
  help        Show this help message

ENVIRONMENT:
  REMOTE_HOST            Remote SSH host fallback (default: ws)
  AGENTGW_CONFIG         Agentgw config path (default: ~/.agentgw/config.json)
  RELEASE_REMOTE_HOST    Remote SSH host for release deployment
  RELEASE_REMOTE_DIR     Remote directory for release artifacts (default: /opt/phonetalk/releases)
  API_REMOTE_HOST        Remote SSH host for API deployment
  API_REMOTE_DIR         Remote directory for API service (default: /opt/phonetalk/api)
  PORTAL_REMOTE_DIR      Remote directory for portal (default: /opt/phonetalk/portal)
  AGENTGW_HUB            Tunnelhub base URL
  AGENTGW_TUNNEL_URL     Full tunnel URL (overrides AGENTGW_HUB)
  VERSION                Force version override (default: auto-bump patch)

EXAMPLES:
  # Full dev cycle (default)
  ./scripts/deploy.sh all

  # Local development only
  ./scripts/deploy.sh local

  # Web release only
  ./scripts/deploy.sh web

  # Full release with explicit version
  VERSION=v0.3.0 ./scripts/deploy.sh all

  # Tunnelhub update only
  ./scripts/deploy.sh tunnelhub
EOF
}

deploy_web() {
    local oss_only=false portal_only=false api_only=false
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --oss-only)    oss_only=true; shift ;;
            --portal-only) portal_only=true; shift ;;
            --api-only)    api_only=true; shift ;;
            *) echo "Unknown web option: $1"; exit 1 ;;
        esac
    done
    local do_oss=true do_portal=true do_api=true
    if [[ "$oss_only" == true ]]; then
        do_portal=false; do_api=false
    elif [[ "$portal_only" == true ]]; then
        do_oss=false; do_api=false
    elif [[ "$api_only" == true ]]; then
        do_oss=false; do_portal=false
    fi
    echo "[deploy] Running web release (oss=$do_oss portal=$do_portal api=$do_api)..."
    if is_release_up_to_date; then
        echo "[deploy] Binaries unchanged since last release. Skipping web release."
        exit 0
    fi
    ./scripts/build.sh web
    ./scripts/package.sh
    if [[ "$do_oss" == true ]]; then
        deploy_release_artifacts || { echo "[deploy] ERROR: release artifact deployment failed"; exit 1; }
    fi
    if [[ "$do_portal" == true ]]; then
        deploy_portal || { echo "[deploy] ERROR: portal deployment failed"; exit 1; }
    fi
    if [[ "$do_api" == true ]]; then
        deploy_api_service || { echo "[deploy] ERROR: API deployment failed"; exit 1; }
    fi
    echo "[deploy] Web release completed"
}

deploy_npm() {
    echo "[deploy] Running npm release..."
    if is_release_up_to_date; then
        echo "[deploy] Binaries unchanged since last release. Skipping npm release."
        exit 0
    fi
    ./scripts/build.sh go
    ./scripts/package.sh

    # Copy packaged artifacts to npm package
    local NPM_PKG_DIR="npm/agnet"
    rm -rf "$NPM_PKG_DIR/platform" "$NPM_PKG_DIR/install.sh" "$NPM_PKG_DIR/static"
    if [[ -d "dist/platform" ]]; then
        cp -r "dist/platform" "$NPM_PKG_DIR/platform"
    fi
    if [[ -f "dist/install.sh" ]]; then
        cp "dist/install.sh" "$NPM_PKG_DIR/install.sh"
    fi
    if [[ -d "dist/static" ]]; then
        cp -r "dist/static" "$NPM_PKG_DIR/static"
    fi

    # Update package.json version
    if command -v node &>/dev/null; then
        node -e "
const fs = require('fs');
const pkg = JSON.parse(fs.readFileSync('$NPM_PKG_DIR/package.json', 'utf8'));
pkg.version = '${VERSION}';
fs.writeFileSync('$NPM_PKG_DIR/package.json', JSON.stringify(pkg, null, 2) + '\n');
console.log('[deploy] Updated npm package version to ${VERSION}');
"
    else
        echo "[deploy] WARNING: node not found, skipping package.json version update"
    fi

    # Publish
    if [[ "${NPM_DRY_RUN:-}" == "1" ]]; then
        echo "[deploy] NPM dry-run: cd $NPM_PKG_DIR && npm publish --dry-run"
        (cd "$NPM_PKG_DIR" && npm publish --dry-run)
    else
        echo "[deploy] Publishing npm package..."
        (cd "$NPM_PKG_DIR" && npm publish --access public)
    fi

    echo "[deploy] NPM release completed"
}

# ── Main ──────────────────────────────────────────────────────────────

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    TARGET="${1:-all}"

    case "$TARGET" in
        help|--help|-h)
            show_deploy_help
            exit 0
            ;;
        sandbox)
            # R-012: spawn isolated agentd+agentgw under .sandbox/<id>/
            # No worktree guard — sandbox is the *intended* worktree workflow.
            shift || true
            deploy_sandbox "$@"
            ;;
        sandbox-stop)
            shift || true
            deploy_sandbox_stop "$@"
            ;;
        sandbox-list)
            deploy_sandbox_list
            ;;
        local)
            guard_no_worktree_for_real_deploy local
            echo "[deploy] Running local deployment..."
            deploy_local_with_web_flag "${@:2}"
            build_agentd_mac
            build_agentgw_mac
            build_agentd_linux
            deploy_local
            # dry-run: run CHECK_ONLY validation against existing dist artifacts BEFORE
            # package.sh recreates them, so a missing artifact is caught immediately.
            [[ "${DEPLOY_DRY_RUN:-0}" == "1" ]] && restart_agentgw
            ./scripts/package.sh
            # Fan out to every node listed in ~/.agentgw/config.json (e.g. ws + oracle),
            # not just $REMOTE_HOST. resolve_remote_targets() filters out localhost.
            [[ "${DEPLOY_DRY_RUN:-0}" == "1" ]] || deploy_remote_targets_parallel
            [[ "${DEPLOY_DRY_RUN:-0}" == "1" ]] || restart_agentgw
            [[ "${DEPLOY_DRY_RUN:-0}" == "1" ]] || deploy_mobile
            ;;
        web)
            guard_no_worktree_for_real_deploy web
            # deploy_web: package.sh → OSS/portal/API deploy; uses is_release_up_to_date.
            # Supports --oss-only / --portal-only / --api-only flags (portal_only api_only oss_only).
            shift || true
            deploy_web "$@"
            ;;
        npm)
            guard_no_worktree_for_real_deploy npm
            # deploy_npm: build.sh go → package.sh → npm publish; uses is_release_up_to_date.
            deploy_npm
            ;;
        tunnelhub)
            guard_no_worktree_for_real_deploy tunnelhub
            echo "[deploy] Running tunnelhub deployment..."
            if [[ -f "tunnelhub/scripts/build_and_deploy.sh" ]]; then
                bash tunnelhub/scripts/build_and_deploy.sh
            else
                echo "[deploy] tunnelhub/scripts/build_and_deploy.sh not found, skipping"
            fi
            ;;
        all)
            guard_no_worktree_for_real_deploy all
            echo "[deploy] Running full release cycle..."
            bash "$0" local
            bash "$0" web
            bash "$0" npm
            bash "$0" tunnelhub
            ;;
        *)
            echo "Unknown target: $TARGET"
            echo "Run '$0 help' for usage."
            exit 1
            ;;
    esac
fi
