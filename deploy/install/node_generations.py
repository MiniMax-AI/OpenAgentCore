"""Private node generation preparation and collection; Core owns all authorization."""
import copy
import contextlib
import fcntl
import io
import stat
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import zipapp


def atomic_json(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix=".generation-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w") as stream:
            json.dump(value, stream, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        descriptor = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def helper_archive(args, installer):
    """Read the trusted bootstrap before dropping access to its private directory."""
    source = Path(sys.argv[0])
    if getattr(args, "bundle", None) is not None:
        source = args.bundle / "node-install.pyz"
    if source.suffix == ".pyz" and source.is_file() and not source.is_symlink():
        return source.read_bytes()
    with tempfile.TemporaryDirectory() as directory:
        package = Path(directory)
        source_dir = Path(installer.__file__).parent
        for name in ("node_install.py", "node_spec.py", "distribution.py", "node_generations.py"):
            shutil.copyfile(source_dir / name, package / ("__main__.py" if name == "node_install.py" else name))
        archive = io.BytesIO()
        zipapp.create_archive(package, archive, compressed=True)
        return archive.getvalue()


def install_helper(root, args, installer, archive=None):
    """Keep the executed, trusted installer available to the unprivileged node."""
    settings = root / "preparation.json"
    value = {"source_url": args.source_url or args.core_url}
    if settings.exists() and installer.private_json(settings) != value:
        raise installer.InstallError("The retained node artifact origin differs; preserve its configuration")
    target = root / "generation-preparer.pyz"
    installer.existing_file(target)
    if archive is None:
        archive = helper_archive(args, installer)
    with tempfile.TemporaryDirectory(dir=root) as directory:
        staged = Path(directory) / "helper.pyz"
        staged.write_bytes(archive)
        os.chmod(staged, 0o600)
        os.replace(staged, target)
    atomic_json(settings, value)


def generation_marker(root, generation, suffix, digest, installation, installer):
    path = root / "state/node/generations" / (str(generation) + suffix)
    if not path.exists() and not path.is_symlink():
        return None
    installer.no_links(path)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    with os.fdopen(descriptor) as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > (16384 if suffix == ".preparing" else 4096)):
            raise installer.InstallError("Invalid generation ownership journal")
        try:
            value = json.load(stream)
        except ValueError as error:
            raise installer.InstallError("Invalid generation ownership journal") from error
        named = path.lstat()
        if (named.st_dev, named.st_ino) != (info.st_dev, info.st_ino):
            raise installer.InstallError("Generation ownership journal was replaced")
    if digest is None:
        digest = value.get("specification_digest") if isinstance(value, dict) else None
        if not isinstance(digest, str) or not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise installer.InstallError("Invalid preparation specification identity")
    expected = {"installation_id": installation, "generation": generation, "specification_digest": digest}
    if (not isinstance(value, dict) or type(value.get("generation")) is not int
            or any(value.get(key) != item for key, item in expected.items())):
        raise installer.InstallError("Invalid generation ownership journal")
    keys = set(expected)
    if suffix == ".collecting":
        keys.add("native_complete")
        if type(value.get("native_complete")) is not bool:
            raise installer.InstallError("Invalid generation collection journal")
    if suffix == ".preparing":
        keys.add("import_started")
        if "configuration" in value:
            keys.add("configuration")
            plan = value["configuration"]
            if (not isinstance(plan, dict) or plan.get("installation_id") != installation or plan.get("generation") != generation
                    or plan.get("provider") not in ("docker", "microsandbox")
                    or installer.node_spec.digest(plan["provider"], plan.get("specification")) != digest):
                raise installer.InstallError("Invalid generation preparation plan")
        if type(value.get("import_started")) is not bool:
            raise installer.InstallError("Invalid generation preparation journal")
    if set(value) != keys:
        raise installer.InstallError("Invalid generation journal fields")
    return value


def marker_identity(args):
    return {"installation_id": args.installation_id, "generation": args.generation,
            "specification_digest": args.specification_digest}


