"""Exercise release publication with a controlled gh executable, no network."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import shutil
import hashlib

ROOT = Path(__file__).resolve().parent.parent


def release_files(base, payload=b"archive"):
    digest = hashlib.sha256(payload).hexdigest()
    name = f"Shioji-1.2.3-darwin-arm64-{digest}.tar.gz"
    (base / name).write_bytes(payload)
    (base / "update-darwin-arm64.json").write_text(json.dumps({"version": "1.2.3", "url": f"https://github.com/test/repo/releases/download/v1.2.3/{name}", "size": len(payload), "signature": "test-signature"}))
    return name


class AppUpdatePublicationTests(unittest.TestCase):
    def test_manifest_is_last_and_prereleases_do_not_advertise_updates(self):
        for prerelease in (False, True):
            with self.subTest(prerelease=prerelease), tempfile.TemporaryDirectory() as directory:
                base = Path(directory)
                executable = base / "gh"
                executable.write_text("#!/usr/bin/env python3\nimport os,sys,json\nfrom pathlib import Path\np=Path(os.environ['GH_TRACE'])\nwith p.open('a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\nif sys.argv[1:3]==['release','view']: print(json.dumps({'tagName':'v1.2.3','isDraft':False,'isPrerelease':os.environ['PRE']=='1'}))\n")
                executable.chmod(0o755)
                for name in ("Shioji-1.2.3-macos-arm64.dmg", "Shioji-1.2.3-macos-arm64.dmg.sha256"):
                    (base / name).write_text("artifact")
                archive = release_files(base)
                trace = base / "trace"
                env = {**os.environ, "PATH": str(base)+os.pathsep+os.environ["PATH"], "GH_TRACE": str(trace), "PRE": str(int(prerelease))}
                result = subprocess.run(["python3", str(ROOT / "scripts/publish-app-update.py"), "--repo", "test/repo", "--tag", "v1.2.3", "--directory", str(base)], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                calls = [json.loads(line) for line in trace.read_text().splitlines()]
                uploads = [call for call in calls if call[:2] == ["release", "upload"]]
                self.assertEqual(len(uploads), 1 if prerelease else 2)
                if not prerelease:
                    self.assertIn(str(base / archive), uploads[0])
                    self.assertNotIn(str(base / "update-darwin-arm64.json"), uploads[0])
                    self.assertIn(str(base / "update-darwin-arm64.json"), uploads[-1])

    def test_failed_rerun_keeps_old_manifest_archive_valid(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            remote = base / "remote"
            remote.mkdir()
            for name in ("Shioji-1.2.3-macos-arm64.dmg", "Shioji-1.2.3-macos-arm64.dmg.sha256"):
                (base / name).write_text("artifact")
            old = release_files(base, b"original signed archive")
            shutil.copy(base / old, remote / old)
            shutil.copy(base / "update-darwin-arm64.json", remote / "update-darwin-arm64.json")
            before = (remote / "update-darwin-arm64.json").read_bytes()
            new = release_files(base, b"rebuilt signed archive")
            executable = base / "gh"
            executable.write_text("#!/usr/bin/env python3\nimport sys,json,shutil,os\nfrom pathlib import Path\nif sys.argv[1:3]==['release','view']: print(json.dumps({'tagName':'v1.2.3','isDraft':False,'isPrerelease':False}))\nelse:\n for arg in sys.argv[3:]:\n  p=Path(arg)\n  if p.is_file():\n   if p.name=='update-darwin-arm64.json': sys.exit(1)\n   shutil.copy(p,Path(os.environ['REMOTE'])/p.name)\n")
            executable.chmod(0o755)
            env = {**os.environ, "PATH": str(base)+os.pathsep+os.environ["PATH"], "REMOTE": str(remote)}
            result = subprocess.run(["python3", str(ROOT / "scripts/publish-app-update.py"), "--repo", "test/repo", "--tag", "v1.2.3", "--directory", str(base)], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(before, (remote / "update-darwin-arm64.json").read_bytes())
            self.assertEqual((remote / old).read_bytes(), b"original signed archive")
            self.assertEqual((remote / new).read_bytes(), b"rebuilt signed archive")

    def test_missing_update_keys_rejected_before_build(self):
        env = dict(os.environ, CODE_SIGN_IDENTITY="test-identity", NOTARY_PROFILE="test-profile")
        for name in ("SHIOJI_UPDATE_PUBLIC_KEY", "SHIOJI_UPDATE_PRIVATE_KEY", "RELEASE_TAG"):
            env.pop(name, None)
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            (base / "packaging/assets").mkdir(parents=True)
            shutil.copy(ROOT / "packaging/build-release.sh", base / "packaging/build-release.sh")
            shutil.copy(ROOT / "VERSION", base / "VERSION")
            (base / "packaging/assets/Shioji.icns").write_text("fixture")
            (base / "packaging/build-dev.sh").write_text("echo 'UNEXPECTED BUILD' >&2\nexit 99\n")
            result = subprocess.run(["bash", str(base / "packaging/build-release.sh")], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("SHIOJI_UPDATE_PUBLIC_KEY", result.stderr)
            self.assertNotIn("UNEXPECTED BUILD", result.stderr)


if __name__ == "__main__":
    unittest.main()
