#!/usr/bin/env python3
"""Start one user-owned V1 Runtime from the connected Core's distribution."""
import argparse
import fcntl
import http.client
import json
import os
from pathlib import Path
import platform
import re
import signal
import stat
import subprocess
import sys
import tempfile
import termios
import time
import urllib.error
import urllib.request
from urllib.parse import urlencode, urlsplit
import uuid

from distribution import DistributionError, load_manifest, obtain_artifact, runtime_archive, image_identities, ensure_docker_image


class InstallError(Exception):
    pass


def canonical_uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except ValueError:
        return False


def identity(environment, remote):
    try:
        address = urlsplit(remote)
        address.port
        if (not canonical_uuid(environment) or address.scheme != 'wss' or not address.hostname
                or address.username is not None or address.password is not None
                or address.path != '/api/v1/agent-daemon/ws' or any(c in remote for c in '?#\\')
                or any(c.isspace() for c in remote)):
            raise ValueError()
    except (ValueError, TypeError):
        raise InstallError('Use the returned Environment UUID and unchanged reachable wss remote_url') from None
    return {'environment_id': environment, 'remote_url': remote}


def private_read(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise InstallError('Credential and state files must have absolute paths without symlinks')
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                    or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > 16384):
                raise InstallError('Credential and state files must be private mode-0600 owned regular files')
            return stream.read(16385)
    except OSError:
        raise InstallError('Cannot read private credential or state file') from None


def credential(raw, environment):
    try:
        if len(raw) > 16384:
            raise ValueError()
        value = json.loads(raw)
        if (not isinstance(value, dict) or set(value) != {'key_id', 'environment_id', 'executor_token'}
                or not canonical_uuid(value['key_id']) or value['environment_id'] != environment
                or not isinstance(value['executor_token'], str) or not value['executor_token']
                or any(c.isspace() or c == '\x00' for c in value['executor_token'])):
            raise ValueError()
    except (ValueError, TypeError, KeyError):
        raise InstallError('Invalid restricted executor credential JSON') from None
    return value


def prompt_credential(environment):
    """Read the credential JSON from the terminal with echo off.

    Input is read until one complete JSON object parses, so both the compact
    form and Web's pretty-printed file work. The secret never enters argv, the
    environment, shell history or the screen; leftover typed-ahead input is
    discarded so no fragment reaches the shell afterwards.
    """
    try:
        tty = os.open('/dev/tty', os.O_RDWR | os.O_NOCTTY)
    except OSError:
        raise InstallError('Use --credential-file in noninteractive sessions; credentials are never echoed') from None
    try:
        previous = termios.tcgetattr(tty)
        hidden = termios.tcgetattr(tty)
        hidden[3] &= ~(termios.ECHO | termios.ECHONL)
        raw = b''
        try:
            # Echo is off before the prompt appears, so nothing pasted is shown.
            termios.tcsetattr(tty, termios.TCSAFLUSH, hidden)
            os.write(tty, b'Paste the restricted executor credential JSON (input hidden), then press Enter: ')
            while True:
                chunk = os.read(tty, 4096)
                if not chunk:
                    raise InstallError('No executor credential was entered')
                raw += chunk
                text = raw.decode('utf-8', 'replace').strip()
                if len(raw) > 16384 or text[:1] not in ('', '{'):
                    raise InstallError('Invalid restricted executor credential JSON')
                if not text:
                    continue
                try:
                    json.loads(text)
                    break
                except ValueError:
                    # The credential is one flat object: a closing brace ends it.
                    if text.endswith('}'):
                        raise InstallError('Invalid restricted executor credential JSON') from None
        finally:
            termios.tcsetattr(tty, termios.TCSAFLUSH, previous)
            os.write(tty, b'\n')
    except termios.error:
        raise InstallError('Cannot read the credential from this terminal; use --credential-file') from None
    finally:
        os.close(tty)
    return credential(raw, environment)


