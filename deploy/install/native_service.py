"""Install only the native Core user service; Core retains Runtime ownership."""

import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tempfile


REQUIRED = ("bin/agents-api", "bin/agents-api-microsandbox-provider",
            "microsandbox/msb", "microsandbox/libkrunfw.so.5.6.1",
            "e2b/agents-api-e2b-provider")


def is_native(state):
    return state["mode"] != "web-only" and state["provider"] == "microsandbox"


def _unit_name(state):
    project = state.get("project", "")
    if not isinstance(project, str) or not re.fullmatch(r"parsar-[0-9a-f]{10}", project):
        raise RuntimeError("Native Core requires its installation's generated project name")
    return project + "-core.service"


def _path(value):
    path = Path(value)
    text = str(path)
    # EnvironmentFile accepts glob patterns and WorkingDirectory is not a shell
    # word. Reject ambiguous paths instead of expanding another file or unit line.
    if (not path.is_absolute() or path.resolve() != path or text != text.strip()
            or any(ord(char) < 32 for char in text) or any(char in text for char in "\\*?[]")):
        raise RuntimeError("Native Core paths must be canonical absolute paths without control characters, backslashes or wildcards")
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


def _files(native):
    if native.is_symlink() or not native.is_dir():
        raise RuntimeError("The distribution is missing its native Core payload")
    files = {}
    for path in native.rglob("*"):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise RuntimeError("Native Core payload must contain only regular files and directories")
        if path.is_file():
            files[str(path.relative_to(native))] = path
    if not set(REQUIRED).issubset(files):
        raise RuntimeError("The distribution is missing a required native Core executable or firmware")
    return files


def preflight(bundle):
    """Check host prerequisites without launching a helper operation or VM."""
    if platform.system() != "Linux":
        raise RuntimeError("Native microsandbox installation requires Linux")
    if not os.access("/dev/kvm", os.R_OK | os.W_OK):
        raise RuntimeError("Native microsandbox requires read/write access to /dev/kvm; ask the host administrator to grant access")
    _checked(["systemctl", "--user", "show", "--property=Version", "--value"],
             "The systemd user manager is unavailable; establish a user session before installation")
    linger = _checked(["loginctl", "show-user", str(os.getuid()), "--property=Linger", "--value"],
                      "Cannot check user lingering; ask the host administrator to configure it")
    if linger != "yes":
        raise RuntimeError("User lingering must be enabled by the host administrator before native Core installation")
    try:
        native = _path(bundle) / "native"
        files = _files(native)
        for name in REQUIRED[1:]:
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


def _environment(values):
    lines = []
    for key, value in values.items():
        if (not isinstance(key, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key)
                or not isinstance(value, str) or any(char in value for char in "\x00\r\n")):
            raise RuntimeError("Native Core environment requires valid names and single-line string values")
        # EnvironmentFile double quotes preserve these shell metacharacters.
        escaped = re.sub(r'([\\"`$])', r'\\\1', value)
        lines.append(key + '="' + escaped + '"\n')
    return "".join(sorted(lines))


def _private_write(path, content):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise RuntimeError("Native Core configuration must be a regular private file")
    if path.exists() and path.read_text() == content:
        os.chmod(path, 0o600)
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".core-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(content)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def prepare(root, state, bundle, environment):
    if not is_native(state):
        return
    try:
        root, bundle = _path(root), _path(bundle)
        unit_name = _unit_name(state)
        environment_text = _environment(environment)
        source, target = bundle / "native", root / "native"
        incoming = _files(source)
        if target.exists() or target.is_symlink():
            installed = _files(target)
            if incoming.keys() != installed.keys() or any(_digest(path) != _digest(installed[name]) for name, path in incoming.items()):
                raise RuntimeError("Installed native Core files differ; preserve the installation and follow the upgrade guide")
        else:
            root.mkdir(parents=True, mode=0o700, exist_ok=True)
            with tempfile.TemporaryDirectory(prefix=".native-", dir=root) as temporary:
                staged = Path(temporary) / "native"
                shutil.copytree(source, staged)
                os.replace(staged, target)
        for path in [target, *target.rglob("*")]:
            executable = path.is_dir() or path.parent == target / "bin" or path == target / "microsandbox/msb" or path == target / "e2b/agents-api-e2b-provider"
            os.chmod(path, 0o700 if executable else 0o600)
        config = root / "config"
        if config.is_symlink():
            raise RuntimeError("Native Core configuration directory must not be a symlink")
        config.mkdir(mode=0o700, exist_ok=True)
        os.chmod(config, 0o700)
        # ':' disables command-line environment substitution. The executable is
        # still Core itself; no shell, wrapper or provider shutdown hook is used.
        executable = str(target / "bin/agents-api").replace("%", "%%").replace('"', '\\"')
        unit = ("[Unit]\nDescription=Parsar Core\n\n[Service]\nType=exec\n"
                + 'ExecStart=:"' + executable + '"\n'
                + "WorkingDirectory=" + str(root).replace("%", "%%") + "\n"
                + "EnvironmentFile=" + str(config / "core.env").replace("%", "%%") + "\n"
                + "Restart=on-failure\nKillMode=process\nUMask=0077\n\n[Install]\nWantedBy=default.target\n")
        _private_write(config / "core.env", environment_text)
        _private_write(config / unit_name, unit)
    except (OSError, UnicodeError):
        raise RuntimeError("Cannot prepare private native Core files; existing Runtime state was not removed") from None


def start(root, state):
    if not is_native(state):
        return
    unit = _path(root) / "config" / _unit_name(state)
    if unit.is_symlink() or not unit.is_file():
        raise RuntimeError("Native Core service must be prepared before starting it")
    _checked(["systemctl", "--user", "daemon-reload"], "Cannot reload the systemd user manager")
    _checked(["systemctl", "--user", "enable", "--now", str(unit)], "Cannot enable or start this installation's native Core service")


def stop(root, state):
    if is_native(state):
        _path(root)
        _checked(["systemctl", "--user", "stop", _unit_name(state)], "Cannot stop this installation's native Core service")


def active(state):
    if not is_native(state):
        return False
    result = _run(["systemctl", "--user", "is-active", "--quiet", _unit_name(state)],
                  "Cannot query this installation's native Core service")
    return result.returncode == 0
