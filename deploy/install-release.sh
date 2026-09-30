#!/usr/bin/env bash
# Download one matched release and delegate installation to its bundled installer.
set -euo pipefail
command -v python3 >/dev/null || { echo 'Python 3.9+ is required.' >&2; exit 1; }
# Keep the caller's stdin available to the bundled installer.
exec python3 /dev/fd/3 "$@" 3<<'PY'
import argparse
import contextlib
import errno
import fcntl
import hashlib
import http.client
import json
import os
import pathlib
import platform
import re
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = "MiniMax-AI/OpenAgentCore"
API = "https://api.github.com/repos/" + REPOSITORY
ARCHIVE = re.compile(r"oac-([0-9a-f]{40})-linux-amd64\.tar\.gz")


class ReleaseError(Exception):
    pass


class TransferError(ReleaseError):
    pass


class Progress:
    """Byte progress for the standalone downloader, without terminal escapes in logs."""
    def __init__(self, label, total=None):
        self.label, self.total = label, total
        self.started = time.monotonic()
        self.updated = 0
        self.visible = sys.stderr.isatty() and os.environ.get("TERM") != "dumb"

    def update(self, size):
        now = time.monotonic()
        if not self.visible or now - self.updated < 0.2 and size != self.total:
            return
        self.updated = now
        bar = ""
        if self.total:
            percent = min(100, size * 100 // self.total)
            filled = percent // 5
            bar = "[" + "=" * filled + " " * (20 - filled) + f"] {percent:3d}% "
        print(f"\r{self.label}: {bar}{size / 1048576:.1f} MiB ({now - self.started:.0f}s)",
              end="\033[K", file=sys.stderr, flush=True)

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        if self.visible:
            with contextlib.suppress(OSError):
                print(file=sys.stderr, flush=True)


def retry(operation):
    for attempt in range(3):
        try:
            return operation()
        except (TransferError, TimeoutError, ConnectionError, http.client.HTTPException):
            if attempt == 2:
                raise ReleaseError("Download interrupted after 3 attempts. Check the network and rerun the command.") from None
            print("==> Download interrupted; retrying...", flush=True)
            time.sleep(attempt + 1)


class DownloadRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != "https":
            raise ReleaseError("Release downloads require HTTPS")
        redirected = super().redirect_request(request, fp, code, msg, headers, newurl)
        return redirected


def open_url(url, binary=False, offset=0):
    headers = {"Accept": "application/octet-stream" if binary else "application/vnd.github+json",
               "User-Agent": "OpenAgentCore-installer", "X-GitHub-Api-Version": "2022-11-28"}
    if offset:
        headers["Range"] = f"bytes={offset}-"
    request = urllib.request.Request(url, headers=headers)
    try:
        return urllib.request.build_opener(DownloadRedirect()).open(request, timeout=60)
    except urllib.error.HTTPError as error:
        if error.code in (408, 429, 500, 502, 503, 504):
            error.close()
            raise TransferError("Temporary GitHub download failure") from None
        error.close()
        if error.code in (401, 403, 404):
            raise ReleaseError("Release unavailable. Check the version and GitHub access limits.") from None
        raise ReleaseError("GitHub download failed (HTTP " + str(error.code) + "). Retry later.") from None
    except urllib.error.URLError:
        raise TransferError("Could not reach GitHub") from None


def select_release(version):
    endpoint = "/releases/latest" if version == "latest" else "/releases/tags/" + urllib.parse.quote(version, safe="")
    def metadata():
        with open_url(API + endpoint) as response:
            raw = response.read(4 * 1024 * 1024 + 1)
            length = getattr(response, "headers", {}).get("Content-Length")
        if len(raw) > 4 * 1024 * 1024:
            raise ReleaseError("Release metadata is too large")
        if length is not None and len(raw) < int(length):
            raise TransferError("Incomplete release metadata")
        return json.loads(raw)
    release = retry(metadata)
    if (not isinstance(release, dict) or not isinstance(release.get("tag_name"), str)
            or not isinstance(release.get("assets"), list)
            or any(not isinstance(asset, dict) or not isinstance(asset.get("name"), str)
                   for asset in release["assets"])):
        raise ReleaseError("Invalid GitHub release metadata")
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
        if type(asset.get("size")) is not int or asset["size"] <= 0:
            raise ReleaseError("Invalid release asset size")
    return release["tag_name"], bundle, sums[0]


def download(asset, destination, limit=None):
    # Resolve latest once, then download only those immutable asset IDs.
    expected = asset["size"]
    if limit is not None and expected > limit:
        raise ReleaseError("Release checksum file is too large")
    require_space(destination.parent, expected)

    def transfer():
        offset = destination.stat().st_size if destination.exists() else 0
        if offset == expected:
            return
        with open_url(API + "/releases/assets/" + str(asset["id"]), binary=True, offset=offset) as response:
            status = getattr(response, "status", 200)
            if status == 206:
                match = re.fullmatch(r"bytes (\d+)-(\d+)/(\d+)", response.headers.get("Content-Range", ""))
                if not match or tuple(map(int, match.groups())) != (offset, expected - 1, expected):
                    raise ReleaseError("Invalid release download range")
            elif status == 200:
                offset = 0  # Servers may ignore Range; replace the partial file.
            else:
                raise ReleaseError("Unexpected release download response")
            with destination.open("ab" if offset else "wb") as output:
                size = offset
                for chunk in iter(lambda: response.read1(1024 * 1024), b""):
                    size += len(chunk)
                    if size > expected:
                        raise ReleaseError("Release download exceeds its declared size")
                    output.write(chunk)
                    progress.update(size)
            if size != expected:
                raise TransferError("Incomplete release download")
    with Progress("Downloading checksum" if limit else "Downloading archive", expected) as progress:
        retry(transfer)


def require_space(directory, size):
    if shutil.disk_usage(directory).free < size:
        raise ReleaseError(f"Not enough disk space in {directory}; need {size} bytes available. Free space and rerun.")


@contextlib.contextmanager
def ignore_interrupts():
    handlers = {number: signal.signal(number, signal.SIG_IGN)
                for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
    try:
        yield
    finally:
        for number, handler in handlers.items():
            signal.signal(number, handler)


@contextlib.contextmanager
def staging(cache):
    # A stable lock makes one reserved staging directory safe to discard after a crash.
    descriptor = os.open(cache / ".download.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "rb") as lock:
        info = os.fstat(lock.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_nlink != 1:
            raise ReleaseError("Release download lock must be an owned regular file")
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ReleaseError("Another release download is running; wait for it to finish and retry") from None
        temporary = cache / ".download"
        if temporary.exists() or temporary.is_symlink():
            info = temporary.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise ReleaseError("Release staging directory must be private and owned by you")
            with ignore_interrupts():
                shutil.rmtree(temporary)
        try:
            temporary.mkdir(mode=0o700)
            yield temporary, lock.fileno()
        finally:
            with ignore_interrupts():
                if temporary.exists():
                    shutil.rmtree(temporary)


def extract(archive, destination, stem):
    seen = set()
    unpacked = 0
    with tarfile.open(archive, "r:gz") as source, Progress("Extracting") as progress:
        for member in source:
            path = pathlib.PurePosixPath(member.name)
            if (not member.isfile() or path.is_absolute() or ".." in path.parts
                    or "\\" in member.name or len(path.parts) < 2 or path.parts[0] != stem
                    or path in seen):
                raise ReleaseError("Release archive contains an unsafe or duplicate path")
            seen.add(path)
            require_space(destination, member.size)
            target = destination.joinpath(*path.parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            with source.extractfile(member) as data, target.open("xb") as output:
                for chunk in iter(lambda: data.read(1024 * 1024), b""):
                    output.write(chunk)
                    unpacked += len(chunk)
                    progress.update(unpacked)
            target.chmod(member.mode & 0o777)
    root = destination / stem
    if not (root / "install.sh").is_file() or not (root / "manifest.json").is_file():
        raise ReleaseError("Release archive is missing its installer or manifest")
    return root


def run_installer(root, arguments, lock):
    """Wait for the installer to finish cleanup before removing its source bundle."""
    child = None
    interrupted = None

    def forward(signum, frame):
        nonlocal interrupted
        if interrupted is not None:
            return  # Let the installer finish cleanup despite repeated interrupts.
        interrupted = signum
        if child is not None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(child.pid, signum)

    handlers = {number: signal.signal(number, forward)
                for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
    try:
        # The child retains the lock if this downloader is killed without cleanup.
        child = subprocess.Popen(["bash", str(root / "install.sh"), *arguments],
                                 start_new_session=True, pass_fds=(lock,))
        if interrupted is not None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(child.pid, interrupted)
        code = child.wait()
        if interrupted is not None:
            raise KeyboardInterrupt
        if code:
            raise ReleaseError("Bundled installation failed; fix the reported cause and rerun the command")
    finally:
        for number, handler in handlers.items():
            signal.signal(number, handler)


def install(version, arguments):
    if sys.version_info < (3, 9):
        raise ReleaseError("Python 3.9+ is required")
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise ReleaseError("Core installation currently requires Linux amd64")
    home = pathlib.Path.home() / ".oac"
    home.mkdir(mode=0o700, exist_ok=True)
    cache = home / "releases"
    cache.mkdir(mode=0o700, exist_ok=True)
    info = cache.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ReleaseError("Release directory must be private and owned by your current account")
    extracted = None
    try:
        with staging(cache) as (temporary, lock):
            print("==> Finding the " + ("latest stable release" if version == "latest" else "requested release") + "...", flush=True)
            tag, bundle, sums = select_release(version)
            print("Installing OpenAgentCore " + tag, flush=True)
            archive = temporary / bundle["name"]
            checksum = temporary / sums["name"]
            download(sums, checksum, limit=1024)
            expected = checksum.read_text().strip()
            match = re.fullmatch(r"([0-9a-f]{64})  " + re.escape(bundle["name"]), expected)
            if not match:
                raise ReleaseError("Invalid release checksum file")
            print("==> Downloading the release archive (this may take a few minutes)...", flush=True)
            download(bundle, archive)
            print("==> Verifying the archive checksum...", flush=True)
            digest = hashlib.sha256()
            with archive.open("rb") as source, Progress("Verifying", bundle["size"]) as progress:
                checked = 0
                for chunk in iter(lambda: source.read(1024 * 1024), b""):
                    digest.update(chunk)
                    checked += len(chunk)
                    progress.update(checked)
            if digest.hexdigest() != match[1]:
                raise ReleaseError("Release checksum mismatch; installation was not started")
            stem = bundle["name"].removesuffix(".tar.gz")
            print("==> Extracting the verified archive...", flush=True)
            root = extract(archive, temporary, stem)
            manifest = json.loads((root / "manifest.json").read_text())
            if not isinstance(manifest, dict) or manifest.get("source_commit") != ARCHIVE.fullmatch(bundle["name"])[1]:
                raise ReleaseError("Release source does not match its bundle")
            archive.unlink()
            checksum.unlink()
            print("==> Starting the bundled installer...", flush=True)
            run_installer(root, arguments, lock)
            # Only a successful install retains a verified bundle for same-version repair.
            extracted = pathlib.Path(tempfile.mkdtemp(prefix="release-", dir=cache))
            root = root.rename(extracted / stem)
        print("Verified bundle: " + str(root), flush=True)
    except BaseException:
        # A second signal or a closed terminal must not interrupt temporary-file cleanup.
        with ignore_interrupts():
            if extracted is not None:
                shutil.rmtree(extracted)
        raise


def main(argv):
    parser = argparse.ArgumentParser(
        prog="install.sh", description="Install the latest OpenAgentCore release. Other arguments go to its installer.",
        allow_abbrev=False)
    parser.add_argument("--version", default="latest", help="Release tag (default: latest stable release)")
    options, arguments = parser.parse_known_args(argv)
    install(options.version, arguments)


if __name__ == "__main__":
    def interrupted(signum, frame):
        raise KeyboardInterrupt
    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, interrupted)
    try:
        main(sys.argv[1:])
    except (ReleaseError, OSError, ValueError, tarfile.TarError, KeyboardInterrupt) as error:
        message = "interrupted; rerun the same command" if isinstance(error, KeyboardInterrupt) else str(error)
        if isinstance(error, OSError) and error.errno in (errno.ENOSPC, errno.EDQUOT):
            message = "Disk space or quota exhausted. Free space in ~/.oac and rerun the command."
        with contextlib.suppress(OSError):
            print("Installation failed: " + message, file=sys.stderr)
        sys.exit(130 if isinstance(error, KeyboardInterrupt) else 1)
PY
