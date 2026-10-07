"""Qualify the rendered Compose files without building images."""

import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("render_compose", ROOT / "scripts/render-compose.py")
render_compose = importlib.util.module_from_spec(spec)
spec.loader.exec_module(render_compose)


def rendered_compose(directory):
    text = render_compose.render({
        'REVISION': 'd' * 40,
        'INIT_IMAGE': 'ghcr.io/minimax-ai/openagentcore/ingress@sha256:' + 'e' * 64,
    })
    path = Path(directory) / 'compose.yaml'
    path.write_text(text)
    return path


class ComposeTests(unittest.TestCase):
    @classmethod
    def render(cls, public_url=None):
        env = dict(os.environ)
        env.pop('OAC_PUBLIC_URL', None)
        env.pop('OAC_HOST', None)
        env.pop('OAC_WEB_PORT', None)
        for name in ('OAC_IMAGE_CORE', 'OAC_IMAGE_WEB', 'OAC_IMAGE_INGRESS', 'OAC_IMAGE_AGENT_HOST'):
            env.pop(name, None)
        env['OAC_DATA_DIR'] = '/tmp/oac-compose-fixture'
        if public_url is not None:
            env['OAC_PUBLIC_URL'] = public_url
        return json.loads(subprocess.check_output(
            ['docker', 'compose', '--env-file', os.devnull, '-f', str(cls.compose_file),
             'config', '--format', 'json'], env=env))

    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.compose_file = rendered_compose(cls.temporary.name)
        cls.compose = cls.render()

    def test_compose_uses_private_services_and_ordered_initialization(self):
        services = self.compose['services']
        self.assertEqual(services['database']['depends_on']['init']['condition'], 'service_completed_successfully')
        self.assertIn('pg_isready -h 127.0.0.1', services['database']['healthcheck']['test'][1])
        self.assertEqual(services['core']['depends_on']['database']['condition'], 'service_healthy')
        self.assertEqual(sorted(services), ['agent-host', 'core', 'database', 'init', 'web'])
        for service in services.values():
            self.assertNotIn('build', service)
            if service is not services['web']:
                self.assertNotIn('ports', service)
            self.assertTrue(service['image'].endswith(':latest') or service['image'] == 'postgres:16-alpine' or service['image'].endswith('@sha256:' + 'e' * 64))
            for volume in service.get('volumes', []):
                self.assertNotIn('docker.sock', json.dumps(volume))
                self.assertEqual(volume['type'], 'volume')
                self.assertEqual(volume['source'], 'data')
            self.assertNotIn('platform', service)
        self.assertEqual({v['target'] for v in services['web']['volumes']}, {'/run/oac', '/node-payload'})
        agent_host = services['agent-host']
        self.assertEqual(agent_host['network_mode'], 'service:core')
        self.assertEqual((agent_host['cgroup'], sorted(agent_host['cap_add']), agent_host['security_opt']),
                         ('private', ['NET_ADMIN', 'SYS_ADMIN'], ['apparmor=unconfined']))
        self.assertEqual([device['source'] for device in agent_host['devices']], ['/dev/fuse'])
        self.assertEqual(agent_host['command'], ['agent-host', '--identity-file', '/run/agent-host/identity.json',
                                                 '--core-url', 'http://127.0.0.1:8091'])
        identity = {'target': '/run/agent-host', 'subpath': 'secrets/agent-host', 'read_only': True}
        for name in ('core', 'agent-host'):
            self.assertIn(identity, [{'target': v['target'], 'subpath': v['volume']['subpath'], 'read_only': v.get('read_only')}
                                     for v in services[name]['volumes']], name)
        self.assertEqual(services['core']['environment']['OAC_AGENT_HOST_IDENTITY_FILE'], '/run/agent-host/identity.json')
        self.assertIsNone(services['core']['command'])
        self.assertNotIn('OAC_WEB_INSTALLATION_SOCKET', services['web']['environment'])
        self.assertEqual(services['init']['command'], ['/usr/local/bin/oac', 'init'])
        self.assertEqual(services['web']['healthcheck']['test'], ['CMD', '/usr/local/bin/oac-web', 'healthcheck'])
        self.assertNotIn('python3', json.dumps(self.compose))
        self.assertEqual(services['init']['environment']['OAC_REVISION'], 'd' * 40)
        for name in ('OAC_EXECUTION_CONCURRENCY', 'OAC_DEFAULT_HARNESS', 'OAC_HARNESSES', 'OAC_WRITE_AUDIT_RETENTION', 'OAC_LOG_LEVEL'):
            self.assertEqual(services['core']['environment'][name], '', name)

    def test_public_url_can_be_configured_after_initial_startup(self):
        for value in (None, '', 'https://oac.example.test', 'http://localhost:9080'):
            with self.subTest(public_url=value):
                configured = self.render(value)
                expected = value or 'http://localhost:8080'
                for name, setting in (('core', 'OAC_PUBLIC_URL'), ('web', 'OAC_PUBLIC_URL')):
                    self.assertEqual(configured['services'][name]['environment'][setting], expected)
                self.assertEqual(
                    {service: [item.get('target') for item in spec.get('volumes', [])]
                     for service, spec in configured['services'].items()},
                    {service: [item.get('target') for item in spec.get('volumes', [])]
                     for service, spec in self.compose['services'].items()})

    def test_host_ports_publish_only_web(self):
        def ports(config):
            return {name: [(port.get('host_ip'), port['published']) for port in service.get('ports', [])]
                    for name, service in config['services'].items() if service.get('ports')}
        self.assertEqual(ports(self.compose), {'web': [('127.0.0.1', '8080')]})
        env = dict(os.environ, OAC_DATA_DIR='/tmp/oac-compose-fixture', OAC_HOST='0.0.0.0', OAC_WEB_PORT='9080')
        configured = json.loads(subprocess.check_output(
            ['docker', 'compose', '--env-file', os.devnull, '-f', str(self.compose_file),
             'config', '--format', 'json'], env=env))
        self.assertEqual(ports(configured), {'web': [('0.0.0.0', '9080')]})
        env['OAC_HOST'] = '::1'
        configured = json.loads(subprocess.check_output(
            ['docker', 'compose', '--env-file', os.devnull, '-f', str(self.compose_file),
             'config', '--format', 'json'], env=env))
        self.assertEqual(ports(configured), {'web': [('::1', '9080')]})


    def test_platform_network_injection_keeps_the_file_valid(self):
        # Dokploy attaches a project network to the services it routes to or the
        # operator selects. The agent host shares Core's network and joins none.
        transformed = copy.deepcopy(self.compose)
        transformed['networks']['platform'] = {}
        for service in transformed['services'].values():
            if 'network_mode' not in service:
                service.setdefault('networks', {})['platform'] = None
        subprocess.run(
            ['docker', 'compose', '-f', '-', 'config', '--quiet'],
            input=json.dumps(transformed), text=True, check=True)


if __name__ == '__main__':
    unittest.main()
