"""Docker host preparation for a node; node_install.py selects it through its HOSTS table.

Every function takes the installer module first, as node_generations.py does, so this file needs no import of it."""
import json
import os
from pathlib import Path
import re
import shutil
import stat

import distribution

NAME = "Docker"
COMMAND = ("docker", "--host", "unix:///var/run/docker.sock")
SOCKET = Path("/var/run/docker.sock")
# The group that owns the provider's device; the node's service user joins only this one.
GROUP = "docker"
UNIT_AFTER = "network-online.target docker.service"
# Unpacked Runtime copies the Provider's store keeps on the disk beyond the one the import unpacks.
EXTRA_UNPACKED_COPIES = 0
# A root-owned, empty Docker configuration for the service user's docker calls, so no
# CLI plugin or credential helper the service user controls runs in the installer.
CHILD_CONFIG = Path("/run/oac-node-docker")


def network(installation_id):
    return "oac-node-" + installation_id


def networks(installer):
    return installer.checked(list(COMMAND) + ["network", "ls", "--format", "{{.Name}}"], "Cannot inspect Docker networks").splitlines()


def containers(installer, installation_id):
    return installer.checked(list(COMMAND) + ["ps", "--all", "--filter", "label=io.oac.installation=" + installation_id,
                                              "--format", "{{.Names}}"], "Cannot inspect Docker containers").split()


def preflight(installer):
    installer.checked(list(COMMAND) + ["info", "--format", "{{.ServerVersion}}"], "Docker access through /var/run/docker.sock is required")


def host_check(installer):
    """Check Docker without installing it; return the group that grants access and the engine's details."""
    if shutil.which("docker") is None:
        raise installer.InstallError("Docker Engine is not installed. Install it (https://docs.docker.com/engine/install/), "
                                     "then rerun this command." + installer.NOTHING_CHANGED)
    try:
        info = SOCKET.stat()
    except OSError:
        info = None
    if info is None or not stat.S_ISSOCK(info.st_mode):
        raise installer.InstallError("Docker is not running: run `sudo systemctl enable --now docker`, then rerun this command." + installer.NOTHING_CHANGED)
    if info.st_gid == 0 or stat.S_IMODE(info.st_mode) & 0o060 != 0o060:
        raise installer.InstallError("Nodes reach Docker at /var/run/docker.sock through its group, but the socket is not "
                                     "group-accessible (rootless Docker is not supported)." + installer.NOTHING_CHANGED)
    try:
        details = json.loads(installer.checked(list(COMMAND) + ["info", "--format", "{{json .}}"], "Docker is not running"))
    except (installer.InstallError, ValueError):
        raise installer.InstallError("Docker is not running: run `sudo systemctl enable --now docker`, then rerun this command." + installer.NOTHING_CHANGED) from None
    # docker info's JSON uses the Engine API names (CpuCfsQuota), not the Go field names.
    if details.get("MemoryLimit") is not True or details.get("CpuCfsQuota") is not True:
        raise installer.InstallError("Docker on this host does not enforce CPU and memory limits; use cgroup v2, then rerun this command." + installer.NOTHING_CHANGED)
    return installer.device_group(GROUP, SOCKET, info.st_gid), details


def capacity(installer, details):
    return details.get("NCPU", 0), details.get("MemTotal", 0)


def other_node(installer, args):
    # A network that no container uses is a leftover (Docker was down during an uninstall); the install reuses it.
    if network(args.installation_id) in networks(installer) and containers(installer, args.installation_id):
        raise installer.InstallError("Another node for this installation already uses this Docker engine (network " + network(args.installation_id)
                                     + "). Remove it on the Nodes page and uninstall it first." + installer.NOTHING_CHANGED)


def configure(installer, root, args):
    if getattr(args, "checkpoint_root", None) is not None:
        raise installer.InstallError("Checkpoint root applies only to microsandbox nodes")


def native_config(installer, root, args, manifest, runtime_image):
    return {"host": "unix:///var/run/docker.sock", "image": runtime_image, "network": network(args.installation_id),
            "seccomp_file": str(root / "runtime/seccomp.json"), "nested_sandbox": True}


def runtime_imported(installer, root, args, manifest):
    return distribution.docker_image(manifest, "runtime", COMMAND) is not None


def prepare_runtime(installer, root, args, manifest):
    image = distribution.ensure_docker_image(
        manifest, "runtime", lambda: distribution.runtime_archive(manifest, root, getattr(args, "disposable", False)), list(COMMAND))
    installer.drop_runtime_archives(root)
    name = network(args.installation_id)
    if name not in networks(installer):
        installer.checked(list(COMMAND) + ["network", "create", name], "Cannot create node Docker network")
    return image


def verify_retained(installer, stored, runtime_image):
    if stored.get("native", {}).get("image") != runtime_image:
        raise installer.node_spec.SpecificationError("Retained Docker image differs; preserve the node and inspect its configuration")


def image_available(installer, value):
    seccomp = Path(value["native"]["seccomp_file"])
    installer.existing_file(seccomp)
    json.loads(seccomp.read_text())
    raw = installer.checked(list(COMMAND) + ["image", "inspect", value["native"]["image"], "--format", "{{.Id}} {{.Os}}/{{.Architecture}}"], "Cannot inspect pinned image")
    return raw.strip() == value["native"]["image"] + " linux/amd64"


def prepare_host(installer):
    """Create the root-owned empty directory the service user's docker calls read."""
    path = CHILD_CONFIG
    if not path.exists() and not path.is_symlink():
        path.mkdir(mode=0o755)
        os.chmod(path, 0o755)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o022:
        raise installer.InstallError(str(path) + " must be a directory only root can write; inspect the host.")


def child_environment():
    return {"DOCKER_CONFIG": str(CHILD_CONFIG)}


def release(installer, installation_id):
    """Remove the node's Docker network; never containers, volumes or images."""
    if shutil.which("docker") is None:
        return
    name = network(installation_id)
    try:
        if name not in networks(installer):
            return
        remaining = containers(installer, installation_id)
    except installer.InstallError:
        print("Docker is unavailable, so network " + name + " was not removed.")
        return
    if remaining:
        print("Kept Docker network " + name + ": these containers still exist: " + ", ".join(remaining))
        return
    installer.checked(list(COMMAND) + ["network", "rm", name], "Cannot remove Docker network " + name)


def check_removal(installer, installation_id):
    pass


def after_remove(installer, installation_id, native, delete_store):
    """Name the Runtime image the uninstall keeps."""
    image = native.get("image")
    # The service user can write provider.json; print only what an image ID can be.
    if isinstance(image, str) and re.fullmatch(r"sha256:[0-9a-f]{64}", image):
        print("Kept the Runtime image " + image + "; remove it with `sudo docker image rm " + image + "` if no other node uses it.")


def kept_stores(installer):
    return []
