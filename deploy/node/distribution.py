"""Fetch verified, version-matched installation artifacts without build tools."""
import gzip
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit


class DistributionError(Exception):
    pass


class ArtifactError(DistributionError):
    """Transfer or immutable artifact provenance failed, not provider readiness."""


def image_identities(manifest, name):
    """Both immutable IDs describe the same archive, as proven by the builder."""
    identities = []
    for field in ('images', 'image_manifest_digests'):
        mapping = manifest.get(field)
        value = mapping.get(name) if isinstance(mapping, dict) else None
        if not isinstance(value, str) or not re.fullmatch(r'sha256:[0-9a-f]{64}', value):
            raise ArtifactError('Missing or invalid immutable image identity: ' + name)
        identities.append(value)
    return tuple(identities)


def docker_command(arguments, timeout=30):
    try:
        return subprocess.run(arguments, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, text=True, timeout=timeout, check=False)
    except (OSError, subprocess.SubprocessError):
        raise DistributionError('Cannot inspect or load the distribution image; check Docker access and disk space') from None


def ensure_docker_image(manifest, name, archive, docker=('docker',)):
    """Resolve a proven local ID; obtain a verified archive only on a cache miss."""
    expected = image_identities(manifest, name)
    docker = list(docker)

    def inspect():
        for identity in dict.fromkeys(expected):
            result = docker_command(docker + ['image', 'inspect', identity, '--format',
                                             '{{.Id}} {{.Os}}/{{.Architecture}}'])
            if result.returncode:
                continue
            fields = result.stdout.strip().split()
            if len(fields) != 2 or fields[0] not in expected or fields[1] != 'linux/amd64':
                raise DistributionError('Docker image identity or platform differs from the distribution: ' + name)
            return fields[0]
        return None

    identity = inspect()
    if identity is not None:
        return identity
    result = docker_command(docker + ['load', '--input', str(archive())], timeout=1800)
    if result.returncode:
        raise DistributionError('Cannot load the distribution image; check Docker access and free disk space: ' + name)
    identity = inspect()
    if identity is None:
        raise DistributionError('Cannot verify the loaded distribution image: ' + name)
    return identity


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(block)
    return value.hexdigest()


def artifact(manifest, name):
    entry = manifest.get('artifacts', {}).get(name)
    revision = manifest.get('source_commit', '')
    if not isinstance(entry, dict) or not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ArtifactError('Missing versioned artifact: ' + name)
    filename = entry.get('filename', '')
    if (not isinstance(filename, str) or not re.fullmatch(r'[A-Za-z0-9._-]+', filename)
            or revision not in filename or filename in ('.', '..')
            or not re.fullmatch(r'[0-9a-f]{64}', str(entry.get('sha256', '')))
            or type(entry.get('size')) is not int or entry['size'] <= 0):
        raise ArtifactError('Invalid artifact metadata: ' + name)
    return entry


def safe_url(value):
    try:
        parsed = urlsplit(value)
        parsed.port
        if (not parsed.hostname or parsed.username is not None or parsed.password is not None
                or parsed.fragment or any(c.isspace() for c in value)
                or '\\' in value or parsed.scheme not in ('http', 'https')):
            raise ValueError()
    except ValueError:
        raise ArtifactError('Artifact downloads need an http or https URL without credentials') from None
    return value


class ArtifactRedirect(urllib.request.HTTPRedirectHandler):
    """Only artifact bytes may follow HTTPS redirects; metadata stays on Core."""
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        safe_url(newurl)
        if urlsplit(newurl).scheme != 'https' or request.get_method() not in ('GET', 'HEAD'):
            raise ArtifactError('Artifact redirects require HTTPS')
        # Carry resume headers, never credentials or cookies, to a release/CDN host.
        forwarded = {name: value for name, value in request.header_items()
                     if name.lower() in ('range', 'if-range')}
        return urllib.request.Request(newurl, headers=forwarded,
                                      method=request.get_method(), unverifiable=True)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """Bootstrap metadata and credentials stay on the configured console origin."""
    def redirect_request(self, request, fp, code, message, headers, newurl):
        raise ArtifactError('Metadata downloads do not follow redirects; check the console URL '
                                'and the reverse proxy in front of it')


