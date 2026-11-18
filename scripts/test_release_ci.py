#!/usr/bin/env python3
"""Exercise the release admission boundary without GitHub or signing access."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
COMMIT = "a" * 40
RUN = dict(headSha=COMMIT, headBranch="main", event="push", conclusion="success",
           url="https://github.com/example/project/actions/runs/123")


class ReleaseCITests(unittest.TestCase):
    def test_full_go_suite_runs_outside_actions(self):
        for path in sorted((ROOT / ".github/workflows").glob("*.y*ml")):
            with self.subTest(workflow=path.name):
                self.assertNotRegex(path.read_text(), r"\bgo test[^\n]*\s\./\.\.\.(?=\s|$)")

    def invoke(self, runs, api_failure=False):
        with tempfile.TemporaryDirectory() as directory:
            gh = Path(directory) / "gh"
            gh.write_text("#!/usr/bin/env python3\nimport os, sys\n"
                          "if os.environ['TEST_GH_FAILURE'] == '1': sys.exit(1)\n"
                          "print(os.environ['TEST_GH_RUNS'])\n")
            gh.chmod(0o755)
            env = dict(os.environ, PATH=directory + os.pathsep + os.environ["PATH"],
                       TEST_GH_RUNS=json.dumps(runs), TEST_GH_FAILURE=str(int(api_failure)))
            return subprocess.run([sys.executable, str(ROOT / "scripts/require-successful-ci.py"),
                                   "--repo", "example/project", "--commit", COMMIT],
                                  env=env, capture_output=True, text=True)

    def test_matching_successful_main_push_allows_release(self):
        result = self.invoke([RUN])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(RUN["url"], result.stdout)

    def test_other_commits_branches_and_prs_cannot_authorize_release(self):
        for change in (dict(headSha="b" * 40), dict(headBranch="feature"), dict(event="pull_request")):
            with self.subTest(change=change):
                self.assertNotEqual(self.invoke([dict(RUN, **change)]).returncode, 0)

    def test_latest_failure_does_not_fall_back_to_an_older_success(self):
        self.assertNotEqual(self.invoke([dict(RUN, conclusion="failure"), RUN]).returncode, 0)

    def test_missing_pending_cancelled_and_failed_ci_block_release(self):
        for runs in ([], [dict(RUN, conclusion="")], [dict(RUN, conclusion="cancelled")],
                     [dict(RUN, conclusion="failure")]):
            with self.subTest(runs=runs):
                self.assertNotEqual(self.invoke(runs).returncode, 0)

    def test_github_api_failure_blocks_release(self):
        self.assertNotEqual(self.invoke([RUN], api_failure=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
