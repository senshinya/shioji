"""Provision update signing while preserving an established release identity."""
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent


def configure_update_signing(directory, repo, gh):
    variables = json.loads(gh("variable", "list", "--repo", repo, "--env", "release", "--json", "name,value"))
    secrets = json.loads(gh("secret", "list", "--repo", repo, "--env", "release", "--json", "name"))
    remote = next((v["value"].strip() for v in variables if v["name"] == "SHIOJI_UPDATE_PUBLIC_KEY"), None)
    has_secret = any(v["name"] == "SHIOJI_UPDATE_PRIVATE_KEY" for v in secrets)
    private, public = directory / "update.key", directory / "update.pub"
    if remote is None and has_secret:
        raise SystemExit("Remote signing identity is incomplete. Restore its public key; no credentials were changed.")
    if remote is not None and (not private.is_file() or not public.is_file()):
        raise SystemExit("GitHub already has an update identity. Restore its original local key backup; no new key was generated.")
    if remote is not None and public.read_text().strip() != remote:
        raise SystemExit("Local update key does not match GitHub. Restore the original key backup; no credentials were changed.")
    if not private.exists() and not public.exists():
        subprocess.run(["go", "run", "./cmd/shioji-update", "keygen", "--out", str(directory)], cwd=ROOT, check=True)
    if not private.is_file() or not public.is_file():
        raise SystemExit("The update key pair is incomplete. Restore its backup; do not replace a release identity.")
    if private.stat().st_mode & 0o077:
        raise SystemExit("Update private key must be readable only by its owner (chmod 600).")
    key, pub = private.read_bytes(), public.read_text().strip()
    result = subprocess.run(["go", "run", "./cmd/shioji-update", "check-key", "--public-key", pub], cwd=ROOT, env={**os.environ, "SHIOJI_UPDATE_PRIVATE_KEY": key.decode().strip()}, capture_output=True)
    if result.returncode:
        raise SystemExit("The update signing key pair is invalid; nothing was uploaded.")
    # Public identity is published first: a partial setup remains recoverable
    # only using this same local key, rather than generating another identity.
    gh("variable", "set", "SHIOJI_UPDATE_PUBLIC_KEY", "--repo", repo, "--env", "release", "--body", pub)
    gh("secret", "set", "SHIOJI_UPDATE_PRIVATE_KEY", "--repo", repo, "--env", "release", value=key)
