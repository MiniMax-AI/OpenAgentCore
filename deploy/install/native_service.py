"""Install the native Core user service independently of execution nodes."""

import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tempfile


REQUIRED = ("bin/oac-core", "bin/oac-core-migrate", "e2b/oac-e2b-provider")


def is_native(state):
    return state["mode"] != "web-only" and state["native_core"]


def unit_name(state):
    project = state.get("project", "")
    if not isinstance(project, str) or not re.fullmatch(r"parsar-[0-9a-f]{10}", project):
        raise RuntimeError("Native Core requires its installation's generated project name")
    return project + "-core.service"


def _path(value):
    path = Path(value)
    text = str(path)
    # EnvironmentFile accepts glob patterns; systemd executable paths reject
    # quote characters even when ExecStart quotes/escapes the entire word.
    if (not path.is_absolute() or path.resolve() != path or text != text.strip()
            or any(ord(char) < 32 or ord(char) == 127 for char in text)
            or any(char in text for char in "\\*?[]\"'")):
        raise RuntimeError("Native Core paths must be canonical absolute paths without control characters, quotes, backslashes or wildcards")
    return path


def _run(arguments, failure):
    try:
        return subprocess.run(arguments, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, text=True, timeout=30, check=False)
    except (OSError, subprocess.SubprocessError):
        raise RuntimeError(failure) from None


def _checked(arguments, failure):
    result = _run(arguments, failure)
    if result.returncode:
        raise RuntimeError(failure)
    return result.stdout.strip()


def _files(native, required=REQUIRED):
    if native.is_symlink() or not native.is_dir():
        raise RuntimeError("The distribution is missing its native Core payload")
    files = {}
    for path in native.rglob("*"):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise RuntimeError("The distribution requires regular native Core executables")
        name = str(path.relative_to(native))
        if name in required and path.is_file():
            files[name] = path
    if not set(required).issubset(files):
        raise RuntimeError("The distribution is missing a required native Core executable")
    return files


def preflight(bundle, root):
    """Check host prerequisites without launching a helper operation or VM."""
    _path(root)
    if platform.system() != "Linux":
        raise RuntimeError("Native Core installation requires Linux")
    _checked(["systemctl", "--user", "show", "--property=Version", "--value"],
             "The systemd user manager is unavailable; establish a user session before installation")
    linger = _checked(["loginctl", "show-user", str(os.getuid()), "--property=Linger", "--value"],
                      "Cannot check user lingering; ask the host administrator to configure it")
    if linger != "yes":
        raise RuntimeError("User lingering must be enabled by the host administrator before native Core installation")
    try:
        native = _path(bundle) / "native"
        files = _files(native)
        for name in REQUIRED:
            with files[name].open("rb") as stream:
                if stream.read(4) != b"\x7fELF":
                    raise RuntimeError("Native Core payload must contain Linux ELF binaries")
            result = _run(["ldd", str(files[name])], "Cannot check native Core shared libraries")
            diagnostic = result.stdout + result.stderr
            static = "statically linked" in diagnostic or "not a dynamic executable" in diagnostic
            if "not found" in diagnostic or (result.returncode and not static):
                raise RuntimeError("Native Core shared libraries cannot load on this host; install the required host libraries")
    except OSError:
        raise RuntimeError("Cannot inspect the native Core distribution") from None


