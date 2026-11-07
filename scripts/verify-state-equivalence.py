"""Replay identical observable-state probes against a baseline and this checkout."""

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROBES = [
    "internal/testutil/state_trace.go",
    "internal/app/state_trace_test.go",
    "internal/store/state_trace_test.go",
    "internal/harness/codex/state_trace_test.go",
    "internal/ui/state_trace_test.go",
]
PACKAGES = ["./internal/app", "./internal/store", "./internal/harness/codex", "./internal/ui"]
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
TIME = re.compile(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)")
TEMP = re.compile(r"/(?:private/)?(?:var/folders|tmp)/[^\s\"<>]*?/(TestStateTrace[^/]*?)(?:\d+)/(\d+)")


def normalize_observation(value, identities):
    # Discover identifier fields before replacing references. A UUID-looking
    # literal in prose is not automatically a volatile identifier.
    def identify(v):
        if isinstance(v, dict):
            for key, child in v.items():
                for match in UUID.finditer(key):
                    identities.setdefault(match[0], f"<id:{len(identities)}>")
                if isinstance(child, str) and (key.lower().endswith("id") or key.lower() in {"generation", "forkedfrom", "token"}):
                    for match in UUID.finditer(child):
                        identities.setdefault(match[0], f"<id:{len(identities)}>")
                identify(child)
        elif isinstance(v, list):
            for child in v:
                identify(child)

    identify(value)

    def string(s):
        s = TEMP.sub(r"/<test-temp>/\1/\2", s)
        return UUID.sub(lambda m: identities.get(m[0], m[0]), s)

    def normalize(v, field=None):
        if isinstance(v, str):
            # Only Store's receipt time is generated from the replay's wall
            # clock. Fixed harness start/completion timestamps stay exact.
            return "<wall-clock>" if field == "sentAt" and TIME.fullmatch(v) else string(v)
        if isinstance(v, list):
            return [normalize(x) for x in v]
        if isinstance(v, dict):
            return {string(k): normalize(x, k) for k, x in v.items()}
        return v

    return normalize(value)


def replay(tree):
    proc = subprocess.run(["go", "test", "-json", *PACKAGES, "-run", "^TestStateTrace", "-count=1"], cwd=tree, text=True, capture_output=True)
    if proc.returncode:
        for line in proc.stdout.splitlines():
            event = json.loads(line)
            output = event.get("Output", "")
            if event.get("Action") == "fail" or output and "STATE_EQ" not in output and len(output) < 500:
                print(output.rstrip() or event)
        sys.stderr.write(proc.stderr)
        raise RuntimeError(f"state replay failed in {tree}")
    records = {}
    identities = {}
    buffers = {}
    for line in proc.stdout.splitlines():
        event = json.loads(line)
        stream = (event["Package"], event.get("Test", ""))
        output = buffers.pop(stream, "") + event.get("Output", "")
        if not output.endswith("\n"):
            buffers[stream] = output
            continue
        if "STATE_EQ " not in output:
            continue
        value = json.loads(output.split("STATE_EQ ", 1)[1])
        key = (event["Package"], value["test"], value["step"])
        ids = identities.setdefault(key[:2], {})

        if key in records:
            raise RuntimeError(f"duplicate observation {key}")
        records[key] = normalize_observation(value["value"], ids)
    if not records:
        raise RuntimeError("no state observations produced")
    return records


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-tree", type=Path, required=True, help="clean checkout of the pre-refactor revision")
    args = parser.parse_args()
    baseline = args.baseline_tree.resolve()
    if baseline == ROOT:
        parser.error("baseline and candidate must be different checkouts")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=baseline, text=True).strip()
    expected = "6e8bcb0070e490c5ef8cf2a9a4ce6572b3d1a638"
    if revision != expected:
        parser.error(f"baseline must be {expected}, got {revision}")
    dirty = subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=normal"], cwd=baseline, text=True)
    if dirty.strip():
        parser.error("baseline must be clean before temporary probe injection")
    created = []
    try:
        for name in PROBES:
            target = baseline / name
            if target.exists():
                raise RuntimeError(f"refusing to overwrite baseline file: {target}")
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((ROOT / name).read_bytes())
            created.append(target)
        before = replay(baseline)
        after = replay(ROOT)
    finally:
        for path in created:
            path.unlink(missing_ok=True)
    missing = before.keys() ^ after.keys()
    differing = [key for key in before.keys() & after.keys() if before[key] != after[key]]
    if missing or differing:
        for key in sorted(missing):
            print("missing observation:", key)
        for key in sorted(differing)[:8]:
            print("different observation:", key)
            print("baseline:", json.dumps(before[key], ensure_ascii=False, sort_keys=True))
            print("candidate:", json.dumps(after[key], ensure_ascii=False, sort_keys=True))
        return 1
    print(f"PASS: {len(before)} observable checkpoints match baseline {revision[:7]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