def record_root_runtime(root, args, manifest, sums, installer):
    checksums = {"runtime/seccomp.json": sums["runtime/seccomp.json"]}
    if args.provider == "microsandbox":
        checksums.update({name: installer.distribution.artifact(manifest, name)["sha256"] for name in installer.MICRO})
    archive = installer.distribution.artifact(manifest, "images/runtime.tar.gz")
    checksums["images/runtime.tar.gz"] = archive["sha256"]
    checksums["images/runtime.tar"] = archive["unpacked_sha256"]
    value = {"installation_id": args.installation_id, "source_commit": manifest["source_commit"], "sha256": checksums}
    path = root / "runtime-artifacts.json"
    if installer.existing_file(path):
        if installer.private_json(path) != value:
            raise installer.InstallError("Original Runtime artifact ownership differs")
        return
    atomic_json(path, value)


def root_runtime_files(root, value, others, installer):
    """Return only verified original Runtime files that no retained config uses."""
    def paths(configuration):
        if configuration["provider"] == "docker":
            return [Path(configuration["docker"]["seccomp_file"])]
        return [Path(configuration["microsandbox"][key]) for key in ("helper_path", "runtime_path", "firmware_path")]
    names = ["runtime/seccomp.json", "images/runtime.tar.gz", "images/runtime.tar"]
    if value["provider"] == "microsandbox":
        names.extend(installer.MICRO)
    own_paths = paths(value)
    if not any(path == root / name for path in own_paths for name in names):
        return []
    referenced = {Path(os.path.realpath(path)) for item in others for path in paths(item)}
    auxiliary = {root / name for name in ("runtime/seccomp.json", "images/runtime.tar.gz", "images/runtime.tar")}
    # An original Runtime provider also retains its import cache and policy.
    if any(path in referenced for path in own_paths):
        referenced.update(auxiliary)
    candidates = [root / name for name in names if root / name not in referenced]
    if not candidates:
        return []
    record = root / "runtime-artifacts.json"
    installer.no_links(record)
    if not installer.existing_file(record):
        raise installer.InstallError("Original Runtime artifact ownership is missing; preserve its files")
    saved = installer.private_json(record)
    base = installer.private_json(root / "provider.json")
    if not base or base.get("installation_id") != value["installation_id"] or base.get("provider") != value["provider"]:
        raise installer.InstallError("Original Runtime configuration differs")
    source = base["specification"]["runtime"]["source_commit"]
    if (not saved or set(saved) != {"installation_id", "source_commit", "sha256"}
            or saved["installation_id"] != value["installation_id"] or saved["source_commit"] != source
            or not isinstance(saved["sha256"], dict) or set(saved["sha256"]) != set(names)
            or any(not isinstance(digest, str) or not re.fullmatch(r"[a-f0-9]{64}", digest) for digest in saved["sha256"].values())):
        raise installer.InstallError("Original Runtime artifact ownership differs")
    if value["provider"] == "microsandbox" and any(
            saved["sha256"][name] != base["specification"]["runtime"][field]
            for name, field in zip(installer.MICRO[1:], ("runtime_sha256", "firmware_sha256"))):
        raise installer.InstallError("Original Runtime artifact hashes differ from its specification")
    for path in candidates:
        installer.no_links(path)
        if installer.existing_file(path):
            if path.stat().st_nlink != 1 or installer.file_digest(path) != saved["sha256"][str(path.relative_to(root))]:
                raise installer.InstallError("Original Runtime file differs; refusing deletion")
    return candidates


