"""Private node generation preparation and collection; Core owns all authorization."""
import copy
import contextlib
import fcntl
import io
import stat
import json
import os
from pathlib import Path
import re
import sys
import zipfile

from distribution import temporary_file


def atomic_json(path, value):
    with temporary_file(path) as temporary:
        with os.fdopen(os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as stream:
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


def helper_archive(args, installer):
    """Read the trusted bootstrap before dropping access to its private directory."""
    source = Path(sys.argv[0])
    if source.suffix == ".pyz" and source.is_file() and not source.is_symlink():
        return source.read_bytes()
    archive = io.BytesIO()
    source_dir = Path(installer.__file__).parent
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as package:
        for name in ("node_install.py", "node_spec.py", "distribution.py", "node_generations.py", "install_display.py", "node_output.py", "provider_assets.py"):
            package.write(source_dir / name, "__main__.py" if name == "node_install.py" else name)
    return archive.getvalue()


def install_helper(root, args, installer, archive=None):
    """Keep the executed, trusted installer available to the unprivileged node."""
    target = root / "generation-preparer.pyz"
    installer.existing_file(target)
    if archive is None:
        archive = helper_archive(args, installer)
    with temporary_file(target) as staged:
        with os.fdopen(os.open(staged, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as output:
            output.write(archive)
        os.replace(staged, target)


def generation_marker(root, generation, suffix, digest, installation, installer):
    path = root / "state/node/generations" / (str(generation) + suffix)
    if not path.exists() and not path.is_symlink():
        return None
    installer.no_links(path)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    with os.fdopen(descriptor) as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > 4096):
            raise installer.InstallError("Invalid generation ownership journal")
        try:
            value = json.load(stream)
        except ValueError as error:
            raise installer.InstallError("Invalid generation ownership journal") from error
        named = path.lstat()
        if (named.st_dev, named.st_ino) != (info.st_dev, info.st_ino):
            raise installer.InstallError("Generation ownership journal was replaced")
    expected = {"installation_id": installation, "generation": generation, "specification_digest": digest}
    if (not isinstance(value, dict) or type(value.get("generation")) is not int
            or any(value.get(key) != item for key, item in expected.items())):
        raise installer.InstallError("Invalid generation ownership journal")
    if set(value) != set(expected):
        raise installer.InstallError("Invalid generation journal fields")
    return value


def marker_identity(args):
    return {"installation_id": args.installation_id, "generation": args.generation,
            "specification_digest": args.specification_digest}


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
    return result


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


def image_available(value, installer):
    """Whether the installation's Runtime that this configuration names is still on the node."""
    try:
        if value["provider"] == "docker":
            seccomp = Path(value["native"]["seccomp_file"])
            installer.existing_file(seccomp)
            json.loads(seccomp.read_text())
            raw = installer.checked(list(installer.DOCKER) + ["image", "inspect", value["native"]["image"], "--format", "{{.Id}} {{.Os}}/{{.Architecture}}"], "Cannot inspect pinned image")
            return raw.strip() == value["native"]["image"] + " linux/amd64"
        micro = value["native"]
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


def prepare(args, installer):
    """Write the generation's configuration: the installation's own Runtime under the specification Core authorized."""
    root, identity = owned_root(args, installer)
    with installer.install_lock(root), collection_lease(
            root, args.generation, installer, marker_identity(args),
            initialize=args.generation not in retained_configs(root, installer)
            and not (root / "state/node/generations" / (str(args.generation) + ".json")).exists()):
        directory = root / "state/node/generations"
        target = directory / (str(args.generation) + ".json")
        if generation_marker(root, args.generation, ".dropped", args.specification_digest, args.installation_id, installer):
            raise installer.InstallError("A dropped generation cannot be adopted again")
        args.core_url = identity["core_url"]
        configuration = installer.node_spec.fetch(args, "", identity, installer.open_request,
                                                  generation=args.generation, allow_selection_change=True)
        if configuration["specification_digest"] != args.specification_digest:
            raise installer.InstallError("Core generation identity differs from its authorization")
        base = installer.private_json(root / "provider.json")
        value = retained_configs(root, installer).get(args.generation)
        if value is not None:
            installer.node_spec.verify_provider(value, configuration, value.get("native", {}).get("image"))
        else:
            value = copy.deepcopy(base)
            value["generation"], value["specification"] = args.generation, configuration["specification"]
        if not image_available(value, installer):
            raise installer.InstallError("This installation's Runtime is missing on the node; remove the node, uninstall it and add it again")
        if installer.existing_file(target):
            if installer.private_json(target) != value:
                raise installer.InstallError("Immutable generation configuration differs")
        else:
            atomic_json(target, value)


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
        try:
            descriptor = os.open(path, flags | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            raise installer.InstallError(
                "An earlier run stopped while creating " + str(path) + ". Check that no process of the oac-node user still "
                "uses this installation (`pgrep -u oac-node`), delete that file with sudo, then rerun the command.") from None
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
        value = retained_configs(root, installer).get(args.generation)
        if value is None:
            return
        if (value["installation_id"] != args.installation_id
                or installer.node_spec.digest(value["provider"], value["specification"]) != args.specification_digest):
            raise installer.InstallError("Collection grant does not match the local generation")
        # The Runtime belongs to the installation; uninstall removes it. Keep the immutable
        # configuration and this permanent journal as restart identity. They contain no
        # provider credentials and are not readiness.
        atomic_json(root / "state/node/generations" / (str(args.generation) + ".dropped"), marker_identity(args))
