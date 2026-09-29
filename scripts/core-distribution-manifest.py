#!/usr/bin/env python3
"""Verify matched distribution inputs and package independently fetched artifacts."""

import gzip
import hashlib
import json
import os
import pathlib
import posixpath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from urllib.parse import unquote, urlsplit
import zipapp


RUNTIME_ARCHIVE_SHA256 = "47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b"
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
ARTIFACTS = {
    "images/runtime.tar.gz": "runtime.tar.gz",
    "native/bin/oac-node": "sandbox-node",
    "native/bin/oac-daemon": "daemon",
    "native/bin/oac-microsandbox-provider": "microsandbox-provider",
    "native/microsandbox/msb": "msb",
    "native/microsandbox/libkrunfw.so.5.6.1": "libkrunfw.so.5.6.1",
    "runtime/seccomp.json": "seccomp.json",
}


# The docs a distribution carries, by repository path. Links between them stay
# relative; every other relative link points at the same file on GitHub at the
# bundle's commit, so no bundled link leads outside the bundle.
BUNDLED_DOCS = (
    "README.md",
    "README.zh-CN.md",
    "docs/user-guide.md",
    "docs/development.md",
    "docs/configuration.md",
    "docs/getting-started/README.md",
    "docs/getting-started/install.md",
    "docs/getting-started/install-options.md",
    "docs/getting-started/nodes.md",
    "docs/getting-started/operations.md",
    "docs/getting-started/quickstart.md",
    "docs/getting-started/self-hosted.md",
    "services/agents-api/HOSTED-SANDBOX-MANAGER.md",
    "contracts/agents-api/environment-executor-credentials.md",
)
# Files the bundled docs show, copied as they are, so they work offline.
BUNDLED_FILES = ("docs/assets/openagentcore-banner.png", "docs/assets/architecture.png")
REPOSITORY_URL = "https://github.com/MiniMax-AI/parsar-core"
MARKDOWN_LINK = re.compile(r"(!?)\[((?:[^\[\]]|\[[^\]]*\])*)\]\(([^)\s]+)((?:\s+\"[^\"]*\")?)\)")
FENCE = re.compile(r" {0,3}(`{3,}|~{3,})")
HEADING = re.compile(r" {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*\Z")
LIST_ITEM = re.compile(r" {0,3}(?:[-*+]|\d{1,9}[.)])(?:[ \t]|\Z)")


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


def built_image(metadata_file):
    """Print the local store ID of the image one BuildKit build just produced."""
    metadata = json.loads(pathlib.Path(metadata_file).read_text())
    # The classic store names an image by its config digest, the containerd store
    # by its manifest digest; the other value never resolves to itself there.
    config = metadata.get("containerimage.config.digest")
    manifest = metadata.get("containerimage.digest", config)
    if not all(isinstance(value, str) and DIGEST.fullmatch(value) for value in (config, manifest)):
        raise ValueError("Build metadata lacks valid image digests")
    resolved = []
    for candidate in dict.fromkeys((config, manifest)):
        result = subprocess.run(["docker", "image", "inspect", "--format", "{{.Id}}", candidate],
                                stdin=subprocess.DEVNULL, capture_output=True, text=True, check=False)
        if result.returncode == 0 and result.stdout.strip() == candidate:
            resolved.append(candidate)
    if len(resolved) != 1:
        raise ValueError("The local image store does not identify the built image by exactly one of its digests")
    verify_image(resolved[0])
    print(resolved[0])


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


def verify_runtime(image, daemon, source):
    details = verify_image(image)
    source = pathlib.Path(source)
    files = {"/usr/local/bin/oac-daemon": pathlib.Path(daemon)}
    environment = dict(value.split("=", 1) for value in details["Config"]["Env"] if "=" in value)
    if "OAC_RUNTIME_MCODE_BIN" in environment:
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
        filename = f"oac-{revision}-linux-amd64-{suffix}"
        target = assets / filename
        if logical == "runtime/seccomp.json":
            shutil.copyfile(bundle / logical, target)
        else:
            (bundle / logical).replace(target)
        result[logical] = {"filename": filename, "sha256": sha256(target), "size": target.stat().st_size}
        if logical == "images/runtime.tar.gz":
            result[logical].update(unpacked)
    return result


# The installation's management command; it runs without the bundle directory.
OAC_CLI_MODULES = ("oac_cli.py", "config_model.py", "config.schema.json", "configuration.py",
                  "native_service.py", "distribution.py", "node_spec.py")


