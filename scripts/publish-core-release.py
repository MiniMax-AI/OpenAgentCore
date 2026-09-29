#!/usr/bin/env python3
"""Create and upload one draft, then publish its fixed ID without automatic retries."""

import argparse
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import tarfile
from urllib.parse import quote

spec = importlib.util.spec_from_file_location(
    "distribution", pathlib.Path(__file__).with_name("core-distribution-manifest.py"))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)


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


def publish(assets, repository, revision, tag, mode):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
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
    for path in files:
        uploaded = api(repository, "https://uploads.github.com/repos/" + repository
                       + "/" + endpoint + "/assets?name=" + quote(path.name, safe=""),
                       "--method", "POST", "-H", "Content-Type: application/octet-stream",
                       "-H", "Content-Length: " + str(expected[path.name]), "--input", str(path.resolve()))
        if (uploaded["state"] != "uploaded" or uploaded["name"] != path.name
                or uploaded["size"] != expected[path.name]):
            raise ValueError("Asset upload was not confirmed; inspect the draft")
    release = api(repository, endpoint)
    verify_draft(release, tag, revision)
    actual = release["assets"]
    if (len(actual) != len(expected)
            or any(a["state"] != "uploaded" for a in actual)
            or {a["name"]: a["size"] for a in actual} != expected):
        raise ValueError("Release asset inventory differs from the build")
    if mode == "draft":
        return
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
