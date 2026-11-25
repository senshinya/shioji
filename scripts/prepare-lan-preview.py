"""Install the HTTP UUID bootstrap in the Ryugi release actually serving a port.

Run AFTER the final artifacts start command: its launcher can install a new
release. Templates are read per HTTP request, so this needs no server restart.
"""
from pathlib import Path
import argparse
import os
import re
import shlex
import subprocess

BOOTSTRAP = '''<script data-shioji-lan-uuid>
if (typeof globalThis.crypto?.randomUUID !== 'function' &&
    typeof globalThis.crypto?.getRandomValues === 'function') {
  Object.defineProperty(globalThis.crypto, 'randomUUID', {
    configurable: true,
    writable: true,
    value: function randomUUID() {
      const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
      bytes[6] = (bytes[6] & 15) | 64;
      bytes[8] = (bytes[8] & 63) | 128;
      const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
      return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    }
  });
}
// A LAN HTTP request can omit Sec-Fetch-Site. Repair a stale server-supplied
// parent origin only when the browser proves the actual parent is same-origin.
// Cross-origin cloud embedding keeps its configured origin and access checks.
try {
  if (typeof window !== 'undefined' && window.parent !== window &&
      window.parent.location.origin === window.location.origin) {
    document.documentElement.dataset.ryugiEmbedParent = window.location.origin;
  }
} catch (_) {}
</script>'''


def running_viewer(port):
    pids = subprocess.run(["lsof", "-nP", "-t", f"-iTCP:{port}", "-sTCP:LISTEN"], capture_output=True, text=True, check=True).stdout.split()
    for pid in dict.fromkeys(pids):
        command = subprocess.run(["ps", "-ww", "-p", pid, "-o", "command="], capture_output=True, text=True, check=True).stdout.strip()
        # Newer releases overwrite argv with a versioned hub title on macOS.
        hub = re.match(r"^Ryugi hub \((\d+\.\d+\.\d+(?:[-+][\w.-]+)?)\) --artifacts-server-id=[\da-f]+", command)
        if hub:
            cache = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache")
            dist = cache / "ryugi" / "artifacts" / hub[1] / "node_modules" / "ryugi-artifacts" / "dist"
            # The native daemon no longer ships server.cjs.
            if (dist / "viewer" / "index.html").is_file():
                return dist / "viewer"
        for argument in shlex.split(command):
            server = Path(argument)
            if server.name == "server.cjs" and server.parent.parent.name == "ryugi-artifacts":
                return server.parent / "viewer"
    raise RuntimeError(f"Port {port} is not served by a Ryugi artifacts runtime")


def patch_viewer(viewer):
    if not (viewer / "index.html").is_file():
        raise RuntimeError(f"Viewer entry missing: {viewer}")
    for name in ("index.html", "preview.html"):
        path = viewer / name
        if not path.exists():
            continue
        content = path.read_text()
        # Replace an earlier patch too; never trust an old marker's ordering.
        clean = re.sub(r"<script data-shioji-lan-uuid>.*?</script>\s*", "", content, flags=re.S)
        head = re.search(r"<head\b[^>]*>", clean, flags=re.I)
        if head is None:
            raise RuntimeError(f"Unexpected viewer template: {path}")
        patched = clean[:head.end()] + "\n" + BOOTSTRAP + "\n" + clean[head.end():].lstrip("\n")
        if patched != content:
            path.write_text(patched)
        print(f"LAN HTTP bootstrap ready: {path}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    patch_viewer(running_viewer(args.port))
