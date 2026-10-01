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

    def test_unknown_inputs_planner_and_empty_diffs_are_full(self):
        for paths in ([], ["new-component/source.rs"], ["Makefile"], [".github/workflows/new.yml"],
                      [".github/actions/new/action.yml"], [".github/workflows/check.yml"], [".github/workflows/release.yml"], ["scripts/ci_plan.py"], ["../outside"], ["/outside"]):
            self.assertEqual(set(ci.select(paths)["jobs"]), set(ci.JOBS))
            self.assertTrue(ci.select(paths)["image"])

    def test_workflow_changes_select_only_their_consumers(self):
        for workflow, selected in {
            "ci-review": {"hygiene", "lint"},
            "actionlint": {"hygiene", "lint"},
            "native": {"hygiene", "native", "lint"},
            "api-acceptance": {"hygiene", "api", "lint"},
        }.items():
            with self.subTest(workflow=workflow):
                plan = ci.select([f".github/workflows/{workflow}.yml"])
                self.assertEqual(set(plan["jobs"]), selected)
                self.assertEqual(plan["image"], workflow == "api-acceptance")

    def test_node_action_selects_all_direct_consumers_and_lint(self):
        root = Path(__file__).resolve().parents[1]
        workflow = (root / ".github/workflows/check.yml").read_text()
        consumers = {name for name, body in re.findall(
            r"^  ([a-z-]+):\n(.*?)(?=^  [a-z-]+:|\Z)", workflow, re.M | re.S)
            if "uses: ./.github/actions/node" in body}
        for name, filename in (("api", "api-acceptance"), ("native", "native")):
            if "uses: ./.github/actions/node" in (root / f".github/workflows/{filename}.yml").read_text():
                consumers.add(name)
        self.assertEqual(self.jobs(".github/actions/node/action.yml"), consumers | {"hygiene", "lint"})

    def test_dependencies_are_scoped_to_language_consumers(self):
        for path in ("go.mod", "go.sum", "go.work", "go.work.sum"):
            with self.subTest(path=path):
                self.assertEqual(self.jobs(path), {"hygiene", "backend", "distribution", "api", "native"})
                self.assertTrue(ci.select([path])["image"])
        for path in ("package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", ".npmrc", "packages/tsconfig/base.json"):
            with self.subTest(path=path):
                self.assertEqual(self.jobs(path), {"hygiene", "harness", "example", "web", "web-acceptance", "native"})
                self.assertFalse(ci.select([path])["image"])
        self.assertEqual(self.jobs("tsconfig.base.json"), {"hygiene", "example", "web", "web-acceptance"})

    def test_ci_tests_and_metrics_do_not_trigger_product_checks(self):
        for path in ("scripts/ci_plan_test.py", "scripts/ci_metrics.py", "scripts/ci_metrics_test.py"):
            self.assertEqual(self.jobs(path), {"hygiene"})

    def test_workflow_and_code_changes_accumulate(self):
        self.assertEqual(self.jobs(".github/workflows/ci-review.yml", "services/core/internal/store/sessions.go"),
                         {"hygiene", "lint", "backend", "api"})
        self.assertEqual(self.jobs(".github/workflows/native.yml", "apps/web/src/app.tsx"),
                         {"hygiene", "lint", "native", "web", "web-acceptance"})

    def test_every_job_has_a_plan_condition(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/check.yml").read_text()
        bodies = dict(re.findall(r"^  ([a-z-]+):\n(.*?)(?=^  [a-z-]+:|\Z)", workflow, re.M | re.S))
        for job in ci.JOBS:
            with self.subTest(job=job):
                self.assertIn(f"contains(fromJSON(needs.plan.outputs.jobs || '[]'), '{job}')", bodies[job])

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
        self.assertEqual(self.jobs("new-engine/system-prompt.md"), set(ci.JOBS))

    def test_non_pr_events_always_run_full(self):
        for event in ("push", "workflow_dispatch", "workflow_call"):
            self.assertEqual(ci.event_plan(event, {})["jobs"], list(ci.JOBS))
        self.assertEqual(ci.event_plan("pull_request", {}, "release-sha")["jobs"], list(ci.JOBS))

    def test_unavailable_or_wrong_merge_diff_runs_full(self):
        self.assertEqual(ci.event_plan("pull_request", {})["jobs"], list(ci.JOBS))
        event = {"pull_request": {"base": {"sha": "a"}, "head": {"sha": "b"}}}
        with patch.object(ci, "git", return_value=b"parent wrong\n\nmessage"):
            self.assertEqual(ci.event_plan("pull_request", event)["jobs"], list(ci.JOBS))
        with patch.object(ci, "git", side_effect=subprocess.CalledProcessError(1, "git")):
            self.assertEqual(ci.event_plan("pull_request", event)["jobs"], list(ci.JOBS))


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


class DocumentationPushTests(unittest.TestCase):
    def setUp(self):
        self.before, self.after = "a" * 40, "b" * 40
        self.event = {"ref": "refs/heads/main", "before": self.before, "after": self.after, "forced": False, "deleted": False}

    def plan(self, paths, event=None, ref=""):
        with patch.object(ci, "git", side_effect=[self.after.encode(), b""]), patch.object(ci, "changed_paths", return_value=paths) as diff:
            plan = ci.event_plan("push", self.event if event is None else event, ref)
            return plan, diff

    def test_doc_site_configuration_and_docs_only_push_skip_product_checks(self):
        paths = ["docs.json", ".mintignore", "docs/getting-started/index.md", "README.md"]
        self.assertEqual(set(ci.select(paths)["jobs"]), {"hygiene"})
        plan, diff = self.plan(paths)
        self.assertEqual(set(plan["jobs"]), {"hygiene"})
        diff.assert_called_once_with(self.before, self.after)

    def test_generated_documentation_keeps_freshness_checks(self):
        plan, _ = self.plan(["docs/configuration.md", "contracts/agents-api/harness-catalog.md"])
        self.assertEqual(set(plan["jobs"]), {"hygiene", "distribution"})

    def test_code_mixed_unknown_and_empty_pushes_keep_full_gate(self):
        for paths in (["README.md", "services/core/cmd/server/main.go"], ["new.md"], [], ["docs.json", "scripts/generate-harness-catalog.py"]):
            with self.subTest(paths=paths):
                self.assertEqual(self.plan(paths)[0]["jobs"], list(ci.JOBS))

    def test_releases_and_untrusted_pushes_keep_full_gate(self):
        self.assertEqual(self.plan(["README.md"], ref=self.after)[0]["jobs"], list(ci.JOBS))
        for fields in ({"ref": "refs/tags/v1"}, {"forced": True}, {"deleted": True}, {"before": "0" * 40}, {"before": "--bad"}, {"after": "c" * 40}):
            with self.subTest(fields=fields):
                self.assertEqual(self.plan(["README.md"], self.event | fields)[0]["jobs"], list(ci.JOBS))
        with patch.object(ci, "git", side_effect=subprocess.CalledProcessError(1, "git")):
            self.assertEqual(ci.event_plan("push", self.event)["jobs"], list(ci.JOBS))

    def test_push_uses_entire_commit_range_and_fails_closed_on_shallow_history(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp) / "source"
            repo.mkdir()
            def git(*args):
                return subprocess.check_output(["git", "-C", str(repo), *args], stderr=subprocess.DEVNULL).decode().strip()
            git("init", "-b", "main")
            git("config", "user.email", "ci-test@example.invalid")
            git("config", "user.name", "CI test")
            (repo / "README.md").write_text("base\n")
            git("add", "."); git("commit", "-m", "base")
            before = git("rev-parse", "HEAD")
            (repo / "docs.json").write_text("{}\n")
            git("add", "."); git("commit", "-m", "docs")
            docs_head = git("rev-parse", "HEAD")
            previous = Path.cwd()
            try:
                os.chdir(repo)
                event = self.event | {"before": before, "after": docs_head}
                self.assertEqual(ci.event_plan("push", event)["jobs"], ["hygiene"])
                (repo / "code.go").write_text("package example\n")
                git("add", "."); git("commit", "-m", "code")
                (repo / "README.md").write_text("updated\n")
                git("add", "."); git("commit", "-m", "docs again")
                event["after"] = git("rev-parse", "HEAD")
                self.assertEqual(ci.event_plan("push", event)["jobs"], list(ci.JOBS))
                clone = Path(tmp) / "shallow"
                subprocess.run(["git", "clone", "--depth=2", repo.as_uri(), str(clone)], check=True, capture_output=True)
                os.chdir(clone)
                self.assertEqual(ci.event_plan("push", event)["jobs"], list(ci.JOBS))
            finally:
                os.chdir(previous)
