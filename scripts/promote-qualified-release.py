#!/usr/bin/env python3
"""Qualify one candidate on a supervised host, then publish it once its promotion commit lands.

One invocation owns one explicit candidate and one qualification run, using the
existing gh authentication and SSH. It never builds: pass the candidate's flat
files, and it creates the unpublished build-<SHA> draft itself. Produce the files
with make build-core-distribution for that commit, with
CORE_DISTRIBUTION_RELEASE_BASE_URL set to
https://github.com/MiniMax-AI/OpenAgentCore/releases/download/build-<SHA>,
CORE_DISTRIBUTION_OFFLINE=1 and the native installer catalog, or take the Actions
artifact of a manual core-release run with draft_release=false and remove
install.sh and install.sh.sha256. A draft that core-release created holds
install.sh and cannot be promoted.

Inputs
- --source: the full candidate commit, never inferred from latest. It binds the
  archive manifests and the build-<SHA> release tag.
- --assets: only the candidate's flat files: the thin and offline archives with
  their checksums, the Runtime assets and the native installers with their
  checksums. Any other file, including install.sh, is refused. Every archive
  member and asset hash is verified, and the thin and offline manifests and native
  catalogs must match.
- --qualification-package, --qualification-manifest-sha256: a separately reviewed
  private package and its manifest digest. The manifest fixes the complete file
  inventory, ordered Python commands, bounded stage timeouts and private path and
  resource configuration. It is verified before any Release change and again by
  the remote supervisor; candidate assets cannot select or replace it. Keep
  host-specific scripts, user names and credential paths out of this repository,
  and never put credential values in either manifest.
- --promotion-commit: the independently reviewed tooling commit; its tree is the
  one main must reach. It must descend from the candidate, and the local promotion
  scripts must equal their bytes there. It may differ from the candidate only in
  PROMOTION_FILES, and the Makefile only by registering the promotion and
  control-channel tests. Product changes or a different main tree block promotion.
- --host, --remote-root: an existing SSH host alias and an isolated remote parent.
- --state: a new private local evidence directory; a previous one is never reused.
- --merge-wait-seconds: how long to wait for main to reach the promotion commit's
  tree; one day by default, at most seven days.

Flow
1. Check the qualification adapter, the tooling bytes, the candidate files and
   the source tree. Refuse a conflicting tag, a published Release or a draft for
   another commit, and create the build-<SHA> draft with these files when none
   exists. Download every asset and compare its bytes.
2. Copy the assets and the package to a fresh <remote-root>/<UUID>. The reviewed
   adapter supervises fresh-install, current-lifecycle, managed-native-smoke,
   diagnostics-observations-smoke and node-runtime-smoke in that order: one fresh
   container installation, one completed managed Session, read-only diagnostics
   for it, and one current Runtime Session on one new node. The full multi-host,
   generation and GC matrix is not rerun. Every check must pass in this run and
   return this run's identity and its own owned resources; a supplied pass file,
   skipped check or old report never releases the candidate. Candidate bytes are
   verified again after the stages.
3. In the same process, wait until main's tree equals the promotion commit's tree
   and main contains the candidate. A main that is behind waits; a conflicting
   main fails at once. Expiry or cancellation keeps the evidence and grants no
   later permission to publish.
4. Check the tag and that the same draft ID is still an unpublished draft, and
   download every asset again. Then recheck main, the tree, the tag and the draft
   ID, publish that Release ID (never a fresh tag lookup) as the latest release,
   and download once more to check the published bytes.

The SSH stdin channel carries the request and then heartbeats; EOF, timeout,
SIGTERM or SIGHUP stops later work. Each stage runs in its own foreground process
group with an owner outside it that cleans the group on success, failure, timeout
and cancellation, including descendants orphaned by an inner timeout or SIGKILL.
Only explicitly recorded background resources may detach. A write already issued
may have an unknown outcome: keep its intent and resources, never replay it or
claim a rollback. Control tests use short-lived fixture children and never count
as live qualification.

Never overwrite conflicting assets. Reconcile an interrupted run before invoking
again; stored results are evidence, not permission to publish. The command never
merges pull requests or decides what lands on main; it only waits for main. No
runner, background service, GitHub secret or repository visibility change is
needed.
"""

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

REPO = "MiniMax-AI/OpenAgentCore"
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
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
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
