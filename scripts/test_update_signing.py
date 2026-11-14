import json
from pathlib import Path
import tempfile
import unittest

from scripts.update_signing import configure_update_signing


class UpdateSigningTests(unittest.TestCase):
    def test_first_setup_and_matching_rerun_preserve_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            state = {"public": None, "secret": False, "writes": 0}
            def gh(*args, value=None):
                if args[:2] == ("variable", "list"):
                    return json.dumps([] if state["public"] is None else [{"name": "SHIOJI_UPDATE_PUBLIC_KEY", "value": state["public"]}]).encode()
                if args[:2] == ("secret", "list"):
                    return json.dumps([{"name": "SHIOJI_UPDATE_PRIVATE_KEY"}] if state["secret"] else []).encode()
                state["writes"] += 1
                if args[:2] == ("variable", "set"):
                    state["public"] = args[-1]
                if args[:2] == ("secret", "set"):
                    state["secret"] = True
                return b""
            path = Path(directory) / "keys"
            configure_update_signing(path, "test/repo", gh)
            before = (path / "update.key").read_bytes()
            configure_update_signing(path, "test/repo", gh)
            self.assertEqual(before, (path / "update.key").read_bytes())
            self.assertEqual(state["public"], (path / "update.pub").read_text())
            established = state["public"]
            state["public"] = "a-different-established-identity"
            writes = state["writes"]
            with self.assertRaisesRegex(SystemExit, "restore|Restore|match"):
                configure_update_signing(path, "test/repo", gh)
            self.assertEqual(state["writes"], writes)
            self.assertNotEqual(established, state["public"])

    def test_missing_local_keys_never_replace_remote_identity(self):
        for public, secret in (("established-identity", True), (None, True)):
            with self.subTest(public=public), tempfile.TemporaryDirectory() as directory:
                writes = []
                def gh(*args, value=None):
                    if args[:2] == ("variable", "list"):
                        return json.dumps([] if public is None else [{"name": "SHIOJI_UPDATE_PUBLIC_KEY", "value": public}]).encode()
                    if args[:2] == ("secret", "list"):
                        return json.dumps([{"name": "SHIOJI_UPDATE_PRIVATE_KEY"}] if secret else []).encode()
                    writes.append(args)
                    return b""
                path = Path(directory) / "keys"
                with self.assertRaises(SystemExit):
                    configure_update_signing(path, "test/repo", gh)
                self.assertFalse(path.exists())
                self.assertFalse(writes)
