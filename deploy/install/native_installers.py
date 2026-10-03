"""Retain matched catalog metadata and explicitly supplied offline installers."""

import json
import os
from pathlib import Path
import re
import shutil
import tempfile


from distribution import digest


def prepare(root, state, bundle):
    source, target = Path(bundle) / "native-installers", Path(root) / "native-installers"
    if not source.exists():
        return
    if source.is_symlink() or not source.is_dir() or target.is_symlink():
        raise RuntimeError("Invalid native installer directory")
    catalog_path = source / "catalog.json"
    if catalog_path.is_symlink():
        raise RuntimeError("Invalid native installer catalog")
    catalog = json.loads(catalog_path.read_text())
    if catalog["version"] != state["source_commit"]:
        raise RuntimeError("Native installer catalog does not match Core")
    expected = {"catalog.json": digest(catalog_path)}
    for platform, artifact in catalog["artifacts"].items():
        if not re.fullmatch(r"(linux|darwin|windows)-(amd64|arm64)", platform):
            raise RuntimeError("Invalid native installer platform")
        expected[platform + ".tar.gz"] = artifact["sha256"]
    # Validate both directories before filling any missing files. Offline content
    # already installed stays available when repairing with a thin bundle.
    for directory in (source, target):
        if not directory.exists():
            continue
        for path in directory.iterdir():
            if (path.name not in expected or path.is_symlink() or not path.is_file()
                    or digest(path) != expected[path.name]):
                raise RuntimeError("Native installer files differ; preserve them and inspect the distribution")
    target.mkdir(mode=0o700, exist_ok=True)
    for name in expected:
        incoming, installed = source / name, target / name
        if not incoming.exists() or installed.exists():
            continue
        descriptor, temporary = tempfile.mkstemp(prefix=".native-installer-", dir=root)
        try:
            with os.fdopen(descriptor, "wb") as outgoing, incoming.open("rb") as stream:
                shutil.copyfileobj(stream, outgoing)
                outgoing.flush()
                os.fsync(outgoing.fileno())
            os.replace(temporary, installed)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