def bootstraps(bundle, epoch, revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Invalid operator source revision")
    bundle = pathlib.Path(bundle)
    with tempfile.TemporaryDirectory(dir=bundle.parent) as directory:
        modules = (("node_install.py", "__main__.py"), ("distribution.py", "distribution.py"),
                   *((name, name) for name in ("node_spec.py", "node_generations.py", "install_display.py", "node_output.py")))
        for original, packaged in modules:
            target = pathlib.Path(directory) / packaged
            shutil.copyfile(bundle / original, target)
            os.utime(target, (int(epoch), int(epoch)))
        zipapp.create_archive(directory, bundle / "node-install.pyz", compressed=True)
    with tempfile.TemporaryDirectory(dir=bundle.parent) as directory:
        for name in OAC_CLI_MODULES:
            shutil.copyfile(bundle / name, pathlib.Path(directory) / name)
        (pathlib.Path(directory) / "__main__.py").write_text(f"import oac_cli\n\noac_cli.SOURCE_COMMIT = {revision!r}\noac_cli.entry()\n")
        for path in pathlib.Path(directory).iterdir():
            os.utime(path, (int(epoch), int(epoch)))
        zipapp.create_archive(directory, bundle / "oac.pyz", interpreter="/usr/bin/env python3", compressed=True)


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
        "runtime_ref": "oac-runtime@" + digest,
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


def markdown_lines(text):
    """(line, inside a code block) for each line of a Markdown document.

    Fenced blocks close only with a fence of the same character, at least as long.
    A line indented by four or more columns after a blank line is an indented code
    block, except inside a list, where indentation continues the list item.
    """
    fence, in_list, previous_blank, previous_code = None, False, True, False
    for line in text.split("\n"):
        opening = FENCE.match(line)
        if fence is not None:
            closes = opening and opening.group(1)[0] == fence[0] and len(opening.group(1)) >= len(fence) \
                and not line[opening.end():].strip()
            if closes:
                fence = None
            yield line, True
            continue
        if opening:
            fence = opening.group(1)
            yield line, True
            continue
        blank = not line.strip()
        indent = len(line.expandtabs(4)) - len(line.expandtabs(4).lstrip(" "))
        code = not blank and indent >= 4 and not in_list and (previous_blank or previous_code)
        if not blank and not code:
            if LIST_ITEM.match(line):
                in_list = True
            elif indent == 0:
                in_list = False
        previous_blank, previous_code = blank, code or (previous_code and blank)
        yield line, code


def code_span_end(line, position):
    """The end of the code span opening at position, or None when its backticks never close."""
    run = re.match(r"`+", line[position:]).group(0)
    closing = re.compile(r"(?<!`)" + run + r"(?!`)").search(line, position + len(run))
    return closing.end() if closing else None


def links(line):
    """The Markdown links of one line, skipping code spans outside link text."""
    position = 0
    while position < len(line):
        if line[position] == "`":
            end = code_span_end(line, position)
            position = end if end is not None else position + len(re.match(r"`+", line[position:]).group(0))
            continue
        match = MARKDOWN_LINK.match(line, position) if line[position] in "![" else None
        if match:
            yield match
            position = match.end()
        else:
            position += 1


def heading_text(title):
    """A heading's rendered text: code spans keep their content; links, tags and emphasis markers go."""
    rendered, text, position = [], "", 0
    while position < len(title):
        end = code_span_end(title, position) if title[position] == "`" else None
        if end is None:
            text += title[position]
            position += 1
            continue
        rendered.append(plain(text) + title[position:end].strip("`"))
        text, position = "", end
    return "".join(rendered) + plain(text)


def plain(text):
    """Markdown text without link syntax, HTML tags and emphasis markers; intraword underscores stay."""
    text = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"<[^>]+>", "", text)
    text = re.sub(r"(?<![^\W_])_+|_+(?![^\W_])", "", text)
    return re.sub(r"[*~]", "", text)


def heading_anchors(text):
    """GitHub's heading anchors: lowercase, punctuation dropped, spaces to hyphens, repeats numbered."""
    found, counts = set(), {}
    for line, code in markdown_lines(text):
        match = None if code else HEADING.match(line)
        if match:
            base = re.sub(r"[^\w\- ]", "", heading_text(match.group(2) or "").strip().lower()).replace(" ", "-")
            count = counts.get(base, 0)
            counts[base] = count + 1
            found.add(base if count == 0 else f"{base}-{count}")
    return found


