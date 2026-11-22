#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

platform=${1:-ios/simulator}
if [[ $# -gt 0 ]]; then
  shift
fi

resource_dir=cmd/shioji-mobile/resources
mkdir -p "$resource_dir"
work=$(mktemp -d)
asset=$(mktemp "$resource_dir/.worker.XXXXXX")
notice_asset=$(mktemp "$resource_dir/.licenses.XXXXXX")
runtime_asset=$(mktemp "$resource_dir/.runtime.XXXXXX")
trap 'rm -rf "$work"; rm -f "$asset" "$notice_asset" "$runtime_asset"' EXIT
build_time=$(date -u +%s)

# Release packaging can supply the exact signed Worker from the Mac build.
worker=${SHIOJI_MOBILE_WORKER:-}
if [[ -z "$worker" ]]; then
  worker="$work/shioji-worker"
  env -u GOROOT CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
    -ldflags "-s -w -X shioji/internal/runtimebuild.BuiltAt=$build_time" \
    -o "$worker" ./cmd/shioji-worker
  identity=${CODE_SIGN_IDENTITY:--}
  sign=(--force --sign "$identity")
  if [[ "$identity" != - ]]; then
    sign+=(--options runtime --timestamp)
  fi
  /usr/bin/codesign "${sign[@]}" "$worker"
fi
/usr/bin/lipo -verify_arch arm64 "$worker"
/usr/bin/codesign --verify --strict "$worker"
metadata=$("$worker" --describe)
kind=$(printf '%s' "$metadata" | /usr/bin/plutil -extract type raw -o - -)
[[ "$kind" == worker-build ]]
digest=$(printf '%s' "$metadata" | /usr/bin/plutil -extract worker.buildId raw -o - -)
actual=$(/usr/bin/shasum -a 256 "$worker")
[[ "$digest" =~ ^[a-f0-9]{64}$ && "$digest" == "${actual%% *}" ]]
{
  printf '%s\n' "$metadata"
  /usr/bin/gzip -1 -nc "$worker"
} > "$asset"
chmod 644 "$asset"
mv -f "$asset" "$resource_dir/worker-macos-arm64.data"

# The Mac login item is a signed data artifact on iOS. Only the selected Mac
# installs and executes it, using the same identity as desktop packaging.
runtime_bundle=${SHIOJI_MOBILE_RUNTIME:-}
if [[ -z "$runtime_bundle" ]]; then
  runtime_bundle="$work/Shioji Runtime.app"
  version=$(tr -d '\r\n' < VERSION)
  bash packaging/build-runtime.sh "$runtime_bundle" "$worker" "$version" "-s -w -X shioji/internal/runtimebuild.BuiltAt=$build_time"
fi
/usr/bin/codesign --verify --deep --strict "$runtime_bundle"
[[ $(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$runtime_bundle/Contents/Info.plist") == dev.shioji.runtime ]]
cmp "$worker" "$runtime_bundle/Contents/MacOS/shioji-worker"
if [[ -n $(/usr/bin/find "$runtime_bundle" -type l -print -quit) ]]; then
  echo 'The runtime bundle may not contain symbolic links.' >&2
  exit 1
fi
/usr/bin/tar -cf - -C "$runtime_bundle" . | /usr/bin/gzip -1 -nc > "$runtime_asset"
chmod 644 "$runtime_asset"
mv -f "$runtime_asset" "$resource_dir/worker-runtime-macos-arm64.data"

python3 scripts/generate-third-party-notices.py --mobile-output "$notice_asset"
chmod 644 "$notice_asset"
mv -f "$notice_asset" "$resource_dir/THIRD_PARTY_NOTICES.json"

mygo="$PWD/build/mobile/mygo"
mkdir -p "$(dirname "$mygo")"
(cd third_party/mygo && go build -o "$mygo" ./cmd/mygo)
"$mygo" build -platform "$platform" "$@" cmd/shioji-mobile
