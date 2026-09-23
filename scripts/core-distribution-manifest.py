#!/usr/bin/env python3
"""Verify distribution inputs and write the offline bundle's public metadata."""

import gzip
import hashlib
import json
import pathlib
import re
import subprocess
import sys
import tarfile


RUNTIME_ARCHIVE_SHA256 = "47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")


def sha256(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def verify_image(image):
    if not DIGEST.fullmatch(image):
        raise ValueError("Distribution image inputs must be immutable sha256 image IDs")
    details = json.loads(subprocess.check_output(["docker", "image", "inspect", image], text=True))[0]
    if details["Id"] != image or details["Os"] != "linux" or details["Architecture"] != "amd64":
        raise ValueError("Distribution images must be the selected Linux amd64 image")
    return details


def verify_runtime(image, daemon, helpers, source):
    details = verify_image(image)
    helpers, source = pathlib.Path(helpers), pathlib.Path(source)
    files = {"/usr/local/bin/parsar-daemon": pathlib.Path(daemon)}
    for name in ("agents-api-codex-directory", "agents-api-codex-write", "agents-api-workspace-export"):
        files["/usr/local/bin/" + name] = helpers / name
    files["/usr/local/bin/agents-api-runtime-initialize"] = source / "services/agents-api/deploy/runtime/initialize.py"
    files["/usr/local/bin/agents-api-tool-root"] = source / "services/agents-api/deploy/runtime/tool-root.py"
    environment = dict(value.split("=", 1) for value in details["Config"]["Env"] if "=" in value)
    if "PARSAR_CODEX_BIN" in environment:
        files["/etc/codex/requirements.toml"] = source / "services/agents-api/deploy/codex/requirements.toml"
        files["/etc/codex/tool-env.py"] = source / "services/agents-api/deploy/codex/tool-env.py"
    if "PARSAR_CLAUDE_SDK_ENTRYPOINT" in environment:
        files["/usr/local/bin/agents-api-claude-shell-prefix"] = source / "services/agents-api/deploy/claude/shell-prefix.py"
    if "PARSAR_MCODE_BIN" in environment:
        for name in ("launch.mjs", "bridge.mjs", "check.mjs", "tool-executor.mjs", "subagent-snapshot.mjs", "source.json"):
            files["/opt/mcode-harness/" + name] = source / "packages/mcode-harness" / name
    output = subprocess.check_output(
        ["docker", "run", "--rm", "--network", "none", "--entrypoint", "sha256sum", image, *files], text=True
    )
    actual = dict(reversed(line.split(None, 1)) for line in output.splitlines())
    for guest_path, local in files.items():
        if actual.get(guest_path) != sha256(local):
            raise ValueError("Runtime image does not match the committed build: " + guest_path)


def extract_runtime(archive, destination):
    if sha256(archive) != RUNTIME_ARCHIVE_SHA256:
        raise ValueError("microsandbox v0.7.2 release checksum mismatch")
    destination = pathlib.Path(destination)
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive, "r:gz") as bundle:
        for name in ("msb", "libkrunfw.so.5.6.1"):
            members = [member for member in bundle.getmembers() if pathlib.PurePosixPath(member.name).name == name and member.isfile()]
            if len(members) != 1:
                raise ValueError("Release must contain exactly one regular " + name)
            with bundle.extractfile(members[0]) as stream, (destination / name).open("wb") as output:
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    output.write(block)
            (destination / name).chmod(0o555)


def manifest(bundle, stage, revision, source_tree):
    bundle, stage = pathlib.Path(bundle), pathlib.Path(stage)
    inspected = json.loads((stage / "runtime-inspect.json").read_text())
    digest = inspected.get("digest", "")
    if not isinstance(digest, str) or not DIGEST.fullmatch(digest):
        raise ValueError("msb did not return an immutable OCI manifest digest")
    if inspected.get("architecture") != "amd64" or inspected.get("os") != "linux":
        raise ValueError("msb imported an unexpected Runtime platform")
    images = {name: (stage / (name + ".id")).read_text().strip() for name in ("core", "web", "runtime", "database")}
    if any(not DIGEST.fullmatch(image) for image in images.values()):
        raise ValueError("Missing immutable distribution image identity")
    metadata = {
        "source_commit": revision,
        "source_tree": source_tree,
        "platform": "linux/amd64",
        "images": images,
        "runtime_ref": "parsar-core-runtime@" + digest,
        "microsandbox": {
            "version": "0.7.2",
            "runtime_sha256": sha256(stage / "core/microsandbox/msb"),
            "firmware_sha256": sha256(stage / "core/microsandbox/libkrunfw.so.5.6.1"),
        },
    }
    (bundle / "manifest.json").write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    members = sorted(path for path in bundle.rglob("*") if path.is_file())
    (bundle / "SHA256SUMS").write_text("".join(sha256(path) + "  " + path.relative_to(bundle).as_posix() + "\n" for path in members))


def archive(bundle, epoch):
    bundle = pathlib.Path(bundle)
    output = bundle.with_name(bundle.name + ".tar.gz")
    with output.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as tar:
            for path in sorted(bundle.rglob("*")):
                if not path.is_file():
                    continue
                relative = path.relative_to(bundle)
                info = tarfile.TarInfo(bundle.name + "/" + relative.as_posix())
                info.size = path.stat().st_size
                if relative.parts[0] == "native":
                    info.mode = path.stat().st_mode & 0o777
                else:
                    info.mode = 0o755 if relative.as_posix() == "install.sh" else 0o644
                info.mtime = int(epoch)
                with path.open("rb") as stream:
                    tar.addfile(info, stream)
    output.with_name(output.name + ".sha256").write_text(sha256(output) + "  " + output.name + "\n")


if __name__ == "__main__":
    commands = {"extract-runtime": extract_runtime, "verify-runtime": verify_runtime, "verify-image": verify_image, "manifest": manifest, "archive": archive}
    try:
        commands[sys.argv[1]](*sys.argv[2:])
    except (KeyError, TypeError):
        sys.exit("Usage: core-distribution-manifest.py extract-runtime|verify-runtime|verify-image|manifest|archive ARGS...")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
