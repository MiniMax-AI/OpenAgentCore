"""Unit tests for oac-workspace-quota with a fake XFS backend.

Directory traversal, ownership, the journal and every state transition are
real; only privileged XFS ioctls and xfs_quota are replaced. Live qualification
on a real XFS filesystem is separate.
"""

import importlib.machinery
import importlib.util
import io
import json
import os
import pathlib
import tempfile
import threading
import unittest

HERE = pathlib.Path(__file__).resolve().parent
FIXTURES = HERE.parent / "testdata" / "quota"
loader = importlib.machinery.SourceFileLoader("oac_workspace_quota", str(HERE / "oac-workspace-quota"))
spec = importlib.util.spec_from_loader(loader.name, loader)
helper = importlib.util.module_from_spec(spec)
loader.exec_module(helper)

ENSURE = json.loads((FIXTURES / "ensure-request.json").read_text())
FILESYSTEM = json.loads((FIXTURES / "ensure-response.json").read_text())["filesystem_uuid"]


class Crash(Exception):
    pass


class FakeXFS:
    """In-memory project attributes and dquots keyed by real inode numbers."""
    attributes_by_inode = {}
    generations = {}
    limits_by_project = {}
    usage_by_project = {}
    enforcement = True
    filesystem = FILESYSTEM

    def __init__(self, mount_point=None, lock_fd=None):
        pass

    @classmethod
    def reset(cls):
        cls.attributes_by_inode, cls.generations, cls.limits_by_project, cls.usage_by_project = {}, {}, {}, {}
        cls.enforcement, cls.filesystem = True, FILESYSTEM

    def filesystem_uuid(self, fd):
        return self.filesystem

    def attributes(self, fd):
        return self.attributes_by_inode.get(os.fstat(fd).st_ino, (0, False))

    def project(self, fd, project):
        self.attributes_by_inode[os.fstat(fd).st_ino] = (project, True)
        self.usage_by_project[project] = (4096, 1)

    def generation(self, fd):
        return self.generations.get(os.fstat(fd).st_ino, 1)

    def enforced(self):
        return self.enforcement

    def limits(self, project):
        return self.limits_by_project.get(project, (0, 0, 0, 0, 0, 0))

    def usage(self, project):
        return self.usage_by_project.get(project, (0, 0))

    def set_limits(self, project, bytes_hard, inodes_hard):
        self.limits_by_project[project] = (0, bytes_hard, 0, inodes_hard, 0, 0)