def rewrite_line(line, rewrite):
    """Apply rewrite(image, target) to each link of one line, and to an image inside link text."""
    pieces, position = [], 0
    for match in links(line):
        pieces.append(line[position:match.start()])
        text = rewrite_line(match.group(2), rewrite)
        pieces.append(f"{match.group(1)}[{text}]({rewrite(bool(match.group(1)), match.group(3))}{match.group(4)})")
        position = match.end()
    return "".join(pieces) + line[position:]


def rewrite_links(text, rewrite):
    """Apply rewrite(image, target) to every Markdown link outside code blocks and code spans."""
    return "\n".join(line if code else rewrite_line(line, rewrite) for line, code in markdown_lines(text))


def split_links(text):
    """Line numbers where a link starts on one line and ends on the next; links are read line by line."""
    lines, found = list(markdown_lines(text)), []
    for number, ((first, first_code), (second, second_code)) in enumerate(zip(lines, lines[1:]), 1):
        if not (first_code or second_code or not second.strip()):
            joined = first + " " + second.lstrip()
            if any(match.start() < len(first) < match.end() for match in links(joined)):
                found.append(number)
    return found


def docs(source, bundle, revision, names=BUNDLED_DOCS, files=BUNDLED_FILES):
    """Copy the bundled docs and the files they show; a link that leaves them points at
    this revision on GitHub.

    Every relative link must name an existing file, and an anchor an existing heading,
    so a broken link fails the build instead of shipping.
    """
    source, bundle = pathlib.Path(source), pathlib.Path(bundle)
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Bundled docs need the full source commit")
    bundled = set(names) | set(files)
    anchors = {}

    def headings(path):
        if path not in anchors:
            anchors[path] = heading_anchors((source / path).read_text(encoding="utf-8"))
        return anchors[path]

    def versioned(name):
        def rewrite(image, link):
            if re.match(r"[A-Za-z][A-Za-z0-9+.-]*:", link):
                for kind in ("blob", "tree", "raw"):
                    if link.startswith(f"{REPOSITORY_URL}/{kind}/main/"):
                        return f"{REPOSITORY_URL}/{kind}/@SOURCE_REVISION@/" + link[len(f"{REPOSITORY_URL}/{kind}/main/"):]
                return link
            path, _, anchor = link.partition("#")
            target = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(path))) if path else name
            if target == ".." or target.startswith("../") or not (source / target).exists():
                raise ValueError(f"{name}: link to a missing file: {link}")
            if anchor and target.endswith(".md") and unquote(anchor) not in headings(target):
                raise ValueError(f"{name}: link to a missing heading: {link}")
            if target in bundled:
                return link
            kind = "raw" if image else "tree" if (source / target).is_dir() else "blob"
            return f"{REPOSITORY_URL}/{kind}/@SOURCE_REVISION@/{target}" + ("#" + anchor if anchor else "")
        return rewrite

    for name in names:
        original = (source / name).read_text(encoding="utf-8")
        for number in split_links(original):
            raise ValueError(f"{name}:{number}: keep each link on one line")
        text = rewrite_links(original, versioned(name))
        target = bundle / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text.replace("@SOURCE_REVISION@", revision), encoding="utf-8")
    for name in files:
        (bundle / name).parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / name, bundle / name)
    check_docs(bundle, names, files)


def check_docs(bundle, names=BUNDLED_DOCS, files=BUNDLED_FILES):
    """Every relative link in the bundled docs resolves inside the bundle."""
    bundle = pathlib.Path(bundle)
    for name in names:
        def check(image, link):
            if not re.match(r"[A-Za-z][A-Za-z0-9+.-]*:", link):
                path, _, anchor = link.partition("#")
                target = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(path))) if path else name
                if target not in set(names) | set(files) or not (bundle / target).is_file() or (
                        anchor and target.endswith(".md")
                        and unquote(anchor) not in heading_anchors((bundle / target).read_text(encoding="utf-8"))):
                    raise ValueError(f"Bundled {name} links outside the bundle: {link}")
            return link
        rewrite_links((bundle / name).read_text(encoding="utf-8"), check)


if __name__ == "__main__":
    commands = {"extract-runtime": extract_runtime, "verify-runtime": verify_runtime, "verify-image": verify_image,
                "built-image": built_image, "manifest": manifest, "archive": archive, "bootstraps": bootstraps,
                "release-base": release_base, "docs": docs}
    try:
        commands[sys.argv[1]](*sys.argv[2:])
    except (KeyError, TypeError):
        sys.exit("Usage: core-distribution-manifest.py extract-runtime|verify-runtime|verify-image|built-image|manifest|archive|bootstraps|release-base|docs ARGS...")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
