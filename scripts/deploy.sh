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

set -euo pipefail
cd "$(dirname "$0")/.."

REPO_ROOT="$(pwd)"
INSTALL_SCRIPT="$REPO_ROOT/scripts/install.sh"

# Source build functions (incremental caching, parallel builds)
source scripts/build.sh

# After sourcing build.sh, these variables point to out/ directory:
# LOCAL_BIN, LINUX_BIN, GW_BIN, APK_OUTPUT, etc.

REMOTE_HOST="${REMOTE_HOST:-ws}"
REMOTE_LOG="/tmp/agentd.log"

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

deploy_remote() {
    echo "[deploy] Deploying to $REMOTE_HOST (as user, NOT sudo)..."

    local remote_arch_raw remote_arch remote_bin
    remote_arch_raw="$(ssh -o ConnectTimeout=5 "$REMOTE_HOST" "uname -m" 2>/dev/null | tr -d '[:space:]' || true)"
    remote_arch="$(normalize_linux_arch "$remote_arch_raw")"
    if [[ -z "$remote_arch" ]]; then
        echo "[deploy] ERROR: Unsupported remote architecture '$remote_arch_raw'"
        return 1
    fi
    echo "[deploy] Remote architecture: $remote_arch_raw -> $remote_arch"
    remote_bin="$(resolve_remote_agentd_binary "$remote_arch")" || {
        echo "[deploy] ERROR: Failed to prepare agentd binary for linux-$remote_arch"
        return 1
    }

    # Sync token from agentgw config to remote agentd so auth stays consistent
    local token
    token="$(python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(d.get('token',''))" ~/.agentgw/config.json 2>/dev/null || true)"
    if [[ -n "$token" ]]; then
        echo "[deploy] Syncing token to remote agentd config..."
        ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p ~/.agentd && python3 -c \"
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
\"" || true
    fi

    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p ~/bin" || return 1
    scp -o ConnectTimeout=5 "$remote_bin" "$REMOTE_HOST:~/bin/agentd-new" || return 1

    echo "[deploy] Stopping remote agentd..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "pkill -f '[a]gentd start' 2>/dev/null; sudo pkill -f '[a]gentd start' 2>/dev/null; sleep 1" || true

    echo "[deploy] Replacing binary and starting remote agentd..."
    ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 "$REMOTE_HOST" \
        "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && \
         bash -c 'nohup ~/bin/agentd start > $REMOTE_LOG 2>&1 </dev/null & \
                   disown; exit 0'" || true
    sleep 3
    echo "[deploy] Checking remote status..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "if pgrep -u \$(whoami) -f 'agentd start' > /dev/null; then echo 'OK (running as user)'; else echo 'WARN: may be running as root'; fi; tail -3 $REMOTE_LOG" || true
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
    echo "[deploy] Refreshing local gateway runtime via install.sh restart..."
    bash "$INSTALL_SCRIPT" restart
}

# ── Help ──────────────────────────────────────────────────────────────