class HelperTest(unittest.TestCase):
    def setUp(self):
        FakeXFS.reset()
        helper.BACKEND = FakeXFS
        helper.step = lambda name: None
        self.directory = tempfile.TemporaryDirectory()
        base = pathlib.Path(self.directory.name)
        self.mount = base / "xfs"
        self.export = self.mount / "export"
        self.export.mkdir(parents=True, mode=0o700)
        os.chmod(self.export, 0o700)
        marker = self.export / ".oac-storage-root"
        marker.write_text(ENSURE["namespace_id"] + "\n")
        os.chmod(marker, 0o600)
        self.config = base / "config.json"
        self.config.write_text(json.dumps({"export_root": str(self.export), "mount_point": str(self.mount)}))
        os.chmod(self.config, 0o600)
        self.assertEqual(helper.init(str(self.config), 1000, 1004)["projid_start"], 1000)

    def tearDown(self):
        self.directory.cleanup()

    def envelope(self, request=ENSURE):
        objects = self.export / "objects"
        objects.mkdir(mode=0o700, exist_ok=True)
        obj = objects / request["object_id"]
        obj.mkdir(mode=0o700)
        for name in ("staging", "trash"):
            (obj / name).mkdir(mode=0o700)
        identity = obj / "identity"
        identity.write_text(json.dumps({k: request[k] for k in ("tenant_id", "environment_id", "object_id")}))
        os.chmod(identity, 0o600)
        return obj

    def serve(self, request):
        return helper.serve(str(self.config), io.BytesIO(json.dumps(request).encode()))

    def other(self, **changes):
        request = dict(ENSURE, object_id=str(__import__("uuid").uuid4()))
        request.update(changes)
        return request

    def test_fixture_round_trip_and_replay(self):
        self.envelope()
        expected = json.loads((FIXTURES / "ensure-response.json").read_text())
        for _ in range(3):
            self.assertEqual(self.serve(ENSURE), expected)
        result = helper.serve(str(self.config), io.BytesIO((FIXTURES / "ensure-request.json").read_bytes()))
        self.assertEqual(json.dumps(result, sort_keys=True) + "\n", (FIXTURES / "ensure-response.json").read_text())
        check = helper.serve(str(self.config), io.BytesIO((FIXTURES / "check-request.json").read_bytes()))
        self.assertEqual(json.dumps(check, sort_keys=True) + "\n", (FIXTURES / "check-response.json").read_text())

    def test_error_codes_match_shared_fixture(self):
        self.assertEqual(helper.ERROR_CODES, frozenset(json.loads((FIXTURES / "errors.json").read_text())))

    def test_concurrent_same_reference_converges(self):
        self.envelope()
        results = []
        threads = [threading.Thread(target=lambda: results.append(self.serve(ENSURE))) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual({r.get("project_id") for r in results}, {1000})

    def test_distinct_references_never_share_ids_and_range_is_bounded(self):
        projects = []
        for _ in range(5):
            request = self.other()
            self.envelope(request)
            projects.append(self.serve(request)["project_id"])
        self.assertEqual(projects, [1000, 1001, 1002, 1003, 1004])
        request = self.other()
        self.envelope(request)
        self.assertEqual(self.serve(request), {"error": "range_exhausted"})
        check = self.serve({"operation": "check", "namespace_id": ENSURE["namespace_id"]})
        self.assertEqual(check, {"error": "range_exhausted"})

    def test_crash_at_every_step_replays_same_project_and_limits(self):
        for point in ("allocated", "projected-inode", "projected", "limited", "ready"):
            FakeXFS.reset()
            request = self.other()
            self.envelope(request)

            def crash(name, point=point):
                if name == point:
                    raise Crash(name)
            helper.step = crash
            with self.assertRaises(Crash):
                self.serve(request)
            helper.step = lambda name: None
            first = self.serve(request)
            self.assertNotIn("error", first, point)
            self.assertEqual(self.serve(request), first, point)
            self.assertEqual(FakeXFS.limits_by_project[first["project_id"]], (0, 2048 << 20, 0, 524288, 0, 0))

    def test_binding_changes_are_refused(self):
        self.envelope()
        self.assertEqual(self.serve(ENSURE)["project_id"], 1000)
        for change in ({"capacity_mib": 4096, "inodes_hard": 4096 * 256}, {"inodes_hard": 1},
                       {"configuration_id": "77777777-7777-4777-8777-777777777777"}):
            self.assertEqual(self.serve(dict(ENSURE, **change)), {"error": "binding_mismatch"})
        self.assertEqual(self.serve(dict(ENSURE, tenant_id="77777777-7777-4777-8777-777777777777")), {"error": "identity_mismatch"})

    def test_replaced_staging_inode_is_refused(self):
        obj = self.envelope()
        self.serve(ENSURE)
        (obj / "staging").rmdir()
        (obj / "staging").mkdir(mode=0o700)
        # A reused inode number has a new generation.
        FakeXFS.generations[os.stat(obj / "staging").st_ino] = 2
        self.assertEqual(self.serve(ENSURE), {"error": "object_replaced"})

    def test_foreign_or_inherited_projects_are_refused(self):
        obj = self.envelope()
        FakeXFS.attributes_by_inode[os.stat(obj.parent).st_ino] = (9, True)
        self.assertEqual(self.serve(ENSURE), {"error": "foreign_project"})
        FakeXFS.reset()
        FakeXFS.attributes_by_inode[os.stat(obj / "staging").st_ino] = (9, True)
        self.assertEqual(self.serve(ENSURE), {"error": "foreign_project"})
        FakeXFS.reset()
        FakeXFS.usage_by_project[1000] = (4096, 1)
        self.assertEqual(self.serve(ENSURE), {"error": "foreign_project"})
        FakeXFS.reset()
        self.assertEqual(self.serve(ENSURE)["project_id"], 1000)
        FakeXFS.attributes_by_inode[os.stat(obj / "staging").st_ino] = (1000, False)
        self.assertEqual(self.serve(ENSURE), {"error": "foreign_project"})
        FakeXFS.attributes_by_inode[os.stat(obj / "staging").st_ino] = (0, False)
        self.assertEqual(self.serve(ENSURE), {"error": "foreign_project"})

    def test_ready_limits_are_verified_not_repaired(self):
        self.envelope()
        self.serve(ENSURE)
        FakeXFS.limits_by_project[1000] = (0, 0, 0, 0, 0, 0)
        self.assertEqual(self.serve(ENSURE), {"error": "quota_mismatch"})
        self.assertEqual(FakeXFS.limits_by_project[1000], (0, 0, 0, 0, 0, 0))
        FakeXFS.limits_by_project[1000] = (1 << 20, 2048 << 20, 0, 524288, 0, 0)
        self.assertEqual(self.serve(ENSURE), {"error": "quota_mismatch"})

    def test_different_existing_limits_before_ready_are_refused(self):
        self.envelope()
        FakeXFS.limits_by_project[1000] = (0, 1 << 30, 0, 524288, 0, 0)
        self.assertEqual(self.serve(ENSURE), {"error": "quota_mismatch"})

    def test_existing_user_data_is_never_projected(self):
        obj = self.envelope()
        (obj / "staging" / "attempt").mkdir()
        self.assertEqual(self.serve(ENSURE), {"error": "nonempty_object"})
        (obj / "staging" / "attempt").rmdir()
        (obj / "live").mkdir()
        self.assertEqual(self.serve(ENSURE), {"error": "nonempty_object"})
        (obj / "live").rmdir()
        (obj / "trash" / "retired").mkdir()
        self.assertEqual(self.serve(ENSURE), {"error": "nonempty_object"})

    def test_deleted_missing_and_unsafe_objects(self):
        self.assertEqual(self.serve(ENSURE), {"error": "object_missing"})
        obj = self.envelope()
        (obj / "deleted-marker").write_text("deleted\n")
        self.assertEqual(self.serve(ENSURE), {"error": "object_deleted"})
        (obj / "deleted-marker").unlink()
        (obj / "staging").rmdir()
        (obj / "staging").symlink_to(obj / "trash")
        self.assertEqual(self.serve(ENSURE), {"error": "unsafe_path"})
        (obj / "staging").unlink()
        (obj / "staging").mkdir(mode=0o755)
        os.chmod(obj / "staging", 0o755)
        self.assertEqual(self.serve(ENSURE), {"error": "unsafe_path"})

    def test_journal_identity_and_enforcement(self):
        self.envelope()
        self.assertEqual(self.serve(dict(ENSURE, namespace_id="77777777-7777-4777-8777-777777777777")), {"error": "journal_mismatch"})
        FakeXFS.filesystem = "88888888-8888-4888-8888-888888888888"
        self.assertEqual(self.serve(ENSURE), {"error": "journal_mismatch"})
        FakeXFS.filesystem = FILESYSTEM
        FakeXFS.enforcement = False
        self.assertEqual(self.serve(ENSURE), {"error": "enforcement_off"})
        FakeXFS.enforcement = True
        with self.assertRaises(helper.Refused):
            helper.init(str(self.config), 1000, 2000)
        marker = self.export / ".oac-storage-root"
        marker.write_text("99999999-9999-4999-8999-999999999999\n")
        self.assertEqual(self.serve(ENSURE), {"error": "journal_mismatch"})

    def test_invalid_requests(self):
        self.envelope()
        for request in (dict(ENSURE, object_id="{" + ENSURE["object_id"] + "}"), dict(ENSURE, namespace_id=ENSURE["namespace_id"].replace("2", "A")), dict(ENSURE, extra=1),
                        dict(ENSURE, capacity_mib=True), dict(ENSURE, capacity_mib=0), dict(ENSURE, operation="delete"),
                        {"operation": "check"}, dict(ENSURE, tenant_id="00000000-0000-0000-0000-000000000000")):
            self.assertEqual(self.serve(request), {"error": "invalid_request"}, request)
        self.assertEqual(helper.serve(str(self.config), io.BytesIO(b"{" * 5000)), {"error": "invalid_request"})
        self.assertEqual(helper.serve(str(self.config), io.BytesIO(b"[]")), {"error": "invalid_request"})

    def test_unsafe_configuration(self):
        os.chmod(self.config, 0o666)
        self.assertEqual(self.serve(ENSURE), {"error": "unsafe_path"})
        os.chmod(self.config, 0o600)
        self.config.write_text(json.dumps({"export_root": str(self.mount), "mount_point": str(self.mount)}))
        self.assertEqual(self.serve(ENSURE), {"error": "unsafe_path"})


if __name__ == "__main__":
    unittest.main()
