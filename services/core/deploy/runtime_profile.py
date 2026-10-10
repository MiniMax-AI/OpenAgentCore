"""Preserve the Runtime's exported PATH in the pinned Debian login profile."""
from pathlib import Path

DEBIAN_PATH = '''if [ "$(id -u)" -eq 0 ]; then
  PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
else
  PATH="/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games"
fi
export PATH
'''


def preserve_runtime_path(profile: str) -> str:
    if profile.count(DEBIAN_PATH) != 1:
        raise ValueError("Pinned Debian profile PATH block changed")
    # Bash creates an unexported PATH when its caller supplied none. Check the
    # exported environment so that case still receives Debian's UID defaults.
    replacement = ('# Preserve the Runtime tool environment across login shells.\n'
                   'if ! /usr/bin/printenv PATH >/dev/null; then\n'
                   + ''.join('  ' + line for line in DEBIAN_PATH.splitlines(keepends=True))
                   + 'fi\n')
    return profile.replace(DEBIAN_PATH, replacement)


if __name__ == '__main__':
    path = Path('/etc/profile')
    path.write_text(preserve_runtime_path(path.read_text()))
