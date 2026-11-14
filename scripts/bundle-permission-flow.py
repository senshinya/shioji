#!/usr/bin/env python3
"""Build the pinned Swift bridge and copy its library/resources before signing."""
import argparse
from pathlib import Path
import shutil
import subprocess

root = Path(__file__).resolve().parent.parent


def bundle(contents: Path):
    command = [
        "swift", "build", "--package-path", str(root / "native/permission-flow"),
        "--scratch-path", str(root / "build/native/permission-flow"),
        "--configuration", "release", "--product", "ShiojiPermissionFlow",
        "--force-resolved-versions",
    ]
    subprocess.run(command, cwd=root, check=True)
    binary = Path(subprocess.check_output(command + ["--show-bin-path"], cwd=root, text=True).strip())
    frameworks = contents / "Frameworks"
    resources = contents / "Resources"
    frameworks.mkdir(parents=True, exist_ok=True)
    resources.mkdir(parents=True, exist_ok=True)
    shutil.copy2(binary / "libShiojiPermissionFlow.dylib", frameworks)
    library = frameworks / "libShiojiPermissionFlow.dylib"
    commands = subprocess.check_output(["otool", "-l", str(library)], text=True).splitlines()
    for index, line in enumerate(commands):
        if line.strip() == "cmd LC_RPATH":
            path = commands[index + 2].strip().removeprefix("path ").rsplit(" (offset ", 1)[0]
            if path.startswith("/") and path != "/usr/lib/swift":
                subprocess.run(["install_name_tool", "-delete_rpath", path, str(library)], check=True)
    shutil.copytree(binary / "PermissionFlow_PermissionFlow.bundle", resources / "PermissionFlow_PermissionFlow.bundle", dirs_exist_ok=True)
    # Keep any compatibility runtime needed below the build machine's OS.
    subprocess.run(["xcrun", "swift-stdlib-tool", "--copy", "--platform", "macosx", "--scan-folder", str(frameworks), "--destination", str(frameworks)], check=True)
    licenses = resources / "licenses"
    licenses.mkdir(exist_ok=True)
    shutil.copy2(root / "third_party/permissionflow/LICENSE", licenses / "PermissionFlow-LICENSE.txt")
    print(frameworks / "libShiojiPermissionFlow.dylib")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("contents", type=Path, help="destination App Contents directory")
    bundle(parser.parse_args().contents.resolve())
