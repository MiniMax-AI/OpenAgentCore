"""Private SDK connection receipts; Core alone owns execution and allocation truth."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import stat
import time


class Failure(Exception):
    def __init__(self, code):
        self.code = code


def fsync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


class Receipt:
    def __init__(self, request, remaining):
        self.identity = dict(request['Reference'], InstallationID=request['Config']['InstallationID'])
        self.root = Path(request['Config']['StateDir'])
        value = self.root.lstat()
        if not stat.S_ISDIR(value.st_mode) or value.st_mode & 0o077 or value.st_uid != os.getuid():
            raise Failure('invalid')
        digest = hashlib.sha256(json.dumps(self.identity, sort_keys=True).encode()).hexdigest()
        self.path = self.root / (digest + '.json')
        self.lock = os.open(self.root / (digest + '.lock'), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        self.remaining = remaining
        self.data = None

    def __enter__(self):
        try:
            while True:
                self.remaining()
                try:
                    fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    time.sleep(min(.05, self.remaining()))
            try:
                fd = os.open(self.path, os.O_RDONLY | os.O_NOFOLLOW)
            except FileNotFoundError:
                return self
            with os.fdopen(fd) as source:
                value = os.fstat(source.fileno())
                if not stat.S_ISREG(value.st_mode) or value.st_mode & 0o077 or value.st_uid != os.getuid() or value.st_size > 65536:
                    raise Failure('ownership')
                self.data = json.load(source)
            if self.data.get('identity') != self.identity or self.data.get('version') != 1:
                raise Failure('ownership')
            return self
        except BaseException:
            os.close(self.lock)
            raise

    def __exit__(self, *unused):
        os.close(self.lock)

    def save(self, **values):
        data = dict(self.data or {'version': 1, 'identity': self.identity,
                                 'ids': [], 'settled': False, 'bootstrap_complete': False})
        data.update(values)
        temporary = self.path.with_suffix('.tmp')
        # A crashed writer's private temporary file has no committed meaning.
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as target:
            json.dump(data, target)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, self.path)
        fsync_directory(self.root)
        self.data = data
