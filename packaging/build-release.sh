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
SHIOJI_APP_PATH="$app" bash packaging/build-dev.sh
signature=$(codesign --display --verbose=4 "$app" 2>&1)
if ! grep -q '^Authority=Developer ID Application:' <<< "$signature" || ! grep -q 'flags=.*runtime' <<< "$signature"; then
  echo 'Release requires a Developer ID Application signature with hardened runtime.' >&2
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
stage=$(mktemp -d "$out/dmg-stage.XXXXXX")
trap 'rm -rf "$stage"' EXIT
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
