#!/usr/bin/env python3
"""Fetch the pinned arm64 usvg runtime at build time, before app signing."""
import hashlib
import io
import json
from pathlib import Path
import shutil
import sys
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def install(data, manifest, target):
    if hashlib.sha256(data).hexdigest() != manifest["archive_sha256"]:
        raise ValueError("Diagram runtime archive checksum mismatch")
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        try:
            binary = archive.read(manifest["entry"])
        except KeyError:
            raise ValueError("Diagram runtime archive is missing usvg") from None
    if hashlib.sha256(binary).hexdigest() != manifest["binary_sha256"]:
        raise ValueError("Diagram runtime binary checksum mismatch")
    target.parent.mkdir(parents=True, exist_ok=True)
    staging = target.with_name(target.name + ".partial")
    staging.write_bytes(binary)
    staging.chmod(0o755)
    staging.replace(target)


def main():
    manifest = json.loads((ROOT / "third_party/usvg/runtime.json").read_text())
    cache = ROOT / "build/diagram-runtime/usvg"
    if not cache.exists() or hashlib.sha256(cache.read_bytes()).hexdigest() != manifest["binary_sha256"]:
        with urllib.request.urlopen(manifest["url"], timeout=60) as response:
            data = response.read()
        install(data, manifest, cache)
    if len(sys.argv) > 1:
        destination = Path(sys.argv[1])
        destination.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(cache, destination / "usvg")
        (destination / "usvg").chmod(0o755)
        licenses = destination / "licenses"
        licenses.mkdir(exist_ok=True)
        shutil.copyfile(ROOT / "third_party/usvg/LICENSE-MIT", licenses / "usvg-LICENSE.txt")
        shutil.copyfile(ROOT / "third_party/pele/LICENSE", licenses / "Pele-LICENSE.txt")
        print(destination / "usvg")
    else:
        print(cache)


if __name__ == "__main__":
    main()
