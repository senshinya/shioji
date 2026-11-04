#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
  echo 'Shioji targets Apple Silicon macOS.' >&2
  exit 1
fi
app=build/Shioji.app
mkdir -p "$app/Contents/MacOS"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$app/Contents/MacOS/shioji" ./cmd/shioji
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$app/Contents/MacOS/shioji-worker" ./cmd/shioji-worker
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.shioji.app</string>
<key>CFBundleName</key><string>Shioji</string>
<key>CFBundleDisplayName</key><string>Shioji</string>
<key>CFBundleExecutable</key><string>shioji</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>0.1.0</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
<!-- Retain flat AppKit window controls while preserving native actions. -->
<key>UIDesignRequiresCompatibility</key><true/>
<key>NSLocalNetworkUsageDescription</key><string>连接你选择的开发设备。</string>
</dict></plist>
PLIST
codesign --force --sign - "$app/Contents/MacOS/shioji-worker"
codesign --force --sign - "$app"
codesign --verify --strict "$app"
echo "$PWD/$app"