def retained_configs(root, installer):
    directory = root / "state/node/generations"
    result = {}
    base = installer.private_json(root / "provider.json")
    if base and not generation_marker(root, base["generation"], ".dropped", installer.node_spec.digest(base["provider"], base["specification"]), base["installation_id"], installer):
        result[base["generation"]] = base
    if directory.exists():
        for path in sorted(directory.glob("*.json")):
            if not re.fullmatch(r"[1-9][0-9]*\.json", path.name):
                raise installer.InstallError("Invalid retained generation filename")
            value = installer.private_json(path)
            if value is None or value.get("generation") != int(path.stem):
                raise installer.InstallError("Invalid retained generation configuration")
            if not generation_marker(root, value["generation"], ".dropped", installer.node_spec.digest(value["provider"], value["specification"]), value["installation_id"], installer):
                if value["generation"] in result and result[value["generation"]] != value:
                    raise installer.InstallError("Conflicting retained generation configuration")
                result[value["generation"]] = value
    if directory.exists():
        for path in sorted(directory.glob("*.preparing")):
            if not re.fullmatch(r"[1-9][0-9]*\.preparing", path.name):
                raise installer.InstallError("Invalid preparation filename")
            if not base:
                raise installer.InstallError("Preparation installation identity is unavailable")
            journal = generation_marker(root, int(path.stem), ".preparing", None, base["installation_id"], installer)
            plan = journal.get("configuration")
            if plan is None:
                if int(path.stem) not in result:
                    raise installer.InstallError("Preparation plan is missing")
                continue
            validate_preparation_plan(root, plan, base, installer)
            if generation_marker(root, plan["generation"], ".dropped", journal["specification_digest"], plan["installation_id"], installer):
                continue
            final = result.get(plan["generation"])
            if final is not None:
                verify_plan_final(plan, final, installer)
            else:
                result[plan["generation"]] = plan
    return result


def validate_preparation_plan(root, plan, base, installer):
    if (not base or any(plan.get(key) != base.get(key) for key in ("installation_id", "provider", "core_url"))
            or type(plan.get("generation")) is not int):
        raise installer.InstallError("Preparation plan installation differs")
    runtime = plan["specification"]["runtime"]
    source = runtime["source_commit"]
    if not re.fullmatch(r"[a-f0-9]{40}", source):
        raise installer.InstallError("Preparation release identity differs")
    if plan["provider"] == "docker":
        policy = Path(plan["docker"]["seccomp_file"])
        if policy not in (root / "runtime/seccomp.json", root / "releases" / source / "runtime/seccomp.json"):
            raise installer.InstallError("Preparation policy is outside its immutable release")
        if plan["docker"]["image"] not in (runtime["image_id"], runtime["image_manifest_digest"]):
            raise installer.InstallError("Preparation image differs from the specification")
        installer.no_links(policy)
    else:
        micro = plan["microsandbox"]
        paths = [Path(micro[key]) for key in ("helper_path", "runtime_path", "firmware_path")]
        if not any(paths == [release / name for name in installer.MICRO] for release in (root, root / "releases" / source)):
            raise installer.InstallError("Preparation artifacts are outside their immutable release")
        expected_home = generation_home(root, plan, base, installer)
        if Path(micro["runtime_home"]) != expected_home:
            raise installer.InstallError("Preparation native store differs")
        for path in paths + [expected_home]:
            installer.no_links(path)
    configuration = dict(plan, core_url=plan["core_url"].removesuffix("/api/v1"))
    installer.node_spec.verify_provider(plan, configuration, plan.get("docker", {}).get("image"))


def verify_plan_final(plan, final, installer):
    expected = copy.deepcopy(plan)
    if plan["provider"] == "docker":
        image = final.get("docker", {}).get("image")
        runtime = plan["specification"]["runtime"]
        if image not in (runtime["image_id"], runtime["image_manifest_digest"]):
            raise installer.InstallError("Final Docker image differs from the preparation specification")
        expected["docker"]["image"] = image
    if expected != final:
        raise installer.InstallError("Final generation differs from its immutable preparation plan")


def owned_root(args, installer):
    if os.getuid() == 0:
        raise installer.InstallError("Generation operations run only as the node service user")
    root = Path.home() / ".oac/nodes" / args.installation_id
    if not root.is_dir():
        raise installer.InstallError("Retained node installation is missing")
    installer.safe_directory(root)
    identity = installer.private_json(root / "state/node/identity.json")
    if not identity or identity["identity"]["installation_id"] != args.installation_id:
        raise installer.InstallError("Retained node identity differs")
    return root, identity


