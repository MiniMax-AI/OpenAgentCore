"""E2B build handoff: explicit connection selectors and non-secret provenance.

README.md owns the file contract. Web's reader is checked against the same
fixtures; connection selectors are checked against the Provider's fixtures.
"""
import ipaddress
import re
from uuid import UUID

OFFICIAL_API_URL = 'https://api.e2b.app'
OFFICIAL_DOMAIN = 'e2b.app'
MAX_MANIFEST_BYTES = 16384
FIELDS = {'api_url', 'domain', 'template', 'image', 'runtime_sha256', 'base'}


def valid_domain(value):
    if not isinstance(value, str) or len(value) > 253 or '.' not in value or value.endswith(('.local', '.localhost')):
        return False
    try:
        ipaddress.ip_address(value)
        return False
    except ValueError:
        pass
    return all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', label) for label in value.split('.'))


def normalize_endpoint(api_url, domain):
    if not api_url and not domain:
        return OFFICIAL_API_URL, OFFICIAL_DOMAIN
    if not isinstance(api_url, str) or len(api_url) > 512 or not valid_domain(domain):
        raise ValueError('Invalid E2B connection selectors')
    host = api_url.removeprefix('https://')
    if api_url != 'https://' + host or not valid_domain(host) or not (host == domain or host.endswith('.' + domain)):
        raise ValueError('Invalid E2B connection selectors')
    return api_url, domain


def valid_template(value):
    if not isinstance(value, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,128}:[0-9a-f-]{36}', value):
        return False
    build = value.split(':')[1]
    try:
        return str(UUID(build)) == build and UUID(build).int != 0
    except ValueError:
        return False


def validate_manifest(value):
    if not isinstance(value, dict) or set(value) != FIELDS or not all(isinstance(item, str) for item in value.values()):
        raise ValueError('Invalid template build file')
    if not value['api_url'] or not value['domain']:
        raise ValueError('Missing connection selectors')
    normalize_endpoint(value['api_url'], value['domain'])
    if (not valid_template(value['template']) or
            not re.fullmatch(r'sha256:[0-9a-f]{64}', value['image']) or
            not re.fullmatch(r'[0-9a-f]{64}', value['runtime_sha256']) or
            len(value['base']) > 512 or
            not re.fullmatch(r'[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64}', value['base'])):
        raise ValueError('Invalid template build provenance')
    return value
