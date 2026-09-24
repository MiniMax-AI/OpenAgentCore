#!/usr/bin/env python3
"""One request over stdin; no service, model calls or provider credentials in logs."""
import importlib.metadata
import json
import logging
import os
import sys

from provider import Provider
from sdk import SDK_VERSION
from state import Failure

MAX_REQUEST = 72 * 1024 * 1024


def main():
    logging.disable(logging.CRITICAL)
    os.umask(0o077)
    # Private SDK routing selectors may otherwise override the official cloud.
    for key in list(os.environ):
        if key.startswith(('E2B_', 'PYTHON')):
            os.environ.pop(key)
    try:
        if importlib.metadata.version('e2b') != SDK_VERSION:
            raise Failure('invalid')
        if sys.argv[1:] == ['--check']:
            # Construct the SDK's native transport without sending any request.
            # This also verifies its dynamically imported dependencies survived
            # freezing; importing e2b alone does not exercise that boundary.
            from pyqwest import SyncHTTPTransport
            SyncHTTPTransport(tls_include_system_certs=True)
            print(json.dumps({'Version': 1, 'SDKVersion': SDK_VERSION}))
            return
        if len(sys.argv) != 1:
            raise Failure('invalid')
        data = sys.stdin.buffer.read(MAX_REQUEST + 1)
        if len(data) > MAX_REQUEST:
            raise Failure('invalid')
        result = Provider(json.loads(data)).execute()
    except Failure as error:
        result = {'Version': 1, 'ErrorCode': error.code}
    except BaseException:
        result = {'Version': 1, 'ErrorCode': 'unconfirmed'}
    sys.stdout.write(json.dumps(result) + '\n')


if __name__ == '__main__':
    main()
