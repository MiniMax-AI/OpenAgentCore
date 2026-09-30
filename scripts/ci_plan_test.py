import importlib.util
import re
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_plan as ci


class SelectionTests(unittest.TestCase):
    def jobs(self, *paths):
        return set(ci.select(paths)["jobs"])

    def test_documents_only_need_repository_integrity(self):
        for path in ("docs/maintainers.md", "README.md", "contracts/agents-api/admin-api.md", "docs/assets/logo.svg"):
            self.assertEqual(self.jobs(path), {"hygiene"})

    def test_installer_does_not_download_a_browser_or_run_database_tests(self):
        self.assertEqual(self.jobs("deploy/install/install.py", "scripts/install-release.test.py"), {"hygiene", "distribution"})

    def test_web_and_core_have_different_consumers(self):
        self.assertEqual(self.jobs("apps/web/src/app.tsx"), {"hygiene", "web", "web-acceptance"})
        plan = ci.select(["services/core/internal/store/sessions.go"])
        self.assertEqual(set(plan["jobs"]), {"hygiene", "backend", "api"})
        self.assertFalse(plan["image"])

    def test_image_and_native_inputs_keep_their_acceptance(self):
        self.assertTrue(ci.select(["deploy/distribution/Dockerfile"])["image"])
        self.assertTrue(ci.select(["services/core/tools/e2b-provider/requirements.txt"])["image"])
        self.assertIn("native", self.jobs("services/core/internal/nativeinstaller/catalog.go"))
        self.assertIn("native", self.jobs("scripts/build-native-installer.mjs"))

    def test_every_tracked_path_produces_a_valid_plan(self):
        root = Path(__file__).resolve().parents[1]
        paths = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
        for path in filter(None, paths):
            with self.subTest(path=path):
                ci.validate_plan(ci.select([path]))

    def test_generated_outputs_keep_freshness_checks(self):
        spec = importlib.util.spec_from_file_location("catalog_generator", Path(__file__).with_name("generate-harness-catalog.py"))
        generator = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(generator)
        with patch.object(generator, "go", side_effect=lambda source: source):
            outputs = generator.render(generator.load_catalog(generator.ROOT / generator.CATALOG))
        for path in [*map(str, outputs), "deploy/install/harness_catalog.py", "docs/configuration.md",
                     "docs/getting-started/install-options.md"]:
            with self.subTest(path=path):
                self.assertIn("distribution", self.jobs(path))

    def test_distribution_image_build_keeps_api_acceptance(self):
        plan = ci.select(["scripts/build-core-distribution.sh"])
        self.assertTrue(plan["image"])
        self.assertEqual(set(plan["jobs"]), {"hygiene", "distribution", "api"})

    def test_shared_protocol_and_catalog_propagate_to_consumers(self):
        for path in ("contracts/agents-api/v1/session.go", "internal/harnessconfig/builtin/catalog.json"):
            self.assertTrue({"backend", "api", "native", "web", "web-acceptance", "example", "distribution"} <= self.jobs(path))
        self.assertTrue({"web", "web-acceptance", "example", "api"} <= self.jobs("packages/agents-client/src/client.ts"))

    def test_core_fixtures_retain_client_and_installer_consumers(self):
        self.assertTrue({"backend", "api", "web", "web-acceptance", "example"} <= self.jobs(
            "services/core/internal/sandbox/testdata/node-diagnostics.json"))
        for path in ("services/core/internal/sandbox/testdata/deployment-contract.json",
                     "services/core/internal/sandbox/e2b/testdata/configuration-selectors.json"):
            with self.subTest(path=path):
                self.assertTrue({"backend", "api", "distribution"} <= self.jobs(path))

    def test_unknown_inputs_fail_without_starting_a_full_run(self):
        for paths in (["new-component/source.rs"], [".github/workflows/unmapped.yml"], ["../outside"], ["/outside"]):
            with self.subTest(paths=paths), self.assertRaises(ValueError):
                ci.select(paths)
        self.assertEqual(self.jobs(), {"hygiene"})

    def test_shared_dependencies_select_their_consumers(self):
        self.assertEqual(self.jobs("go.sum"), {"hygiene", "backend", "api", "native", "distribution"})
        self.assertEqual(self.jobs("pnpm-lock.yaml"), {"hygiene", "web", "web-acceptance", "example", "harness", "native"})
        self.assertFalse(ci.select(["pnpm-lock.yaml"])["image"])

    def test_ci_changes_select_owned_checks(self):
        for path in ("scripts/ci_policy.py", "scripts/ci_plan.py", ".github/workflows/check.yml", ".github/workflows/release.yml"):
            self.assertEqual(self.jobs(path), {"hygiene", "lint"})
        self.assertEqual(self.jobs(".github/workflows/ci-backend.yml"), {"hygiene", "backend", "lint"})
        self.assertEqual(self.jobs(".github/workflows/native.yml"), {"hygiene", "native", "lint"})
        self.assertEqual(self.jobs(".github/workflows/ci-web-acceptance.yml"), {"hygiene", "web", "web-acceptance", "lint"})
        self.assertEqual(self.jobs(".github/actions/node/action.yml"),
                         {"hygiene", "lint", "web", "web-acceptance", "example", "harness", "native"})

    def test_mixed_changes_accumulate(self):
        self.assertEqual(self.jobs("docs/maintainers.md", "deploy/install/install.py", "apps/web/src/app.tsx"),
                         {"hygiene", "distribution", "web", "web-acceptance"})

    def test_installer_pr_300_replay(self):
        self.assertEqual(self.jobs(
            "deploy/install-release.sh", "deploy/install/README.md", "deploy/install/install.py",
            "deploy/install/install_display.py", "deploy/install/test_install.py", "deploy/install/test_install_output.py",
            "docs/getting-started/install.md", "scripts/install-release.test.py"), {"hygiene", "distribution"})

    def test_workflow_graph_cannot_silently_omit_or_add_a_gate_dependency(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/check.yml").read_text().split("jobs:\n", 1)[1]
        jobs = set(re.findall(r"^  ([a-z-]+):$", workflow, re.M))
        self.assertEqual(jobs, set(ci.JOBS) | {"plan", "check"})
        gate = workflow.split("  check:\n", 1)[1]
        dependencies = re.search(r"needs: \[(.+)\]", gate).group(1).split(", ")
        self.assertEqual(set(dependencies), set(ci.JOBS) | {"plan"})

    def test_unknown_markdown_is_not_assumed_to_be_documentation(self):
        with self.assertRaises(ValueError):
            self.jobs("new-engine/system-prompt.md")

    def test_full_is_explicit_and_bound_to_an_immutable_checkout(self):
        sha = "a" * 40
        with patch.object(ci, "git", return_value=(sha + "\n").encode()):
            for event in ("push", "workflow_dispatch", "workflow_call"):
                with self.assertRaises(ValueError):
                    ci.event_plan(event, {})
                self.assertEqual(ci.event_plan(event, {}, sha, "full")["jobs"], list(ci.JOBS))
            for ref in ("", "main", "b" * 40):
                with self.assertRaises(ValueError):
                    ci.event_plan("push", {}, ref, "full")
            with self.assertRaises(ValueError):
                ci.event_plan("pull_request", {}, sha)

    def test_unavailable_or_wrong_merge_diff_stops_planning(self):
        with self.assertRaises(ValueError):
            ci.event_plan("pull_request", {})
        event = {"pull_request": {"base": {"sha": "a"}, "head": {"sha": "b"}}}
        with patch.object(ci, "git", return_value=b"parent wrong\n\nmessage"), self.assertRaises(ValueError):
            ci.event_plan("pull_request", event)
        with patch.object(ci, "git", side_effect=subprocess.CalledProcessError(1, "git")), self.assertRaises(ValueError):
            ci.event_plan("pull_request", event)


class GitDiffTests(unittest.TestCase):
    def test_real_merge_in_shallow_clone_handles_deleted_renamed_and_odd_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "source"
            repo.mkdir()
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL).decode().strip()
            git("init", "-b", "main")
            git("config", "user.email", "ci-test@example.invalid")
            git("config", "user.name", "CI test")
            (repo / "services/core").mkdir(parents=True)
            (repo / "services/core/deleted.go").write_text("package example\n")
            (repo / "docs").mkdir()
            (repo / "docs/old.md").write_text("rename me\n")
            git("add", "."); git("commit", "-m", "base")
            base = git("rev-parse", "HEAD")
            git("switch", "-c", "topic")
            (repo / "services/core/deleted.go").unlink()
            (repo / "apps/web").mkdir(parents=True)
            (repo / "docs/old.md").rename(repo / "apps/web/renamed\nwith space.ts")
            git("add", "."); git("commit", "-m", "change")
            head = git("rev-parse", "HEAD")
            git("switch", "main"); git("merge", "--no-ff", "topic", "-m", "merge")
            clone = Path(tmp) / "shallow"
            subprocess.run(["git", "clone", "--depth=2", repo.as_uri(), str(clone)], check=True, capture_output=True)
            previous = Path.cwd()
            try:
                os.chdir(clone)
                paths = ci.changed_paths(base, "HEAD")
                self.assertEqual(set(paths), {"docs/old.md", "services/core/deleted.go", "apps/web/renamed\nwith space.ts"})
                plan = ci.event_plan("pull_request", {"pull_request": {"base": {"sha": base}, "head": {"sha": head}}})
                self.assertEqual(set(plan["jobs"]), {"hygiene", "backend", "api", "web", "web-acceptance"})
                (clone / "old.md").write_text("untracked content cannot change the diff\n")
                self.assertEqual(paths, ci.changed_paths(base, "HEAD"))
            finally:
                os.chdir(previous)


