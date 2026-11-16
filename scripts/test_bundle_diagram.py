import hashlib
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location("bundle_diagram", Path(__file__).with_name("bundle-diagram.py"))

class DiagramBundleTests(unittest.TestCase):
    def module(self):
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def archive(self, binary=b"pinned runtime", name="usvg"):
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w") as archive:
            archive.writestr(name, binary)
        return data.getvalue()

    def manifest(self, data, binary=b"pinned runtime"):
        return {"archive_sha256": hashlib.sha256(data).hexdigest(), "binary_sha256": hashlib.sha256(binary).hexdigest(), "entry": "usvg"}

    def test_installs_only_verified_runtime_as_executable(self):
        module = self.module()
        data = self.archive()
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / "usvg"
            module.install(data, self.manifest(data), target)
            self.assertEqual(target.read_bytes(), b"pinned runtime")
            self.assertTrue(target.stat().st_mode & 0o111)

    def test_archive_and_binary_mismatch_never_replace_runtime(self):
        module = self.module()
        for data, manifest in ((self.archive(), self.manifest(b"wrong")), (self.archive(b"wrong"), self.manifest(self.archive(b"wrong")))):
            with tempfile.TemporaryDirectory() as temp:
                target = Path(temp) / "usvg"
                target.write_bytes(b"existing")
                with self.assertRaises(ValueError):
                    module.install(data, manifest, target)
                self.assertEqual(target.read_bytes(), b"existing")

    def test_archive_paths_are_not_extracted(self):
        module = self.module()
        data = self.archive(name="../usvg")
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / "runtime/usvg"
            with self.assertRaises(ValueError):
                module.install(data, self.manifest(data), target)
            self.assertFalse(target.exists())
