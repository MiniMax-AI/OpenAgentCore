"""Retain installed wheel license files/metadata and bundled system-library notices."""
import importlib.metadata
import json
from pathlib import Path
import shutil
import sys
import sysconfig

output = Path(sys.argv[1])
packages = []
for distribution in importlib.metadata.distributions():
    name, version = distribution.metadata['Name'], distribution.version
    directory = output / (name + '-' + version)
    directory.mkdir(exist_ok=True)
    directory.joinpath('METADATA').write_text(distribution.read_text('METADATA') or '')
    for entry in distribution.files or []:
        if any(word in entry.name.lower() for word in ['license', 'copying', 'notice']) or '/licenses/' in str(entry):
            source = Path(distribution.locate_file(entry))
            if source.is_file():
                target = directory / str(entry).replace('/', '_').replace('..', '_')
                shutil.copyfile(source, target)
    packages.append({'name': name, 'version': version})
(output / 'packages.json').write_text(json.dumps(sorted(packages, key=lambda p: p['name']), indent=2) + '\n')
python_license = Path(sysconfig.get_path('stdlib')) / 'LICENSE.txt'
if not python_license.is_file():
    raise RuntimeError('CPython license is missing')
shutil.copyfile(python_license, output / 'CPython-LICENSE.txt')
system = output / 'system'
system.mkdir()
for copyright_file in Path('/usr/share/doc').glob('*/copyright'):
    shutil.copyfile(copyright_file, system / copyright_file.parent.name)
