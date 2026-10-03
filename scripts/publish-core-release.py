#!/usr/bin/env python3
"""Create and upload one draft, then publish its fixed ID without automatic retries."""

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import shutil
import gzip
import tempfile
import tarfile
from urllib.parse import quote

spec = importlib.util.spec_from_file_location(
    "distribution", pathlib.Path(__file__).with_name("core-distribution-manifest.py"))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)

REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")


def parallel_each(function, items):
    """Bound network transfers and propagate failures before publication."""
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(function, item) for item in items]
        try:
            return [future.result() for future in as_completed(futures)]
        except BaseException:
            for future in futures:
                future.cancel()
            raise


def api(repository, endpoint, *args):
    url = endpoint if endpoint.startswith("https://") else "repos/" + repository + "/" + endpoint
    return json.loads(subprocess.check_output(["gh", "api", url, *args], text=True))


def verify_tag(repository, tag, revision):
    endpoint = "git/ref/tags/" + quote(tag, safe="")
    for _ in range(10):
        obj = api(repository, endpoint)["object"]
        if obj["type"] == "commit":
            if obj["sha"] != revision:
                raise ValueError("Version tag no longer points to the built source")
            return
        if obj["type"] != "tag":
            raise ValueError("Version tag does not resolve to a commit")
        endpoint = "git/tags/" + obj["sha"]
    raise ValueError("Too many nested annotated tags")


def refuse_existing(repository, tag):
    # The tag lookup endpoint omits drafts. Listing includes authenticated drafts.
    page = 1
    while True:
        releases = api(repository, "releases?per_page=100&page=" + str(page))
        if any(release["tag_name"] == tag for release in releases):
            raise ValueError("Release or draft already exists; inspect it before retrying")
        if len(releases) < 100:
            return
        page += 1


def verify_draft(release, tag, revision):
    if (not release["draft"] or release["tag_name"] != tag
            or release["target_commitish"] != revision
            or type(release["id"]) is not int or release["id"] <= 0):
        raise ValueError("Release draft identity changed")


IMAGE_NAMES = ("core", "web", "runtime", "ingress")


def registry_manifest(reference):
    result = subprocess.run(["docker", "manifest", "inspect", reference],
                            text=True, capture_output=True)
    if result.returncode:
        # Authentication, transport and registry failures must not authorize a push.
        if "manifest unknown" in result.stderr.lower() or "no such manifest:" in result.stderr.lower():
            return None
        raise RuntimeError("Cannot inspect registry image " + reference + ": " + result.stderr)
    return json.loads(result.stdout)


def registry_image(reference):
    manifest = registry_manifest(reference)
    selected = reference
    if manifest is not None and "manifests" in manifest:
        descriptors = manifest["manifests"]
        if len(descriptors) != 1:
            raise ValueError("Expected one Linux amd64 registry image: " + reference)
        digest = descriptors[0]["digest"]
        if not distribution.DIGEST.fullmatch(digest):
            raise ValueError("Invalid registry image descriptor")
        selected = reference.rsplit(":", 1)[0] + "@" + digest
        manifest = registry_manifest(selected)
        if manifest is None:
            raise ValueError("Registry index refers to a missing image")
    return manifest, selected


