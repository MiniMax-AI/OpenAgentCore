"""Publish one release's node payload without replacing bytes already installed."""
import json
import os
from pathlib import Path
import re
import shutil
import tempfile

from distribution import artifact, digest


class PayloadError(Exception):
    pass


def prepare_node_payload(root, state, bundle):
    destination = root / "node-payload"
    # Each release remains immutable and addressable while old nodes retain it.
    # The only mutable publication is a small, atomically replaced active pointer.
    def publish(source):
        manifest = json.loads((source / "manifest.json").read_text())
        revision = manifest.get("source_commit", "")
        if not re.fullmatch(r"[0-9a-f]{40}", revision):
            raise PayloadError("Invalid node payload release identity")
        metadata_names = ("node-install.pyz", "manifest.json", "SHA256SUMS", "runtime/seccomp.json")
        names = list(metadata_names)
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
        target = destination / "releases" / revision
        if target.is_symlink() or target.parent.is_symlink():
            raise PayloadError("Installed node payload differs; preserve it and inspect the distribution")
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if target.exists():
            # Validate the complete published metadata and every existing declared
            # artifact before filling any absence. Existing bytes are immutable.
            for name in metadata_names:
                previous = target / name
                if (previous.parent.is_symlink() or previous.is_symlink() or not previous.is_file()
                        or digest(previous) != digest(source / name)):
                    raise PayloadError("Installed node payload differs; preserve it and inspect the distribution")
            artifacts = target / "artifacts"
            if artifacts.is_symlink() or artifacts.exists() and not artifacts.is_dir():
                raise PayloadError("Installed node artifact directory differs")
            missing = []
            for logical in manifest.get("artifacts", {}):
                entry = artifact(manifest, logical)
                name = "artifacts/" + entry["filename"]
                previous = target / name
                if previous.is_symlink() or previous.exists() and (not previous.is_file()
                        or previous.stat().st_size != entry["size"] or digest(previous) != entry["sha256"]):
                    raise PayloadError("Installed node artifact differs; refusing repair")
                if not previous.exists() and name in names:
                    missing.append((name, entry))
            if missing:
                artifacts.mkdir(mode=0o700, exist_ok=True)
                for name, entry in missing:
                    descriptor, temporary = tempfile.mkstemp(prefix=".payload-", dir=artifacts)
                    try:
                        with os.fdopen(descriptor, "wb") as outgoing, (source / name).open("rb") as incoming:
                            shutil.copyfileobj(incoming, outgoing)
                            outgoing.flush()
                            os.fsync(outgoing.fileno())
                        if Path(temporary).stat().st_size != entry["size"] or digest(Path(temporary)) != entry["sha256"]:
                            raise PayloadError("Node artifact changed during repair")
                        # Publish without replacing bytes introduced concurrently.
                        os.link(temporary, target / name)
                    finally:
                        os.unlink(temporary)
                for directory in (artifacts, target):
                    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
                    try:
                        os.fsync(descriptor)
                    finally:
                        os.close(descriptor)
            return revision
        with tempfile.TemporaryDirectory(prefix=".payload-", dir=target.parent) as temporary:
            stage = Path(temporary) / "release"
            stage.mkdir(mode=0o700)
            for name in names:
                path = source / name
                if path.is_symlink() or not path.is_file():
                    raise PayloadError("Invalid node payload source file")
                copied = stage / name
                copied.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with path.open("rb") as incoming, copied.open("xb") as outgoing:
                    os.chmod(copied, 0o600)
                    shutil.copyfileobj(incoming, outgoing)
                    outgoing.flush()
                    os.fsync(outgoing.fileno())
            os.rename(stage, target)
        return revision

    if destination.is_symlink():
        raise PayloadError("Invalid node payload directory")
    destination.mkdir(mode=0o700, exist_ok=True)
    revision = publish(bundle)
    pointer = destination / "active.json"
    if pointer.is_symlink():
        raise PayloadError("Invalid active node payload pointer")
    if pointer.exists():
        if json.loads(pointer.read_text()) != {"source_commit": revision}:
            raise PayloadError("Installed node payload differs; preserve it and inspect the distribution")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".active-", dir=destination)
    try:
        with os.fdopen(descriptor, "w") as stream:
            json.dump({"source_commit": revision}, stream)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, pointer)
        descriptor = os.open(destination, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
