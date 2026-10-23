"""Docker image identity: both immutable IDs prove a cached or loaded Runtime image."""
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import distribution
import host_docker


class DockerIdentityTests(unittest.TestCase):
    def setUp(self):
        self.config = 'sha256:' + 'a' * 64
        self.oci = 'sha256:' + 'b' * 64
        self.manifest = {'images': {'runtime': self.config},
                         'image_manifest_digests': {'runtime': self.oci}}
        self.archive = Mock(return_value=Path('/verified/runtime.tar'))

    def result(self, identity=None, platform='linux/amd64', code=0):
        return SimpleNamespace(returncode=code, stdout=(identity + ' ' + platform) if identity else '')

    def ensure(self):
        return host_docker.ensure_image(self.manifest, 'runtime', self.archive)

    def test_both_stores_use_proven_immutable_cache_without_archive(self):
        for responses, identity in (([self.result(self.config)], self.config),
                                    ([self.result(code=1), self.result(self.oci)], self.oci),
                                    ([self.result(self.oci)], self.oci)):
            with self.subTest(identity=identity), patch.object(host_docker, 'run', side_effect=responses) as command:
                self.assertEqual(self.ensure(), identity)
                self.assertTrue(all('load' not in call.args[0] for call in command.call_args_list))
        self.archive.assert_not_called()

    def test_load_is_verified_by_either_digest_on_both_stores(self):
        for identity in (self.config, self.oci):
            replies = [self.result(code=1), self.result(code=1), self.result()]
            if identity == self.oci:
                replies.append(self.result(code=1))
            replies.append(self.result(identity))
            with self.subTest(identity=identity), patch.object(host_docker, 'run', side_effect=replies) as command:
                self.assertEqual(self.ensure(), identity)
                self.assertEqual(command.call_args_list[2].args[0], list(host_docker.COMMAND) + ['load', '--input', '/verified/runtime.tar'])
        self.assertEqual(self.archive.call_count, 2)

    def test_wrong_id_platform_or_malformed_inspection_is_never_trusted(self):
        invalid = [self.result('sha256:' + 'c' * 64), self.result(self.oci, 'linux/arm64'),
                   self.result(self.config, 'windows/amd64'), self.result()]
        for reply in invalid:
            for loaded in (False, True):
                responses = ([self.result(code=1), self.result(code=1), self.result()] if loaded else []) + [reply]
                with self.subTest(reply=reply, loaded=loaded), patch.object(host_docker, 'run', side_effect=responses), \
                        self.assertRaisesRegex(distribution.DistributionError, 'identity or platform'):
                    self.ensure()

    def test_successful_load_without_inspectable_identity_fails(self):
        with patch.object(host_docker, 'run', side_effect=[self.result(code=1), self.result(code=1),
                          self.result(), self.result(code=1), self.result(code=1)]), \
                self.assertRaisesRegex(distribution.DistributionError, 'Cannot verify'):
            self.ensure()

    def test_both_metadata_identities_are_required_before_docker_or_archive(self):
        for field in ('images', 'image_manifest_digests'):
            original = self.manifest[field]
            for invalid in (None, {}, {'runtime': 'mutable:tag'}, {'runtime': 'sha256:' + 'g' * 64}):
                self.manifest[field] = invalid
                with self.subTest(field=field, invalid=invalid), patch.object(host_docker, 'run') as command, \
                        self.assertRaisesRegex(distribution.DistributionError, 'immutable image identity'):
                    self.ensure()
                command.assert_not_called()
                self.archive.assert_not_called()
            self.manifest[field] = original


if __name__ == "__main__":
    unittest.main()