def write_private(path, value):
    data = json.dumps(value).encode()
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
    except OSError:
        raise InstallError('Cannot write private Runtime installation state; inspect retained files') from None


def checked(command, message, timeout=30):
    try:
        result = subprocess.run(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, check=False)
        if result.returncode:
            raise InstallError(message)
        return result.stdout.decode()
    except (OSError, subprocess.SubprocessError, UnicodeError):
        raise InstallError(message) from None


def preflight():
    if (sys.version_info < (3, 9) or platform.system() != 'Linux'
            or platform.machine() not in ('x86_64', 'amd64') or os.getuid() == 0):
        raise InstallError('Run Python 3.9+ as a non-root Linux amd64 user with Docker access')
    checked(['docker', '--host', 'unix:///var/run/docker.sock', 'info', '--format', '{{.ServerVersion}}'],
            'Docker access through /var/run/docker.sock is required')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        raise InstallError('Core connection redirects are not supported; check the returned remote_url')


def open_connection(request, timeout):
    return urllib.request.build_opener(NoRedirect()).open(request, timeout=timeout)


class _ConnectionDeadline(Exception):
    pass


def connection_request(remote, environment, key):
    # Only the validated daemon origin receives the restricted credential.
    identity(environment, remote)
    address = urlsplit(remote)
    endpoint = 'https://' + address.netloc + '/api/v1/agent-daemon/connection?'
    return urllib.request.Request(endpoint + urlencode({'environment_id': environment}),
                                  headers={'Authorization': 'Bearer ' + key['executor_token']})


def credential_verdict(remote, environment, key):
    """One connection read: 'accepted', 401 or 409 when Core rejects the key, or None when Core cannot answer."""
    try:
        with open_connection(connection_request(remote, environment, key), 10) as response:
            response.read(4097)
        return 'accepted'
    except urllib.error.HTTPError as error:
        return error.code if error.code in (401, 409) else None
    except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.HTTPException):
        return None


def wait_connected(remote, environment, key, container, timeout=60):
    request = connection_request(remote, environment, key)
    guidance = (' Inspect with: docker --host unix:///var/run/docker.sock logs --tail 100 ' + container
                + '. Check the Runtime network, TLS and executor credential, then rerun the same installation command.'
                + ' Keep the existing container, volumes and installation state; do not replace history.')
    started_at = time.monotonic()
    deadline = started_at + timeout
    detail = 'Core has not confirmed this Environment connection'

    def deadline_expired(_signal, _frame):
        raise _ConnectionDeadline()

    # Socket timeouts only limit inactivity. The Linux CLI needs a process timer
    # as well so a slow response cannot keep the overall deadline alive.
    previous_handler = signal.signal(signal.SIGALRM, deadline_expired)
    previous_timer = signal.setitimer(signal.ITIMER_REAL, max(0.001, timeout))
    try:
        while time.monotonic() < deadline:
            try:
                with open_connection(request, min(10, max(0.1, deadline - time.monotonic()))) as response:
                    raw = response.read(4097)
                if len(raw) > 4096:
                    raise ValueError()
                result = json.loads(raw)
                if (not isinstance(result, dict) or set(result) != {'environment_id', 'status'}
                        or result['environment_id'] != environment
                        or result['status'] not in ('connected', 'disconnected')):
                    raise ValueError()
                if time.monotonic() >= deadline:
                    raise _ConnectionDeadline()
                if result['status'] == 'connected':
                    print('Runtime connected to Environment ' + environment + ': ' + container)
                    return
                detail = 'Core reports this Environment disconnected'
            except urllib.error.HTTPError as error:
                if error.code == 404:
                    raise InstallError('Core connection check was not found (HTTP 404); route /api/v1 on the Core origin'
                                       ' directly to Core, not to Web.' + guidance) from None
                if error.code not in (408, 429, 500, 502, 503, 504):
                    raise InstallError('Core connection check rejected (HTTP ' + str(error.code)
                                       + '); verify the exact Environment and active executor key.' + guidance) from None
                detail = 'Core connection check unavailable (HTTP ' + str(error.code) + ')'
            except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
                detail = 'Cannot reach the Core connection endpoint; check DNS, TLS and network access'
            except (ValueError, TypeError, UnicodeError):
                raise InstallError('Core returned an invalid connection response.' + guidance) from None
            except InstallError as error:
                raise InstallError(str(error) + guidance) from None
            remaining = deadline - time.monotonic()
            if remaining > 0:
                time.sleep(min(2, remaining))
    except _ConnectionDeadline:
        pass
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous_handler)
        if previous_timer[0] > 0:
            remaining_timer = max(0.001, previous_timer[0] - (time.monotonic() - started_at))
            signal.setitimer(signal.ITIMER_REAL, remaining_timer, previous_timer[1])
    raise InstallError('Runtime connection timed out: ' + detail + '.' + guidance)


