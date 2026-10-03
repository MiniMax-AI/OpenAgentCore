#!/usr/bin/env python3
"""Fill the Compose template with one release's node metadata.

The template is deploy/compose/compose.yaml. Images stay on their default
latest tags. A release publishes the rendered file; this script does not run Docker.
"""
import hashlib
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
TEMPLATE = ROOT / "deploy/compose/compose.yaml"
PORTS = ROOT / "deploy/compose/ports.yaml"
TOKENS = ("REVISION", "RELEASE_BASE", "ARCHIVE_CHECKSUM")


def render(values):
    """Return compose.yaml text. values uses the token names in TOKENS."""
    missing = [name for name in TOKENS if name not in values]
    if missing:
        raise ValueError("Missing Compose values: " + ", ".join(missing))
    if not re.fullmatch(r"[0-9a-f]{40}", values["REVISION"]):
        raise ValueError("REVISION must be a full source commit SHA")
    if not re.fullmatch(r"[0-9a-f]{64}", values["ARCHIVE_CHECKSUM"]):
        raise ValueError("ARCHIVE_CHECKSUM must be a SHA-256 hex digest")
    base = values["RELEASE_BASE"]
    if not base.startswith("https://") or not base.endswith("/") or " " in base:
        raise ValueError("RELEASE_BASE must be an https URL ending with /")
    text = TEMPLATE.read_text()
    for name in TOKENS:
        token = "__OAC_" + name + "__"
        if token not in text:
            raise ValueError("Compose template is missing " + token)
        text = text.replace(token, values[name])
    leftover = sorted(set(re.findall(r"__OAC_[A-Z_]+__", text)))
    if leftover:
        raise ValueError("Unreplaced Compose tokens: " + ", ".join(leftover))
    return text


CHECKSUMS = "compose-sha256sums.txt"


def write_assets(directory, values):
    """Write compose.yaml, the port files and one checksum list for them."""
    directory = pathlib.Path(directory)
    files = {
        "compose.yaml": render(values).encode(),
        "ports.yaml": PORTS.read_bytes(),
    }
    written, lines = [], []
    for name, data in files.items():
        path = directory / name
        path.write_bytes(data)
        written.append(path)
        lines.append(hashlib.sha256(data).hexdigest() + "  " + name + "\n")
    checksums = directory / CHECKSUMS
    checksums.write_text("".join(lines))
    return [*written, checksums]
