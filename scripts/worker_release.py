"""Select the original signed worker for an App-only release."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parent.parent
WORKER = "Shioji.app/Contents/MacOS/shioji-worker"
BUILD_ENV = {key: value for key, value in os.environ.items() if key != "GOROOT"}
BUILD_ENV.update(CGO_ENABLED="0", GOOS="darwin", GOARCH="arm64")


def output(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def matching_tags(root, version):
    """Return releases with the same worker inputs, oldest equivalent first."""
    tags = output("git", "tag", "--merged", "HEAD", "--sort=-version:refname", cwd=root).splitlines()
    current_version = tuple(map(int, version.split(".")))
    previous = [
        tag for tag in tags
        if re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag)
        and tuple(map(int, tag[1:].split("."))) <= current_version
    ]
    if not previous:
        return []

    # Include transitive packages (including local module replacements), along
    # with module resolution inputs. Compare directories so deleted sources
    # and removed embed files are covered too.
    local = output(
        "go", "list", "-deps", "-f",
        "{{with .Module}}{{if or .Main .Replace}}{{$.Dir}}{{end}}{{end}}",
        "./cmd/shioji-worker", cwd=root, env=BUILD_ENV,
    )
    paths = {"go.mod", "go.sum"}
    for directory in filter(None, local.splitlines()):
        path = Path(directory).resolve()
        if not path.is_relative_to(root.resolve()):
            return []
        paths.add(str(path.relative_to(root.resolve())))
    paths = [*sorted(paths), ":(glob,exclude)**/*_test.go"]
    if output("git", "ls-files", "--others", "--", *paths, cwd=root):
        return []

    matching = []
    for tag in previous:
        result = subprocess.run(["git", "diff", "--quiet", tag, "--", *paths], cwd=root)
        if result.returncode == 1:
            break
        result.check_returncode()
        matching.append(tag)
    return matching[::-1]


def reuse_worker(repo, version, team, destination):
    matching = matching_tags(ROOT, version)
    if not matching:
        print("Building a new worker")
        return
    published = json.loads(output(
        "gh", "release", "list", "--repo", repo, "--exclude-drafts",
        "--exclude-pre-releases", "--limit", "100", "--json", "tagName",
    ))
    published = {release["tagName"] for release in published}
    revisions = {output("git", "rev-parse", f"{tag}^{{commit}}", cwd=ROOT) for tag in matching}
    go_version = output("go", "env", "GOVERSION", cwd=ROOT, env=BUILD_ENV)
    for tag in matching:
        if tag not in published:
            continue
        assets = json.loads(output("gh", "release", "view", tag, "--repo", repo, "--json", "assets"))["assets"]
        if not any(asset["name"] == "update-darwin-arm64.json" for asset in assets):
            continue
        manifest = json.loads(output(
            "gh", "release", "download", tag, "--repo", repo,
            "--pattern", "update-darwin-arm64.json", "--output", "-",
        ))
        prefix = f"https://github.com/{repo}/releases/download/{tag}/Shioji-{tag[1:]}-darwin-arm64-"
        archive_name = re.fullmatch(re.escape(prefix) + r"([a-f0-9]{64})\.tar\.gz", manifest["url"])
        if manifest["version"] != tag[1:] or archive_name is None:
            raise ValueError(f"Invalid update manifest for {tag}")
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / "update.tar.gz"
            subprocess.run([
                "gh", "release", "download", tag, "--repo", repo,
                "--pattern", manifest["url"].rsplit("/", 1)[-1], "--output", str(archive),
            ], check=True)
            with archive.open("rb") as incoming:
                if hashlib.file_digest(incoming, "sha256").hexdigest() != archive_name[1]:
                    raise ValueError(f"Worker source archive digest mismatch for {tag}")
            with tarfile.open(archive, "r:gz") as bundle:
                member = bundle.getmember(WORKER)
                if not member.isfile():
                    raise ValueError(f"Worker in {tag} is not a regular file")
                candidate = Path(directory) / "shioji-worker"
                with bundle.extractfile(member) as incoming, candidate.open("wb") as binary:
                    shutil.copyfileobj(incoming, binary)
            requirement = (
                '=identifier "shioji-worker" and anchor apple generic and '
                'certificate 1[field.1.2.840.113635.100.6.2.6] exists and '
                f'certificate leaf[subject.OU] = "{team}" and '
                'certificate leaf[field.1.2.840.113635.100.6.1.13] exists'
            )
            subprocess.run(["codesign", "--verify", "--strict", "-R", requirement, str(candidate)], check=True)
            build = output("go", "version", "-m", str(candidate), env=BUILD_ENV).splitlines()
            if build[0].rsplit(": ", 1)[-1] != go_version:
                continue
            settings = {line.strip() for line in build[1:]}
            expected = {
                "build\tvcs.modified=false",
                "build\tGOOS=darwin", "build\tGOARCH=arm64", "build\tCGO_ENABLED=0",
            }
            if not expected <= settings or not any(f"build\tvcs.revision={revision}" in settings for revision in revisions):
                raise ValueError(f"Published worker metadata does not match {tag}")
            shutil.copyfile(candidate, destination)
            destination.chmod(0o755)
            print(f"Reusing signed worker from {tag}")
            return
    print("Building a new worker")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo")
    parser.add_argument("version")
    parser.add_argument("team")
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Z0-9]{10}", args.team):
        parser.error("Invalid Developer ID team")
    reuse_worker(args.repo, args.version, args.team, args.destination)