def inspect_prior_launch(root, state, replacing=False):
    docker = 'docker --host unix:///var/run/docker.sock'
    filters = (' --filter label=io.parsar.agents-api.installation=' + state['installation_id']
               + ' --filter label=io.parsar.agents-api.environment=' + state['environment_id'])
    guidance = (' Inspect retained resources with: ' + docker + ' ps -a' + filters
                + '; ' + docker + ' volume ls' + filters
                + '. Do not delete the receipt or create replacement history.')
    receipt = root / 'started.json'
    if not receipt.exists():
        raise InstallError('A Runtime launch was already attempted without confirmed startup.' + guidance)
    prior = json.loads(private_read(receipt))
    name = prior.get('container', '') if isinstance(prior, dict) else ''
    if not re.fullmatch(r'parsar-selfhost-[0-9a-f]{32}', name) or prior.get('status') != 'started':
        raise InstallError('Invalid retained Runtime startup receipt.' + guidance)
    try:
        raw = checked(['docker', '--host', 'unix:///var/run/docker.sock', 'container', 'inspect', name,
                       '--format', '{{json .Config.Labels}} {{.State.Status}} {{.Image}}'], 'Cannot inspect retained Runtime')
        labels, status, image = raw.strip().rsplit(' ', 2)
        labels = json.loads(labels)
    except (InstallError, ValueError):
        raise InstallError('The prior Runtime cannot be confirmed.' + guidance) from None
    expected = {'io.parsar.agents-api.installation': state['installation_id'],
                'io.parsar.agents-api.environment': state['environment_id'],
                'io.parsar.agents-api.user-owned': 'true'}
    if not isinstance(labels, dict) or any(labels.get(key) != value for key, value in expected.items()):
        raise InstallError('The retained container does not match this installation and Environment.' + guidance)
    if image not in (state['runtime_image'], state['runtime_manifest']):
        raise InstallError('The retained container image does not match this distribution.' + guidance)
    if replacing and status in ('running', 'exited', 'restarting', 'created'):
        return name
    if status == 'running':
        print('Runtime already running: ' + name)
        return name
    if status == 'exited':
        raise InstallError('The existing Runtime is stopped. Preserve its history and resume that same container with: '
                           + docker + ' start ' + name + '. Then rerun the same installation command to confirm connection.')
    raise InstallError('The retained Runtime requires inspection before continuing.' + guidance)


