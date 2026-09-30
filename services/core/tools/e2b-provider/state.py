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


def private_root(config):
    root = Path(config['StateDir'])
    value = root.lstat()
    if not stat.S_ISDIR(value.st_mode) or value.st_mode & 0o077 or value.st_uid != os.getuid():
        raise Failure('invalid')
    return root


def receipt_digest(identity):
    return hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()


def endpoint_identity(config):
    return {'api_url': config.get('APIURL') or 'https://api.e2b.app',
            'domain': config.get('Domain') or 'e2b.app'}


def load_receipt(path, identity, config):
    """Return one committed receipt, or None; os.replace publishes whole versions."""
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    except FileNotFoundError:
        return None
    with os.fdopen(fd) as source:
        value = os.fstat(source.fileno())
        if not stat.S_ISREG(value.st_mode) or value.st_mode & 0o077 or value.st_uid != os.getuid() or value.st_size > 65536:
            raise Failure('ownership')
        data = json.load(source)
    if data.get('identity') != identity or data.get('version') != 1:
        raise Failure('ownership')
    # Receipts from releases before custom endpoints belong to official E2B.
    if data.get('endpoint', endpoint_identity({})) != endpoint_identity(config):
        raise Failure('ownership')
    return data


def read_receipt(config, root, reference):
    """Read without the allocation lock. Observation never waits for or writes receipts."""
    identity = dict(reference, InstallationID=config['InstallationID'])
    return load_receipt(root / (receipt_digest(identity) + '.json'), identity, config)


class Receipt:
    def __init__(self, request, remaining):
        self.config = request['Config']
        self.identity = dict(request['Reference'], InstallationID=request['Config']['InstallationID'])
        self.root = private_root(request['Config'])
        digest = receipt_digest(self.identity)
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
            self.data = load_receipt(self.path, self.identity, self.config)
            return self
        except BaseException:
            os.close(self.lock)
            raise

    def __exit__(self, *unused):
        os.close(self.lock)

    def save(self, **values):
        data = dict(self.data or {'version': 1, 'identity': self.identity,
                                 'endpoint': endpoint_identity(self.config),
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
