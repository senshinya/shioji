#!/usr/bin/env python3
"""Publish verified release assets; the auto-update manifest is always last."""
import argparse
import json
import hashlib
from pathlib import Path
import re
import subprocess
from urllib.parse import urlparse


def publish(repo, tag, directory):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag):
        raise SystemExit("Release tag must be v<major.minor.patch>.")
    metadata = json.loads(subprocess.check_output(["gh", "release", "view", tag, "--repo", repo, "--json", "tagName,isDraft,isPrerelease"], text=True))
    if metadata["tagName"] != tag or metadata["isDraft"]:
        raise SystemExit("Only the selected published release may receive update assets.")
    version = tag[1:]
    names = [f"Shioji-{version}-macos-arm64.dmg", f"Shioji-{version}-macos-arm64.dmg.sha256"]
    if not metadata["isPrerelease"]:
        manifest = json.loads((Path(directory) / "update-darwin-arm64.json").read_text())
        url = urlparse(manifest["url"])
        name = Path(url.path).name
        if manifest["version"] != version or not re.fullmatch(rf"Shioji-{re.escape(version)}-darwin-arm64-[a-f0-9]{{64}}\.tar\.gz", name):
            raise SystemExit("Update manifest must name an immutable archive for this release.")
        if manifest["url"] != f"https://github.com/{repo}/releases/download/{tag}/{name}":
            raise SystemExit("Update archive URL does not belong to this release.")
        archive = Path(directory) / name
        with archive.open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        if not name.endswith(f"-{digest}.tar.gz") or archive.stat().st_size != manifest["size"]:
            raise SystemExit("Archive does not match its immutable name or manifest size.")
        names += [name, "update-darwin-arm64.json"]
    paths = [Path(directory) / name for name in names]
    if not all(path.is_file() and path.stat().st_size for path in paths):
        raise SystemExit("Required release assets are missing; nothing was uploaded.")
    prefix = ["gh", "release", "upload", tag, "--repo", repo, "--clobber"]
    if metadata["isPrerelease"]:
        subprocess.run([*prefix, *(str(p) for p in paths)], check=True)
    else:
        subprocess.run([*prefix, *(str(p) for p in paths[:-1])], check=True)
        subprocess.run([*prefix, str(paths[-1])], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--directory", required=True)
    args = parser.parse_args()
    publish(args.repo, args.tag, args.directory)
