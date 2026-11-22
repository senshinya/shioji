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
build_ldflags="-X shioji/internal/runtimebuild.BuiltAt=$worker_build_time"
app_ldflags="$build_ldflags"
# Development bundles have no feed or key and never check for updates.
if [[ "${SHIOJI_RELEASE_UPDATES:-0}" == 1 ]]; then
  : "${SHIOJI_UPDATE_PUBLIC_KEY:?Missing update public key}"
  : "${APPLE_TEAM_ID:?Missing Developer ID team}"
  [[ "$SHIOJI_UPDATE_PUBLIC_KEY" =~ ^[A-Za-z0-9+/]{43}=$ ]] || { echo 'Invalid update public key.' >&2; exit 1; }
  [[ "$APPLE_TEAM_ID" =~ ^[A-Z0-9]{10}$ ]] || { echo 'Invalid Developer ID team.' >&2; exit 1; }
  build_ldflags="-s -w $build_ldflags"
  app_ldflags="$build_ldflags -X github.com/egoist/mygo.production=1 -X github.com/egoist/mygo.packageUpdateFeed=https://github.com/senshinya/shioji/releases/latest/download/update-darwin-arm64.json -X github.com/egoist/mygo.packageUpdateKey=$SHIOJI_UPDATE_PUBLIC_KEY -X shioji/internal/appupdate.TeamID=$APPLE_TEAM_ID"
fi
mkdir -p "$app/Contents/MacOS"
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$app_ldflags" -o "$app/Contents/MacOS/shioji" ./cmd/shioji
env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$build_ldflags" -o "$app/Contents/MacOS/shioji-worker" ./cmd/shioji-worker
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
# Sign once, then copy the exact worker. Timestamped signatures affect BuildID.
codesign "${sign_options[@]}" "$app/Contents/MacOS/shioji-worker"
bash packaging/build-runtime.sh "$runtime_bundle" "$app/Contents/MacOS/shioji-worker" "$version" "$build_ldflags"
# VERSION is authoritative for both bundles; quoted plist templates stay literal.
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$app/Contents/Info.plist"
if [[ -f packaging/assets/Shioji.icns ]]; then
  cp packaging/assets/Shioji.icns "$app/Contents/Resources/Shioji.icns"
  /usr/libexec/PlistBuddy -c 'Add :CFBundleIconFile string Shioji' "$app/Contents/Info.plist"
fi
python3 scripts/bundle-terminal.py "$app/Contents/Resources"
codesign "${sign_options[@]}" "$app/Contents/Resources/libghostty-vt.dylib"
python3 scripts/bundle-diagram.py "$app/Contents/Resources"
codesign "${sign_options[@]}" "$app/Contents/Resources/usvg"
python3 scripts/bundle-permission-flow.py "$app/Contents"
for library in "$app/Contents/Frameworks/"*.dylib; do
  codesign "${sign_options[@]}" "$library"
done
codesign "${sign_options[@]}" "$app"
codesign --verify --deep --strict "$app"
cmp "$app/Contents/MacOS/shioji-worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
echo "$PWD/$app"
