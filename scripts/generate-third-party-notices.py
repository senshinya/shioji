#!/usr/bin/env python3
"""Bundle the licenses of Go modules actually used by the app and worker."""
import argparse
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
parser.add_argument('--mobile-output', type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
command = ['go', 'list', '-deps', '-json']
if args.mobile_output:
    output = subprocess.check_output(command + ['./cmd/shioji-worker'], cwd=root, text=True)
    output += subprocess.check_output(command + ['./cmd/shioji-mobile'], cwd=root, text=True,
                                      env={**os.environ, 'GOOS': 'ios', 'GOARCH': 'arm64', 'CGO_ENABLED': '1'})
else:
    output = subprocess.check_output(command + ['./cmd/shioji', './cmd/shioji-worker'], cwd=root, text=True)
decoder = json.JSONDecoder()
modules = {}
while output.strip():
    package, end = decoder.raw_decode(output.lstrip())
    output = output.lstrip()[end:]
    module = package.get('Module')
    if module and not module.get('Main'):
        modules[module['Path']] = module
notices = []
for name, module in sorted(modules.items()):
    directory = Path(module.get('Replace', module)['Dir'])
    files = sorted(p for p in directory.iterdir() if p.is_file() and p.suffix.lower() not in (".go", ".py", ".rs", ".js", ".ts") and p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')))
    if not files:
        raise SystemExit(f'Missing license for {name} in {directory}')
    text = '\n\n'.join(p.name + '\n\n' + p.read_text() for p in files)
    notices.append({'module': name, 'version': module.get('Version', ''), 'text': text})
# Static assets are not part of Go's module dependency graph.
if not args.mobile_output:
    notices.append({'module': 'Lucide icons (book-open, minimize-2)', 'version': '0.468.0', 'text': (root / 'third_party/lucide/LICENSE').read_text()})
    permission_flow = json.loads((root / 'native/permission-flow/Package.resolved').read_text())
    revision = next(pin['state']['revision'] for pin in permission_flow['pins'] if pin['identity'] == 'permissionflow')
    notices.append({'module': 'github.com/jaywcjlove/PermissionFlow', 'version': revision, 'text': (root / 'third_party/permissionflow/LICENSE').read_text()})
notices.append({'module': 'github.com/obsidianmd/pele', 'version': '1.13.0', 'text': (root / 'third_party/pele/LICENSE').read_text()})
if not args.mobile_output:
    notices.append({'module': 'github.com/linebender/resvg (usvg)', 'version': '0.50.0', 'text': (root / 'third_party/usvg/LICENSE-MIT').read_text()})
text = json.dumps(notices, ensure_ascii=False, indent=2) + '\n'
target = args.mobile_output or root / 'internal/ui/third_party_notices.json'
if args.check:
    if not target.exists() or target.read_text() != text:
        raise SystemExit('Third-party notices are stale; run scripts/generate-third-party-notices.py')
else:
    target.write_text(text)
print(f'PASS: {len(notices)} component notices match app and worker dependencies')
