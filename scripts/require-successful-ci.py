#!/usr/bin/env python3
"""Release only a commit already validated by the main branch's latest CI."""
import argparse
import json
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    runs = json.loads(subprocess.check_output([
        "gh", "run", "list", "--repo", args.repo, "--workflow", "ci.yml",
        "--branch", "main", "--event", "push", "--commit", args.commit,
        "--limit", "1", "--json", "headSha,headBranch,event,conclusion,url",
    ], text=True))
    for run in runs:
        if (run.get("headSha") != args.commit or run.get("headBranch") != "main"
                or run.get("event") != "push"):
            continue
        if run.get("conclusion") != "success":
            raise SystemExit("The latest main CI has not passed: " + run.get("url", ""))
        print("Verified main CI for " + args.commit + ": " + run["url"])
        return
    raise SystemExit("No successful main CI exists for the checked-out release commit.")


if __name__ == "__main__":
    main()
