"""Fetch verified, version-matched installation artifacts without build tools."""
import gzip
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import stat
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit


class DistributionError(Exception):
    pass


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
        raise DistributionError('Missing versioned artifact: ' + name)
    filename = entry.get('filename', '')
    if (not isinstance(filename, str) or not re.fullmatch(r'[A-Za-z0-9._-]+', filename)
            or revision not in filename or filename in ('.', '..')
            or not re.fullmatch(r'[0-9a-f]{64}', str(entry.get('sha256', '')))
            or type(entry.get('size')) is not int or entry['size'] <= 0):
        raise DistributionError('Invalid artifact metadata: ' + name)
    return entry


def safe_url(value):
    try:
        parsed = urlsplit(value)
        parsed.port
        loopback = parsed.hostname == 'localhost'
        if parsed.hostname and not loopback:
            try:
                loopback = ipaddress.ip_address(parsed.hostname).is_loopback
            except ValueError:
                pass
        if (not parsed.hostname or parsed.username is not None or parsed.password is not None
                or parsed.fragment or any(c.isspace() for c in value)
                or '\\' in value or parsed.scheme != 'https' and not (parsed.scheme == 'http' and loopback)):
            raise ValueError()
    except ValueError:
        raise DistributionError('Artifact downloads require HTTPS; loopback HTTP is only for local testing') from None
    return value


class SecureRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        safe_url(newurl)
        if urlsplit(request.full_url).scheme == 'https' and urlsplit(newurl).scheme != 'https':
            raise DistributionError('Artifact redirect cannot downgrade HTTPS')
        return super().redirect_request(request, fp, code, message, headers, newurl)


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
            raise DistributionError('Cached artifact differs; preserve the installation and inspect: ' + logical_path)
        return target
    source = None
    if offline_root is not None:
        candidate = Path(offline_root) / 'artifacts' / entry['filename']
        if candidate.exists():
            if candidate.is_symlink() or not candidate.is_file():
                raise DistributionError('Offline artifact must be a regular file: ' + logical_path)
            source = candidate
    base = manifest.get('artifact_base_url', '')
    if source is None:
        if not isinstance(base, str) or not base or urlsplit(base).query:
            raise DistributionError('No downloadable artifact source; use the matching offline bundle')
        url = safe_url(base.rstrip('/') + '/' + entry['filename'])
    for attempt in range(3):
        fd, temporary = tempfile.mkstemp(prefix='.artifact-', dir=target.parent)
        try:
            with os.fdopen(fd, 'wb') as output:
                stream = source.open('rb') if source else urllib.request.build_opener(SecureRedirect()).open(url, timeout=30)
                with stream:
                    count = 0
                    while True:
                        block = stream.read(1024 * 1024)
                        if not block:
                            break
                        count += len(block)
                        if count > entry['size']:
                            raise DistributionError('Artifact exceeds published size: ' + logical_path)
                        output.write(block)
                    if count != entry['size']:
                        raise http.client.IncompleteRead(b'', entry['size'] - count)
            if digest(temporary) != entry['sha256']:
                raise DistributionError('Artifact checksum mismatch: ' + logical_path)
            os.chmod(temporary, 0o700 if logical_path.startswith('native/') else 0o600)
            os.replace(temporary, target)
            return target
        except urllib.error.HTTPError as error:
            if error.code not in (408, 429, 500, 502, 503, 504) or attempt == 2:
                raise DistributionError(f'Artifact download failed (HTTP {error.code}): {logical_path}. Check release access and retry.') from None
        except (urllib.error.URLError, socket.timeout, ConnectionError, http.client.HTTPException):
            if attempt == 2 or source:
                raise DistributionError('Artifact transfer interrupted: ' + logical_path + '. Check network access and rerun; installed state is retained.') from None
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
        time.sleep(attempt + 1)
    raise DistributionError('Artifact download did not complete')


def runtime_archive(manifest, cache_root, offline_root=None):
    entry = artifact(manifest, 'images/runtime.tar.gz')
    expanded = {'sha256': entry.get('unpacked_sha256'), 'size': entry.get('unpacked_size')}
    if (not re.fullmatch(r'[0-9a-f]{64}', str(expanded['sha256']))
            or type(expanded['size']) is not int or expanded['size'] <= 0):
        raise DistributionError('Runtime archive is missing unpacked verification metadata')
    root = Path(cache_root)
    target = checked_path(root / 'images/runtime.tar')
    if target.exists():
        if not matches(target, expanded):
            raise DistributionError('Cached Runtime archive differs; preserve state and inspect it')
        return target
    archive = obtain_artifact(manifest, 'images/runtime.tar.gz', root / 'images/runtime.tar.gz', offline_root)
    fd, temporary = tempfile.mkstemp(prefix='.runtime-', dir=target.parent)
    try:
        with os.fdopen(fd, 'wb') as output, gzip.open(archive, 'rb') as stream:
            count = 0
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                count += len(block)
                if count > expanded['size']:
                    raise DistributionError('Runtime archive exceeds published unpacked size')
                output.write(block)
        if not matches(Path(temporary), expanded):
            raise DistributionError('Unpacked Runtime checksum mismatch')
        os.replace(temporary, target)
    except (gzip.BadGzipFile, EOFError):
        raise DistributionError('Invalid compressed Runtime archive') from None
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
                    with urllib.request.build_opener(SecureRedirect()).open(url, timeout=30) as stream:
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
        if not manifest.get('artifact_base_url') and source_url:
            manifest['artifact_base_url'] = source_url.rstrip('/') + '/node-install/artifacts'
        return manifest
    except (ValueError, TypeError, AttributeError):
        raise DistributionError('Invalid distribution metadata') from None
