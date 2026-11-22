"""Exercise worker release selection against a real Go dependency graph and Git history."""
import subprocess
import tempfile
import unittest
from pathlib import Path

from worker_release import matching_tags


class WorkerReleaseTests(unittest.TestCase):
    def git(self, *args):
        subprocess.run(["git", "-C", str(self.root), *args], check=True, capture_output=True)

    def commit(self, tag=None):
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "fixture")
        if tag:
            self.git("tag", tag)

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.git("init")
        (self.root / "go.mod").write_text("module fixture\n\ngo 1.24\n")
        worker = self.root / "cmd/shioji-worker"
        worker.mkdir(parents=True)
        (worker / "main.go").write_text('package main\nimport "fixture/shared"\nfunc main() { println(shared.Version) }\n')
        (self.root / "shared").mkdir()
        self.shared = self.root / "shared/version.go"
        self.shared.write_text("package shared\nconst Version = 1\n")
        self.commit("v0.7.7")
        self.shared.write_text("package shared\nconst Version = 2\n")
        self.commit("v0.7.8")
        (self.root / "README.md").write_text("App changed")
        self.commit("v0.7.9")

    def test_prefers_original_published_worker_across_app_releases(self):
        (self.root / "shared/version_test.go").write_text("package shared\n")
        self.commit()
        self.assertEqual(matching_tags(self.root, "0.7.10"), ["v0.7.8", "v0.7.9"])

    def test_transitive_worker_change_requires_a_new_build(self):
        self.shared.unlink()
        (self.root / "shared/replacement.go").write_text("package shared\nconst Version = 3\n")
        self.commit()
        self.assertEqual(matching_tags(self.root, "0.7.10"), [])


if __name__ == "__main__":
    unittest.main()
