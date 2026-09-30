"""Controller refusal tests; these fixtures never count as live qualification."""

import hashlib
import importlib.util
import io
import json
import pathlib
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "promotion", pathlib.Path(__file__).with_name("promote-qualified-release.py"))
PathControl = pathlib.Path(__file__).with_name('qualification_control.py')
promotion = importlib.util.module_from_spec(spec)
spec.loader.exec_module(promotion)


class PromotionTests(unittest.TestCase):
    def setUp(self):
        promotion.select_source("a" * 40)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.package = self.root / "private-package"
        self.package.mkdir()
        (self.package / "fixture.py").write_text("# Control fixture, never live qualification\n")
        self.manifest = {"version": 1, "files": {"fixture.py": promotion.file_identity(self.package / "fixture.py")},
            "configuration": {}, "stages": [{"name": name, "python": sys.executable,
                "script": "fixture.py", "args": [], "timeout_seconds": 3} for name in promotion.REQUIRED_CHECKS]}
        (self.package / "manifest.json").write_text(promotion.canonical(self.manifest))
        self.manifest_hash = promotion.file_identity(self.package / "manifest.json")["sha256"]
        self.kwargs = dict(source=promotion.SOURCE, package=self.package, manifest_hash=self.manifest_hash)
        self.tree = "b" * 40
        self.metadata = {
            "source_commit": promotion.SOURCE, "source_tree": self.tree,
            "artifact_base_url": promotion.BASE, "platform": "linux/amd64",
            "images": {name: "sha256:" + "a" * 64 for name in ("core", "web", "runtime", "database", "ingress")},
            "image_manifest_digests": {name: "sha256:" + "b" * 64 for name in ("core", "web", "runtime", "database", "ingress")},
            "runtime_ref": "oac-runtime@sha256:" + "c" * 64, "artifacts": {},
        }
        for logical, suffix in promotion.distribution.ARTIFACTS.items():
            name = promotion.STEM + "-" + suffix
            (self.assets / name).write_bytes(logical.encode())
            self.metadata["artifacts"][logical] = dict(promotion.file_identity(self.assets / name), filename=name)
        self.archives()

    def archives(self):
        for offline in (False, True):
            data = {"manifest.json": json.dumps(self.metadata).encode(), "source.tar.gz": b"source fixture"}
            if hasattr(self, "native"):
                data["native-installers/catalog.json"] = json.dumps(self.native).encode()
                if offline:
                    for platform in self.native["artifacts"]:
                        data["native-installers/" + platform + ".tar.gz"] = b"native"
            data["SHA256SUMS"] = "".join(
                hashlib.sha256(value).hexdigest() + "  " + key + "\n" for key, value in data.items()
                if not (key.startswith("native-installers/") and key.endswith(".tar.gz"))).encode()
            if offline:
                data.update({"artifacts/" + entry["filename"]: (self.assets / entry["filename"]).read_bytes()
                             for entry in self.metadata["artifacts"].values()})
            name = promotion.STEM + ("-offline" if offline else "") + ".tar.gz"
            with tarfile.open(self.assets / name, "w:gz") as archive:
                for key, value in data.items():
                    entry = tarfile.TarInfo(promotion.STEM + "/" + key)
                    entry.size = len(value)
                    archive.addfile(entry, io.BytesIO(value))
            checksum = promotion.file_identity(self.assets / name)["sha256"]
            (self.assets / (name + ".sha256")).write_text(checksum + "  " + name + "\n")

    def test_independent_native_assets_remain_in_qualification_inventory(self):
        name = f"oac-native-{promotion.SOURCE}-linux-amd64.tar.gz"
        digest = hashlib.sha256(b"native").hexdigest()
        (self.assets / name).write_bytes(b"native")
        (self.assets / (name + ".sha256")).write_text(digest + "  " + name + "\n")
        self.native = {"version": promotion.SOURCE, "artifacts": {"linux-amd64": {
            "sha256": digest, "url": promotion.BASE + "/" + name}}}
        self.archives()
        _, inventory = promotion.inspect_candidate(self.assets)
        self.assertIn(name, inventory)
        (self.assets / name).unlink()
        with self.assertRaisesRegex(ValueError, "Native asset"):
            promotion.inspect_candidate(self.assets)

    def test_full_asset_inventory_and_archive_contents(self):
        metadata, inventory = promotion.inspect_candidate(self.assets)
        self.assertEqual(metadata, self.metadata)
        promotion.verify_files(self.assets, inventory)
        name = next(iter(self.metadata["artifacts"].values()))["filename"]
        (self.assets / name).write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "checksum/size"):
            promotion.inspect_candidate(self.assets)
        with self.assertRaisesRegex(ValueError, "bytes/set"):
            promotion.verify_files(self.assets, inventory)

    def test_source_and_url_mismatch(self):
        for key in ("source_commit", "artifact_base_url"):
            with self.subTest(key=key):
                old = self.metadata[key]
                self.metadata[key] = "wrong"
                self.archives()
                with self.assertRaisesRegex(ValueError, "source/platform/release URL"):
                    promotion.inspect_candidate(self.assets)
                self.metadata[key] = old

    def test_missing_or_extra_asset_rejected(self):
        extra = self.assets / "pass.json"
        extra.write_text('{"passed":true}')
        with self.assertRaisesRegex(ValueError, "asset set mismatch"):
            promotion.inspect_candidate(self.assets)
        extra.unlink()
        name = next(iter(self.metadata["artifacts"].values()))["filename"]
        (self.assets / name).unlink()
        with self.assertRaisesRegex(ValueError, "checksum/size"):
            promotion.inspect_candidate(self.assets)

    def test_missing_manifest_artifact_rejected(self):
        self.metadata["artifacts"].pop(next(iter(self.metadata["artifacts"])))
        self.archives()
        with self.assertRaisesRegex(ValueError, "Missing or unexpected"):
            promotion.inspect_candidate(self.assets)

    def test_unlanded_and_unrelated_main_changes_rejected(self):
        for comparison in ({"status": "diverged", "files": []},
                           {"status": "ahead", "files": [{"filename": "services/core/main.go"}]},
                           {"status": "ahead", "files": [{"filename": "Makefile", "previous_filename": "go.mod"}]}):
            with self.subTest(comparison=comparison), mock.patch.object(
                    promotion, "api", side_effect=[{"commit": {"tree": {"sha": self.tree}}}, comparison]):
                with self.assertRaises(ValueError):
                    promotion.verify_landed(self.tree, "d" * 40)

    def test_landed_source_keeps_original_identity(self):
        with mock.patch.object(promotion, "api", side_effect=[
            {"commit": {"tree": {"sha": self.tree}}},
            {"status": "ahead", "files": [{"filename": "scripts/promote-qualified-release.py"}]},
            {"commit": {"tree": {"sha": "e" * 40}}},
            {"sha": "f" * 40, "commit": {"tree": {"sha": "e" * 40}}},
            {"status": "ahead"},
        ]):
            promotion.verify_landed(self.tree, "d" * 40)

    def test_conflicting_tag_rejected(self):
        with mock.patch.object(promotion, "api", return_value=[{
            "ref": "refs/tags/" + promotion.TAG, "object": {"type": "commit", "sha": "f" * 40},
        }]):
            with self.assertRaisesRegex(ValueError, "Conflicting"):
                promotion.verify_tag()

    def test_main_tree_mismatch_blocks_old_candidate(self):
        with mock.patch.object(promotion, "api", side_effect=[
            {"commit": {"tree": {"sha": self.tree}}}, {"status": "ahead", "files": []},
            {"commit": {"tree": {"sha": "d" * 40}}},
            {"sha": "e" * 40, "commit": {"tree": {"sha": "f" * 40}}},
        ]):
            with self.assertRaisesRegex(ValueError, "Main tree differs"):
                promotion.verify_landed(self.tree, "a" * 40)

    def test_makefile_allowance_cannot_hide_build_changes(self):
        encode = lambda value: promotion.base64.b64encode(value.encode()).decode()
        anchor = "\tPYTHONDONTWRITEBYTECODE=1 python3 scripts/core-distribution-manifest.test.py\n"
        with mock.patch.object(promotion, "api", side_effect=[
            {"commit": {"tree": {"sha": self.tree}}},
            {"status": "ahead", "files": [{"filename": "Makefile"}]},
            {"content": encode(anchor)}, {"content": encode(anchor + "build:\n\techo changed\n")},
        ]):
            with self.assertRaisesRegex(ValueError, "exceeds promotion test"):
                promotion.verify_landed(self.tree, "a" * 40)

    def test_unreviewed_package_has_no_remote_side_effects(self):
        (self.package / "fixture.py").write_text("changed")
        with mock.patch.object(promotion, "gh") as gh, mock.patch.object(promotion, "api") as api:
            with self.assertRaisesRegex(ValueError, "bytes/set mismatch"):
                promotion.promote(self.assets, self.root / "state", "mx2", "/tmp/acceptance", "d" * 40, **self.kwargs)
            gh.assert_not_called()
            api.assert_not_called()
            self.assertFalse((self.root / "state").exists())

    def qualify(self, checks=None, failure=False, wrong_identity=False):
        adapter = self.root / "adapter.py"
        adapter.write_text("# Transport test fixture, never live acceptance\n")
        adapter.with_name('qualification_control.py').write_bytes(PathControl.read_bytes())
        request = {"source": promotion.SOURCE, "tree": self.tree, "run_id": "test-run",
                   "inventory_sha256": "c" * 64, "directory": "/tmp/test", "inventory": {}}

        def transport(argv, request_input=None, **kwargs):
            if "exec(compile" in argv[-1]:
                if failure:
                    raise subprocess.CalledProcessError(1, argv)
                result = {key: request[key] for key in (
                    "source", "tree", "run_id", "inventory_sha256", "adapter_sha256")}
                result["status"] = "passed"
                result["qualification_manifest_sha256"] = self.manifest_hash
                result["checks"] = checks if checks is not None else {name: "passed" for name in promotion.REQUIRED_CHECKS}
                if wrong_identity:
                    result["run_id"] = "previous-run"
                return json.dumps(result)
            return ""

        with mock.patch.object(promotion, "run", side_effect=transport), \
             mock.patch.object(promotion.qualification_adapter.control, "transport", side_effect=transport):
            return promotion.qualification(request, "mx2", "/tmp/test", adapter, self.assets, self.package, self.manifest_hash)

    def test_nonzero_ssh_missing_checks_and_replayed_result_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.qualify(failure=True)
        with self.assertRaisesRegex(ValueError, "missing, failed or skipped"):
            self.qualify(checks={"fresh-install": "passed"})
        with self.assertRaisesRegex(ValueError, "identity mismatch"):
            self.qualify(wrong_identity=True)

    def test_complete_transport_result(self):
        self.assertEqual(self.qualify()["checks"], {name: "passed" for name in promotion.REQUIRED_CHECKS})

    def test_publication_waits_for_live_result_and_merge(self):
        for failure in ("qualification", "wait_for_landed"):
            with self.subTest(failure=failure):
                state = self.root / failure
                draft = {"id": 1, "draft": True, "target_commitish": promotion.SOURCE}
                ready = json.dumps({"ready": True, "required_checks": list(promotion.REQUIRED_CHECKS)})
                with mock.patch.object(promotion, "run", return_value=ready), \
                     mock.patch.object(promotion, "verify_tooling"), \
                     mock.patch.object(promotion, "api", return_value={"commit": {"tree": {"sha": self.tree}}}), \
                     mock.patch.object(promotion, "verify_tag"), \
                     mock.patch.object(promotion, "release_state", return_value=draft), \
                     mock.patch.object(promotion, "download"), \
                     mock.patch.object(promotion, "verify_files"), \
                     mock.patch.object(promotion, "qualification", return_value={}) as qualify, \
                     mock.patch.object(promotion, "wait_for_landed") as landed, \
                     mock.patch.object(promotion, "gh") as gh:
                    (qualify if failure == "qualification" else landed).side_effect = ValueError("blocked")
                    with self.assertRaisesRegex(ValueError, "blocked"):
                        promotion.promote(self.assets, state, "mx2", "/tmp/acceptance", "d" * 40, **self.kwargs)
                    gh.assert_not_called()

    def test_success_publishes_only_after_final_download_verification(self):
        draft = {"id": 1, "draft": True, "target_commitish": promotion.SOURCE}
        final = dict(draft, draft=False, prerelease=False, html_url="https://example.invalid/release")
        ready = json.dumps({"ready": True, "required_checks": list(promotion.REQUIRED_CHECKS)})
        events = []

        def download(path, inventory):
            events.append(path.name)

        with mock.patch.object(promotion, "run", return_value=ready), \
                     mock.patch.object(promotion, "verify_tooling"), \
             mock.patch.object(promotion, "api", return_value={"commit": {"tree": {"sha": self.tree}}}), \
             mock.patch.object(promotion, "verify_tag"), \
             mock.patch.object(promotion, "release_state", side_effect=[draft, draft, draft, draft, final]), \
             mock.patch.object(promotion, "download", side_effect=download), \
             mock.patch.object(promotion, "verify_files"), \
             mock.patch.object(promotion, "qualification", side_effect=lambda *args: events.append("qualified") or {}), \
             mock.patch.object(promotion, "wait_for_landed", side_effect=lambda *args: events.append("landed")), \
             mock.patch.object(promotion, "verify_landed", side_effect=lambda *args: events.append("rechecked")), \
             mock.patch.object(promotion, "publish_release", side_effect=lambda release_id, body: events.append(("publish", release_id))), \
             mock.patch("builtins.print"):
            promotion.promote(self.assets, self.root / "success", "mx2", "/tmp/acceptance", "d" * 40, **self.kwargs)
        self.assertEqual(events, ["downloaded", "qualified", "landed", "before-publication",
                                  "rechecked", ("publish", 1), "published"])

    def test_final_identity_changes_never_publish(self):
        draft = {"id": 1, "draft": True, "target_commitish": promotion.SOURCE}
        ready = json.dumps({"ready": True, "required_checks": list(promotion.REQUIRED_CHECKS)})
        for change in ("main", "tag", "replacement", "published"):
            with self.subTest(change=change):
                final = dict(draft)
                if change == "replacement":
                    final["id"] = 2
                if change == "published":
                    final["draft"] = False
                with mock.patch.object(promotion, "run", return_value=ready), \
                     mock.patch.object(promotion, "verify_tooling"), \
                     mock.patch.object(promotion, "api", return_value={"commit": {"tree": {"sha": self.tree}}}), \
                     mock.patch.object(promotion, "verify_tag", side_effect=[None, None, ValueError("changed") if change == "tag" else None]), \
                     mock.patch.object(promotion, "release_state", side_effect=[draft, draft, draft, final]), \
                     mock.patch.object(promotion, "download") as download, \
                     mock.patch.object(promotion, "verify_files"), \
                     mock.patch.object(promotion, "qualification", return_value={}), \
                     mock.patch.object(promotion, "wait_for_landed"), \
                     mock.patch.object(promotion, "verify_landed", side_effect=ValueError("changed") if change == "main" else None), \
                     mock.patch.object(promotion, "publish_release") as publish:
                    with self.assertRaises(ValueError):
                        promotion.promote(self.assets, self.root / change, "mx2", "/tmp/acceptance", "d" * 40, **self.kwargs)
                    self.assertEqual(download.call_args_list[-1].args[0].name, "before-publication")
                    publish.assert_not_called()

    def test_publication_targets_verified_release_id(self):
        body = self.root / "publication.json"
        body.write_text('{"draft":false}')
        with mock.patch.object(promotion, "run", return_value='{"id":37,"draft":false,"prerelease":false}') as run:
            promotion.publish_release(37, body)
            self.assertEqual(run.call_args.args[0], ["gh", "api", "--method", "PATCH",
                "repos/" + promotion.REPO + "/releases/37", "--input", str(body)])
        with mock.patch.object(promotion, "run", return_value='{"id":38,"draft":false,"prerelease":false}'):
            with self.assertRaisesRegex(ValueError, "identity mismatch"):
                promotion.publish_release(37, body)

    def test_download_hashes_stream_without_storing_an_archive(self):
        payload = b"downloaded bytes"
        inventory = {"asset.tar.gz": {"sha256": hashlib.sha256(payload).hexdigest(), "size": len(payload)}}
        class Process:
            def __init__(self, *args, **kwargs):
                self.stdout = io.BytesIO(payload)
            def __enter__(self):
                return self
            def __exit__(self, *args):
                self.stdout.close()
            def wait(self):
                return 0
        with mock.patch.object(promotion, "release_state", return_value={"id": 1}), \
             mock.patch.object(promotion, "run", return_value='[[{"name":"asset.tar.gz"}]]'), \
             mock.patch.object(promotion.subprocess, "Popen", Process):
            promotion.download(self.root / "streamed", inventory)
            self.assertTrue((self.root / "streamed.json").is_file())
            self.assertFalse((self.root / "streamed").exists())
            inventory["asset.tar.gz"]["sha256"] = "0" * 64
            with self.assertRaisesRegex(ValueError, "bytes changed"):
                promotion.download(self.root / "bad-stream", inventory)
            self.assertFalse((self.root / "bad-stream.json").exists())

    def test_recorded_pass_cannot_resume_an_interrupted_run(self):
        state = self.root / "previous-run"
        state.mkdir()
        (state / "qualification.json").write_text('{"passed":true}')
        ready = json.dumps({"ready": True, "required_checks": list(promotion.REQUIRED_CHECKS)})
        with mock.patch.object(promotion, "run", return_value=ready), \
                     mock.patch.object(promotion, "verify_tooling"), mock.patch.object(promotion, "gh") as gh:
            with self.assertRaises(FileExistsError):
                promotion.promote(self.assets, state, "mx2", "/tmp/acceptance", "d" * 40, **self.kwargs)
            gh.assert_not_called()


