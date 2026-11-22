#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${CODE_SIGN_IDENTITY:?CODE_SIGN_IDENTITY must select a Developer ID Application identity}"
: "${NOTARY_PROFILE:?NOTARY_PROFILE must select a validated notarytool keychain profile}"
version=$(tr -d '\r\n' < VERSION)
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'VERSION must contain a numeric major.minor.patch version.' >&2
  exit 1
fi
if [[ "${RELEASE_TAG:-v$version}" != "v$version" ]]; then
  echo 'Release tag does not match VERSION.' >&2
  exit 1
fi
if [[ "$CODE_SIGN_IDENTITY" == - ]]; then
  echo 'CODE_SIGN_IDENTITY cannot be ad-hoc for a release.' >&2
  exit 1
fi
: "${SHIOJI_UPDATE_PUBLIC_KEY:?Missing release environment SHIOJI_UPDATE_PUBLIC_KEY variable}"
: "${SHIOJI_UPDATE_PRIVATE_KEY:?Missing release environment SHIOJI_UPDATE_PRIVATE_KEY secret}"
: "${APPLE_TEAM_ID:?Missing release environment APPLE_TEAM_ID variable}"
env -u GOROOT go run ./cmd/shioji-update check-key --public-key "$SHIOJI_UPDATE_PUBLIC_KEY"
notary_options=(--keychain-profile "$NOTARY_PROFILE")
if [[ -n "${NOTARY_KEYCHAIN:-}" ]]; then
  notary_options+=(--keychain "$NOTARY_KEYCHAIN")
fi
if [[ ! -f packaging/assets/Shioji.icns ]]; then
  echo 'The production application icon is missing.' >&2
  exit 1
fi
out=build/release
app="$out/Shioji.app"
mkdir -p "$out"
worker_stage=$(mktemp -d "$out/worker-stage.XXXXXX")
trap 'rm -rf "$worker_stage"' EXIT
python3 scripts/worker_release.py "${GH_REPO:-senshinya/shioji}" "$version" "$APPLE_TEAM_ID" "$worker_stage/shioji-worker"
reused_worker=
if [[ -f "$worker_stage/shioji-worker" ]]; then
  reused_worker="$worker_stage/shioji-worker"
fi
SHIOJI_RELEASE_UPDATES=1 SHIOJI_APP_PATH="$app" SHIOJI_REUSED_WORKER="$reused_worker" bash packaging/build-dev.sh
signature=$(codesign --display --verbose=4 "$app" 2>&1)
if ! grep -q '^Authority=Developer ID Application:' <<< "$signature" || ! grep -q 'flags=.*runtime' <<< "$signature"; then
  echo 'Release requires a Developer ID Application signature with hardened runtime.' >&2
  exit 1
fi
if ! grep -q "^TeamIdentifier=$APPLE_TEAM_ID$" <<< "$signature"; then
  echo 'Release signature does not match the embedded update team.' >&2
  exit 1
fi
codesign --verify --deep --strict "$app"
# Notarize and staple the app before wrapping it, so copying out of the DMG
# retains an offline-verifiable app ticket.
zip="$out/Shioji-$version-notary.zip"
ditto -c -k --keepParent "$app" "$zip"
xcrun notarytool submit "$zip" "${notary_options[@]}" --wait --timeout 30m --output-format json > "$out/app-notary.json"
python3 packaging/verify-notary.py "$out/app-notary.json"
xcrun stapler staple "$app"
xcrun stapler validate "$app"
update_options=()
if [[ -n "${SHIOJI_RELEASE_NOTES_FILE:-}" ]]; then
  update_options+=(--notes-file "$SHIOJI_RELEASE_NOTES_FILE")
fi
env -u GOROOT go run ./cmd/shioji-update package --app "$app" --version "$version" --out "$out" \
  --base-url "https://github.com/senshinya/shioji/releases/download/v$version" \
  --public-key "$SHIOJI_UPDATE_PUBLIC_KEY" "${update_options[@]}"
stage=$(mktemp -d "$out/dmg-stage.XXXXXX")
trap 'rm -rf "$stage" "$worker_stage"' EXIT
ditto "$app" "$stage/Shioji.app"
ln -s /Applications "$stage/Applications"
cp internal/ui/third_party_notices.json "$stage/THIRD_PARTY_NOTICES.json"
dmg="$out/Shioji-$version-macos-arm64.dmg"
hdiutil create -volname Shioji -srcfolder "$stage" -format UDZO -ov "$dmg"
codesign --force --sign "$CODE_SIGN_IDENTITY" --timestamp "$dmg"
xcrun notarytool submit "$dmg" "${notary_options[@]}" --wait --timeout 30m --output-format json > "$out/dmg-notary.json"
python3 packaging/verify-notary.py "$out/dmg-notary.json"
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
codesign --verify --strict "$dmg"
spctl --assess --type execute --verbose=2 "$app"
shasum -a 256 "$dmg" | sed "s|$out/||" > "$dmg.sha256"
rm -f "$zip"
echo "$PWD/$dmg"
