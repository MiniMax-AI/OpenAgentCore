#!/usr/bin/env python3
"""Build the provider with the pinned SDK fork and its matching embedded FFI."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

SDK_MODULE = "github.com/superradcompany/microsandbox/sdk/go"
# The SDK source (go.mod replace), this FFI and the runtime archive come from this tag of the fork; see docs/maintainers.md.
FORK_TAG = "oac-v0.7.11-2"
SDK_FORK = "github.com/SaladDay/microsandbox/sdk/go"
FFI_FILE = "libmicrosandbox_go_ffi-linux-amd64.so"
FFI_URL = f"https://github.com/SaladDay/microsandbox/releases/download/{FORK_TAG}/{FFI_FILE}"
FFI_SHA256 = "c04e6256ee24335e2de4635361d5ae53674573818859113474020274d1fd0a29"


def verify_ffi(path):
    with Path(path).open("rb") as stream:
        digest = hashlib.sha256()
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    if digest.hexdigest() != FFI_SHA256:
        raise ValueError("microsandbox FFI checksum mismatch")


def install_ffi(source, destination):
    verify_ffi(source)
    if destination.is_symlink() or not destination.is_file() or destination.stat().st_size != 0:
        raise ValueError("The SDK must contain an empty FFI release sentinel")
    shutil.copyfile(source, destination)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("build", "test", "tag"))
    parser.add_argument("output", nargs="?", type=Path)
    args = parser.parse_args()
    if args.operation == "tag":
        print(FORK_TAG)
        return
    if (args.operation == "build") != (args.output is not None):
        parser.error("build requires an absolute output path; test takes no output")
    if args.output is not None and not args.output.is_absolute():
        parser.error("output must be absolute")
    root = Path(__file__).resolve().parents[1]
    module = root / "services/core/tools/microsandbox-provider"
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="1")
    def run(*command, cwd=module):
        try:
            return subprocess.check_output(command, cwd=cwd, env=env, text=True)
        except subprocess.CalledProcessError as error:
            print(error.output, end="")
            raise
    if run("go", "env", "GOOS", "GOARCH").splitlines() != ["linux", "amd64"]:
        parser.error("the provider distribution requires Linux amd64")
    run("go", "mod", "download", SDK_MODULE)
    sdk = json.loads(run("go", "list", "-mod=readonly", "-m", "-json", SDK_MODULE))
    if sdk.get("Replace", {}).get("Path") != SDK_FORK:
        raise ValueError("The SDK must be the pinned fork module")
    cache = Path(os.environ.get("OAC_DEV_HOME", str(Path.home() / ".oac"))) / "cache"
    if not cache.is_absolute():
        raise ValueError("OAC_DEV_HOME must be absolute")
    cache.mkdir(parents=True, exist_ok=True)
    ffi = cache / (FFI_SHA256 + ".so")
    with tempfile.TemporaryDirectory(prefix="oac-microsandbox-build-", dir=cache) as temporary:
        stage = Path(temporary)
        if not ffi.exists():
            download = stage / FFI_FILE
            run("curl", "--fail", "--location", "--silent", "--show-error", "--retry", "3", "--output", str(download), FFI_URL)
            verify_ffi(download)
            os.replace(download, ffi)
        verify_ffi(ffi)
        for source in [*module.glob("*.go"), module / "go.mod", module / "go.sum"]:
            shutil.copyfile(source, stage / source.name)
        run("go", "mod", "vendor", "-o", str(stage / "vendor"))
        install_ffi(ffi, stage / "vendor" / SDK_MODULE / "internal/bundle/bundles" / FFI_FILE)
        if args.operation == "test":
            print(run("go", "test", "-mod=vendor", "./...", "-count=1", cwd=stage), end="")
            print(run("go", "vet", "-mod=vendor", "./...", cwd=stage), end="")
        else:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            print(run("go", "build", "-mod=vendor", "-trimpath", "-o", str(args.output), ".", cwd=stage), end="")


if __name__ == "__main__":
    main()
