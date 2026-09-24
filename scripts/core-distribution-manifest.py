#!/usr/bin/env python3
"""Verify matched distribution inputs and package independently fetched artifacts."""

import gzip
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from urllib.parse import urlsplit
import zipapp


RUNTIME_ARCHIVE_SHA256 = "47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
ARTIFACTS = {
    "images/runtime.tar.gz": "runtime.tar.gz",
    "native/bin/parsar-sandbox-node": "sandbox-node",
    "native/bin/parsar-daemon": "daemon",
    "native/bin/parsar-runtime": "runtime-launcher",
    "native/bin/agents-api-microsandbox-provider": "microsandbox-provider",
    "native/microsandbox/msb": "msb",
    "native/microsandbox/libkrunfw.so.5.6.1": "libkrunfw.so.5.6.1",
    "runtime/seccomp.json": "seccomp.json",
}


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


def image_identities(archive, build_id):
    """Bind both Docker store identities to one exported Linux amd64 image."""
    if not DIGEST.fullmatch(build_id):
        raise ValueError("Missing immutable distribution image identity")
    with tarfile.open(archive, "r:") as contents:
        members = contents.getmembers()

        def member(name):
            matches = [entry for entry in members if entry.name == name]
            if len(matches) != 1 or not matches[0].isfile():
                raise ValueError("Image archive must contain one regular " + name)
            return matches[0]

        def blob(descriptor, parse=False):
            digest = descriptor.get("digest", "")
            size = descriptor.get("size")
            if (not isinstance(digest, str) or not DIGEST.fullmatch(digest)
                    or type(size) is not int or size <= 0):
                raise ValueError("Invalid image archive descriptor")
            entry = member("blobs/sha256/" + digest.removeprefix("sha256:"))
            if entry.size != size or parse and size > 1024 * 1024:
                raise ValueError("Image archive descriptor size mismatch")
            checksum, chunks = hashlib.sha256(), []
            with contents.extractfile(entry) as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    checksum.update(block)
                    if parse:
                        chunks.append(block)
            if "sha256:" + checksum.hexdigest() != digest:
                raise ValueError("Image archive blob checksum mismatch")
            return json.loads(b"".join(chunks)) if parse else None

        index = member("index.json")
        if index.size > 1024 * 1024:
            raise ValueError("Image archive index exceeds size limit")
        with contents.extractfile(index) as stream:
            descriptors = json.load(stream).get("manifests", [])
        if len(descriptors) != 1:
            raise ValueError("Image archive must select exactly one image")
        descriptor = descriptors[0]
        manifest_digest = descriptor.get("digest", "")
        image = blob(descriptor, parse=True)
        # A containerd export may wrap its single platform in an image index.
        if descriptor.get("mediaType") in ("application/vnd.oci.image.index.v1+json",
                                            "application/vnd.docker.distribution.manifest.list.v2+json"):
            descriptors = image.get("manifests", [])
            if len(descriptors) != 1:
                raise ValueError("Image archive must select exactly one platform")
            descriptor = descriptors[0]
            image = blob(descriptor, parse=True)
        if descriptor.get("mediaType") not in ("application/vnd.oci.image.manifest.v1+json",
                                                "application/vnd.docker.distribution.manifest.v2+json"):
            raise ValueError("Image archive must select an image manifest")
        config_descriptor = image.get("config", {})
        config = blob(config_descriptor, parse=True)
        config_digest = config_descriptor["digest"]
        if config.get("os") != "linux" or config.get("architecture") != "amd64":
            raise ValueError("Image archive contains an unexpected platform")
        for layer in image.get("layers", []):
            blob(layer)
        if build_id not in (config_digest, manifest_digest):
            raise ValueError("Image archive does not match the selected build image")
        return config_digest, manifest_digest


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


def release_base(value):
    if not value:
        return ""
    parsed = urlsplit(value)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.query or parsed.fragment
            or any(c.isspace() or ord(c) < 32 for c in value) or "\\" in value
            or not parsed.path.strip("/") or "latest" in parsed.path.lower().split("/")):
        raise ValueError("Release base must be a versioned HTTPS directory, never latest")
    parsed.port
    return value.rstrip("/")


