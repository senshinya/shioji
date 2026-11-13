#!/usr/bin/env python3
"""Safely provision the selected certificate or interactively enter notarization credentials."""
import argparse
import base64
import getpass
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("part", choices=["certificate", "notarization"])
parser.add_argument("--repo", default="senshinya/shioji")
parser.add_argument("--identity", help="Public SHA-1 fingerprint, only needed if more than one Developer ID identity exists")
args = parser.parse_args()

def gh(*command, value=None):
    result = subprocess.run(["gh", *command], input=value, capture_output=True)
    if result.returncode:
        raise SystemExit("GitHub configuration failed; check gh authentication and repository administration access.")
    return result.stdout

def secret(name, value):
    gh("secret", "set", name, "--repo", args.repo, "--env", "release", value=value)
    print("Configured encrypted release secret:", name)

gh("api", "--method", "PUT", "repos/" + args.repo + "/environments/release", "--silent")
if args.part == "certificate":
    identities = subprocess.check_output(["security", "find-identity", "-v", "-p", "codesigning"], text=True)
    matches = re.findall(r'([0-9A-F]{40}) "Developer ID Application:[^\n]+\(([A-Z0-9]{10})\)"', identities)
    if args.identity:
        matches = [entry for entry in matches if entry[0] == args.identity.upper()]
    if len(matches) != 1:
        raise SystemExit("Select exactly one valid Developer ID Application identity with --identity.")
    fingerprint, team = matches[0]
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix="shioji-signing-") as directory:
        private = Path(directory)
        password = secrets.token_urlsafe(48)
        password_file, certificate = private / "password", private / "identity.p12"
        password_file.write_text(password)
        result = subprocess.run(["swift", str(ROOT / "packaging/export-signing-identity.swift"), fingerprint, str(password_file), str(certificate)])
        if result.returncode:
            raise SystemExit("macOS blocked private-key export. Run this command in the Mac's Terminal and allow the system prompt. No signing secret was uploaded.")
        # A GitHub secret is at most 48 KiB. This export contains one identity.
        encoded = base64.b64encode(certificate.read_bytes())
        if len(encoded) > 48 * 1024:
            raise SystemExit("The selected certificate exceeds GitHub's secret size limit.")
        secret("BUILD_CERTIFICATE_BASE64", encoded)
        secret("P12_PASSWORD", password.encode())
        gh("variable", "set", "APPLE_TEAM_ID", "--repo", args.repo, "--env", "release", "--body", team)
    print("Temporary export removed. The original keychain identity was left intact.")
else:
    print("Use the Apple Account enrolled in your Developer Program.")
    print("Generate an app-specific password at https://account.apple.com/account/manage — never enter your normal Apple password here.")
    apple_id = input("Apple ID email: ").strip()
    password = getpass.getpass("App-specific password (hidden): ").strip()
    if not apple_id or not re.fullmatch(r"[a-z]{4}(?:-[a-z]{4}){3}", password):
        raise SystemExit("An Apple ID and a valid app-specific password are required; nothing was uploaded.")
    secret("APPLE_ID", apple_id.encode())
    secret("APPLE_APP_SPECIFIC_PASSWORD", password.encode())
    print("Notarization credentials configured; the release workflow validates them with Apple before building.")
