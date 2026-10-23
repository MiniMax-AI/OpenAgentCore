"""microsandbox host preparation for a node; node_install.py selects it through its HOSTS table.

Every function takes the installer module first, as node_generations.py does, so this file needs no import of it."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import stat
import subprocess
from urllib.parse import urlsplit
import uuid

import distribution
import provider_assets

NAME = "microsandbox"
ARTIFACTS = provider_assets.artifacts("microsandbox", ("runtime",))
KVM = Path("/dev/kvm")
# The group that owns the provider's device; the node's service user joins only this one.
GROUP = "kvm"
UNIT_AFTER = "network-online.target"
# Unpacked Runtime copies the Provider's store keeps on the disk beyond the one the import unpacks.
EXTRA_UNPACKED_COPIES = 1


def home(installer, installation_id):
    directory = Path.home() / ".oac/m" / hashlib.sha256(installation_id.encode()).hexdigest()[:12]
    if len(os.fsencode(directory)) > 48:
        raise installer.InstallError("HOME is too long for microsandbox Unix socket paths; use a user with a shorter persistent home directory")
    return directory


def checkpoint_root(args):
    return getattr(args, "checkpoint_root", None) or Path.home() / ".oac/checkpoints" / hashlib.sha256(args.installation_id.encode()).hexdigest()[:12]


def prepare_checkpoint_root(installer, directory, *, initialize=True):
    directory = Path(directory)
    if not directory.is_absolute() or directory.resolve() != directory:
        raise installer.InstallError("Checkpoint root must be a canonical absolute directory")
    if not initialize and not directory.is_dir():
        raise installer.InstallError("Retained checkpoint root is missing")
    installer.safe_directory(directory)
    marker = directory / ".oac-checkpoint-store"
    if not installer.existing_file(marker):
        if not initialize:
            raise installer.InstallError("Retained checkpoint store identity is missing")
        if any(directory.iterdir()):
            raise installer.InstallError("Checkpoint root contains unowned state")
        try:
            descriptor = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        except FileExistsError:
            pass
        else:
            with os.fdopen(descriptor, "w") as output:
                output.write(str(uuid.uuid4()) + "\n")
                output.flush()
                os.fsync(output.fileno())
    installer.existing_file(marker)
    value = marker.read_text()
    try:
        identity = uuid.UUID(value.rstrip("\n"))
    except ValueError:
        raise installer.InstallError("Invalid checkpoint store identity") from None
    if identity.int == 0 or value != str(identity) + "\n":
        raise installer.InstallError("Invalid checkpoint store identity")
    with marker.open("rb") as source:
        os.fsync(source.fileno())
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def environment(installer, root, args):
    return dict(os.environ, MSB_BACKEND="local", MSB_HOME=str(home(installer, args.installation_id)),
                MSB_PATH=str(root / ARTIFACTS[1]), MSB_LIBKRUNFW_PATH=str(root / ARTIFACTS[2]))


def image_ready(installer, root, args, manifest):
    inspect = [str(root / ARTIFACTS[1]), "image", "inspect", manifest["runtime_ref"], "--format", "json"]
    try:
        value = json.loads(installer.checked(inspect, "Runtime image is not installed", env=environment(installer, root, args)))
        return (value.get("digest") == manifest["runtime_ref"].split("@", 1)[1]
                and value.get("architecture") == "amd64" and value.get("os") == "linux")
    except (installer.InstallError, ValueError, AttributeError):
        return False


def preflight(installer):
    if not os.access(KVM, os.R_OK | os.W_OK):
        raise installer.InstallError("microsandbox requires read/write access to /dev/kvm")


def host_check(installer):
    """Check KVM without installing it; return the group that grants access (None when everyone has it)."""
    try:
        info = KVM.stat()
    except OSError:
        info = None
    if info is None or not stat.S_ISCHR(info.st_mode):
        raise installer.InstallError("KVM is unavailable: enable hardware virtualization (or nested virtualization for this VM) and "
                                     "load kvm_intel or kvm_amd, then rerun this command." + installer.NOTHING_CHANGED)
    if stat.S_IMODE(info.st_mode) & 0o006 == 0o006:
        return None, {}
    if info.st_gid == 0 or stat.S_IMODE(info.st_mode) & 0o060 != 0o060:
        raise installer.InstallError("/dev/kvm must be group-accessible, for example root:kvm with mode 0660 (your distribution's "
                                     "KVM package sets this), then rerun this command." + installer.NOTHING_CHANGED)
    return installer.device_group(GROUP, KVM, info.st_gid), {}


def capacity(installer, details):
    memory = 0
    with open("/proc/meminfo") as stream:
        for line in stream:
            if line.startswith("MemTotal:"):
                memory = int(line.split()[1]) * 1024
    return os.cpu_count() or 0, memory


def other_node(installer, args):
    pass


def configure(installer, root, args):
    retained_provider = installer.private_json(root / "provider.json")
    if retained_provider:
        retained_root = Path(retained_provider["native"]["checkpoint_root"])
        if getattr(args, "checkpoint_root", None) not in (None, retained_root):
            raise installer.InstallError("Retained checkpoint root differs; preserve its state")
        args.checkpoint_root = retained_root
    runtime_home = home(installer, args.installation_id)
    prepare_checkpoint_root(installer, checkpoint_root(args), initialize=retained_provider is None)
    installer.safe_directory(runtime_home)
    owner = runtime_home / "oac-installation.json"
    if not owner.exists() and any(runtime_home.iterdir()):
        raise installer.InstallError("Microsandbox home contains unowned state; refusing to adopt it")
    installer.write_once(owner, installer.json_text({"installation_id": args.installation_id}))


def native_config(installer, root, args, manifest, runtime_image):
    endpoint = urlsplit(args.core_url)
    port = endpoint.port or (443 if endpoint.scheme == "https" else 80)
    addresses = sorted({entry[4][0] for entry in socket.getaddrinfo(endpoint.hostname, port, type=socket.SOCK_STREAM)})
    core_rules = [{"action": "allow", "direction": "egress", "destination": address, "protocol": "tcp", "port": str(port)} for address in addresses]
    return {
        "helper_path": str(root / ARTIFACTS[0]), "runtime_path": str(root / ARTIFACTS[1]), "firmware_path": str(root / ARTIFACTS[2]),
        "image": manifest["runtime_ref"], "runtime_sha256": manifest["microsandbox"]["runtime_sha256"],
        "firmware_sha256": manifest["microsandbox"]["firmware_sha256"],
        "runtime_home": str(home(installer, args.installation_id)),
        "checkpoint_root": str(checkpoint_root(args)),
        "network": {"default_egress": "deny", "default_ingress": "deny", "rules": core_rules + [
            {"action": "allow", "direction": "egress", "destination": "public"},
            {"action": "allow", "direction": "egress", "destination": "host", "protocol": "udp", "port": "53"},
            {"action": "allow", "direction": "egress", "destination": "host", "protocol": "tcp", "port": "53"},
        ]},
    }


def runtime_imported(installer, root, args, manifest):
    return (root / ARTIFACTS[1]).is_file() and image_ready(installer, root, args, manifest)


def prepare_runtime(installer, root, args, manifest):
    for name in ARTIFACTS:
        result = subprocess.run(["ldd", str(root / name)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
        output = result.stdout.decode()
        if "not found" in output or (result.returncode and "statically linked" not in output and "not a dynamic executable" not in output):
            raise installer.InstallError("Install the microsandbox host shared-library prerequisites")
    if not image_ready(installer, root, args, manifest):
        archive = distribution.runtime_archive(manifest, root, getattr(args, "disposable", False))
        installer.checked([str(root / ARTIFACTS[1]), "image", "load", "--input", str(archive), "--tag", manifest["runtime_ref"], "--quiet"],
                          "Cannot import the microsandbox runtime image; check free disk space and host libraries", timeout=1800,
                          env=environment(installer, root, args))
        if not image_ready(installer, root, args, manifest):
            raise installer.InstallError("Imported microsandbox runtime image identity or platform differs")
    installer.drop_runtime_archives(root)


def verify_retained(installer, stored, runtime_image):
    pass


def image_available(installer, value):
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


def prepare_host(installer):
    pass


def child_environment():
    return {}


def release(installer, installation_id):
    pass


def check_removal(installer, installation_id):
    installer.no_links(home(installer, installation_id))


def after_remove(installer, installation_id, native, delete_store):
    """Delete the node's store once Core confirmed the removal; otherwise say how."""
    runtime_home = home(installer, installation_id)
    if runtime_home.exists():
        installer.no_links(runtime_home)
        if delete_store and installer.private_json(runtime_home / "oac-installation.json") == {"installation_id": installation_id}:
            shutil.rmtree(runtime_home)
            print("Deleted the microsandbox store " + str(runtime_home) + ".")
        else:
            sudo_mode = str(Path.home()) == str(installer.SERVICE_HOME)
            print("Kept the microsandbox store " + str(runtime_home) + " with its images and any sandbox state. Delete it with `"
                  + ("sudo -u " + installer.SERVICE_USER + " rm -rf " if sudo_mode else "rm -rf ") + str(runtime_home) + "`.")


def kept_stores(installer):
    return [str(installer.SERVICE_HOME / ".oac/m" / name) for name in installer.listdir_nofollow(installer.SERVICE_HOME, ".oac", "m")]