def generation_home(root, configuration, base, installer):
    runtime = configuration["specification"]["runtime"]
    previous = base["microsandbox"]
    if (runtime["runtime_sha256"], runtime["firmware_sha256"]) == (previous["runtime_sha256"], previous["firmware_sha256"]):
        return Path(previous["runtime_home"])
    material = ":".join((configuration["installation_id"], runtime["runtime_sha256"], runtime["firmware_sha256"]))
    home = Path.home() / ".oac/m" / hashlib.sha256(material.encode()).hexdigest()[:12]
    if len(os.fsencode(home)) > 48:
        raise installer.InstallError("HOME is too long for versioned microsandbox socket paths")
    return home


def image_available(value, installer):
    try:
        if value["provider"] == "docker":
            seccomp = Path(value["docker"]["seccomp_file"])
            installer.existing_file(seccomp)
            json.loads(seccomp.read_text())
            raw = installer.checked(list(installer.DOCKER) + ["image", "inspect", value["docker"]["image"], "--format", "{{.Id}} {{.Os}}/{{.Architecture}}"], "Cannot inspect pinned image")
            return raw.strip() == value["docker"]["image"] + " linux/amd64"
        micro = value["microsandbox"]
        for key in ("helper_path", "runtime_path", "firmware_path"):
            if not installer.existing_file(Path(micro[key])):
                return False
        if (installer.file_digest(Path(micro["runtime_path"])) != micro["runtime_sha256"]
                or installer.file_digest(Path(micro["firmware_path"])) != micro["firmware_sha256"]):
            return False
        env = dict(os.environ, MSB_BACKEND="local", MSB_HOME=micro["runtime_home"], MSB_PATH=micro["runtime_path"], MSB_LIBKRUNFW_PATH=micro["firmware_path"])
        image = json.loads(installer.checked([micro["runtime_path"], "image", "inspect", micro["image"], "--format", "json"], "Cannot inspect pinned image", env=env))
        return image.get("digest") == micro["image"].split("@", 1)[1] and image.get("architecture") == "amd64" and image.get("os") == "linux"
    except (installer.InstallError, OSError, ValueError):
        return False


def runtime_files(root, value, args, manifest, sums, installer):
    """Restore only absent immutable bytes; existing conflicts are never replaced."""
    source = args.configuration["specification"]["runtime"]["source_commit"]
    release = root / "releases" / source
    if value is not None:
        if args.provider == "microsandbox":
            release = Path(value["microsandbox"]["helper_path"]).parents[2]
            if any(Path(value["microsandbox"][key]) != release / name for key, name in zip(
                    ("helper_path", "runtime_path", "firmware_path"), installer.MICRO)):
                raise installer.InstallError("Retained Runtime artifact paths differ")
        else:
            release = Path(value["docker"]["seccomp_file"]).parents[1]
            if Path(value["docker"]["seccomp_file"]) != release / "runtime/seccomp.json":
                raise installer.InstallError("Retained Runtime seccomp path differs")
        if release not in (root, root / "releases" / source):
            raise installer.InstallError("Retained Runtime artifacts are outside this installation")
    installer.no_links(release)
    installer.safe_directory(release)
    names = ("runtime/seccomp.json",) + (installer.MICRO if args.provider == "microsandbox" else ())
    saved = installer.private_json(release / "manifest.json")
    if (release / "manifest.json").exists():
        if saved is None:
            raise installer.InstallError("Retained Runtime manifest is unreadable")
        try:
            installer.node_spec.verify_release(args.configuration, saved)
        except installer.node_spec.SpecificationError as error:
            raise installer.RuntimeDownloadError("Retained Runtime release provenance differs") from error
    # Inspect every existing byte before starting any repair, so a missing file
    # cannot hide a conflicting sibling or redirect a later download.
    for name in names:
        path = release / name
        installer.no_links(path)
        if installer.existing_file(path):
            expected = sums[name] if name == "runtime/seccomp.json" else installer.distribution.artifact(manifest, name)["sha256"]
            if installer.file_digest(path) != expected:
                raise installer.RuntimeDownloadError("Retained Runtime artifact checksum differs")
    for name in names:
        installer.safe_directory((release / name).parent)
        if name == "runtime/seccomp.json":
            installer.download(args.source_url, name, release, sums[name], prefix="releases/" + source + "/")
        else:
            installer.distribution.obtain_artifact(manifest, name, release / name)
            os.chmod(release / name, 0o700)
    atomic_json(release / "manifest.json", manifest)
    return release


