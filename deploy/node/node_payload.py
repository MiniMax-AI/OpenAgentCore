"""Publish the installation's node payload without replacing installed bytes."""
import json
import os
import shutil
import tempfile

from distribution import artifact, digest


class PayloadError(Exception):
    pass


def publish(destination, source):
    """Copy the release in source into destination, which holds exactly one release.

    Installed bytes are immutable: a file with other bytes is refused, and only absent files are added."""
    if destination.is_symlink():
        raise PayloadError("Invalid node payload directory")
    manifest = json.loads((source / "manifest.json").read_text())
    names = ["node-install.pyz", "manifest.json", "SHA256SUMS", "runtime/seccomp.json"]
    for logical in manifest.get("artifacts", {}):
        entry = artifact(manifest, logical)
        name = "artifacts/" + entry["filename"]
        path = source / name
        if path.exists():
            if (path.is_symlink() or not path.is_file()
                    or not path.resolve().is_relative_to(source.resolve())
                    or path.stat().st_size != entry["size"] or digest(path) != entry["sha256"]):
                raise PayloadError("Offline artifact verification failed: " + logical)
            names.append(name)
    destination.mkdir(mode=0o700, exist_ok=True)
    for name in names:
        incoming, target = source / name, destination / name
        if incoming.is_symlink() or not incoming.is_file():
            raise PayloadError("Invalid node payload source file")
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if target.parent.is_symlink() or target.is_symlink():
            raise PayloadError("Installed node payload differs; preserve it and inspect the distribution")
        if target.exists():
            if not target.is_file() or digest(target) != digest(incoming):
                raise PayloadError("Installed node payload differs; preserve it and inspect the distribution")
            continue
        descriptor, temporary = tempfile.mkstemp(prefix=".payload-", dir=target.parent)
        try:
            with os.fdopen(descriptor, "wb") as outgoing, incoming.open("rb") as stream:
                shutil.copyfileobj(stream, outgoing)
                outgoing.flush()
                os.fsync(outgoing.fileno())
            # Publish without replacing bytes introduced concurrently.
            os.link(temporary, target)
        except FileExistsError:
            raise PayloadError("Installed node payload differs; preserve it and inspect the distribution") from None
        finally:
            os.unlink(temporary)
    for directory in {(destination / name).parent for name in names}:
        descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