def _digest(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.digest()


def prepare(root, state, bundle, replace=False):
    """Install the bundle's native Core binaries. Only a conversion replaces different ones."""
    if not is_native(state):
        return
    try:
        root, bundle = _path(root), _path(bundle)
        unit_name(state)
        source, target = bundle / "native", root / "native"
        incoming = _files(source)
        if target.exists() or target.is_symlink():
            # Only conversion reads the historical executable layout. The new
            # bundle and every normal launch require the renamed commands.
            required = REQUIRED
            if replace and not (target / "bin/oac-core").exists():
                required = ("bin/agents-api", "bin/agents-api-migrate", "e2b/agents-api-e2b-provider")
            installed = _files(target, required)
            if incoming.keys() == installed.keys() and all(_digest(path) == _digest(installed[name]) for name, path in incoming.items()):
                replace = False
            elif not replace:
                raise RuntimeError("Installed native Core files differ; preserve the installation and follow the upgrade guide")
        if not target.exists() or replace:
            root.mkdir(parents=True, mode=0o700, exist_ok=True)
            with tempfile.TemporaryDirectory(prefix=".native-", dir=root) as temporary:
                staged = Path(temporary) / "native"
                for name, path in incoming.items():
                    destination = staged / name
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(path, destination)
                if target.exists():
                    os.replace(target, Path(temporary) / "previous")
                os.replace(staged, target)
        for path in [target, target / "bin", target / "e2b", *(target / name for name in REQUIRED)]:
            os.chmod(path, 0o700)
    except (OSError, UnicodeError):
        raise RuntimeError("Cannot prepare private native Core files; existing state was not removed") from None


def unit_text(root, header, inputs=None):
    """The unit. PARSAR_INPUTS carries the inputs digest the running Core started with."""
    root = _path(root)
    # ':' disables command-line environment substitution. The executable is
    # still Core itself; no shell, wrapper or provider shutdown hook is used.
    executable = str(root / "native/bin/oac-core").replace("%", "%%").replace('"', '\\"')
    return ("# " + header + "\n"
            + "[Unit]\nDescription=Parsar Core\n\n[Service]\nType=exec\n"
            + 'ExecStart=:"' + executable + '"\n'
            + "WorkingDirectory=" + str(root).replace("%", "%%") + "\n"
            + "EnvironmentFile=" + str(root / "generated/core.env").replace("%", "%%") + "\n"
            + ("Environment=PARSAR_INPUTS=" + inputs + "\n" if inputs else "")
            + "Restart=on-failure\nKillMode=process\nUMask=0077\n\n[Install]\nWantedBy=default.target\n")


def daemon_reload():
    _checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")


def start(root, state):
    """Enable the generated unit by path and start it."""
    if not is_native(state):
        return
    unit = _path(root) / "generated" / unit_name(state)
    if unit.is_symlink() or not unit.is_file():
        raise RuntimeError("Native Core service must be generated before starting it; run parsar apply")
    daemon_reload()
    _checked(["systemctl", "--user", "enable", "--now", str(unit)], "Cannot enable or start this installation's native Core service")


def restart(state):
    if is_native(state):
        _checked(["systemctl", "--user", "restart", unit_name(state)], "Cannot restart this installation's native Core service")


def stop(root, state):
    if is_native(state):
        _path(root)
        _checked(["systemctl", "--user", "stop", unit_name(state)], "Cannot stop this installation's native Core service")


def disable(state):
    """Stop the unit and remove its enablement link, as a conversion does before moving it."""
    if is_native(state):
        _checked(["systemctl", "--user", "disable", "--now", unit_name(state)],
                 "Cannot disable this installation's native Core service")


def _process_environment(pid):
    try:
        raw = Path(f"/proc/{pid}/environ").read_bytes()
    except OSError:
        return {}
    return dict(item.split("=", 1) for item in raw.decode(errors="replace").split("\0") if "=" in item)


def running_inputs(state):
    """PARSAR_INPUTS of the running Core process, or None when it is not running."""
    if not is_native(state):
        return None
    result = _run(["systemctl", "--user", "show", "--property=MainPID", "--value", unit_name(state)],
                  "Cannot query this installation's native Core service")
    pid = result.stdout.strip()
    if result.returncode or not pid.isdigit() or pid == "0":
        return None
    return _process_environment(int(pid)).get("PARSAR_INPUTS")


def active(state):
    if not is_native(state):
        return False
    result = _run(["systemctl", "--user", "is-active", "--quiet", unit_name(state)],
                  "Cannot query this installation's native Core service")
    return result.returncode == 0