def prepare(args, installer):
    root, identity = owned_root(args, installer)
    with installer.install_lock(root), collection_lease(
            root, args.generation, installer, marker_identity(args),
            initialize=args.generation not in retained_configs(root, installer)
            and not (root / "state/node/generations" / (str(args.generation) + ".json")).exists()):
        directory = root / "state/node/generations"
        target = directory / (str(args.generation) + ".json")
        if (generation_marker(root, args.generation, ".dropped", args.specification_digest, args.installation_id, installer)
                or generation_marker(root, args.generation, ".collecting", args.specification_digest, args.installation_id, installer)):
            raise installer.InstallError("A dropped generation cannot be adopted again")
        preparation = generation_marker(root, args.generation, ".preparing", args.specification_digest, args.installation_id, installer)
        args.core_url = identity["core_url"]
        args.configuration = installer.node_spec.fetch(args, "", identity, installer.open_request,
                                                       generation=args.generation, allow_selection_change=True)
        if args.configuration["specification_digest"] != args.specification_digest:
            raise installer.InstallError("Core generation identity differs from its authorization")
        args.provider = args.configuration["provider"]
        configurations = retained_configs(root, installer)
        base = installer.private_json(root / "provider.json")
        runtime = args.configuration["specification"]["runtime"]
        value = configurations.get(args.generation)
        finalized = target.exists() or base["generation"] == args.generation
        if value is not None:
            installer.node_spec.verify_provider(value, args.configuration, value.get("docker", {}).get("image"))
        else:
            for candidate in configurations.values():
                # Unpublished plans must never be used as ready reuse candidates.
                if any((directory / (str(candidate["generation"]) + suffix)).exists() for suffix in (".preparing", ".collecting", ".dropped")):
                    continue
                if candidate["specification"]["runtime"] == runtime and image_available(candidate, installer):
                    value = copy.deepcopy(candidate)
                    value["generation"] = args.generation
                    value["specification"] = args.configuration["specification"]
                    if args.provider == "microsandbox":
                        value["microsandbox"].update(value["specification"]["resources"])
                    break
        if not finalized or value is None or not image_available(value, installer):
            settings = installer.private_json(root / "preparation.json")
            args.source_url = installer.origin(settings["source_url"])
            args.bundle = None
            manifest, sums = installer.metadata(args.source_url, prefix="releases/" + runtime["source_commit"] + "/")
            try:
                installer.node_spec.verify_release(args.configuration, manifest)
            except installer.node_spec.SpecificationError as error:
                raise installer.RuntimeDownloadError("Runtime release provenance differs") from error
            if args.provider == "microsandbox":
                args.runtime_home = Path(value["microsandbox"]["runtime_home"]) if value else generation_home(root, args.configuration, base, installer)
            if value is None:
                value = installer.provider_config(root / "releases" / runtime["source_commit"], args, manifest, runtime["image_id"])
            if preparation is None:
                preparation = dict(marker_identity(args), import_started=False, configuration=copy.deepcopy(value))
                atomic_json(directory / (str(args.generation) + ".preparing"), preparation)
            release = runtime_files(root, value, args, manifest, sums, installer)
            if args.provider == "microsandbox":
                installer.safe_directory(args.runtime_home)
                owner = args.runtime_home / "oac-installation.json"
                if not owner.exists() and any(args.runtime_home.iterdir()):
                    raise installer.InstallError("Versioned microsandbox store contains unowned state")
                installer.write_once(owner, installer.json_text({"installation_id": args.installation_id}))
            preparation = dict(preparation, import_started=True)
            atomic_json(directory / (str(args.generation) + ".preparing"), preparation)
            runtime_image = installer.prepare_runtime(release, args, manifest)
            if finalized:
                installer.node_spec.verify_provider(value, args.configuration, runtime_image)
            elif args.provider == "docker":
                if runtime_image not in (runtime["image_id"], runtime["image_manifest_digest"]):
                    raise installer.InstallError("Resolved Docker image is outside the authorized specification")
                value = copy.deepcopy(value)
                value["docker"]["image"] = runtime_image
        if preparation and preparation.get("configuration"):
            verify_plan_final(preparation["configuration"], value, installer)
        if installer.existing_file(target):
            if installer.private_json(target) != value:
                raise installer.InstallError("Immutable generation configuration differs")
        else:
            atomic_json(target, value)
        # If interrupted after publication, restart rechecks the same plan and
        # final identity. It never edits the published provider configuration.
        pending = directory / (str(args.generation) + ".preparing")
        pending.unlink(missing_ok=True)
        descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)