def replace_credential(args, manifest, root, state, stored, rejection, supplied):
    """Give the same container a rotated credential after Core rejected the stored one.

    rejection is Core's answer for the stored credential: 401 when it was revoked
    or rotated, so only that same key rotated can replace it; 409 when the
    Environment is bound to a different credential. The container, its volumes and
    native history stay. The stored copy changes last, so every interruption
    leaves it rejected and rerunning the same command replaces it again.
    """
    environment = args.environment_id
    name = inspect_prior_launch(root, state, replacing=True)
    if rejection == 401:
        advice = ('Rotate this same credential in Web (Session > Executor credentials > Rotate); '
                  'a newly issued credential cannot replace it.')
        print('Executor credential ' + stored['key_id'] + ' is no longer accepted by Core. ' + advice
              + ('' if supplied else ' Then paste the rotated credential.'))
    else:
        advice = ('Rotate the credential first used for this Environment in Web (Session > Executor credentials > Rotate) '
                  'instead of issuing a new one.')
        print('Environment ' + environment + ' is bound to a different executor credential than ' + stored['key_id'] + '. '
              + advice + ('' if supplied else ' Then paste the rotated credential.'))
    replacement = supplied or prompt_credential(environment)
    if rejection == 401 and replacement['key_id'] != stored['key_id']:
        raise InstallError('The pasted credential is ' + replacement['key_id'] + ', not ' + stored['key_id'] + '. '
                           + advice + ' Then rerun this command. Nothing was changed.')
    verdict = credential_verdict(args.remote, environment, replacement)
    if verdict == 409:
        raise InstallError('Environment ' + environment + ' is bound to a different executor credential. Rotate the credential '
                           'first used for this Environment instead of issuing a new one, then rerun this command. Nothing was changed.')
    if verdict != 'accepted':
        raise InstallError('Core did not accept the replacement credential ('
                           + ('HTTP 401' if verdict == 401 else 'Core unavailable')
                           + '). Nothing was changed; rerun this command with a currently valid credential.')
    launcher = obtain_artifact(manifest, 'native/bin/parsar-runtime', root / 'native/bin/parsar-runtime', args.offline_root)
    docker = ['docker', '--host', 'unix:///var/run/docker.sock']
    fd, staged = tempfile.mkstemp(prefix='.executor-key-', dir=root)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(json.dumps(replacement).encode())
            stream.flush()
            os.fsync(stream.fileno())
        checked(docker + ['stop', name], 'Cannot stop the Runtime to replace its credential; rerun this command', timeout=60)
        checked([str(launcher), 'replace-credential', '--container', name, '--credential-file', staged],
                'Runtime credential replacement failed; rerun this command', timeout=90)
        checked(docker + ['start', name], 'Cannot start the Runtime ' + name + '; rerun this command')
        os.replace(staged, root / 'executor-key.json')
    finally:
        if os.path.exists(staged):
            os.unlink(staged)
    print('Executor credential replaced; restarted the same Runtime: ' + name)
    wait_connected(args.remote, environment, replacement, name)


