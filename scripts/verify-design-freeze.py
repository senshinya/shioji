"""Verify the frozen design source without modifying it."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "docs/design-freeze-2026-11-03.json"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', action='store_true', help='verify the Git-visible design subset; historical ignored assets remain a local archive')
    args = parser.parse_args()
    manifest = json.loads(MANIFEST.read_text())
    expected = {entry["path"]: entry for entry in manifest["files"]}
    actual = {
        path.relative_to(ROOT).as_posix(): path
        for path in (ROOT / "design").rglob("*")
        if path.is_file() and path.name != ".DS_Store"
    }
    if args.repository:
        # Filter the expected list too, so absent required files are still errors.
        names = sorted(expected.keys() | actual.keys())
        check = subprocess.run(
            ['git', '-C', str(ROOT), 'check-ignore', '--no-index', '-z', '--stdin'],
            input=b''.join(name.encode() + b'\0' for name in names), capture_output=True,
        )
        if check.returncode not in (0, 1):
            print('Repository verification requires a working Git repository.', file=sys.stderr)
            return 1
        ignored = set(check.stdout.decode().strip('\0').split('\0'))
        expected = {name: entry for name, entry in expected.items() if name not in ignored}
        actual = {name: path for name, path in actual.items() if name not in ignored}
    failures = [f"missing: {name}" for name in sorted(expected.keys() - actual.keys())]
    failures += [f"added: {name}" for name in sorted(actual.keys() - expected.keys())]
    for name in sorted(expected.keys() & actual.keys()):
        data = actual[name].read_bytes()
        entry = expected[name]
        if len(data) != entry["bytes"] or hashlib.sha256(data).hexdigest() != entry["sha256"]:
            failures.append(f"changed: {name}")
    if failures:
        print("\n".join(failures))
        return 1
    scope = "repository-visible" if args.repository else "complete local archive"
    print(f"PASS: {len(expected)} frozen design files match the 2026-11-03 baseline ({scope})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