def checked_path(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise DistributionError('Artifact destination must be an absolute path without symlinks')
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.exists() and (not stat.S_ISREG(path.stat().st_mode) or path.stat().st_uid != os.getuid()):
        raise DistributionError('Artifact destination must be an owned regular file')
    return path


def matches(path, entry):
    return path.stat().st_size == entry['size'] and digest(path) == entry['sha256']


def obtain_artifact(manifest, logical_path, destination, offline_root=None):
    entry = artifact(manifest, logical_path)
    target = checked_path(destination)
    if target.exists():
        if not matches(target, entry):
            raise ArtifactError('Cached artifact differs; preserve the installation and inspect: ' + logical_path)
        return target
    source = None
    if offline_root is not None:
        candidate = Path(offline_root) / 'artifacts' / entry['filename']
        if candidate.exists():
            if candidate.is_symlink() or not candidate.is_file():
                raise DistributionError('Offline artifact must be a regular file: ' + logical_path)
            source = candidate
    if source is not None:
        return copy_artifact(source, target, entry, logical_path)
    base = manifest.get('artifact_base_url', '')
    if not isinstance(base, str) or not base or urlsplit(base).query:
        raise ArtifactError('No downloadable artifact source; use the matching offline bundle')
    url = safe_url(base.rstrip('/') + '/' + entry['filename'])
    # A private partial file survives interruptions and reruns; the next attempt asks
    # for the missing bytes only. The complete file is still verified as a whole.
    partial = target.with_name('.' + target.name + '.partial')
    for attempt in range(3):
        try:
            download_partial(url, partial, entry, logical_path)
            break
        except urllib.error.HTTPError as error:
            if error.code == 416:
                discard_partial(partial)
            elif error.code not in (408, 429, 500, 502, 503, 504) or attempt == 2:
                raise ArtifactError(f'Artifact download failed (HTTP {error.code}): {logical_path}. Check the console and retry.') from None
        except (urllib.error.URLError, socket.timeout, ConnectionError, http.client.HTTPException):
            if attempt == 2:
                raise ArtifactError('Artifact transfer interrupted: ' + logical_path + '. Check network access and rerun; '
                                        'the download resumes where it stopped.') from None
        time.sleep(attempt + 1)
    else:
        raise ArtifactError('Artifact download did not complete')
    if digest(partial) != entry['sha256']:
        discard_partial(partial)
        raise ArtifactError('Artifact checksum mismatch: ' + logical_path)
    os.chmod(partial, 0o700 if logical_path.startswith('native/') else 0o600)
    os.replace(partial, target)
    validator_path(partial).unlink(missing_ok=True)
    return target


def validator_path(partial):
    return partial.with_name(partial.name + '.validator')


def discard_partial(partial):
    partial.unlink(missing_ok=True)
    validator_path(partial).unlink(missing_ok=True)


def copy_artifact(source, target, entry, logical_path):
    fd, temporary = tempfile.mkstemp(prefix='.artifact-', dir=target.parent)
    try:
        with os.fdopen(fd, 'wb') as output, source.open('rb') as stream:
            count = 0
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                count += len(block)
                if count > entry['size']:
                    raise ArtifactError('Artifact exceeds published size: ' + logical_path)
                output.write(block)
        if count != entry['size'] or digest(temporary) != entry['sha256']:
            raise ArtifactError('Artifact checksum mismatch: ' + logical_path)
        os.chmod(temporary, 0o700 if logical_path.startswith('native/') else 0o600)
        os.replace(temporary, target)
        return target
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


# A download that brings fewer bytes than this in a window stops; a rerun resumes it.
SLOW_SECONDS, SLOW_BYTES = 60, 64 * 1024


def download_partial(url, partial, entry, logical_path):
    """Complete the partial file, asking only for the bytes it is missing."""
    size = entry['size']
    offset = 0
    if partial.is_symlink() or (partial.exists() and not partial.is_file()):
        raise DistributionError('Partial download must be a regular file: ' + logical_path)
    if partial.exists():
        info = partial.stat()
        if info.st_uid != os.getuid():
            raise DistributionError('Partial download must be owned by this user: ' + logical_path)
        offset = info.st_size if info.st_size <= size else 0
    if offset == size:
        return
    headers = {}
    if offset:
        headers['Range'] = f'bytes={offset}-'
        # If-Range makes a server whose file changed since the partial began send it whole.
        try:
            validator = validator_path(partial).read_text().strip()
        except OSError:
            validator = ''
        if validator:
            headers['If-Range'] = validator
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.build_opener(ArtifactRedirect()).open(request, timeout=30) as stream:
        if offset and (stream.status != 206 or not stream.headers.get('Content-Range', '').startswith(f'bytes {offset}-')):
            offset = 0  # The server sent the whole file; start over.
        if not offset:
            validator = stream.headers.get('ETag') or stream.headers.get('Last-Modified') or ''
            if validator and all(32 <= ord(c) < 127 for c in validator) and len(validator) < 200:
                with os.fdopen(os.open(validator_path(partial), os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600), 'w') as note:
                    note.write(validator)
            else:
                validator_path(partial).unlink(missing_ok=True)
        flags = os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW | (os.O_APPEND if offset else os.O_TRUNC)
        with os.fdopen(os.open(partial, flags, 0o600), 'ab' if offset else 'wb') as output:
            count, reported = offset, offset * 10 // size
            window, window_count = time.monotonic(), 0
            while True:
                block = stream.read1(1024 * 1024)
                if not block:
                    break
                count += len(block)
                if count > size:
                    output.close()
                    discard_partial(partial)
                    raise ArtifactError('Artifact exceeds published size: ' + logical_path)
                output.write(block)
                window_count += len(block)
                if time.monotonic() - window >= SLOW_SECONDS:
                    if window_count < SLOW_BYTES:
                        raise ArtifactError(f'Artifact download stalled (under {SLOW_BYTES // 1024} KiB in {SLOW_SECONDS} s): '
                                                f'{logical_path}. The downloaded part is kept; check the network, then rerun '
                                                'the command to resume.')
                    window, window_count = time.monotonic(), 0
                if size >= 50 * 1024 * 1024 and count * 10 // size > reported:
                    reported = count * 10 // size
                    try:
                        print(f'Downloading {logical_path}: {reported * 10}% of {size // (1024 * 1024)} MiB', flush=True)
                    except BrokenPipeError:
                        # Nobody reads the output any more (the installer's terminal is gone);
                        # stop instead of retrying it as a network error.
                        raise DistributionError('Output closed; stopped downloading ' + logical_path) from None
        if count != size:
            raise http.client.IncompleteRead(b'', size - count)


def runtime_archive(manifest, cache_root, offline_root=None):
    entry = artifact(manifest, 'images/runtime.tar.gz')
    expanded = {'sha256': entry.get('unpacked_sha256'), 'size': entry.get('unpacked_size')}
    if (not re.fullmatch(r'[0-9a-f]{64}', str(expanded['sha256']))
            or type(expanded['size']) is not int or expanded['size'] <= 0):
        raise ArtifactError('Runtime archive is missing unpacked verification metadata')
    root = Path(cache_root)
    target = checked_path(root / 'images/runtime.tar')
    if target.exists():
        if not matches(target, expanded):
            raise ArtifactError('Cached Runtime archive differs; preserve state and inspect it')
        return target
    archive = obtain_artifact(manifest, 'images/runtime.tar.gz', root / 'images/runtime.tar.gz', offline_root)
    fd, temporary = tempfile.mkstemp(prefix='.runtime-', dir=target.parent)
    try:
        with os.fdopen(fd, 'wb') as output, gzip.open(archive, 'rb') as stream:
            count = 0
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                count += len(block)
                if count > expanded['size']:
                    raise ArtifactError('Runtime archive exceeds published unpacked size')
                output.write(block)
        if not matches(Path(temporary), expanded):
            raise ArtifactError('Unpacked Runtime checksum mismatch')
        os.replace(temporary, target)
    except (gzip.BadGzipFile, EOFError):
        raise ArtifactError('Invalid compressed Runtime archive') from None
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return target


def load_manifest(source_url=None, offline_root=None):
    """Read the matched public manifest without transmitting installation credentials."""
    def read(name):
        if offline_root is not None:
            with (Path(offline_root) / name).open('rb') as stream:
                data = stream.read(1024 * 1024 + 1)
        else:
            if not source_url:
                raise DistributionError('A Core source URL or offline bundle is required')
            url = safe_url(source_url.rstrip('/') + '/node-install/' + name)
            for attempt in range(3):
                try:
                    with urllib.request.build_opener(NoRedirect()).open(url, timeout=30) as stream:
                        data = stream.read(1024 * 1024 + 1)
                    break
                except urllib.error.HTTPError as error:
                    if error.code not in (408, 429, 500, 502, 503, 504) or attempt == 2:
                        raise DistributionError(f'Distribution metadata unavailable (HTTP {error.code}); check the Core source URL') from None
                except (urllib.error.URLError, socket.timeout, ConnectionError, http.client.HTTPException):
                    if attempt == 2:
                        raise DistributionError('Cannot reach distribution metadata; check network and TLS trust, then retry') from None
                time.sleep(attempt + 1)
        if len(data) > 1024 * 1024:
            raise DistributionError('Distribution metadata exceeds size limit')
        return data
    sums = {}
    try:
        for line in read('SHA256SUMS').decode().splitlines():
            checksum, name = line.split('  ', 1)
            if name in sums or not re.fullmatch(r'[0-9a-f]{64}', checksum):
                raise ValueError()
            sums[name] = checksum
        raw = read('manifest.json')
        if hashlib.sha256(raw).hexdigest() != sums.get('manifest.json'):
            raise DistributionError('Distribution manifest checksum mismatch')
        manifest = json.loads(raw)
        if (manifest.get('platform') != 'linux/amd64'
                or not re.fullmatch(r'[0-9a-f]{40}', manifest.get('source_commit', ''))):
            raise DistributionError('Unsupported distribution platform or revision')
        # Resolve artifacts through the console, which selects local bytes or a pinned HTTPS release.
        manifest['artifact_base_url'] = source_url.rstrip('/') + '/node-install/artifacts' if source_url and offline_root is None else ''
        return manifest
    except (ValueError, TypeError, AttributeError):
        raise DistributionError('Invalid distribution metadata') from None
