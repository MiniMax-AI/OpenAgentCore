"""Shared Go/Python exchanges, including the copied hermetic helper source tree."""
import importlib.metadata
import io
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import helper_contract_generated as contract
import main
from main import valid_response
from provider import Provider
import sdk
from state import Failure


class SharedContractTest(unittest.TestCase):
    def test_exchanges(self):
        fixtures = json.loads(Path(__file__).with_name('testdata').joinpath('contract.json').read_text())
        for case in fixtures:
            with self.subTest(kind=case['kind'], name=case['name']):
                if case['kind'] == 'request':
                    try:
                        Provider(case['payload'])
                        valid = True
                    except (Failure, ValueError, TypeError, KeyError):
                        valid = False
                elif case['kind'] == 'response':
                    valid = valid_response(case['payload'])
                else:
                    continue
                self.assertEqual(valid, case['valid'])

    def test_locked_sdk(self):
        self.assertEqual(importlib.metadata.version('e2b'), contract.SDK_VERSION)
        self.assertIn('e2b==' + contract.SDK_VERSION + ' ', Path(__file__).with_name('requirements.lock').read_text())

    def test_error_vocabulary(self):
        for code in contract.ERROR_CODES:
            if code:
                self.assertEqual(Failure(code).code, code)
        with self.assertRaises(ValueError):
            Failure('unexpected')

    def test_request_and_response_byte_bounds(self):
        for oversized_request in (True, False):
            output = io.StringIO()
            source = io.BytesIO(b' ' * (contract.MAX_REQUEST + 1) if oversized_request else b'{}')
            result = {'Version': contract.PROTOCOL_VERSION, 'ErrorCode': '',
                      'Command': {'Stdout': 'x' * contract.MAX_RESPONSE}}
            provider = Mock(return_value=Mock(execute=Mock(return_value=result)))
            with self.subTest(request=oversized_request), patch.object(main.sys, 'argv', ['helper']), \
                    patch.object(main.sys, 'stdin', SimpleNamespace(buffer=source)), \
                    patch.object(main.sys, 'stdout', output), patch.object(main, 'Provider', provider), \
                    patch.object(main.os, 'umask'), patch.object(main.logging, 'disable'), \
                    patch.dict(main.os.environ):
                main.main()
            self.assertEqual(json.loads(output.getvalue()), {'Version': contract.PROTOCOL_VERSION,
                             'ErrorCode': 'invalid' if oversized_request else 'unconfirmed'})
            if oversized_request:
                provider.assert_not_called()

    def test_command_limits_count_bytes(self):
        client = Mock()
        with patch.object(sdk.base64, 'b64decode', return_value=b'x' * (contract.MAX_COMMAND_INPUT + 1)):
            with self.assertRaises(Failure) as raised:
                sdk.run(client, {'Args': ['true'], 'Stdin': 'encoded'}, lambda: 3)
        self.assertEqual(raised.exception.code, 'invalid')
        client.commands.run.assert_not_called()
        for extra in ('', 'é'):
            text = 'x' * contract.MAX_OUTPUT + extra
            def wait(**callbacks):
                callbacks['on_stdout'](text)
                return SimpleNamespace(stdout=text, stderr='', exit_code=0)
            client.commands.run.return_value.wait.side_effect = wait
            if extra:
                with self.assertRaises(Failure) as raised:
                    sdk.run(client, {'Args': ['true']}, lambda: 3)
                self.assertEqual(raised.exception.code, 'command_unconfirmed')
            else:
                self.assertEqual(sdk.run(client, {'Args': ['true']}, lambda: 3)['Stdout'], text)


if __name__ == '__main__':
    unittest.main()
