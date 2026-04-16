#!/usr/bin/env bash
# Deploy agentd + build APK / IPA + auto-flash to connected devices
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

AGENTD_DIR="./agentd"
AGENTGW_DIR="./agentgw"
AGENTAPP_DIR="./agentapp"
LOCAL_BIN="$AGENTD_DIR/agentd"
LINUX_BIN="$AGENTD_DIR/agentd-linux"
APK_OUTPUT="$AGENTGW_DIR/agentapp.apk"
IPA_OUTPUT="$AGENTGW_DIR/agentapp.ipa"
REMOTE_HOST="${REMOTE_HOST:-ws}"
REMOTE_LOG="/tmp/agentd.log"

# Check if an output file is up-to-date versus its source files.
# Usage: up_to_date <output> <find_args...>
up_to_date() {
    local output="$1"
    shift
    [[ ! -f "$output" ]] && return 1
    local newer
    newer=$(find "$@" -newer "$output" -print 2>/dev/null | head -1)
    [[ -z "$newer" ]]
}

show_help() {
  cat <<EOF
Usage: ./scripts/deploy.sh [TARGET]

Build and deploy agentd / agentgw / agentapp for development.
After building APK & IPA, automatically detects connected mobile devices
and installs the artifacts (Android via adb, iOS via ios-deploy).

TARGETS:
  all              Build macOS + Linux + APK + IPA, deploy local + remote, restart agentgw (default)
  build            Build agentd (macOS + Linux), APK and IPA; auto-install to connected devices
  local            Build macOS agentd, restart local agentd, restart agentgw
  ws               Build Linux agentd, deploy to remote \$REMOTE_HOST, restart agentgw
  apk              Build APK only and auto-install to connected Android devices
  ipa              Build IPA only and auto-install to connected iOS devices
  flutter-android  Use \`flutter install\` to flash Android (auto-detects device)
  flutter-ios      Use \`flutter install\` to flash iOS (auto-detects device)
  sim              Build and install to iOS Simulator via xcrun simctl
  cfgutil          Install existing IPA via Apple Configurator 2 (cfgutil)
  mobile           Install existing APK/IPA to connected devices without rebuilding
  gw               Restart agentgw only
  help             Show this help message

ENVIRONMENT:
  REMOTE_HOST    Remote SSH host for 'ws' target (default: ws)

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

  # Use flutter install for iOS (useful when ios-deploy fails)
  ./scripts/deploy.sh flutter-ios

  # Install to iOS Simulator
  ./scripts/deploy.sh sim
EOF
}

build_mac() {
    if up_to_date "$LOCAL_BIN" agentd -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \); then
        echo "[deploy] macOS binary up-to-date, skipping build"
        return 0
    fi
    echo "[deploy] Building agentd for macOS..."
    (cd "$AGENTD_DIR" && go build -o agentd ./cmd/agentd/)
    echo "[deploy] macOS binary: $(ls -lh "$LOCAL_BIN" | awk '{print $5}')"
}

build_linux() {
    if up_to_date "$LINUX_BIN" agentd -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \); then
        echo "[deploy] Linux binary up-to-date, skipping build"
        return 0
    fi
    echo "[deploy] Building agentd for Linux amd64..."
    (cd "$AGENTD_DIR" && GOOS=linux GOARCH=amd64 go build -o agentd-linux ./cmd/agentd/)
    echo "[deploy] Linux binary: $(ls -lh "$LINUX_BIN" | awk '{print $5}')"
}

build_apk() {
    if up_to_date "$APK_OUTPUT" agentapp/lib -type f -name '*.dart' agentapp/pubspec.yaml agentapp/pubspec.lock; then
        echo "[deploy] APK up-to-date, skipping build"
        return 0
    fi
    echo "[deploy] Building APK..."
    (cd "$AGENTAPP_DIR" && flutter build apk --release --no-tree-shake-icons)
    local apk="$AGENTAPP_DIR/build/app/outputs/flutter-apk/app-release.apk"
    if [[ -f "$apk" ]]; then
        cp "$apk" "$APK_OUTPUT"
        echo "[deploy] APK: $(ls -lh "$APK_OUTPUT" | awk '{print $5}')"
    else
        echo "[deploy] ERROR: APK not found at $apk"
        return 1
    fi
}

build_ipa() {
    if ! command -v xcodebuild &>/dev/null; then
        echo "[deploy] Skipping IPA (Xcode not found)"
        return 0
    fi
    if up_to_date "$IPA_OUTPUT" agentapp/lib -type f -name '*.dart' agentapp/pubspec.yaml agentapp/pubspec.lock; then
        echo "[deploy] IPA up-to-date, skipping build"
        return 0
    fi
    echo "[deploy] Building iOS IPA..."
    (cd "$AGENTAPP_DIR" && flutter build ipa --release --export-method ad-hoc 2>/dev/null) || {
        echo "[deploy] WARNING: iOS IPA build failed (needs Apple Developer account / provisioning profile)"
        return 0
    }
    local ipa
    ipa=$(ls -t "$AGENTAPP_DIR/build/ios/ipa/"*.ipa 2>/dev/null | head -1)
    if [[ -n "$ipa" && -f "$ipa" ]]; then
        cp "$ipa" "$IPA_OUTPUT"
        echo "[deploy] IPA: $(ls -lh "$IPA_OUTPUT" | awk '{print $5}')"
    else
        echo "[deploy] WARNING: IPA not found after build"
    fi
}

install_android() {
    if ! command -v adb &>/dev/null; then
        echo "[deploy] adb not found, trying flutter install..."
        install_android_flutter
        return
    fi
    local devices
    devices=$(adb devices | grep -v "List" | grep "device$" | awk '{print $1}')
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
    # If all adb installs failed, fallback to flutter install
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

    # No device found via ios-deploy/idevice_id, try cfgutil
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

    # Last resort: flutter install
    echo "[deploy] No iOS device connected via ios-deploy/ideviceinstaller/cfgutil, trying flutter install..."
    install_ios_flutter
}

install_ios_flutter() {
    if ! command -v flutter &>/dev/null; then
        echo "[deploy] flutter not found, skipping iOS install"
        return 1
    fi
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

build_all() {
    # Build macOS, Linux, APK and IPA in parallel
    local mac_pid linux_pid apk_pid ipa_pid mac_ok=0 linux_ok=0 apk_ok=0 ipa_ok=0
    build_mac & mac_pid=$!
    build_linux & linux_pid=$!
    build_apk & apk_pid=$!
    build_ipa & ipa_pid=$!
    wait "$mac_pid" && mac_ok=1 || true
    wait "$linux_pid" && linux_ok=1 || true
    wait "$apk_pid" && apk_ok=1 || true
    wait "$ipa_pid" && ipa_ok=1 || true
    echo "[deploy] Build results: mac=$mac_ok linux=$linux_ok apk=$apk_ok ipa=$ipa_ok"
    [[ $mac_ok -eq 1 && $linux_ok -eq 1 && $apk_ok -eq 1 ]]
}

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
    # Upload to temp name (can't overwrite running binary)
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "mkdir -p ~/bin" || return 1
    scp -o ConnectTimeout=5 "$LINUX_BIN" "$REMOTE_HOST:~/bin/agentd-new" || return 1

    # Stop old agentd (try user-owned first, then root-owned as fallback)
    echo "[deploy] Stopping remote agentd..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "pkill -f '[a]gentd start' 2>/dev/null; sudo pkill -f '[a]gentd start' 2>/dev/null; sleep 1" || true

    # Replace old binary and start as normal user.
    # Use bash -c with double-fork + setsid so the process fully detaches
    # from the SSH session. A simple nohup & disown can still hang because
    # the background process inherits the SSH file descriptors.
    echo "[deploy] Replacing binary and starting remote agentd..."
    ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 "$REMOTE_HOST" \
        "mv ~/bin/agentd-new ~/bin/agentd && chmod +x ~/bin/agentd && \
         bash -c 'nohup ~/bin/agentd start > $REMOTE_LOG 2>&1 </dev/null & \
                   disown; exit 0'" || true
    # Give the remote process time to start (SSH returns immediately)
    sleep 3
    echo "[deploy] Checking remote status..."
    ssh -o ConnectTimeout=5 "$REMOTE_HOST" "if pgrep -u \$(whoami) -f 'agentd start' > /dev/null; then echo 'OK (running as user)'; else echo 'WARN: may be running as root'; fi; tail -3 $REMOTE_LOG" || true
}

deploy_all() {
    # Deploy local and remote in parallel
    local local_pid remote_pid local_ok=0 remote_ok=0
    deploy_local & local_pid=$!
    deploy_remote & remote_pid=$!
    wait "$local_pid" && local_ok=1 || true
    wait "$remote_pid" && remote_ok=1 || true
    [[ $local_ok -eq 1 && $remote_ok -eq 1 ]]
}

restart_agentgw() {
    echo "[deploy] Restarting agentgw (to reconnect WS tunnels to agentd)..."
    pkill -f "agentgw start" 2>/dev/null || true
    sleep 1
    nohup "$AGENTGW_DIR/agentgw" start > /tmp/agentgw.log 2>&1 &
    sleep 2
    if pgrep -f "agentgw start" > /dev/null; then
        echo "[deploy] agentgw started (PID $(pgrep -f "agentgw start"))"
    else
        echo "[deploy] ERROR: agentgw failed to start"
        tail -5 /tmp/agentgw.log
        return 1
    fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    TARGET="${1:-all}"

    case "$TARGET" in
        help|--help|-h)
            show_help
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
        mobile)
            deploy_mobile
            ;;
        local)
            build_mac
            deploy_local
            restart_agentgw
            ;;
        ws)
            build_linux
            deploy_remote
            restart_agentgw
            ;;
        gw)
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
