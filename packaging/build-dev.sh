#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
  echo 'Shioji targets Apple Silicon macOS.' >&2
  exit 1
fi
app=build/Shioji.app
worker_build_time=$(date -u +%s)
mkdir -p "$app/Contents/MacOS"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time" -o "$app/Contents/MacOS/shioji" ./cmd/shioji
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time" -o "$app/Contents/MacOS/shioji-worker" ./cmd/shioji-worker
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
runtime_bundle="$app/Contents/Resources/Shioji Runtime.app"
mkdir -p "$runtime_bundle/Contents/MacOS" "$runtime_bundle/Contents/Library/LaunchAgents"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$runtime_bundle/Contents/MacOS/shioji-runtime" ./cmd/shioji-runtime
cp "$app/Contents/MacOS/shioji-worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
cat > "$runtime_bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.shioji.runtime</string>
<key>CFBundleName</key><string>Shioji Runtime</string>
<key>CFBundleExecutable</key><string>shioji-runtime</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
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
codesign --force --sign - "$runtime_bundle/Contents/MacOS/shioji-worker"
codesign --force --sign - "$runtime_bundle/Contents/MacOS/shioji-runtime"
codesign --force --sign - "$runtime_bundle"
codesign --verify --deep --strict "$runtime_bundle"
codesign --force --sign - "$app/Contents/MacOS/shioji-worker"
codesign --force --sign - "$app"
codesign --verify --strict "$app"
echo "$PWD/$app"
