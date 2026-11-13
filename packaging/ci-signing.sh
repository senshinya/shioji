#!/bin/bash
set -euo pipefail
umask 077
: "${BUILD_CERTIFICATE_BASE64:?Missing release environment BUILD_CERTIFICATE_BASE64 secret}"
: "${P12_PASSWORD:?Missing release environment P12_PASSWORD secret}"
: "${APPLE_ID:?Missing release environment APPLE_ID secret}"
: "${APPLE_APP_SPECIFIC_PASSWORD:?Missing release environment APPLE_APP_SPECIFIC_PASSWORD secret}"
: "${APPLE_TEAM_ID:?Missing release environment APPLE_TEAM_ID variable}"
: "${RUNNER_TEMP:?}"
: "${GITHUB_ENV:?}"
certificate="$RUNNER_TEMP/shioji-certificate.p12"
keychain="$RUNNER_TEMP/shioji-signing.keychain-db"
keychain_password=$(openssl rand -hex 32)
python3 - <<'PYCODE'
import base64, os
from pathlib import Path
p = Path(os.environ['RUNNER_TEMP']) / 'shioji-certificate.p12'
p.write_bytes(base64.b64decode(os.environ['BUILD_CERTIFICATE_BASE64'], validate=True))
p.chmod(0o600)
PYCODE
# Record the cleanup target before any keychain mutations can fail.
printf 'NOTARY_KEYCHAIN=%s\n' "$keychain" >> "$GITHUB_ENV"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$certificate" -P "$P12_PASSWORD" -k "$keychain" -t cert -f pkcs12 -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -k "$keychain_password" "$keychain" >/dev/null
security list-keychains -d user -s "$keychain" "$HOME/Library/Keychains/login.keychain-db"
identity=$(security find-identity -v -p codesigning "$keychain" | awk '/Developer ID Application:/ {print $2}')
if [[ ! "$identity" =~ ^[0-9A-F]{40}$ ]]; then
  echo 'Release certificate must contain exactly one valid Developer ID Application identity.' >&2
  exit 1
fi
xcrun notarytool store-credentials shioji-release --apple-id "$APPLE_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" --team-id "$APPLE_TEAM_ID" --keychain "$keychain" >/dev/null
printf 'CODE_SIGN_IDENTITY=%s\nNOTARY_PROFILE=shioji-release\n' "$identity" >> "$GITHUB_ENV"
rm -f "$certificate"
