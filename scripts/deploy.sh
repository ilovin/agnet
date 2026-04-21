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

# Source build functions (incremental caching, parallel builds)
source scripts/build.sh

# After sourcing build.sh, these variables point to out/ directory:
# LOCAL_BIN, LINUX_BIN, GW_BIN, APK_OUTPUT, etc.

REMOTE_HOST="${REMOTE_HOST:-ws}"
REMOTE_LOG="/tmp/agentd.log"

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

deploy_local() {
    echo "[deploy] Restarting local agentd..."
    pkill -f "./agentd start" 2>/dev/null || true
    pkill -f "$AGENTD_DIR/agentd start" 2>/dev/null || true

    # Safety net: if a stale listener still holds 7373, kill it directly.
    local port_pids=""
    port_pids="$(lsof -nP -tiTCP:7373 -sTCP:LISTEN 2>/dev/null | tr '\n' ' ' | xargs 2>/dev/null || true)"
    if [[ -n "$port_pids" ]]; then
        echo "[deploy] Killing stale 7373 listener(s): $port_pids"
        kill $port_pids 2>/dev/null || true
        sleep 1
        port_pids="$(lsof -nP -tiTCP:7373 -sTCP:LISTEN 2>/dev/null | tr '\n' ' ' | xargs 2>/dev/null || true)"
        if [[ -n "$port_pids" ]]; then
            echo "[deploy] Force killing stubborn 7373 listener(s): $port_pids"
            kill -9 $port_pids 2>/dev/null || true
        fi
    fi

    sleep 1
    nohup "$LOCAL_BIN" start > /tmp/agentd-local.log 2>&1 &
    sleep 2
    if lsof -nP -iTCP:7373 -sTCP:LISTEN >/dev/null 2>&1; then
        echo "[deploy] Local agentd started (PID $(lsof -nP -tiTCP:7373 -sTCP:LISTEN | head -1))"
    else
        echo "[deploy] ERROR: local agentd failed to start"
        tail -5 /tmp/agentd-local.log
        return 1
    fi
    tail -3 /tmp/agentd-local.log
}

deploy_remote() {
    echo "[deploy] Deploying to $REMOTE_HOST (as user, NOT sudo)..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p ~/bin" || return 1
    scp -o ConnectTimeout=5 "$LINUX_BIN" "$REMOTE_HOST:~/bin/agentd-new" || return 1

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
    local local_pid remote_pid local_ok=0 remote_ok=0
    deploy_local & local_pid=$!
    deploy_remote & remote_pid=$!
    wait "$local_pid" && local_ok=1 || true
    wait "$remote_pid" && remote_ok=1 || true
    [[ $local_ok -eq 1 && $remote_ok -eq 1 ]]
}

# ── Gateway management ────────────────────────────────────────────────

restart_agentgw() {
    build_agentgw_mac
    build_web
    echo "[deploy] Restarting agentgw (to reconnect tunnels to agentd)..."
    pkill -f "agentgw start" 2>/dev/null || true
    sleep 1
    local -a env_args=()
    local runtime_env="$HOME/.agentgw/runtime.env"
    if [[ -f "$runtime_env" ]]; then
        env_args+=(env)
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            env_args+=("$line")
        done < "$runtime_env"
    fi
    nohup "${env_args[@]}" "$GW_BIN" start --qr > /tmp/agentgw.log 2>&1 &
    sleep 2
    if pgrep -f "agentgw start" > /dev/null; then
        echo "[deploy] agentgw started (PID $(pgrep -f "agentgw start"))"
    else
        echo "[deploy] ERROR: agentgw failed to start"
        tail -5 /tmp/agentgw.log
        return 1
    fi
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
  local            Build macOS agentd + agentgw, restart local agentd, restart agentgw
  ws               Build Linux agentd, deploy to remote \$REMOTE_HOST, restart agentgw
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
  help             Show this help message

ENVIRONMENT:
  REMOTE_HOST            Remote SSH host for 'ws' target (default: ws)
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
            ;;
        mobile)
            deploy_mobile
            ;;
        devices)
            detect_devices
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
        all)
            build_all
            deploy_all
            restart_agentgw
            deploy_mobile
            ;;
        *)
            echo "Unknown target: $TARGET"
            echo "Run '$0 help' for usage."
            exit 1
            ;;
    esac
fi
