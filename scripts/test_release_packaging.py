#!/usr/bin/env python3
"""Release entry-point admission checks; never touch the real signing identity."""
import os
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parent.parent

class ReleaseAdmissionTests(unittest.TestCase):
    def invoke(self, **values):
        env = dict(os.environ)
        for name in ("CODE_SIGN_IDENTITY", "NOTARY_PROFILE", "RELEASE_TAG"):
            env.pop(name, None)
        env.update(values)
        return subprocess.run(["bash", str(ROOT / "packaging/build-release.sh")], env=env, capture_output=True, text=True)

    def test_unsigned_release_is_rejected(self):
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("CODE_SIGN_IDENTITY", result.stderr)

    def test_notarization_is_mandatory(self):
        result = self.invoke(CODE_SIGN_IDENTITY="test-identity")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("NOTARY_PROFILE", result.stderr)

    def test_tag_must_match_version_file_before_build(self):
        result = self.invoke(CODE_SIGN_IDENTITY="test-identity", NOTARY_PROFILE="test-profile", RELEASE_TAG="v99.99.99")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match VERSION", result.stderr)

    def test_explicit_adhoc_identity_is_rejected(self):
        result = self.invoke(CODE_SIGN_IDENTITY="-", NOTARY_PROFILE="test-profile")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot be ad-hoc", result.stderr)

    def test_rejected_notarization_cannot_be_distributed(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            result_file = Path(directory) / "notary.json"
            for status, expected in (("Accepted", 0), ("Invalid", 1), ("In Progress", 1)):
                result_file.write_text('{"status": "' + status + '"}')
                result = subprocess.run(["python3", str(ROOT / "packaging/verify-notary.py"), str(result_file)], capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, status)

if __name__ == "__main__":
    unittest.main()
