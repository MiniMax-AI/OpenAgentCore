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
import test_node_generations


class GenerationReviewRegressions(unittest.TestCase):
    def fixture(self, kind):
        case = kind()
        case.setUp()
        self.addCleanup(case.doCleanups)
        return case

    def test_live_helper_replaced_inode_is_refused_after_restart(self):
        case = self.fixture(test_node_generations.GenerationTests)
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
        self.assertTrue((case.directory / '1.dropped').exists())

    def test_interrupted_lease_initialization_is_not_reclaimed(self):
        case = self.fixture(test_node_generations.GenerationTests)
        identity = dict(node_generations.marker_identity(case.args), generation=3)
        with mock.patch.object(node_generations, 'atomic_json', side_effect=OSError('power loss')):
            with self.assertRaises(OSError):
                with node_generations.collection_lease(case.root, 3, installer, identity, initialize=True):
                    self.fail('initialization not durable')
        with self.assertRaisesRegex(installer.InstallError, 'earlier run stopped while creating.*delete that file'):
            with node_generations.collection_lease(case.root, 3, installer, identity, initialize=True):
                self.fail('interrupted inode adopted')
        (case.directory / '1.lease-identity').unlink()
        with self.assertRaises(OSError):
            node_generations.collect(case.args, installer)
        self.assertFalse((case.directory / '1.dropped').exists())

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
        node_payload.publish(root / 'node-payload', bundle)
        return root, bundle, root / 'node-payload', entries

    def test_publish_is_flat_and_adds_and_repairs_verified_missing_artifacts(self):
        with tempfile.TemporaryDirectory() as temp:
            root, bundle, payload, entries = self.payload_fixture(Path(temp))
            self.assertEqual(sorted(path.name for path in payload.iterdir()), ['SHA256SUMS', 'manifest.json', 'node-install.pyz', 'runtime'])
            (bundle / 'artifacts').mkdir()
            for name, entry in entries.items():
                (bundle / 'artifacts' / entry['filename']).write_bytes((name + ' verified artifact').encode())
            original = (payload / 'manifest.json').read_bytes()
            node_payload.publish(payload, bundle)
            artifact = payload / 'artifacts' / entries['node']['filename']
            self.assertEqual(artifact.read_bytes(), b'node verified artifact')
            artifact.unlink()
            node_payload.publish(payload, bundle)
            self.assertEqual(artifact.read_bytes(), b'node verified artifact')
            self.assertEqual((payload / 'manifest.json').read_bytes(), original)
            self.assertEqual(artifact.stat().st_nlink, 1)

    def test_published_conflict_refuses_every_missing_repair(self):
        with tempfile.TemporaryDirectory() as temp:
            root, bundle, payload, entries = self.payload_fixture(Path(temp))
            (bundle / 'artifacts').mkdir(); (payload / 'artifacts').mkdir()
            node = entries['node']['filename']; helper = entries['helper']['filename']
            (bundle / 'artifacts' / node).write_bytes(b'node verified artifact')
            (bundle / 'artifacts' / helper).write_bytes(b'helper verified artifact')
            (payload / 'artifacts' / helper).write_bytes(b'changed')
            with self.assertRaises(node_payload.PayloadError):
                node_payload.publish(payload, bundle)
            self.assertEqual((payload / 'artifacts' / helper).read_bytes(), b'changed')

    def test_another_release_cannot_replace_published_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root, bundle, payload, _ = self.payload_fixture(Path(temp))
            before = (payload / 'manifest.json').read_bytes()
            (bundle / 'manifest.json').write_text(json.dumps({'source_commit': 'b' * 40, 'artifacts': {}}))
            with self.assertRaises(node_payload.PayloadError):
                node_payload.publish(payload, bundle)
            self.assertEqual((payload / 'manifest.json').read_bytes(), before)

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

    def test_lease_record_is_fsynced_before_any_caller_can_enter(self):
        case = self.fixture(test_node_generations.GenerationTests)
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
