#!/usr/bin/env python3
"""Require an accepted Apple notarization response before distributing."""
import json
from pathlib import Path
import sys
result = json.loads(Path(sys.argv[1]).read_text())
if result.get("status") != "Accepted":
    raise SystemExit("Apple notarization was not accepted; inspect the local result and notarytool log.")
