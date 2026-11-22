#!/bin/bash
set -euo pipefail
runtime_bundle=$1
worker=$2
version=$3
build_ldflags=$4
identity=${CODE_SIGN_IDENTITY:--}
sign_options=(--force --sign "$identity")
if [[ "$identity" != - ]]; then
  sign_options+=(--options runtime --timestamp)
fi
mkdir -p "$runtime_bundle/Contents/MacOS" "$runtime_bundle/Contents/Library/LaunchAgents"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$build_ldflags" -o "$runtime_bundle/Contents/MacOS/shioji-runtime" ./cmd/shioji-runtime
# Preserve the signed worker identity across the two bundles.
cp "$worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
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
<key>ProcessType</key><string>Interactive</string>
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
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$runtime_bundle/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$runtime_bundle/Contents/Info.plist"
codesign "${sign_options[@]}" "$runtime_bundle/Contents/MacOS/shioji-runtime"
codesign "${sign_options[@]}" "$runtime_bundle"
codesign --verify --deep --strict "$runtime_bundle"
