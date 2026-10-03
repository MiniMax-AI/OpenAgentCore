"""Immutable preparation, durable lease identity and published repair regressions."""
import fcntl
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import distribution
import node_generations
import node_install as installer
import node_payload
import test_node_install
import test_node_generations


class GenerationReviewRegressions(unittest.TestCase):
    def fixture(self, kind):
        case = kind()
        case.setUp()
        self.addCleanup(case.doCleanups)
        return case

    def test_containerd_resolution_recovers_interrupted_commit(self):
        case = self.fixture(test_node_install.NodeInstallTests)
        case.containerd = True
        case.payloads["runtime/seccomp.json"] = b"{}"
        case.refresh_manifest()
        case.install()
        case.manifest['source_commit'] = 'f' * 40
        case.refresh_manifest()
        configuration = json.loads(case.configuration_response(None).read())
        configuration['generation'] = 2
        case.args.generation = 2
        case.args.specification_digest = configuration['specification_digest']
        path = case.root / 'state/node/generations/2.json'
        original = node_generations.atomic_json
        def interrupted(path, value):
            original(path, value)
            if path.name == '2.json':
                raise OSError('interrupted after resolved configuration')
        with mock.patch.object(installer.node_spec, 'fetch', return_value=configuration):
            with mock.patch.object(node_generations, 'atomic_json', side_effect=interrupted):
                with self.assertRaises(OSError):
                    node_generations.prepare(case.args, installer)
            self.assertEqual(installer.private_json(path)['docker']['image'], case.manifest['image_manifest_digests']['runtime'])
            self.assertTrue(installer.private_json(path.with_suffix('.preparing'))['import_started'])
            node_generations.prepare(case.args, installer)
            saved = path.read_bytes()
            node_generations.prepare(case.args, installer)
            self.assertEqual(path.read_bytes(), saved)
            self.assertFalse(path.with_suffix('.preparing').exists())
            self.assertTrue(node_generations.image_available(installer.private_json(path), installer))

    def test_unresolved_import_remains_discoverable_and_collectible(self):
        case = self.fixture(test_node_install.NodeInstallTests)
        case.containerd = True
        case.payloads["runtime/seccomp.json"] = b"{}"
        case.refresh_manifest()
        case.install()
        case.manifest['source_commit'] = 'f' * 40
        case.refresh_manifest()
        configuration = json.loads(case.configuration_response(None).read())
        configuration['generation'] = 2
        case.args.generation = 2
        case.args.specification_digest = configuration['specification_digest']
        with mock.patch.object(installer.node_spec, 'fetch', return_value=configuration), mock.patch.object(installer, 'prepare_runtime', side_effect=installer.InstallError('daemon unavailable')):
            with self.assertRaises(installer.InstallError):
                node_generations.prepare(case.args, installer)
        self.assertIn(2, node_generations.retained_configs(case.root, installer))
        record = installer.private_json(case.root / 'state/node/generations/2.preparing')
        self.assertTrue(record['import_started'])
        self.assertEqual(record['configuration']['specification'], configuration['specification'])
        self.assertFalse((case.root / 'state/node/generations/2.json').exists())
        node_generations.collect(case.args, installer)
        node_generations.collect(case.args, installer)
        self.assertNotIn(2, node_generations.retained_configs(case.root, installer))
        self.assertTrue((case.root / 'provider.json').exists())

    def test_live_helper_replaced_inode_is_refused_after_restart(self):
        case = self.fixture(test_node_generations.CollectionTests)
        lease = case.directory / '1.lease'
        helper = subprocess.Popen([sys.executable, '-c', 'import os,fcntl,sys; f=os.open(sys.argv[1],os.O_RDWR);fcntl.flock(f,fcntl.LOCK_SH);print("locked",flush=True);sys.stdin.read()', str(lease)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            self.assertEqual(helper.stdout.readline().strip(), 'locked')
            old = lease.with_suffix('.old')
            lease.rename(old)
            lease.touch(mode=0o600)
            for initialize in (False, True):
                with self.assertRaises(installer.InstallError):
                    with node_generations.collection_lease(case.root, 1, installer, node_generations.marker_identity(case.args), initialize=initialize):
                        self.fail('replacement adopted')
            with self.assertRaises(installer.InstallError):
                node_generations.collect(case.args, installer)
            self.assertIsNone(helper.poll())
            self.assertTrue(case.release.exists())
            self.assertFalse((case.directory / '1.dropped').exists())
            with old.open('rb') as stream:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            lease.unlink()
            old.rename(lease)
        finally:
            helper.communicate(timeout=10)
            self.assertEqual(helper.returncode, 0)
        node_generations.collect(case.args, installer)
        self.assertFalse(case.release.exists())

    def test_interrupted_lease_initialization_is_not_reclaimed(self):
        case = self.fixture(test_node_generations.CollectionTests)
        identity = dict(node_generations.marker_identity(case.args), generation=3)
        with mock.patch.object(node_generations, 'atomic_json', side_effect=OSError('power loss')):
            with self.assertRaises(OSError):
                with node_generations.collection_lease(case.root, 3, installer, identity, initialize=True):
                    self.fail('initialization not durable')
        with self.assertRaises(FileExistsError):
            with node_generations.collection_lease(case.root, 3, installer, identity, initialize=True):
                self.fail('interrupted inode adopted')
        (case.directory / '1.lease-identity').unlink()
        with self.assertRaises(OSError):
            node_generations.collect(case.args, installer)
        self.assertTrue(case.release.exists())

    def payload_fixture(self, directory):
        root = directory / 'installation'; root.mkdir()
        bundle = directory / 'bundle'; bundle.mkdir()
        source = 'a' * 40
        entries = {}
        for name in ('node', 'helper'):
            raw = (name + ' verified artifact').encode()
            entries[name] = {'filename': name + '-' + source, 'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}
        files = {'node-install.pyz': b'node', 'SHA256SUMS': b'fixture', 'runtime/seccomp.json': b'{}', 'manifest.json': json.dumps({'source_commit': source, 'artifacts': entries}).encode()}
        for name, raw in files.items():
            path = bundle / name; path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(raw)
        node_payload.prepare_node_payload(root, {'mode': 'all'}, bundle)
        return root, bundle, root / 'node-payload/releases' / source, entries

    def test_thin_release_adds_and_repairs_verified_missing_artifact(self):
        with tempfile.TemporaryDirectory() as temp:
            root, bundle, release, entries = self.payload_fixture(Path(temp))
            (bundle / 'artifacts').mkdir()
            for name, entry in entries.items():
                (bundle / 'artifacts' / entry['filename']).write_bytes((name + ' verified artifact').encode())
            original = (release / 'manifest.json').read_bytes()
            node_payload.prepare_node_payload(root, {'mode': 'all'}, bundle)
            artifact = release / 'artifacts' / entries['node']['filename']
            self.assertEqual(artifact.read_bytes(), b'node verified artifact')
            artifact.unlink()
            node_payload.prepare_node_payload(root, {'mode': 'all'}, bundle)
            self.assertEqual(artifact.read_bytes(), b'node verified artifact')
            self.assertEqual((release / 'manifest.json').read_bytes(), original)
            self.assertEqual(artifact.stat().st_nlink, 1)

    def test_published_conflict_refuses_every_missing_repair(self):
        with tempfile.TemporaryDirectory() as temp:
            root, bundle, release, entries = self.payload_fixture(Path(temp))
            (bundle / 'artifacts').mkdir(); (release / 'artifacts').mkdir()
            node = entries['node']['filename']; helper = entries['helper']['filename']
            (bundle / 'artifacts' / node).write_bytes(b'node verified artifact')
            (release / 'artifacts' / helper).write_bytes(b'changed')
            with self.assertRaises(node_payload.PayloadError):
                node_payload.prepare_node_payload(root, {'mode': 'all'}, bundle)
            self.assertFalse((release / 'artifacts' / node).exists())
            self.assertEqual((release / 'artifacts' / helper).read_bytes(), b'changed')

    def test_transfer_classification_excludes_provider_and_path_errors(self):
        with self.assertRaises(distribution.ArtifactError):
            distribution.image_identities({}, 'runtime')
        with mock.patch.object(distribution.subprocess, 'run', side_effect=OSError('private daemon detail')):
            with self.assertRaises(distribution.DistributionError) as got:
                distribution.docker_command(['docker'])
            self.assertNotIsInstance(got.exception, distribution.ArtifactError)
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp); (root / 'link').symlink_to('/missing')
            with self.assertRaises(distribution.DistributionError) as got:
                distribution.checked_path(root / 'link')
            self.assertNotIsInstance(got.exception, distribution.ArtifactError)

    def test_private_helper_exit_classification_never_parses_text(self):
        for expression, code in (("installer.RuntimeDownloadError('secret')", 65), ("installer.distribution.ArtifactError('secret')", 65), ("installer.InstallError('runtime_download_failed secret')", 1)):
            script = "import sys,runpy,node_generations; sys.argv=['node_install.py','--installation-id','94be54a1-138c-4f30-bc87-b13686272dbe','--generation-action','prepare','--generation','2','--specification-digest','" + 'a' * 64 + "'];\ndef fail(args,installer): raise " + expression + "\nnode_generations.prepare=fail; runpy.run_module('node_install',run_name='__main__',alter_sys=True)"
            result = subprocess.run([sys.executable, '-c', script], capture_output=True, text=True, timeout=10, cwd=Path(__file__).parent)
            self.assertEqual(result.returncode, code, result.stderr)
            if code == 65:
                self.assertNotIn('secret', result.stderr)

    def test_lease_record_is_fsynced_before_any_caller_can_enter(self):
        case = self.fixture(test_node_generations.CollectionTests)
        identity = dict(node_generations.marker_identity(case.args), generation=4)
        operations = []
        original = node_generations.os.fsync
        def sync(fd):
            operations.append(fd)
            return original(fd)
        with mock.patch.object(node_generations.os, 'fsync', side_effect=sync):
            with node_generations.collection_lease(case.root, 4, installer, identity, initialize=True):
                self.assertGreaterEqual(len(operations), 3)  # lease, identity, directory
                record = installer.private_json(case.directory / '4.lease-identity')
                lease = (case.directory / '4.lease').stat()
                self.assertEqual((record['device'], record['inode']), (lease.st_dev, lease.st_ino))

    def test_unpublished_plan_refuses_path_or_installation_drift(self):
        case = self.fixture(test_node_install.NodeInstallTests)
        case.payloads["runtime/seccomp.json"] = b"{}"
        case.refresh_manifest(); case.install()
        case.manifest['source_commit'] = 'f' * 40; case.refresh_manifest()
        configuration = json.loads(case.configuration_response(None).read()); configuration['generation'] = 2
        case.args.generation = 2; case.args.specification_digest = configuration['specification_digest']
        with mock.patch.object(installer.node_spec, 'fetch', return_value=configuration), mock.patch.object(installer, 'prepare_runtime', side_effect=installer.InstallError('stopped')):
            with self.assertRaises(installer.InstallError): node_generations.prepare(case.args, installer)
        path=case.root/'state/node/generations/2.preparing'
        original=installer.private_json(path)
        for field,value in (("installation_id","foreign"),("core_url","http://foreign/api/v1")):
            changed=json.loads(json.dumps(original)); changed['configuration'][field]=value
            node_generations.atomic_json(path,changed)
            with self.assertRaises(installer.InstallError): node_generations.retained_configs(case.root,installer)
        changed=json.loads(json.dumps(original)); changed['configuration']['docker']['seccomp_file']=str(case.home/'foreign')
        node_generations.atomic_json(path,changed)
        with self.assertRaises(installer.InstallError): node_generations.collect(case.args,installer)
        self.assertFalse((case.root/'state/node/generations/2.dropped').exists())
        node_generations.atomic_json(path,original)
        node_generations.collect(case.args,installer)

    def test_pending_fifo_is_refused_without_opening_a_blocking_reader(self):
        case = self.fixture(test_node_generations.CollectionTests)
        import os
        os.mkfifo(case.directory/'2.preparing',0o600)
        with self.assertRaises(installer.InstallError): node_generations.retained_configs(case.root,installer)

    def test_final_identity_never_changes_on_resolver_drift(self):
        case = self.fixture(test_node_install.NodeInstallTests)
        case.payloads["runtime/seccomp.json"] = b"{}"; case.refresh_manifest(); case.install()
        case.manifest['source_commit']='f'*40; case.refresh_manifest()
        cfg=json.loads(case.configuration_response(None).read()); cfg['generation']=2
        case.args.generation=2;case.args.specification_digest=cfg['specification_digest']
        with mock.patch.object(installer.node_spec,'fetch',return_value=cfg):
            node_generations.prepare(case.args,installer)
            path=case.root/'state/node/generations/2.json';before=path.read_bytes()
            # The same spec allows both IDs, but a finalized local provider is immutable.
            case.containerd=True
            with self.assertRaises(installer.node_spec.SpecificationError): node_generations.prepare(case.args,installer)
            self.assertEqual(path.read_bytes(),before)

    def test_collecting_generation_is_retained_but_never_reused(self):
        case=self.fixture(test_node_install.NodeInstallTests)
        case.args.provider="microsandbox";case.install()
        cfg=json.loads(case.configuration_response(None).read());cfg['generation']=2
        case.args.generation=2;case.args.specification_digest=cfg['specification_digest']
        original=installer.private_json(case.root/'provider.json')
        marker=dict(installation_id=case.args.installation_id,generation=1,specification_digest=installer.node_spec.digest('microsandbox',original['specification']),native_complete=False)
        node_generations.atomic_json(case.root/'state/node/generations/1.collecting',marker)
        real=node_generations.image_available
        def available(value,installer):
            self.assertNotEqual(value['generation'],1,'collecting entry was considered for reuse')
            return real(value,installer)
        with mock.patch.object(installer.node_spec,'fetch',return_value=cfg),mock.patch.object(node_generations,'image_available',side_effect=available):
            node_generations.prepare(case.args,installer)
        final=installer.private_json(case.root/'state/node/generations/2.json')
        self.assertNotEqual(final['microsandbox']['helper_path'],original['microsandbox']['helper_path'])
        self.assertIn(1,node_generations.retained_configs(case.root,installer))

    def test_operator_update_refuses_before_download_or_update(self):
        result = subprocess.run(
            [sys.executable, 'node_install.py', '--installation-id', '94be54a1-138c-4f30-bc87-b13686272dbe',
             '--update', '--source-url', 'https://core.invalid', '--core-url', 'https://core.invalid'],
            capture_output=True, text=True, timeout=10, cwd=Path(__file__).parent,
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('updates are not supported', result.stderr)
        self.assertIn('reinstall', result.stderr)