class SupervisionTests(unittest.TestCase):
    """Actual short-lived child fixtures exercise supervision, never Core acceptance."""
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.package = self.root / 'tools'
        self.package.mkdir()
        self.assets = self.root / 'assets'
        self.assets.mkdir()
        (self.assets / 'fixture.tar.gz').write_bytes(b'not a release')
        self.script = self.package / 'fixture.py'
        self.script.write_text("""import json,sys
q=json.load(sys.stdin);name=sys.argv[1]
r={k:q[k] for k in ('source','tree','run_id','inventory_sha256','adapter_sha256')}
count=q['owned_resources'].get('child_count',0)
assert (q['previous_stage_result'] is None)==(count==0)
r.update(status='passed',checks={name:'passed'},owned_resources={'child_count':count+1})
print(json.dumps(r))
""")
        self.manifest = {'version': 1, 'configuration': {}, 'files': {}, 'stages': [
            {'name': name, 'python': sys.executable, 'script': 'fixture.py', 'args': [name],
             'timeout_seconds': 3} for name in promotion.REQUIRED_CHECKS]}
        inventory = {'fixture.tar.gz': promotion.file_identity(self.assets / 'fixture.tar.gz')}
        self.request = {'source': 'a'*40, 'tree': 'b'*40, 'run_id': 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
            'directory': str(self.assets), 'inventory': inventory,
            'inventory_sha256': hashlib.sha256(promotion.canonical(inventory).encode()).hexdigest(),
            'adapter_sha256': 'd'*64, 'required_checks': list(promotion.REQUIRED_CHECKS),
            'qualification_package': str(self.package)}
        self.freeze()

    def freeze(self):
        self.manifest['files'] = {'fixture.py': promotion.file_identity(self.script)}
        path = self.package / 'manifest.json'
        path.write_text(promotion.canonical(self.manifest))
        self.request['qualification_manifest_sha256'] = promotion.file_identity(path)['sha256']

    def test_required_actual_children_and_exclusive_supervision(self):
        result = promotion.qualification_adapter.qualify(self.request)
        self.assertEqual(result['owned_resources'], {'child_count': len(promotion.REQUIRED_CHECKS)})
        self.assertEqual(result['checks'], {name: 'passed' for name in promotion.REQUIRED_CHECKS})
        with self.assertRaises(FileExistsError):
            promotion.qualification_adapter.qualify(self.request)

    def test_package_mutation_and_unlisted_file_rejected(self):
        self.script.write_text('# changed')
        with self.assertRaisesRegex(ValueError, 'bytes/set mismatch'):
            promotion.qualification_adapter.qualify(self.request)
        self.freeze()
        (self.package / 'pass.json').write_text('{"passed":true}')
        with self.assertRaisesRegex(ValueError, 'bytes/set mismatch'):
            promotion.qualification_adapter.qualify(self.request)
        self.assertFalse((self.assets / 'qualification').exists())

    def test_old_pass_file_cannot_replace_failed_child(self):
        self.script.write_text("import sys;sys.exit(9)\n")
        self.freeze()
        (self.assets / 'old-pass.json').write_text('{"status":"passed"}')
        with self.assertRaisesRegex(ValueError, 'child failed'):
            promotion.qualification_adapter.qualify(self.request)
        self.assertFalse((self.assets / 'qualification/supervision.result.json').exists())

    def test_wrong_identity_and_extra_passed_check_rejected(self):
        self.script.write_text(self.script.read_text().replace("print(json.dumps(r))", "r['checks']['invented']='passed';print(json.dumps(r))"))
        self.freeze()
        with self.assertRaisesRegex(ValueError, 'identity or required check'):
            promotion.qualification_adapter.qualify(self.request)

    def test_child_timeout_never_completes_qualification(self):
        self.script.write_text('import time;time.sleep(20)\n')
        self.manifest['stages'][0]['timeout_seconds'] = 1
        self.freeze()
        with self.assertRaises(subprocess.TimeoutExpired):
            promotion.qualification_adapter.qualify(self.request)
        self.assertFalse((self.assets / 'qualification/supervision.result.json').exists())

    def test_structured_commands_reject_traversal_and_missing_stage(self):
        for change in ('path', 'stage'):
            if change == 'path':
                self.manifest['stages'][0]['script'] = '../fixture.py'
            else:
                self.manifest['stages'] = self.manifest['stages'][:-1]
            self.freeze()
            with self.subTest(change=change), self.assertRaises(ValueError):
                promotion.qualification_adapter.qualify(self.request)

    def test_merge_wait_stays_in_process_without_requalification(self):
        with mock.patch.object(promotion, 'verify_landed', side_effect=[False, False, True]) as landed, \
             mock.patch.object(promotion.time, 'monotonic', side_effect=[0, 1, 2]), \
             mock.patch.object(promotion.time, 'sleep') as sleep, \
             mock.patch.object(promotion, 'qualification') as qualify:
            promotion.wait_for_landed('b'*40, 'd'*40, 60)
            self.assertEqual(landed.call_count, 3)
            self.assertEqual(sleep.call_count, 2)
            qualify.assert_not_called()

    def test_only_ancestor_main_waits_for_exact_reviewed_tree(self):
        promotion.select_source('a'*40)
        prefix=[{'commit':{'tree':{'sha':'b'*40}}}, {'status':'ahead','files':[]},
                {'commit':{'tree':{'sha':'d'*40}}}, {'sha':'e'*40,'commit':{'tree':{'sha':'f'*40}}}]
        with mock.patch.object(promotion,'api',side_effect=prefix+[{'status':'ahead'}]):
            self.assertIs(promotion.verify_landed('b'*40,'c'*40,allow_pending=True),False)
        with mock.patch.object(promotion,'api',side_effect=prefix+[{'status':'diverged'}]):
            with self.assertRaisesRegex(ValueError,'Main tree differs'):
                promotion.verify_landed('b'*40,'c'*40,allow_pending=True)

    def test_merge_timeout_and_conflict_do_not_publish(self):
        with mock.patch.object(promotion, 'verify_landed', return_value=False), \
             mock.patch.object(promotion.time, 'monotonic', side_effect=[0, 61]), \
             mock.patch.object(promotion, 'gh') as gh:
            with self.assertRaises(TimeoutError):
                promotion.wait_for_landed('b'*40, 'd'*40, 60)
            gh.assert_not_called()
        with mock.patch.object(promotion, 'verify_landed', side_effect=ValueError('conflict')), \
             mock.patch.object(promotion.time, 'sleep') as sleep:
            with self.assertRaisesRegex(ValueError, 'conflict'):
                promotion.wait_for_landed('b'*40, 'd'*40, 60)
            sleep.assert_not_called()

    def test_candidate_source_is_explicit_and_strict(self):
        for source in ('latest', '', 'a'*39, 'A'*40):
            with self.subTest(source=source), self.assertRaises(ValueError):
                promotion.select_source(source)
        promotion.select_source('f'*40)
        self.assertEqual(promotion.TAG, 'build-'+'f'*40)
        self.assertIn('f'*40, promotion.BASE)


if __name__ == "__main__":
    unittest.main()