def package_artifacts(bundle, stage, revision):
    """Move optional payload out of Core; the manifest owns every asset digest."""
    assets = stage / "artifacts"
    assets.mkdir()
    runtime = bundle / "images/runtime.tar"
    compressed = bundle / "images/runtime.tar.gz"
    unpacked = {"unpacked_sha256": sha256(runtime), "unpacked_size": runtime.stat().st_size}
    with runtime.open("rb") as source, compressed.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=6) as output:
            shutil.copyfileobj(source, output, 1024 * 1024)
    runtime.unlink()
    result = {}
    for logical, suffix in ARTIFACTS.items():
        filename = f"parsar-core-{revision}-linux-amd64-{suffix}"
        target = assets / filename
        if logical == "runtime/seccomp.json":
            shutil.copyfile(bundle / logical, target)
        else:
            (bundle / logical).replace(target)
        result[logical] = {"filename": filename, "sha256": sha256(target), "size": target.stat().st_size}
        if logical == "images/runtime.tar.gz":
            result[logical].update(unpacked)
    return result


def bootstraps(bundle, epoch):
    bundle = pathlib.Path(bundle)
    for source, output in (("node_install.py", "node-install.pyz"),
                           ("self_hosted_install.py", "self-hosted-install.pyz")):
        with tempfile.TemporaryDirectory(dir=bundle.parent) as directory:
            for original, packaged in ((source, "__main__.py"), ("distribution.py", "distribution.py")):
                target = pathlib.Path(directory) / packaged
                shutil.copyfile(bundle / original, target)
                os.utime(target, (int(epoch), int(epoch)))
            if source == "node_install.py":
                target = pathlib.Path(directory) / "node_spec.py"
                shutil.copyfile(bundle / "node_spec.py", target)
                os.utime(target, (int(epoch), int(epoch)))
            zipapp.create_archive(directory, bundle / output, compressed=True)


def manifest(bundle, stage, revision, source_tree, artifact_base_url="", offline="0"):
    bundle, stage = pathlib.Path(bundle), pathlib.Path(stage)
    artifact_base_url = release_base(artifact_base_url)
    if not artifact_base_url and offline != "1":
        raise ValueError("Online distributions require a release base; select explicit offline output otherwise")
    if not re.fullmatch(r"[0-9a-f]{40}", revision) or not re.fullmatch(r"[0-9a-f]{40}", source_tree):
        raise ValueError("Distribution source commit and tree must be full Git identities")
    inspected = json.loads((stage / "runtime-inspect.json").read_text())
    digest = inspected.get("digest", "")
    if not isinstance(digest, str) or not DIGEST.fullmatch(digest):
        raise ValueError("msb did not return an immutable OCI manifest digest")
    if inspected.get("architecture") != "amd64" or inspected.get("os") != "linux":
        raise ValueError("msb imported an unexpected Runtime platform")
    identities = {name: image_identities(bundle / "images" / (name + ".tar"),
                                        (stage / (name + ".id")).read_text().strip())
                  for name in ("core", "web", "runtime", "database")}
    metadata = {
        "source_commit": revision,
        "source_tree": source_tree,
        "platform": "linux/amd64",
        "artifact_base_url": artifact_base_url,
        "artifacts": package_artifacts(bundle, stage, revision),
        "images": {name: identity[0] for name, identity in identities.items()},
        "image_manifest_digests": {name: identity[1] for name, identity in identities.items()},
        "runtime_ref": "parsar-core-runtime@" + digest,
        "microsandbox": {
            "version": "0.7.2",
            "runtime_sha256": sha256(stage / "core/microsandbox/msb"),
            "firmware_sha256": sha256(stage / "core/microsandbox/libkrunfw.so.5.6.1"),
        },
    }
    (bundle / "manifest.json").write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    checksums(bundle)


def checksums(bundle):
    bundle = pathlib.Path(bundle)
    # Optional payload hashes are already authenticated by manifest.json.
    members = sorted(path for path in bundle.rglob("*") if path.is_file()
                     and path.relative_to(bundle).parts[0] != "artifacts"
                     and path.name != "SHA256SUMS")
    (bundle / "SHA256SUMS").write_text("".join(sha256(path) + "  " + path.relative_to(bundle).as_posix() + "\n" for path in members))


def archive(bundle, epoch, variant=""):
    bundle = pathlib.Path(bundle)
    if variant not in ("", "offline"):
        raise ValueError("Unknown distribution archive variant")
    output = bundle.with_name(bundle.name + ("-" + variant if variant else "") + ".tar.gz")
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
    commands = {"extract-runtime": extract_runtime, "verify-runtime": verify_runtime, "verify-image": verify_image,
                "manifest": manifest, "archive": archive, "bootstraps": bootstraps, "release-base": release_base}
    try:
        commands[sys.argv[1]](*sys.argv[2:])
    except (KeyError, TypeError):
        sys.exit("Usage: core-distribution-manifest.py extract-runtime|verify-runtime|verify-image|manifest|archive|bootstraps|release-base ARGS...")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