def install(args, root):
    target = identity(args.environment_id, args.remote)
    manifest = load_manifest(source_url=args.source_url, offline_root=args.offline_root)
    revision = manifest.get('source_commit', '')
    runtime_image, runtime_manifest = image_identities(manifest, 'runtime')
    if (manifest.get('platform') != 'linux/amd64' or not re.fullmatch(r'[0-9a-f]{40}', revision)
            or not re.fullmatch(r'sha256:[0-9a-f]{64}', runtime_image)):
        raise InstallError('The distribution does not contain a matched Linux amd64 Runtime')
    target.update(source_commit=revision, runtime_image=runtime_image, runtime_manifest=runtime_manifest)
    state_file = root / 'installation.json'
    if state_file.exists():
        state = json.loads(private_read(state_file))
        if (not isinstance(state, dict) or set(state) != set(target) | {'installation_id'}
                or any(state.get(key) != value for key, value in target.items())
                or not canonical_uuid(state['installation_id'])):
            raise InstallError('This installation belongs to another Environment or distribution')
    else:
        state = dict(target, installation_id=str(uuid.uuid4()))
        write_private(state_file, state)
    if (root / 'launch.json').exists() or (root / 'launch.json').is_symlink():
        key = credential(private_read(root / 'executor-key.json'), args.environment_id)
        supplied = credential(private_read(Path(args.credential_file)), args.environment_id) if args.credential_file else None
        rejection = credential_verdict(args.remote, args.environment_id, key)
        if rejection in (401, 409):
            replace_credential(args, manifest, root, state, key, rejection, supplied)
            return
        name = inspect_prior_launch(root, state)
        if supplied and supplied != key:
            raise InstallError('Stored executor credential differs; inspect the existing installation')
        wait_connected(args.remote, args.environment_id, key, name)
        return
    key_file = root / 'executor-key.json'
    if key_file.exists():
        key = credential(private_read(key_file), args.environment_id)
        if args.credential_file and credential(private_read(Path(args.credential_file)), args.environment_id) != key:
            raise InstallError('Stored executor credential differs; inspect the existing installation')
    else:
        if args.credential_file:
            key = credential(private_read(Path(args.credential_file)), args.environment_id)
        else:
            key = prompt_credential(args.environment_id)
        write_private(key_file, key)
    launcher = obtain_artifact(manifest, 'native/bin/parsar-runtime', root / 'native/bin/parsar-runtime', args.offline_root)
    seccomp = obtain_artifact(manifest, 'runtime/seccomp.json', root / 'runtime/seccomp.json', args.offline_root)
    runtime_image = ensure_docker_image(
        manifest, 'runtime', lambda: runtime_archive(manifest, root, args.offline_root),
        ('docker', '--host', 'unix:///var/run/docker.sock'))
    command = [str(launcher), '--installation-id', state['installation_id'],
               '--environment-id', args.environment_id, '--remote', args.remote,
               '--image', runtime_image, '--seccomp-file', str(seccomp), '--credential-file', str(key_file)]
    # Persist the attempt before Docker mutation. Uncertain outcomes require
    # inspection, never another bootstrap or replacement native history.
    write_private(root / 'launch.json', {'installation_id': state['installation_id'], 'environment_id': args.environment_id})
    result = json.loads(checked(command, 'Runtime launch failed or is uncertain. Inspect retained installation state and Docker containers', timeout=150))
    if (not isinstance(result, dict) or result.get('status') != 'started'
            or not re.fullmatch(r'parsar-selfhost-[0-9a-f]{32}', result.get('container', ''))):
        raise InstallError('Runtime launcher returned an invalid result; inspect the retained container')
    write_private(root / 'started.json', result)
    print('Runtime started: ' + result['container'])
    wait_connected(args.remote, args.environment_id, key, result['container'])
    print('Stop this user-owned Runtime with: docker --host unix:///var/run/docker.sock stop ' + result['container'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--source-url', help='Core console HTTPS origin')
    source.add_argument('--offline-root', type=Path, help='matching extracted offline distribution')
    parser.add_argument('--environment-id', required=True)
    parser.add_argument('--remote', required=True, help='unchanged Session environment remote_url')
    parser.add_argument('--credential-file', help='absolute private restricted executor credential JSON')
    parser.add_argument('--install-dir', type=Path)
    args = parser.parse_args()
    preflight()
    identity(args.environment_id, args.remote)
    root = args.install_dir or Path.home() / '.parsar/self-hosted' / args.environment_id
    if not root.is_absolute() or root.resolve() != root:
        raise InstallError('Installation directory must be absolute and have no symlinks')
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = root.stat()
    if not root.is_dir() or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise InstallError('Installation directory must be owned by this user and mode 0700')
    lock = os.open(root / '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1:
            raise InstallError('Installation lock must be private and owned')
        try:
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise InstallError('Another installation is already running') from None
        install(args, root)


if __name__ == '__main__':
    try:
        main()
    except (InstallError, DistributionError) as error:
        raise SystemExit(str(error)) from None
    except (OSError, ValueError, KeyError, TypeError):
        raise SystemExit('Runtime installation failed; inspect the retained installation state') from None