def publish_images(assets, repository, revision, tag):
    """Load the checked release archives; never rebuild or replace another image."""
    image_tag = tag.replace("+", "_")
    if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}", image_tag):
        raise ValueError("Release version exceeds the container tag format")
    stem = "oac-" + revision + "-linux-amd64"
    # Extract named regular members only, never archive-controlled paths.
    with tempfile.TemporaryDirectory(prefix="oac-ghcr-") as directory:
        directory = pathlib.Path(directory)
        with tarfile.open(assets / (stem + ".tar.gz"), "r:gz") as archive:
            manifest = json.load(archive.extractfile(stem + "/manifest.json"))
            if manifest["source_commit"] != revision or manifest["platform"] != "linux/amd64":
                raise ValueError("Registry images do not match the release")
            for name in IMAGE_NAMES:
                if name == "runtime":
                    continue
                member = archive.getmember(stem + "/images/" + name + ".tar")
                if not member.isfile():
                    raise ValueError("Expected a regular image archive")
                with archive.extractfile(member) as source, (directory / (name + ".tar")).open("wb") as target:
                    shutil.copyfileobj(source, target)
        runtime = manifest["artifacts"]["images/runtime.tar.gz"]
        filename = runtime["filename"]
        if pathlib.Path(filename).name != filename:
            raise ValueError("Invalid Runtime asset filename")
        runtime_path = assets / filename
        if runtime_path.is_symlink() or distribution.sha256(runtime_path) != runtime["sha256"]:
            raise ValueError("Runtime image checksum mismatch")
        with gzip.open(runtime_path, "rb") as source, (directory / "runtime.tar").open("wb") as target:
            shutil.copyfileobj(source, target)
        for name in IMAGE_NAMES:
            expected = (manifest["images"][name], manifest["image_manifest_digests"][name])
            if distribution.image_identities(directory / (name + ".tar"), expected[0]) != expected:
                raise ValueError("Release image identity mismatch: " + name)
        references = {}
        # Validate every local image and every existing tag before the first push.
        for name in IMAGE_NAMES:
            path = directory / (name + ".tar")
            subprocess.run(["docker", "load", "--input", str(path)], check=True)
            config = manifest["images"][name]
            local = distribution.resolve_image(config, manifest["image_manifest_digests"][name])
            reference = "ghcr.io/" + repository.lower() + "/" + name + ":" + image_tag
            remote, selected = registry_image(reference)
            if remote is not None and remote.get("config", {}).get("digest") != config:
                raise ValueError("Registry tag already names a different image: " + reference)
            references[name] = (reference, config, local, remote)
        def push_image(item):
            name, (reference, config, local, remote) = item
            print("Publishing registry image " + name, flush=True)
            if remote is None:
                subprocess.run(["docker", "tag", local, reference], check=True)
                subprocess.run(["docker", "push", reference], check=True)
            remote, selected = registry_image(reference)
            if remote is None or remote.get("config", {}).get("digest") != config:
                raise ValueError("Registry image verification failed: " + reference)
            # Inspect the registry's descriptor, not the local Docker image ID.
            details = json.loads(subprocess.check_output(
                ["docker", "manifest", "inspect", "--verbose", selected], text=True))
            digest = details["Descriptor"]["digest"]
            if not distribution.DIGEST.fullmatch(digest):
                raise ValueError("Invalid registry manifest digest")
            print("Verified registry image " + name, flush=True)
            return name, {"tag": reference, "digest": reference.rsplit(":", 1)[0] + "@" + digest}
        return dict(sorted(parallel_each(push_image, references.items())))


