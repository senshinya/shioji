#!/usr/bin/env python3
"""Bundle the licenses of Go modules actually used by the app and worker."""
import argparse
import json
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
output = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/shioji', './cmd/shioji-worker'], cwd=root, text=True)
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
    files = sorted(p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')))
    if not files:
        raise SystemExit(f'Missing license for {name} in {directory}')
    text = '\n\n'.join(p.name + '\n\n' + p.read_text() for p in files)
    notices.append({'module': name, 'version': module.get('Version', ''), 'text': text})
# Static assets are not part of Go's module dependency graph.
notices.append({'module': 'Lucide icons (book-open, minimize-2)', 'version': '0.468.0', 'text': (root / 'third_party/lucide/LICENSE').read_text()})
text = json.dumps(notices, ensure_ascii=False, indent=2) + '\n'
target = root / 'internal/ui/third_party_notices.json'
if args.check:
    if not target.exists() or target.read_text() != text:
        raise SystemExit('Third-party notices are stale; run scripts/generate-third-party-notices.py')
else:
    target.write_text(text)
print(f'PASS: {len(notices)} component notices match app and worker dependencies')
