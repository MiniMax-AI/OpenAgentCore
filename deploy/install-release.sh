#!/usr/bin/env bash
# Download one matched release and delegate installation to its bundled installer.
set -euo pipefail
command -v python3 >/dev/null || { echo 'Python 3.9+ is required.' >&2; exit 1; }
# Keep the caller's stdin available to the bundled installer.
exec python3 /dev/fd/3 "$@" 3<<'PY'
import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = "MiniMax-AI/OpenAgentCore"
API = "https://api.github.com/repos/" + REPOSITORY
ARCHIVE = re.compile(r"oac-([0-9a-f]{40})-linux-amd64\.tar\.gz")


class ReleaseError(Exception):
    pass


class DownloadRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != "https":
            raise ReleaseError("Release downloads require HTTPS")
        redirected = super().redirect_request(request, fp, code, msg, headers, newurl)
        return redirected


def open_url(url, binary=False):
    headers = {"Accept": "application/octet-stream" if binary else "application/vnd.github+json",
               "User-Agent": "OpenAgentCore-installer", "X-GitHub-Api-Version": "2022-11-28"}
    request = urllib.request.Request(url, headers=headers)
    try:
        return urllib.request.build_opener(DownloadRedirect()).open(request, timeout=60)
    except urllib.error.HTTPError as error:
        if error.code in (401, 403, 404):
            raise ReleaseError("Release unavailable. Check the version and GitHub access limits.") from None
        raise ReleaseError("GitHub download failed (HTTP " + str(error.code) + "). Retry later.") from None
    except urllib.error.URLError:
        raise ReleaseError("Could not reach GitHub. Check the network and retry.") from None


def select_release(version):
    endpoint = "/releases/latest" if version == "latest" else "/releases/tags/" + urllib.parse.quote(version, safe="")
    with open_url(API + endpoint) as response:
        release = json.load(response)
    if release.get("draft") or (version == "latest" and release.get("prerelease")):
        raise ReleaseError("Select a published release; latest excludes prereleases")
    assets = release.get("assets", [])
    bundles = [asset for asset in assets if ARCHIVE.fullmatch(asset["name"])]
    if len(bundles) != 1:
        raise ReleaseError("This release must contain exactly one Linux amd64 control-plane bundle")
    bundle = bundles[0]
    sums = [asset for asset in assets if asset["name"] == bundle["name"] + ".sha256"]
    if len(sums) != 1:
        raise ReleaseError("This release is missing its unique bundle checksum")
    for asset in (bundle, sums[0]):
        if type(asset.get("id")) is not int or asset["id"] <= 0:
            raise ReleaseError("Invalid release asset identity")
    return release["tag_name"], bundle, sums[0]


def download(asset, destination, limit=None):
    # Resolve latest once, then download only those immutable asset IDs.
    with open_url(API + "/releases/assets/" + str(asset["id"]), binary=True) as response:
        with destination.open("wb") as output:
            size = 0
            for chunk in iter(lambda: response.read(1024 * 1024), b""):
                size += len(chunk)
                if limit is not None and size > limit:
                    raise ReleaseError("Release checksum file is too large")
                output.write(chunk)


def extract(archive, destination, stem):
    seen = set()
    with tarfile.open(archive, "r:gz") as source:
        for member in source:
            path = pathlib.PurePosixPath(member.name)
            if (not member.isfile() or path.is_absolute() or ".." in path.parts
                    or "\\" in member.name or len(path.parts) < 2 or path.parts[0] != stem
                    or path in seen):
                raise ReleaseError("Release archive contains an unsafe or duplicate path")
            seen.add(path)
            target = destination.joinpath(*path.parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            with source.extractfile(member) as data, target.open("xb") as output:
                shutil.copyfileobj(data, output)
            target.chmod(member.mode & 0o777)
    root = destination / stem
    if not (root / "install.sh").is_file() or not (root / "manifest.json").is_file():
        raise ReleaseError("Release archive is missing its installer or manifest")
    return root


def install(version, arguments):
    if sys.version_info < (3, 9):
        raise ReleaseError("Python 3.9+ is required")
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise ReleaseError("Core installation currently requires Linux amd64")
    print("==> Finding the " + ("latest stable release" if version == "latest" else "requested release") + "...", flush=True)
    tag, bundle, sums = select_release(version)
    print("Installing OpenAgentCore " + tag, flush=True)
    home = pathlib.Path.home() / ".oac"
    home.mkdir(mode=0o700, exist_ok=True)
    cache = home / "releases"
    cache.mkdir(mode=0o700, exist_ok=True)
    extracted = pathlib.Path(tempfile.mkdtemp(prefix="release-", dir=cache))
    try:
        with tempfile.TemporaryDirectory(prefix=".download-", dir=cache) as temporary:
            archive = pathlib.Path(temporary) / bundle["name"]
            checksum = pathlib.Path(temporary) / sums["name"]
            download(sums, checksum, limit=1024)
            expected = checksum.read_text().strip()
            match = re.fullmatch(r"([0-9a-f]{64})  " + re.escape(bundle["name"]), expected)
            if not match:
                raise ReleaseError("Invalid release checksum file")
            print("==> Downloading the release archive (this may take a few minutes)...", flush=True)
            download(bundle, archive)
            print("==> Verifying the archive checksum...", flush=True)
            digest = hashlib.sha256()
            with archive.open("rb") as source:
                for chunk in iter(lambda: source.read(1024 * 1024), b""):
                    digest.update(chunk)
            if digest.hexdigest() != match[1]:
                raise ReleaseError("Release checksum mismatch; installation was not started")
            stem = bundle["name"].removesuffix(".tar.gz")
            print("==> Extracting the verified archive...", flush=True)
            root = extract(archive, extracted, stem)
            manifest = json.loads((root / "manifest.json").read_text())
            if manifest.get("source_commit") != ARCHIVE.fullmatch(bundle["name"])[1]:
                raise ReleaseError("Release source does not match its bundle")
    except BaseException:
        shutil.rmtree(extracted)
        raise
    # Keep the verified extracted bundle for same-version repair.
    print("Verified bundle: " + str(root), flush=True)
    print("==> Starting the bundled installer...", flush=True)
    return subprocess.call(["bash", str(root / "install.sh"), *arguments])


def main(argv):
    parser = argparse.ArgumentParser(
        prog="install.sh", description="Install the latest OpenAgentCore release. Other arguments go to its installer.",
        allow_abbrev=False)
    parser.add_argument("--version", default="latest", help="Release tag (default: latest stable release)")
    options, arguments = parser.parse_known_args(argv)
    return install(options.version, arguments)


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (ReleaseError, OSError, ValueError, tarfile.TarError) as error:
        print("Installation failed: " + str(error), file=sys.stderr)
        sys.exit(1)
PY