@contextlib.contextmanager
def collection_lease(root, generation, installer, identity=None, initialize=False):
    directory = root / "state/node/generations"
    installer.safe_directory(directory)
    path = directory / (str(generation) + ".lease")
    record = directory / (str(generation) + ".lease-identity")
    if identity is None:
        value = retained_configs(root, installer).get(generation)
        if value is None:
            # Dropped configurations remain as immutable identity receipts.
            value = installer.private_json(directory / (str(generation) + ".json")) or installer.private_json(root / "provider.json")
        if not value or value["generation"] != generation:
            raise installer.InstallError("Generation lease identity is unavailable")
        identity = {"installation_id": value["installation_id"], "generation": generation,
                    "specification_digest": installer.node_spec.digest(value["provider"], value["specification"])}
    flags = os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK
    created = False
    if initialize and not record.exists() and not record.is_symlink():
        # O_EXCL prevents adopting an old unrecorded inode after interruption.
        descriptor = os.open(path, flags | os.O_CREAT | os.O_EXCL, 0o600)
        created = True
    else:
        descriptor = os.open(path, flags)
    try:
        info = os.fstat(descriptor)
        if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                or info.st_uid != os.geteuid() or info.st_nlink != 1):
            raise installer.InstallError("Invalid generation helper lease")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise installer.InstallError("Generation helper is still active") from error
        named = path.lstat()
        if (named.st_dev, named.st_ino) != (info.st_dev, info.st_ino):
            raise installer.InstallError("Generation helper lease was replaced")
        expected = dict(identity, device=info.st_dev, inode=info.st_ino)
        if created:
            os.fsync(descriptor)
            atomic_json(record, expected)
        else:
            installer.no_links(record)
            fd = os.open(record, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
            with os.fdopen(fd) as stream:
                saved_info = os.fstat(stream.fileno())
                if (not stat.S_ISREG(saved_info.st_mode) or stat.S_IMODE(saved_info.st_mode) != 0o600
                        or saved_info.st_uid != os.geteuid() or saved_info.st_nlink != 1 or saved_info.st_size > 4096):
                    raise installer.InstallError("Invalid durable generation lease identity")
                saved = json.load(stream)
                named_record = record.lstat()
                if (named_record.st_dev, named_record.st_ino) != (saved_info.st_dev, saved_info.st_ino):
                    raise installer.InstallError("Durable generation lease identity was replaced")
                if saved != expected or any(type(saved.get(key)) is not int for key in ("generation", "device", "inode")):
                    raise installer.InstallError("Generation helper lease identity differs; retain its payload")
        yield
    finally:
        # Never unlink a lease: a replacement inode could bypass an old helper.
        os.close(descriptor)


def collect(args, installer):
    root, _ = owned_root(args, installer)
    with installer.install_lock(root), collection_lease(root, args.generation, installer, marker_identity(args)):
        configurations = retained_configs(root, installer)
        value = configurations.get(args.generation)
        if value is None:
            return
        if (value["installation_id"] != args.installation_id
                or installer.node_spec.digest(value["provider"], value["specification"]) != args.specification_digest):
            raise installer.InstallError("Collection grant does not match the local generation")
        marker = root / "state/node/generations" / (str(args.generation) + ".legacy-unfenced")
        if marker.exists() or marker.is_symlink():
            raise installer.InstallError("The original v1 generation retains unfenced legacy helpers; its local payload is kept")
        source = value["specification"]["runtime"]["source_commit"]
        if not re.fullmatch(r"[a-f0-9]{40}", source):
            raise installer.InstallError("Invalid retained release identity")
        release = root / "releases" / source
        installer.no_links(release)
        directory = root / "state/node/generations"
        journal = generation_marker(root, args.generation, ".collecting", args.specification_digest, args.installation_id, installer)
        if journal is None:
            journal = dict(marker_identity(args), native_complete=False)
            atomic_json(directory / (str(args.generation) + ".collecting"), journal)
        others = [item for generation, item in configurations.items() if generation != args.generation]
        original_files = root_runtime_files(root, value, others, installer)
        if not journal["native_complete"]:
            preparation = generation_marker(root, args.generation, ".preparing", args.specification_digest, args.installation_id, installer)
            if preparation is None or preparation["import_started"]:
                collect_image(args, value, others, installer)
            # Persist native completion before deleting its executable. A fresh
            # Core grant is still required after restart to finish file cleanup.
            journal["native_complete"] = True
            atomic_json(directory / (str(args.generation) + ".collecting"), journal)
        directory = root / "state/node/generations"
        installer.safe_directory(directory)
        if not any(item["specification"]["runtime"]["source_commit"] == source for item in others):
            release = root / "releases" / source
            installer.no_links(release)
            if release.exists():
                shutil.rmtree(release)
            if release.parent.exists():
                descriptor = os.open(release.parent, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    os.fsync(descriptor)
                finally:
                    os.close(descriptor)

        for path in original_files:
            path.unlink(missing_ok=True)
        for parent in {path.parent for path in original_files}:
            if parent.exists():
                descriptor = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    os.fsync(descriptor)
                finally:
                    os.close(descriptor)
        atomic_json(directory / (str(args.generation) + ".dropped"), marker_identity(args))
        # Keep the immutable configuration and permanent journal as restart
        # identity. They contain no provider credentials and are not readiness.


def collect_image(args, value, others, installer):
    if value["provider"] == "microsandbox":
        micro = value["microsandbox"]
        shared = [item for item in others if item["microsandbox"]["runtime_home"] == micro["runtime_home"]]
        home = Path(micro["runtime_home"])
        installer.no_links(home)
        if not home.is_dir() or installer.private_json(home / "oac-installation.json") != {"installation_id": args.installation_id}:
            raise installer.InstallError("Microsandbox store ownership differs")
        runtime_path = Path(micro["runtime_path"])
        installer.no_links(runtime_path)
        if not installer.existing_file(runtime_path) or installer.file_digest(runtime_path) != micro["runtime_sha256"]:
            raise installer.InstallError("Cannot verify retained microsandbox executable")
        env = dict(os.environ, MSB_BACKEND="local", MSB_HOME=micro["runtime_home"], MSB_PATH=micro["runtime_path"], MSB_LIBKRUNFW_PATH=micro["firmware_path"])
        if not any(item["microsandbox"]["image"] == micro["image"] for item in shared):
            # A failed inspect/remove is not proof of absence. A successful full
            # inventory must contain only understood immutable references.
            raw = installer.checked([micro["runtime_path"], "image", "list", "--quiet"], "Cannot verify microsandbox image inventory", env=env)
            references = raw.splitlines()
            if any(not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", item) for item in references):
                raise installer.InstallError("Cannot verify microsandbox image inventory")
            if any(item.split("@", 1)[1] == micro["image"].split("@", 1)[1] for item in references):
                installer.checked([micro["runtime_path"], "image", "remove", micro["image"], "--quiet"], "Runtime image is still in use", env=env)
        if not shared:
            raw = installer.checked([micro["runtime_path"], "sandbox", "list", "--format", "json"], "Cannot verify empty microsandbox store", env=env)
            if json.loads(raw) != []:
                raise installer.InstallError("Microsandbox store still contains native sandboxes")
            home = Path(micro["runtime_home"])
            installer.safe_directory(home)
            if installer.private_json(home / "oac-installation.json") != {"installation_id": args.installation_id}:
                raise installer.InstallError("Microsandbox store ownership differs")
            # Native emptiness never authorizes discarding receipt/lock history.
    else:
        # Docker imported content belongs to the host daemon, including idle
        # serving pins in other installations. This installation has no authority
        # to remove it; its private generation/release files can still be collected.
        return
