#!/usr/bin/env python3
"""Promote one landed batch through existing gh and SSH authentication."""

import argparse
import base64
import hashlib
import importlib.util
import json
import pathlib
import re
import shlex
import shutil
import subprocess
import tarfile
import time
import tempfile
import uuid

spec = importlib.util.spec_from_file_location(
    "distribution", pathlib.Path(__file__).with_name("core-distribution-manifest.py"))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)

REPO = "MiniMax-AI/parsar-core"
SOURCE = TAG = BASE = STEM = None


def select_source(source):
    """One invocation owns one explicit immutable candidate identity."""
    if not isinstance(source, str) or not re.fullmatch(r"[0-9a-f]{40}", source):
        raise ValueError("An explicit full candidate source commit is required")
    global SOURCE, TAG, BASE, STEM
    SOURCE = source
    TAG = "build-" + source
    BASE = "https://github.com/" + REPO + "/releases/download/" + TAG
    STEM = "oac-" + source + "-linux-amd64"


adapter_spec = importlib.util.spec_from_file_location(
    "qualification_adapter", pathlib.Path(__file__).with_name("qualify-core-release.py"))
qualification_adapter = importlib.util.module_from_spec(adapter_spec)
adapter_spec.loader.exec_module(qualification_adapter)

REQUIRED_CHECKS = qualification_adapter.CHECKS
# Only this separate promotion change may follow the qualified source on main.
PROMOTION_FILES = {
    ".github/workflows/release.yml", "AGENTS.md", "CONTRIBUTING.md", "docs/maintainers.md",
    "Makefile", "contracts/agents-api/node-generation-protocol.md",
    "scripts/promote-qualified-release.py",
    "scripts/promote-qualified-release.test.py", "scripts/qualify-core-release.py",
    "scripts/qualification_control.py", "scripts/qualification-control.test.py",
}


def run(argv, **kwargs):
    return subprocess.run(argv, check=True, text=True, capture_output=True, **kwargs).stdout


def gh(*args):
    return run(["gh", *args, "--repo", REPO])


def api(path):
    return json.loads(run(["gh", "api", "repos/" + REPO + "/" + path]))


def digest(stream):
    result = hashlib.sha256()
    for chunk in iter(lambda: stream.read(1024 * 1024), b""):
        result.update(chunk)
    return result.hexdigest()


