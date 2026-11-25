"""Exercise the running preview's bootstrap, including after a tool upgrade."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("prepare-lan-preview.py")


class LANPreviewTests(unittest.TestCase):
    def test_patches_native_hub_release_without_a_node_server(self):
        with tempfile.TemporaryDirectory() as tmp:
            cache = Path(tmp)
            dist = cache / "ryugi/artifacts/1.102.7/node_modules/ryugi-artifacts/dist"
            viewer = dist / "viewer"
            viewer.mkdir(parents=True)
            entry = viewer / "index.html"
            entry.write_text('<head><script type="module">crypto.randomUUID()</script></head>')
            server = cache / "native-hub-fixture.cjs"
            server.write_text(
                "process.title='Ryugi hub (1.102.7) --artifacts-server-id=0123456789abcdef';"
                "const s=require('node:http').createServer((q,r)=>r.end('ok'));"
                "s.listen(0,'127.0.0.1',()=>console.log(s.address().port));"
            )
            with subprocess.Popen(["node", str(server)], stdout=subprocess.PIPE, text=True) as process:
                try:
                    port = process.stdout.readline().strip()
                    result = subprocess.run(
                        [sys.executable, str(SCRIPT), "--port", port],
                        env={**os.environ, "XDG_CACHE_HOME": str(cache)},
                        capture_output=True, text=True,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertLess(entry.read_text().index("data-shioji-lan-uuid"), entry.read_text().index('type="module"'))
                finally:
                    process.terminate()

    def test_patches_the_actual_running_release_after_an_upgrade(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            # A patched old release must not hide an unpatched newer server.
            for version in ("1.70.2", "1.74.0"):
                dist = root / version / "node_modules/ryugi-artifacts/dist"
                viewer = dist / "viewer"
                viewer.mkdir(parents=True)
                for name in ("index.html", "preview.html"):
                    (viewer / name).write_text('<!doctype html><head><script type="module">crypto.randomUUID()</script></head><body></body>')
            old_entry = root / "1.70.2/node_modules/ryugi-artifacts/dist/viewer/index.html"
            old_entry.write_text('<head><script data-shioji-lan-uuid>/* old release patched */</script></head>')
            current = root / "1.74.0/node_modules/ryugi-artifacts/dist"
            server = current / "server.cjs"
            server.write_text("const s=require('node:http').createServer((q,r)=>r.end('ok'));s.listen(0,'127.0.0.1',()=>console.log(s.address().port));")
            process = subprocess.Popen(["node", str(server)], stdout=subprocess.PIPE, text=True)
            try:
                port = process.stdout.readline().strip()
                result = subprocess.run([sys.executable, str(SCRIPT), "--port", port], capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                for name in ("index.html", "preview.html"):
                    html = (current / "viewer" / name).read_text()
                    self.assertLess(html.index("data-shioji-lan-uuid"), html.index('type="module"'))
                    bootstrap = html.split("<script data-shioji-lan-uuid>")[1].split("</script>")[0]
                    # Real crypto RNG, v4 bits, uniqueness, and native preservation.
                    probe = """const vm=require('node:vm'),assert=require('node:assert/strict');
const source=JSON.parse(process.argv[1]);const crypto={getRandomValues:require('node:crypto').webcrypto.getRandomValues.bind(require('node:crypto').webcrypto)};
const context=vm.createContext({crypto});vm.runInContext(source,context);
const values=Array.from({length:1000},()=>crypto.randomUUID());assert.equal(new Set(values).size,1000);
assert(values.every(v=>/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(v)));
const native=()=> 'native';const secure=vm.createContext({crypto:{randomUUID:native}});vm.runInContext(source,secure);assert.equal(secure.crypto.randomUUID,native);
// Insecure LAN requests omit Sec-Fetch-Site; a same-origin parent must not
// inherit a cloud/localhost target from that missing header.
const origin='http://192.168.7.131:27320';
const document={documentElement:{dataset:{ryugiEmbedParent:'https://cloud.example'}}};
const child={location:{origin},parent:{location:{origin}}};
vm.runInContext(source,vm.createContext({crypto:{randomUUID:native},window:child,document}));
assert.equal(document.documentElement.dataset.ryugiEmbedParent,origin);
const foreign={documentElement:{dataset:{ryugiEmbedParent:'https://cloud.example'}}};
const cross={location:{origin},parent:{get location(){throw Error('cross origin')}}};
vm.runInContext(source,vm.createContext({crypto:{randomUUID:native},window:cross,document:foreign}));
assert.equal(foreign.documentElement.dataset.ryugiEmbedParent,'https://cloud.example');
const top={location:{origin}};top.parent=top;
vm.runInContext(source,vm.createContext({crypto:{randomUUID:native},window:top,document:foreign}));
assert.equal(foreign.documentElement.dataset.ryugiEmbedParent,'https://cloud.example');
"""
                    subprocess.run(["node", "-e", probe, json.dumps(bootstrap)], check=True)
                again = subprocess.run([sys.executable, str(SCRIPT), "--port", port], capture_output=True, text=True)
                self.assertEqual(again.returncode, 0, again.stderr)
                self.assertEqual((current / "viewer/index.html").read_text().count("data-shioji-lan-uuid"), 1)
            finally:
                process.terminate()
                process.wait(timeout=5)
                process.stdout.close()


if __name__ == "__main__":
    unittest.main()
