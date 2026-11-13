#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
  echo 'Shioji targets Apple Silicon macOS.' >&2
  exit 1
fi
version=$(tr -d '\r\n' < VERSION)
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'VERSION must contain a numeric major.minor.patch version.' >&2
  exit 1
fi
app=${SHIOJI_APP_PATH:-build/Shioji.app}
identity=${CODE_SIGN_IDENTITY:--}
sign_options=(--force --sign "$identity")
if [[ "$identity" != - ]]; then
  sign_options+=(--options runtime --timestamp)
fi
worker_build_time=$(date -u +%s)
mkdir -p "$app/Contents/MacOS"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time" -o "$app/Contents/MacOS/shioji" ./cmd/shioji
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time" -o "$app/Contents/MacOS/shioji-worker" ./cmd/shioji-worker
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.shioji.app</string>
<key>CFBundleName</key><string>Shioji</string>
<key>CFBundleDisplayName</key><string>Shioji</string>
<key>CFBundleExecutable</key><string>shioji</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>0.0.1</string>
<key>CFBundleShortVersionString</key><string>0.0.1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
<!-- Retain flat AppKit window controls while preserving native actions. -->
<key>UIDesignRequiresCompatibility</key><true/>
<key>NSLocalNetworkUsageDescription</key><string>连接你选择的开发设备。</string>
</dict></plist>
PLIST
runtime_bundle="$app/Contents/Resources/Shioji Runtime.app"
mkdir -p "$runtime_bundle/Contents/MacOS" "$runtime_bundle/Contents/Library/LaunchAgents"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time" -o "$runtime_bundle/Contents/MacOS/shioji-runtime" ./cmd/shioji-runtime
# Sign once, then copy the exact worker. Timestamped signatures affect BuildID.
codesign "${sign_options[@]}" "$app/Contents/MacOS/shioji-worker"
cp "$app/Contents/MacOS/shioji-worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
cat > "$runtime_bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.shioji.runtime</string>
<key>CFBundleName</key><string>Shioji Runtime</string>
<key>CFBundleExecutable</key><string>shioji-runtime</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>0.0.1</string>
<key>CFBundleShortVersionString</key><string>0.0.1</string>
<key>LSUIElement</key><true/>
<key>LSMinimumSystemVersion</key><string>13.0</string>
</dict></plist>
PLIST
cat > "$runtime_bundle/Contents/Library/LaunchAgents/dev.shioji.runtime.agent.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>dev.shioji.runtime.agent</string>
<key>BundleProgram</key><string>Contents/MacOS/shioji-runtime</string>
<key>ProgramArguments</key><array><string>shioji-runtime</string><string>--serve</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>10</integer>
<key>LimitLoadToSessionType</key><string>Aqua</string>
<key>ProcessType</key><string>Background</string>
</dict></plist>
PLIST
cat > "$runtime_bundle/Contents/Library/LaunchAgents/dev.shioji.network.agent.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>dev.shioji.network.agent</string>
<key>BundleProgram</key><string>Contents/MacOS/shioji-runtime</string>
<key>ProgramArguments</key><array><string>shioji-runtime</string><string>--network-serve</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>10</integer>
<key>LimitLoadToSessionType</key><string>Aqua</string>
<key>ProcessType</key><string>Background</string>
</dict></plist>
PLIST
# VERSION is authoritative for both bundles; quoted plist templates stay literal.
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$runtime_bundle/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$runtime_bundle/Contents/Info.plist"
if [[ -f packaging/assets/Shioji.icns ]]; then
  cp packaging/assets/Shioji.icns "$app/Contents/Resources/Shioji.icns"
  /usr/libexec/PlistBuddy -c 'Add :CFBundleIconFile string Shioji' "$app/Contents/Info.plist"
fi
codesign "${sign_options[@]}" "$runtime_bundle/Contents/MacOS/shioji-runtime"
codesign "${sign_options[@]}" "$runtime_bundle"
codesign --verify --deep --strict "$runtime_bundle"
python3 scripts/bundle-terminal.py "$app/Contents/Resources"
codesign "${sign_options[@]}" "$app/Contents/Resources/libghostty-vt.dylib"
codesign "${sign_options[@]}" "$app"
codesign --verify --deep --strict "$app"
cmp "$app/Contents/MacOS/shioji-worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
echo "$PWD/$app"