def file_identity(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError("Asset must be a regular file: " + path.name)
    with path.open("rb") as stream:
        return {"sha256": digest(stream), "size": path.stat().st_size}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def verify_archive(path, offline, native=None):
    """Read without extraction; verify every ordinary member and optional payload."""
    hashes, metadata, sums = {}, None, None
    catalog = {}
    with tarfile.open(path, "r|gz") as archive:
        for member in archive:
            parts = pathlib.PurePosixPath(member.name).parts
            if (not member.isfile() or len(parts) < 2 or parts[0] != STEM
                    or ".." in parts or member.name in hashes):
                raise ValueError("Invalid archive member")
            relative = "/".join(parts[1:])
            stream = archive.extractfile(member)
            if relative in ("manifest.json", "SHA256SUMS", "native-installers/catalog.json"):
                if member.size > 1024 * 1024:
                    raise ValueError("Oversized archive metadata")
                raw = stream.read()
                hashes[member.name] = hashlib.sha256(raw).hexdigest()
                if relative == "manifest.json":
                    metadata = json.loads(raw)
                elif relative == "SHA256SUMS":
                    sums = raw.decode()
                else:
                    catalog = json.loads(raw)
                    if native is not None:
                        native.update(catalog)
            else:
                hashes[member.name] = digest(stream)
    if metadata is None or sums is None:
        raise ValueError("Missing archive manifest/checksums")
    if (metadata.get("source_commit") != SOURCE
            or metadata.get("artifact_base_url") != BASE
            or metadata.get("platform") != "linux/amd64"
            or not re.fullmatch(r"[0-9a-f]{40}", metadata.get("source_tree", ""))):
        raise ValueError("Candidate source/platform/release URL mismatch")
    expected = {}
    for line in sums.splitlines():
        checksum, name = line.split("  ", 1)
        if name in expected or not re.fullmatch(r"[0-9a-f]{64}", checksum):
            raise ValueError("Invalid archive checksum list")
        expected[name] = checksum
    artifacts = metadata.get("artifacts", {})
    if set(artifacts) != set(distribution.ARTIFACTS):
        raise ValueError("Missing or unexpected Runtime artifacts")
    for key in ("images", "image_manifest_digests"):
        values = metadata.get(key, {})
        if set(values) != {"core", "web", "runtime", "database", "ingress"} or any(
                not distribution.DIGEST.fullmatch(value) for value in values.values()):
            raise ValueError("Missing immutable image identities")
    if not re.fullmatch(r"oac-runtime@sha256:[0-9a-f]{64}", metadata.get("runtime_ref", "")):
        raise ValueError("Missing immutable Runtime identity")
    for logical, entry in artifacts.items():
        name = entry["filename"]
        if name != STEM + "-" + distribution.ARTIFACTS[logical]:
            raise ValueError("Unexpected Runtime filename")
        if not re.fullmatch(re.escape(STEM) + r"-[A-Za-z0-9_.-]+", name):
            raise ValueError("Invalid Runtime asset name")
        if offline:
            expected["artifacts/" + name] = entry["sha256"]
    if offline:
        for platform, entry in catalog.get("artifacts", {}).items():
            expected["native-installers/" + platform + ".tar.gz"] = entry["sha256"]
    actual = {name[len(STEM) + 1:]: value for name, value in hashes.items()
              if name != STEM + "/SHA256SUMS"}
    if actual != expected:
        raise ValueError("Archive member checksum/set mismatch")
    return metadata


def inspect_candidate(directory):
    files = {p.name: file_identity(p) for p in directory.iterdir()}
    native, online_native = {}, {}
    metadata = verify_archive(directory / (STEM + "-offline.tar.gz"), True, native)
    if verify_archive(directory / (STEM + ".tar.gz"), False, online_native) != metadata or native != online_native:
        raise ValueError("Thin and offline manifests differ")
    expected = set()
    for suffix in (".tar.gz", "-offline.tar.gz"):
        name = STEM + suffix
        expected.update((name, name + ".sha256"))
        if (directory / (name + ".sha256")).read_text() != files[name]["sha256"] + "  " + name + "\n":
            raise ValueError("Archive checksum mismatch")
    for entry in metadata["artifacts"].values():
        name = entry["filename"]
        expected.add(name)
        if files.get(name) != {key: entry[key] for key in ("sha256", "size")}:
            raise ValueError("Runtime asset checksum/size mismatch")
    if native:
        if native["version"] != SOURCE:
            raise ValueError("Native catalog source mismatch")
        for platform, entry in native["artifacts"].items():
            if not re.fullmatch(r"(linux|darwin|windows)-(amd64|arm64)", platform):
                raise ValueError("Invalid native platform")
            name = f"oac-native-{SOURCE}-{platform}.tar.gz"
            expected.update((name, name + ".sha256"))
            if (entry.get("url") != BASE + "/" + name or files.get(name, {}).get("sha256") != entry["sha256"]
                    or (directory / (name + ".sha256")).read_text() != entry["sha256"] + "  " + name + "\n"):
                raise ValueError("Native asset URL/checksum mismatch")
    if set(files) != expected:
        raise ValueError("Candidate asset set mismatch; provide only generated flat files")
    return metadata, files


def verify_files(directory, inventory):
    actual = {p.name: file_identity(p) for p in directory.iterdir()}
    if actual != inventory:
        raise ValueError("Release asset bytes/set changed")


def verify_landed(tree, promotion_commit, allow_pending=False):
    source = api("commits/" + SOURCE)
    if source["commit"]["tree"]["sha"] != tree:
        raise ValueError("Source tree differs from GitHub commit")
    comparison = api("compare/" + SOURCE + "..." + promotion_commit)
    if comparison["status"] not in ("identical", "ahead"):
        raise ValueError("Promotion commit does not descend from qualified source")
    if any(f["filename"] not in PROMOTION_FILES or f.get("previous_filename", f["filename"]) not in PROMOTION_FILES
           for f in comparison.get("files", [])):
        raise ValueError("Promotion commit contains product changes")
    if any(f["filename"] == "Makefile" for f in comparison.get("files", [])):
        original = base64.b64decode(api("contents/Makefile?ref=" + SOURCE)["content"]).decode()
        updated = base64.b64decode(api("contents/Makefile?ref=" + promotion_commit)["content"]).decode()
        anchor = "\tPYTHONDONTWRITEBYTECODE=1 python3 scripts/core-distribution-manifest.test.py\n"
        addition = ("\tPYTHONDONTWRITEBYTECODE=1 python3 scripts/promote-qualified-release.test.py\n"
                    "\tPYTHONDONTWRITEBYTECODE=1 python3 scripts/qualification-control.test.py\n")
        if original.count(anchor) != 1 or updated != original.replace(anchor, anchor + addition):
            raise ValueError("Makefile change exceeds promotion test registration")
    reviewed_tree = api("commits/" + promotion_commit)["commit"]["tree"]["sha"]
    main = api("commits/main")
    if main["commit"]["tree"]["sha"] != reviewed_tree:
        if allow_pending:
            pending = api("compare/" + main["sha"] + "..." + promotion_commit)
            if pending["status"] == "ahead":
                return False
        raise ValueError("Main tree differs from reviewed promotion commit")
    comparison = api("compare/" + SOURCE + "..." + main["sha"])
    if comparison["status"] not in ("identical", "ahead"):
        raise ValueError("Qualified source has not landed on main")
    return True


def wait_for_landed(tree, promotion_commit, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while not verify_landed(tree, promotion_commit, allow_pending=True):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("Reviewed batch did not land within the merge wait budget")
        # Qualification remains in this process; no saved pass file is reloaded.
        time.sleep(min(30, remaining))


def release_state():
    # Listing avoids treating authentication/network errors as a missing release.
    pages = json.loads(run(["gh", "api", "--paginate", "--slurp",
                           "repos/" + REPO + "/releases?per_page=100"]))
    matches = [release for page in pages for release in page if release["tag_name"] == TAG]
    if len(matches) > 1:
        raise ValueError("Ambiguous release identity")
    return matches[0] if matches else None


def verify_tag(required=False):
    # gh api's matching-refs endpoint returns [] when the exact tag is absent.
    refs = api("git/matching-refs/tags/" + TAG)
    refs = [ref for ref in refs if ref["ref"] == "refs/tags/" + TAG]
    if required and not refs:
        raise ValueError("Published release tag is missing")
    if refs and (refs[0]["object"]["type"] != "commit" or refs[0]["object"]["sha"] != SOURCE):
        raise ValueError("Conflicting release tag")


def download(evidence, inventory):
    release = release_state()
    if not release:
        raise ValueError("Release disappeared")
    pages = json.loads(run(["gh", "api", "--paginate", "--slurp",
                           "repos/" + REPO + "/releases/" + str(release["id"]) + "/assets?per_page=100"]))
    assets = [asset for page in pages for asset in page]
    if len(assets) != len(inventory) or {asset["name"] for asset in assets} != set(inventory):
        raise ValueError("Release asset set mismatch")
    for name, expected in sorted(inventory.items()):
        command = ["gh", "release", "download", TAG, "--repo", REPO,
                   "--pattern", name, "--output", "-", "--allow-escape-sequences"]
        # Stream binary downloads into the hash, never into another archive copy.
        with tempfile.TemporaryFile() as errors:
            with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=errors) as process:
                checksum, size = hashlib.sha256(), 0
                for block in iter(lambda: process.stdout.read(1024 * 1024), b""):
                    checksum.update(block)
                    size += len(block)
                status = process.wait()
                if status:
                    raise subprocess.CalledProcessError(status, command)
        if {"sha256": checksum.hexdigest(), "size": size} != expected:
            raise ValueError("Downloaded release asset bytes changed: " + name)
    evidence.with_suffix(".json").write_text(canonical(inventory) + "\n")


def verify_remote(request, host):
    code = """import hashlib,json,pathlib,sys
request=json.load(sys.stdin)
directory=pathlib.Path(request['directory'])
expected=request['inventory']
actual={}
for name in expected:
    path=directory/name
    if path.is_symlink() or not path.is_file(): raise ValueError('Invalid remote asset')
    digest=hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''): digest.update(block)
    actual[name]={'sha256':digest.hexdigest(),'size':path.stat().st_size}
if actual != expected: raise ValueError('Remote candidate bytes changed')
"""
    run(["ssh", "-o", "BatchMode=yes", host, shlex.join(["python3", "-c", code])],
        input=canonical(request))


def qualification(request, host, remote, adapter, downloaded, package, manifest_hash):
    adapter_bytes = adapter.read_bytes()
    request["adapter_sha256"] = hashlib.sha256(adapter_bytes).hexdigest()
    control_hash = file_identity(adapter.with_name("qualification_control.py"))["sha256"]
    command = shlex.join(["mkdir", "-m", "700", remote])
    run(["ssh", "-o", "BatchMode=yes", host, command])
    run(["scp", "-q", "--", str(adapter), str(adapter.with_name("qualification_control.py")),
         *[str(p) for p in sorted(downloaded.iterdir())],
         host + ":" + remote + "/"])
    qualification_adapter.verify_package(package, manifest_hash)
    run(["scp", "-q", "-r", "--", str(package), host + ":" + remote + "/tools"])
    request["qualification_package"] = remote + "/tools"
    request["qualification_manifest_sha256"] = manifest_hash
    verify_remote(request, host)
    # Hash and execute the same bytes, avoiding a check-then-open script race.
    bootstrap = ("import hashlib,pathlib,sys,types; p=pathlib.Path(sys.argv[1]); b=p.read_bytes(); "
                 "hashlib.sha256(b).hexdigest()==sys.argv[2] or sys.exit('Adapter bytes changed'); "
                 "c=p.with_name('qualification_control.py'); raw=c.read_bytes(); "
                 "hashlib.sha256(raw).hexdigest()==sys.argv[3] or sys.exit('Control bytes changed'); "
                 "m=types.ModuleType('qualification_control'); m.__file__=str(c); "
                 "exec(compile(raw,str(c),'exec'),m.__dict__); sys.modules[m.__name__]=m; "
                 "sys.argv=[str(p)]; exec(compile(b,str(p),'exec'),{'__name__':'__main__','__file__':str(p)})")
    argv = ["python3", "-c", bootstrap, remote + "/" + adapter.name, request["adapter_sha256"], control_hash]
    response = qualification_adapter.control.transport(
        ["ssh", "-o", "BatchMode=yes", host, shlex.join(argv)], request)
    verify_remote(request, host)
    result = json.loads(response)
    required = {"source": SOURCE, "tree": request["tree"], "run_id": request["run_id"],
                "inventory_sha256": request["inventory_sha256"], "adapter_sha256": request["adapter_sha256"]}
    if any(result.get(key) != value for key, value in required.items()):
        raise ValueError("Qualification identity mismatch")
    if (result.get("status") != "passed" or result.get("qualification_manifest_sha256") != manifest_hash
            or result.get("checks") != {name: "passed" for name in REQUIRED_CHECKS}):
        raise ValueError("Qualification checks missing, failed or skipped")
    return result


def verify_tooling(promotion_commit):
    for name in ("promote-qualified-release.py", "qualify-core-release.py", "qualification_control.py", "core-distribution-manifest.py"):
        expected = base64.b64decode(api("contents/scripts/" + name + "?ref=" + promotion_commit)["content"])
        if pathlib.Path(__file__).with_name(name).read_bytes() != expected:
            raise ValueError("Local tooling differs from reviewed commit: " + name)


def publish_release(release_id, body_file):
    if type(release_id) is not int or release_id <= 0:
        raise ValueError("Invalid verified Release ID")
    updated = json.loads(run(["gh", "api", "--method", "PATCH", "repos/" + REPO + "/releases/" + str(release_id),
                              "--input", str(body_file)]))
    if updated.get("id") != release_id or updated.get("draft") is not False or updated.get("prerelease") is not False:
        raise ValueError("Publication response identity mismatch")


def promote(assets, state, host, remote_root, promotion_commit, *, source, package, manifest_hash,
            merge_wait_seconds=86400):
    select_source(source)
    if type(merge_wait_seconds) is not int or not 1 <= merge_wait_seconds <= 604800:
        raise ValueError("Merge wait must be between one second and seven days")
    qualification_adapter.verify_package(package, manifest_hash)
    if package == assets or package.is_relative_to(assets):
        raise ValueError("Qualification tooling must be separate from candidate assets")
    if not re.fullmatch(r"[0-9a-f]{40}", promotion_commit):
        raise ValueError("A reviewed full promotion commit is required")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@-]*", host):
        raise ValueError("Invalid SSH host")
    if (not re.fullmatch(r"/[A-Za-z0-9_./-]+", remote_root)
            or ".." in pathlib.PurePosixPath(remote_root).parts):
        raise ValueError("Remote root must be an absolute safe path")
    adapter = pathlib.Path(__file__).with_name("qualify-core-release.py")
    description = json.loads(run(["python3", str(adapter), "--describe"]))
    if description != {"ready": True, "required_checks": list(REQUIRED_CHECKS)}:
        raise ValueError("Live qualification adapter is not connected; no release changes made")
    verify_tooling(promotion_commit)
    state.mkdir(mode=0o700)  # Never resume a previous pass record.
    metadata, inventory = inspect_candidate(assets)
    (state / "inventory.json").write_text(canonical(inventory) + "\n")
    staged_bytes = sum(entry["size"] for entry in inventory.values())
    reserve = 64 * 1024 * 1024
    if shutil.disk_usage(state).free < reserve:
        raise ValueError("Insufficient local evidence/streaming disk reserve")
    (state / "capacity.json").write_text(canonical({"existing_asset_bytes": staged_bytes,
                                                  "additional_reserve_bytes": reserve}) + "\n")
    if api("commits/" + SOURCE)["commit"]["tree"]["sha"] != metadata["source_tree"]:
        raise ValueError("Candidate source tree mismatch")
    verify_tag()
    existing = release_state()
    if existing and existing["target_commitish"] != SOURCE:
        raise ValueError("Draft source target mismatch")
    if existing and not existing["draft"]:
        raise ValueError("Release already published; reconcile without rerunning acceptance")
    if not existing:
        notes = state / "release-notes.md"
        notes.write_text("Fresh-install offline distribution from " + SOURCE + ".\n")
        gh("release", "create", TAG, "--draft", "--target", SOURCE,
           "--title", "OpenAgentCore " + SOURCE, "--notes-file", str(notes),
           *[str(assets / name) for name in sorted(inventory)])
    downloaded = state / "downloaded"
    download(downloaded, inventory)
    initial_release = release_state()
    if not initial_release or not initial_release["draft"] or initial_release["target_commitish"] != SOURCE:
        raise ValueError("Draft identity changed")
    request = {"source": SOURCE, "tree": metadata["source_tree"], "run_id": str(uuid.uuid4()),
               "inventory": inventory, "inventory_sha256": hashlib.sha256(canonical(inventory).encode()).hexdigest(),
               "required_checks": list(REQUIRED_CHECKS)}
    remote = remote_root.rstrip("/") + "/" + request["run_id"]
    request["directory"] = remote
    result = qualification(request, host, remote,
                           adapter, assets, package, manifest_hash)
    (state / "request.json").write_text(canonical(request) + "\n")
    (state / "qualification.json").write_text(canonical(result) + "\n")
    verify_files(assets, inventory)
    wait_for_landed(metadata["source_tree"], promotion_commit, merge_wait_seconds)
    verify_tag()
    current = release_state()
    if (not current or not current["draft"] or current["id"] != initial_release["id"]
            or current["target_commitish"] != SOURCE):
        raise ValueError("Draft replaced or published during qualification")
    download(state / "before-publication", inventory)
    notes = state / "release-notes.md"
    notes.write_text("Fresh-install offline distribution from " + SOURCE + ".\n\n"
                     + "Real smoke checks: " + ", ".join(REQUIRED_CHECKS) + ".\n"
                     + "The full multi-host, generation and GC matrix was not rerun for this promotion.\n"
                     + "Asset inventory SHA256: " + request["inventory_sha256"] + ".\n"
                     + "Promotion tooling commit: " + promotion_commit + ".\n")
    # Downloading can take minutes. Revalidate immediately before mutation.
    verify_landed(metadata["source_tree"], promotion_commit)
    verify_tag()
    final_draft = release_state()
    if (not final_draft or final_draft["id"] != initial_release["id"] or not final_draft["draft"]
            or final_draft["target_commitish"] != SOURCE):
        raise ValueError("Draft identity changed during final verification")
    publication = state / "publication.json"
    publication.write_text(canonical({"draft": False, "prerelease": False, "make_latest": "true",
                                       "body": notes.read_text()}) + "\n")
    publish_release(final_draft["id"], publication)
    download(state / "published", inventory)
    final = release_state()
    if not final or final["id"] != current["id"] or final["draft"] or final["prerelease"]:
        raise ValueError("Final publication state mismatch")
    verify_tag(required=True)
    print(final["html_url"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="Full candidate source commit; never inferred from latest")
    parser.add_argument("--qualification-package", required=True, type=pathlib.Path,
                        help="Separately reviewed private package directory, containing manifest.json")
    parser.add_argument("--qualification-manifest-sha256", required=True, help="Explicit reviewed manifest digest")
    parser.add_argument("--merge-wait-seconds", type=int, default=86400, help="Same-process merge wait, at most seven days")
    parser.add_argument("--promotion-commit", required=True, help="Reviewed tooling commit, never artifact provenance")
    parser.add_argument("--assets", required=True, type=pathlib.Path, help="Flat candidate files only")
    parser.add_argument("--state", required=True, type=pathlib.Path, help="New local evidence directory")
    parser.add_argument("--host", required=True, help="Existing SSH host alias")
    parser.add_argument("--remote-root", required=True, help="Existing isolated remote parent directory")
    args = parser.parse_args()
    promote(args.assets.resolve(), args.state.resolve(), args.host, args.remote_root, args.promotion_commit,
            source=args.source, package=args.qualification_package.resolve(),
            manifest_hash=args.qualification_manifest_sha256, merge_wait_seconds=args.merge_wait_seconds)


if __name__ == "__main__":
    main()