show_deploy_help() {
  cat <<EOF
Usage: ./scripts/deploy.sh [TARGET]

Build and deploy agentd / agentgw / agentapp for development.
After building APK & IPA, automatically detects connected mobile devices
and installs the artifacts (Android via adb, iOS via ios-deploy).
Go binaries are rebuilt only when the source-content hash changes.

TARGETS:
  all              Build agentd + agentgw + APK + IPA + Web, deploy local + remote, restart agentgw (default)
  build            Build agentd + agentgw + APK + IPA + Web; auto-install to connected devices
  server           Build all Go binaries, deploy local + remote, restart agentgw (no mobile)
  local            Build macOS agentd + agentgw, restart local agentd, restart agentgw
  ws               Build matching Linux agentd by remote arch, deploy to remote \$REMOTE_HOST, restart agentgw
  apk              Build APK only and auto-install to connected Android devices
  ipa              Build IPA only and auto-install to connected iOS devices
  flutter-android  Use \`flutter install\` to flash Android (auto-detects device)
  flutter-ios      Use \`flutter install\` to flash iOS (auto-detects device)
  sim              Build and install to iOS Simulator via xcrun simctl
  cfgutil          Install existing IPA via Apple Configurator 2 (cfgutil)
  web              Build Flutter Web and copy to agentgw/static
  mobile           Install existing APK/IPA to connected devices without rebuilding
  devices          Detect and list connected mobile devices
  gw               Build and restart agentgw only
  release          Build release tarball (calls release.sh)
  deploy-release   Deploy release artifacts + portal + API to remote server
  deploy-portal    Deploy download portal to remote server
  deploy-api       Deploy API service to remote server
  help             Show this help message

ENVIRONMENT:
  REMOTE_HOST            Remote SSH host for 'ws' target (default: ws)
  RELEASE_REMOTE_HOST    Remote SSH host for release deployment (default: same as REMOTE_HOST)
  RELEASE_REMOTE_DIR     Remote directory for release artifacts (default: /opt/phonetalk/releases)
  API_REMOTE_HOST        Remote SSH host for API deployment (default: same as RELEASE_REMOTE_HOST)
  API_REMOTE_DIR         Remote directory for API service (default: /opt/phonetalk/api)
  PORTAL_REMOTE_DIR      Remote directory for portal (default: /opt/phonetalk/portal)
  AGENTGW_HUB            Tunnelhub base URL (e.g. https://8.146.236.75:443)
  AGENTGW_TUNNEL_URL     Full tunnel URL (overrides AGENTGW_HUB)

EXAMPLES:
  # Full dev cycle (default)
  ./scripts/deploy.sh

  # Build everything and flash to connected phones
  ./scripts/deploy.sh build

  # Deploy only the remote ws node
  REMOTE_HOST=prod ./scripts/deploy.sh ws

  # Quick gateway restart after config change
  ./scripts/deploy.sh gw

  # Build release tarball
  ./scripts/deploy.sh release

  # Deploy release + portal + API to production
  RELEASE_REMOTE_HOST=prod ./scripts/deploy.sh deploy-release

  # Deploy only portal
  RELEASE_REMOTE_HOST=prod ./scripts/deploy.sh deploy-portal

  # Deploy only API service
  API_REMOTE_HOST=prod ./scripts/deploy.sh deploy-api

  # Install already-built APK/IPA to devices
  ./scripts/deploy.sh mobile

  # Check what devices are connected
  ./scripts/deploy.sh devices

  # Use flutter install for iOS (useful when ios-deploy fails)
  ./scripts/deploy.sh flutter-ios

  # Install to iOS Simulator
  ./scripts/deploy.sh sim
EOF
}

# ── Main ──────────────────────────────────────────────────────────────

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    TARGET="${1:-all}"

    case "$TARGET" in
        help|--help|-h)
            show_deploy_help
            exit 0
            ;;
        build)
            build_all
            deploy_mobile
            ;;
        apk)
            build_apk
            install_android
            ;;
        ipa)
            build_ipa
            install_ios
            ;;
        flutter-android)
            install_android_flutter
            ;;
        flutter-ios)
            install_ios_flutter
            ;;
        sim)
            install_ios_simulator
            ;;
        cfgutil)
            install_ios_cfgutil
            ;;
        web)
            build_web
            # Sync static files to agentgw runtime directory
            gw_static="$HOME/.agentgw/static"
            rm -rf "$gw_static"
            mkdir -p "$gw_static"
            cp -R "$WEB_STATIC_DIR/." "$gw_static/"
            echo "[deploy] Web static synced to $gw_static"
            ;;
        mobile)
            deploy_mobile
            ;;
        devices)
            detect_devices
            ;;
        server|bin)
            build_go_all
            deploy_all
            restart_agentgw
            ;;
        local)
            build_agentd_mac
            build_agentgw_mac
            deploy_local
            restart_agentgw
            ;;
        ws)
            build_agentd_linux
            deploy_remote
            restart_agentgw
            ;;
        gw)
            build_agentgw_mac
            restart_agentgw
            ;;
        release)
            echo "[deploy] Building release tarball..."
            ./scripts/release.sh
            ;;
        deploy-release)
            echo "[deploy] Building release..."
            ./scripts/release.sh --skip-ios
            echo "[deploy] Deploying release artifacts + portal + API..."
            deploy_release_artifacts || { echo "[deploy] ERROR: release artifact deployment failed"; exit 1; }
            deploy_portal || { echo "[deploy] ERROR: portal deployment failed"; exit 1; }
            deploy_api_service || { echo "[deploy] ERROR: API deployment failed"; exit 1; }
            echo "[deploy] All components deployed successfully"
            ;;
        deploy-portal)
            deploy_portal || { echo "[deploy] ERROR: portal deployment failed"; exit 1; }
            ;;
        deploy-api)
            deploy_api_service || { echo "[deploy] ERROR: API deployment failed"; exit 1; }
            ;;
        all)
            # Start all builds in parallel. Go binaries + web static are needed
            # for deployment; mobile builds (APK/IPA) can finish in background.
            build_agentd_mac & mac_pid=$!
            build_agentd_linux & linux_pid=$!
            build_agentgw_mac & gw_mac_pid=$!
            build_agentgw_linux & gw_linux_pid=$!
            build_web & web_pid=$!
            build_apk & apk_pid=$!
            build_ipa & ipa_pid=$!

            local mac_ok=0 linux_ok=0 gw_mac_ok=0 gw_linux_ok=0 web_ok=0
            wait "$mac_pid" && mac_ok=1 || true
            wait "$linux_pid" && linux_ok=1 || true
            wait "$gw_mac_pid" && gw_mac_ok=1 || true
            wait "$gw_linux_pid" && gw_linux_ok=1 || true
            wait "$web_pid" && web_ok=1 || true

            if [[ $mac_ok -eq 0 || $linux_ok -eq 0 || $gw_mac_ok -eq 0 || $gw_linux_ok -eq 0 ]]; then
                echo "[deploy] ERROR: Go binary build failed"
                wait "$apk_pid" "$ipa_pid" 2>/dev/null || true
                exit 1
            fi

            # Start deployment immediately — mobile app builds continue in background
            deploy_all & deploy_pid=$!

            local apk_ok=0 ipa_ok=0
            wait "$apk_pid" && apk_ok=1 || true
            wait "$ipa_pid" && ipa_ok=1 || true
            echo "[deploy] Mobile build results: apk=$apk_ok ipa=$ipa_ok"

            wait "$deploy_pid" || { echo "[deploy] ERROR: deployment failed"; exit 1; }

            restart_agentgw

            if [[ $apk_ok -eq 1 || $ipa_ok -eq 1 ]]; then
                deploy_mobile
            fi
            ;;
        *)
            echo "Unknown target: $TARGET"
            echo "Run '$0 help' for usage."
            exit 1
            ;;
    esac
fi
