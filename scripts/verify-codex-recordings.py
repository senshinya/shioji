#!/usr/bin/env python3
"""Verify fixture provenance/digests and each real incoming frame's pinned schema."""
import hashlib
import json
from pathlib import Path
import jsonschema

root = Path(__file__).resolve().parent.parent
protocol = root / 'design/protocol/codex-0.166.0'
validators = {kind: jsonschema.Draft7Validator(json.loads((protocol / (kind + '.schema.json')).read_text())) for kind in ('requests', 'notifications')}
count = 0
for path in sorted((root / 'tests/fixtures/codex').glob('*.jsonl')):
    meta = json.loads(path.with_suffix('.meta.json').read_text())
    data = path.read_bytes()
    assert meta['sha256'] == hashlib.sha256(data).hexdigest(), path
    entries = data.decode().splitlines()
    assert len(entries) == meta['events'], path
    for line in entries:
        entry = json.loads(line)
        if entry['direction'] != 'in':
            continue
        msg = entry['message']
        validators['requests' if 'id' in msg else 'notifications'].validate(msg)
        assert not msg['method'].startswith(('account/', 'auth/'))
        count += 1
print(f'PASS: {count} recorded incoming frames validate against pinned schemas; digests match')