def publish(assets, repository, revision, tag, mode):
    if not REPOSITORY.fullmatch(repository):
        raise ValueError("Expected an owner/repository")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Expected a full source commit SHA")
    if mode not in ("publish", "draft"):
        raise ValueError("Expected publish or draft mode")
    if mode == "publish":
        if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?", tag):
            raise ValueError("Version tags must use vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]")
    elif tag != "build-" + revision:
        raise ValueError("Manual builds use build-<full source SHA>")

    files = sorted(assets.iterdir())
    if not files or any(p.is_symlink() or not p.is_file() for p in files):
        raise ValueError("Expected a nonempty directory containing only regular asset files")
    # The builder validates the manifest and Runtime assets. Verify archives again
    # after the Actions artifact transfer between jobs.
    stem = "oac-" + revision + "-linux-amd64"
    archives = [assets / (stem + ".tar.gz"), assets / "install.sh"]
    if mode == "publish" or (assets / (stem + "-offline.tar.gz")).exists():
        archives.append(assets / (stem + "-offline.tar.gz"))
    for archive in archives:
        checksum = archive.with_name(archive.name + ".sha256")
        if checksum.read_text() != distribution.sha256(archive) + "  " + archive.name + "\n":
            raise ValueError("Distribution archive checksum mismatch")

    # Native archives are independent assets, authenticated by the catalog in
    # the checked control archive. Refuse incomplete transfers before creating a draft.
    with tarfile.open(assets / (stem + ".tar.gz"), "r:gz") as archive:
        catalog = json.load(archive.extractfile(stem + "/native-installers/catalog.json"))
    if catalog["version"] != revision or not catalog["artifacts"]:
        raise ValueError("Native installer catalog does not match the release")
    for platform, entry in catalog["artifacts"].items():
        if not re.fullmatch(r"(linux|darwin|windows)-(amd64|arm64)", platform):
            raise ValueError("Invalid native installer platform")
        path = assets / f"oac-native-{revision}-{platform}.tar.gz"
        if (distribution.sha256(path) != entry["sha256"]
                or path.with_name(path.name + ".sha256").read_text() != entry["sha256"] + "  " + path.name + "\n"):
            raise ValueError("Native installer checksum mismatch")

    # Actions can retain the old repository name after a rename. Resolve it
    # before any writes; binary uploads must not depend on POST redirects.
    canonical = api(repository, "https://api.github.com/repos/" + repository).get("full_name")
    if not isinstance(canonical, str) or not REPOSITORY.fullmatch(canonical):
        raise ValueError("GitHub returned an invalid repository identity")
    repository = canonical
    refuse_existing(repository, tag)
    if mode == "publish":
        verify_tag(repository, tag, revision)
    prerelease = mode == "publish" and "-" in tag.split("+", 1)[0]
    release = api(repository, "releases", "--method", "POST",
                  "-f", "tag_name=" + tag, "-f", "target_commitish=" + revision,
                  "-f", "name=OpenAgentCore " + tag,
                  "-f", "body=Linux amd64 distribution from commit " + revision + ".",
                  "-F", "draft=true", "-F", "prerelease=" + str(prerelease).lower())
    verify_draft(release, tag, revision)
    release_id = release["id"]
    endpoint = "releases/" + str(release_id)
    # Keep every operation bound to the ID returned by creation. No tag lookup,
    # overwrite, deletion or automatic retry can select another release.
    expected = {p.name: p.stat().st_size for p in files}
    def upload(path):
        print(f"Uploading {path.name} ({expected[path.name]} bytes)", flush=True)
        uploaded = api(repository, "https://uploads.github.com/repos/" + repository
                       + "/" + endpoint + "/assets?name=" + quote(path.name, safe=""),
                       "--method", "POST", "-H", "Content-Type: application/octet-stream",
                       "-H", "Content-Length: " + str(expected[path.name]), "--input", str(path.resolve()))
        if (uploaded["state"] != "uploaded" or uploaded["name"] != path.name
                or uploaded["size"] != expected[path.name]):
            raise ValueError("Asset upload was not confirmed; inspect the draft")
        print("Uploaded " + path.name, flush=True)
    parallel_each(upload, sorted(files, key=lambda path: expected[path.name], reverse=True))
    release = api(repository, endpoint)
    verify_draft(release, tag, revision)
    actual = release["assets"]
    if (len(actual) != len(expected)
            or any(a["state"] != "uploaded" for a in actual)
            or {a["name"]: a["size"] for a in actual} != expected):
        raise ValueError("Release asset inventory differs from the build")
    if mode == "draft":
        return
    # GHCR is not transactional with Releases. Keep the Release a draft until
    # every versioned image has been pushed and verified. Matching tags are reusable.
    images = publish_images(assets, repository, revision, tag)
    inventory = json.dumps({"source_commit": revision, "images": images}, indent=2) + "\n"
    # Keep digest receipts in the Actions summary without changing release assets.
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write("## GHCR images\n\n```json\n" + inventory + "```\n")
    print(inventory)
    # Uploads can take minutes. Recheck immediately before the one publish request.
    verify_tag(repository, tag, revision)
    result = api(repository, endpoint, "--method", "PATCH", "-F", "draft=false")
    if result["id"] != release_id or result["draft"] or result["tag_name"] != tag:
        raise ValueError("Publication result is unknown; inspect the existing Release")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assets", required=True, type=pathlib.Path)
    args = parser.parse_args()
    publish(args.assets, os.environ["GH_REPO"], os.environ["RELEASE_REVISION"],
            os.environ["RELEASE_TAG"], os.environ["RELEASE_MODE"])
