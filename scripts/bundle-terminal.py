#!/usr/bin/env python3
"""Bundle the vendored terminal plugin's pinned native library, before signing."""
import hashlib
import json
import pathlib
import shutil
import sys
import urllib.request

root = pathlib.Path(__file__).resolve().parent.parent
manifest = json.loads((root / "third_party/mygo/plugins/terminal/mygo-plugin.json").read_text())
artifact = next(x for x in manifest["libraries"] if x["name"] == "libghostty-vt")["files"]["darwin-arm64"]
cache = pathlib.Path.home() / "Library/Caches/mygo/natives" / artifact["sha256"] / artifact["name"]
if not cache.exists():
    cache.parent.mkdir(parents=True, exist_ok=True)
    with urllib.request.urlopen(artifact["url"], timeout=60) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != artifact["sha256"]:
        raise SystemExit("Terminal library checksum mismatch")
    cache.write_bytes(data)
if hashlib.sha256(cache.read_bytes()).hexdigest() != artifact["sha256"]:
    raise SystemExit("Cached terminal library checksum mismatch")
destination = pathlib.Path(sys.argv[1]) / artifact["name"]
destination.parent.mkdir(parents=True, exist_ok=True)
shutil.copyfile(cache, destination)
destination.chmod(0o755)
print(destination)

licenses = destination.parent / "licenses"
licenses.mkdir(exist_ok=True)
shutil.copyfile(root / "third_party/ghostty-vt/LICENSE", licenses / "Ghostty-LICENSE.txt")
shutil.copyfile(root / "third_party/mygo/LICENSE", licenses / "MyGo-LICENSE.txt")
