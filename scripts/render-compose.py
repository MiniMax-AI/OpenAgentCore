#!/usr/bin/env python3
"""Fill the Compose template with one release's node metadata.

The template is deploy/compose/compose.yaml. The initialization image is pinned; Core and Web default to latest. A release publishes the rendered file; this script does not run Docker.
"""
import hashlib
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
TEMPLATE = ROOT / "deploy/compose/compose.yaml"
TOKENS = ("REVISION", "INIT_IMAGE")


def render(values):
    """Return compose.yaml text. values uses the token names in TOKENS."""
    missing = [name for name in TOKENS if name not in values]
    if missing:
        raise ValueError("Missing Compose values: " + ", ".join(missing))
    if not re.fullmatch(r"[0-9a-f]{40}", values["REVISION"]):
        raise ValueError("REVISION must be a full source commit SHA")
    if not re.fullmatch(r"ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}", values["INIT_IMAGE"]):
        raise ValueError("INIT_IMAGE must be an immutable GHCR image reference")
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
    """Write compose.yaml and its checksum list."""
    directory = pathlib.Path(directory)
    files = {
        "compose.yaml": render(values).encode(),
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
