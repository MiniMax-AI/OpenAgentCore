"""Build and verify the fixed Linux SDK helper inside its pinned build image."""
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile

SOURCE = Path('/source/services/agents-api/tools/e2b-provider')
OUTPUT = Path('/output')
NAME = 'agents-api-e2b-provider'
BASE = 'python:3.12.12-slim-bookworm@sha256:2986c55feb36e6cae00fa1fefb454283e4b33f35e75ff8bdd123b134130be301'


def checked(args, **options):
    subprocess.run(args, check=True, **options)


def main():
    if sys.version_info[:3] != (3, 12, 12) or platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise RuntimeError('Use the pinned Linux amd64 build image')
    os.umask(0o022)
    with tempfile.TemporaryDirectory(prefix='e2b-build-') as temporary:
        root = Path(temporary)
        venv = root / 'venv'
        checked([sys.executable, '-m', 'venv', str(venv)])
        python = str(venv / 'bin/python')
        checked([python, '-m', 'pip', 'install', '--disable-pip-version-check', '--require-hashes',
                 '--only-binary=:all:', '-r', str(SOURCE / 'requirements.lock')])
        checked([python, '-m', 'unittest', 'discover', '-s', str(SOURCE), '-p', '*_test.py', '-v'])
        checked([python, '-m', 'PyInstaller', '--noconfirm', '--clean', '--onedir', '--noupx',
                 '--name', NAME, '--distpath', str(root / 'dist'), '--workpath', str(root / 'work'),
                 '--specpath', str(root), '--recursive-copy-metadata', 'e2b', str(SOURCE / 'main.py')])
        exported = root / 'export' / NAME
        # PyInstaller uses library aliases. Native payloads deliberately contain
        # only regular files/directories so the installer can reject all links.
        shutil.copytree(root / 'dist' / NAME, exported, symlinks=False)
        licenses = exported / 'licenses'
        licenses.mkdir()
        shutil.copy2(SOURCE / 'requirements.lock', exported / 'requirements.lock')
        checked([python, str(SOURCE / 'licenses.py'), str(licenses)])
        shutil.copy2('/source/LICENSE', licenses / 'Parsar-Core-LICENSE')
        report = json.loads(subprocess.check_output([str(exported / NAME), '--check'], text=True))
        if report != {'Version': 1, 'SDKVersion': '2.51.0'}:
            raise RuntimeError('Unexpected helper readiness report')
        manifest = {'format_version': 1, 'source_revision': os.environ['E2B_SOURCE_REVISION'],
                    'sdk_version': report['SDKVersion'], 'python_version': platform.python_version(),
                    'platform': 'linux-amd64', 'libc': platform.libc_ver(), 'build_image': BASE,
                    'entrypoint': NAME,
                    'requirements_sha256': hashlib.sha256((exported / 'requirements.lock').read_bytes()).hexdigest()}
        (exported / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        for entry in exported.rglob('*'):
            if entry.is_symlink() or not (entry.is_file() or entry.is_dir()):
                raise RuntimeError('Unsupported artifact entry')
            entry.chmod(0o755 if entry.is_dir() or entry.stat().st_mode & 0o111 else 0o644)
        destination = OUTPUT / (NAME + '-linux-amd64.tar.gz')
        with tarfile.open(destination, 'w:gz', dereference=True) as archive:
            archive.add(exported, arcname=NAME)
        checksum = hashlib.sha256(destination.read_bytes()).hexdigest()
        destination.with_suffix(destination.suffix + '.sha256').write_text(checksum + '  ' + destination.name + '\n')
        print(json.dumps({'archive': str(destination), 'sha256': checksum, 'bytes': destination.stat().st_size}))


if __name__ == '__main__':
    main()
