"""Regression checks for frozen design verification with local-only archives."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


class FreezeVerification(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'docs').mkdir()
        (self.root / 'design/screenshots/history').mkdir(parents=True)
        shutil.copy2(Path(__file__).with_name('verify-design-freeze.py'), self.root / 'scripts/verify-design-freeze.py')
        self.source = self.root / 'design/prototype.html'
        self.source.write_text('frozen')
        self.archive = self.root / 'design/screenshots/history/old.png'
        self.archive.write_bytes(b'old screenshot')
        entries = [{'path': str(p.relative_to(self.root)), 'bytes': p.stat().st_size,
                    'sha256': hashlib.sha256(p.read_bytes()).hexdigest()} for p in (self.source, self.archive)]
        (self.root / 'docs/design-freeze-2026-11-03.json').write_text(json.dumps({'files': entries}))
        (self.root / '.gitignore').write_text('/design/screenshots/history/\n')
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True, capture_output=True)

    def verify(self, *args):
        return subprocess.run([sys.executable, str(self.root / 'scripts/verify-design-freeze.py'), *args], capture_output=True, text=True)

    def test_repository_verification_works_without_local_archive(self):
        self.archive.unlink()
        self.assertEqual(self.verify('--repository').returncode, 0)

    def test_repository_verification_rejects_missing_required_source(self):
        self.source.unlink()
        self.assertEqual(self.verify('--repository').returncode, 1)

    def test_repository_verification_rejects_changed_source(self):
        self.source.write_text('changed')
        self.assertEqual(self.verify('--repository').returncode, 1)

    def test_repository_verification_rejects_unexpected_source(self):
        (self.root / 'design/unfrozen.html').write_text('unexpected')
        self.assertEqual(self.verify('--repository').returncode, 1)

    def test_full_verification_still_requires_and_checks_archive(self):
        self.assertEqual(self.verify().returncode, 0)
        self.archive.write_bytes(b'changed screenshot')
        self.assertEqual(self.verify().returncode, 1)
        self.archive.unlink()
        self.assertEqual(self.verify().returncode, 1)


if __name__ == '__main__':
    unittest.main()
