#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=8.30.1
tool_dir=$(mktemp -d)
trap 'rm -rf "$tool_dir"' EXIT
scanner=$(command -v gitleaks || true)
if [[ -z "$scanner" ]]; then
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) platform=darwin_arm64 ;;
  Linux-x86_64) platform=linux_x64 ;;
  *) echo 'Install gitleaks on this platform before scanning.' >&2; exit 1 ;;
esac
archive="gitleaks_${version}_${platform}.tar.gz"
base="https://github.com/gitleaks/gitleaks/releases/download/v$version"
curl --fail --silent --show-error --location "$base/$archive" -o "$tool_dir/$archive"
curl --fail --silent --show-error --location "$base/gitleaks_${version}_checksums.txt" -o "$tool_dir/checksums"
python3 - "$tool_dir" "$archive" <<'PYCODE'
import hashlib, pathlib, sys, tarfile
root, name = pathlib.Path(sys.argv[1]), sys.argv[2]
expected = next(line.split()[0] for line in (root / 'checksums').read_text().splitlines() if line.endswith(name))
if hashlib.sha256((root / name).read_bytes()).hexdigest() != expected:
    raise SystemExit('gitleaks archive checksum mismatch')
with tarfile.open(root / name) as archive:
    data = archive.extractfile('gitleaks').read()
    (root / 'gitleaks').write_bytes(data)
    (root / 'gitleaks').chmod(0o755)
PYCODE
scanner="$tool_dir/gitleaks"
fi
"$scanner" git . --log-opts=--all --redact=100
# Include uncommitted and newly added source while excluding ignored runtime data.
python3 - "$tool_dir/source" <<'PYCODE'
from pathlib import Path
import shutil, subprocess, sys
root = Path(sys.argv[1])
paths = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z']).split(b'\0')
for raw in paths:
    if not raw:
        continue
    path = Path(raw.decode())
    if not path.is_file() or path.is_symlink():
        continue
    target = root / path
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(path, target)
PYCODE
"$scanner" dir "$tool_dir/source" --redact=100