class GateTests(unittest.TestCase):
    def test_partial_reruns_cannot_reuse_a_previous_plan(self):
        ci.check_attempt("1", "1")
        ci.check_attempt("2", "2")
        for planned, current in (("1", "2"), (None, "2"), ("", "1"), ("0", "0")):
            with self.subTest(planned=planned), self.assertRaises(ValueError):
                ci.check_attempt(planned, current)

    def test_missing_execution_prerequisites_fail(self):
        for jobs, image in ((["hygiene", "web-acceptance"], False), (["hygiene"], True)):
            with self.assertRaises(ValueError):
                ci.validate_plan({"version": 1, "jobs": jobs, "image": image})

    def fixture(self):
        plan = ci.select(["apps/web/src/app.tsx"])
        needs = {job: {"result": "success" if job in plan["jobs"] else "skipped"} for job in ci.JOBS}
        needs["plan"] = {"result": "success"}
        return plan, needs

    def test_only_deliberately_unselected_jobs_may_skip(self):
        plan, needs = self.fixture()
        ci.check_results(plan, needs)
        for state in ("failure", "cancelled", "skipped", "", None):
            with self.subTest(state=state), self.assertRaises(ValueError):
                ci.check_results(plan, needs | {"web": {"result": state}})

    def test_matrix_result_failure_is_not_hidden_by_other_jobs(self):
        plan = ci.full("test")
        needs = {job: {"result": "success"} for job in (*ci.JOBS, "plan")}
        for name in ("native", "web-acceptance", "api"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                ci.check_results(plan, needs | {name: {"result": "failure"}})

    def test_plan_failure_missing_jobs_and_unexpected_execution_fail(self):
        plan, needs = self.fixture()
        for bad in ({}, {k: v for k, v in needs.items() if k != "native"}, needs | {"plan": {"result": "failure"}},
                    needs | {"native": {"result": "failure"}}, needs | {"native": {"result": "success"}}):
            with self.subTest(needs=bad), self.assertRaises(ValueError):
                ci.check_results(plan, bad)

    def test_malformed_plan_cannot_turn_checks_off(self):
        plan, needs = self.fixture()
        for bad in (None, {}, plan | {"jobs": []}, plan | {"jobs": ["hygiene", "invented"]},
                    plan | {"jobs": ["hygiene", "hygiene"]}, plan | {"image": True}, plan | {"image": "false"}):
            with self.subTest(plan=bad), self.assertRaises(ValueError):
                ci.check_results(bad, needs)


if __name__ == "__main__":
    unittest.main()
