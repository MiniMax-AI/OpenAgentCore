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

    def test_published_documents_also_build_the_website(self):
        for path in ("docs/maintainers.md", "contracts/agents-api/admin-api.md", "docs/assets/logo.svg", "docs.json"):
            self.assertEqual(self.jobs(path), {"hygiene", "website"})
        self.assertEqual(self.jobs("README.md"), {"hygiene"})

    def test_installer_does_not_download_a_browser_or_run_database_tests(self):
        self.assertEqual(self.jobs("deploy/install.sh", "deploy/node/node_payload.py"), {"hygiene", "distribution"})

    def test_compose_inputs_select_live_and_fixture_checks_without_image_builds(self):
        for path in ("deploy/compose/compose.yaml", "deploy/compose/https.yaml", "deploy/compose/dokploy.toml",
                     "scripts/compose-smoke.py", "scripts/render-compose.py", "deploy/compose/test_compose.py"):
            self.assertEqual(self.jobs(path), {"hygiene", "distribution", "compose"})
            self.assertFalse(ci.select([path])["image"])

    def test_web_and_core_have_different_consumers(self):
        self.assertEqual(self.jobs("apps/web/src/app.tsx"), {"hygiene", "web", "web-acceptance"})
        plan = ci.select(["services/core/internal/store/sessions.go"])
        self.assertEqual(set(plan["jobs"]), {"hygiene", "backend", "api", "compose"})
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
        for path in [*map(str, outputs), "scripts/acceptance/harness_catalog.py"]:
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

    def test_shared_inputs_and_planner_are_full(self):
        for paths in (["Makefile"], [".github/workflows/new.yml"],
                      [".github/actions/new/action.yml"], [".github/workflows/check.yml"], [".github/workflows/release.yml"], ["scripts/ci_plan.py"], ["../outside"], ["/outside"]):
            self.assertEqual(set(ci.select(paths)["jobs"]), set(ci.JOBS))
            self.assertTrue(ci.select(paths)["image"])

    def test_workflow_changes_select_only_their_consumers(self):
        for workflow, selected in {
            "ci-review": {"hygiene", "lint"},
            "actionlint": {"hygiene", "lint"},
            "website": {"hygiene", "website", "lint"},
            "native": {"hygiene", "native", "lint"},
            "api-acceptance": {"hygiene", "api", "lint"},
            "cache-warm": {"hygiene", "lint"},
        }.items():
            with self.subTest(workflow=workflow):
                plan = ci.select([f".github/workflows/{workflow}.yml"])
                self.assertEqual(set(plan["jobs"]), selected)
                self.assertEqual(plan["image"], workflow == "api-acceptance")

    def test_cache_actions_select_their_consumers(self):
        self.assertEqual(self.jobs(".github/actions/mcode-companion/action.yml"), {"hygiene", "native", "lint"})
        plan = ci.select([".github/actions/e2b-provider/action.yml"])
        self.assertEqual(set(plan["jobs"]), {"hygiene", "api", "lint"})
        self.assertTrue(plan["image"])

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
                self.assertEqual(self.jobs(path), {"hygiene", "backend", "distribution", "compose", "api", "native"})
                self.assertTrue(ci.select([path])["image"])
        for path in ("package.json", "pnpm-workspace.yaml", ".npmrc"):
            with self.subTest(path=path):
                self.assertEqual(self.jobs(path), {"hygiene", "harness", "example", "web", "web-acceptance", "website", "native"})
                self.assertFalse(ci.select([path])["image"])
        self.assertEqual(self.jobs("tsconfig.base.json"), {"hygiene", "example", "web", "web-acceptance"})

    def test_module_locks_only_select_their_consumers(self):
        cases = {
            "website/pnpm-lock.yaml": {"website"},
            "website/pnpm-workspace.yaml": {"website"},
            "packages/claude-sdk-adapter/pnpm-workspace.yaml": {"harness", "native", "distribution"},
            "apps/web/pnpm-lock.yaml": {"web", "web-acceptance"},
            "apps/web/package.json": {"web", "web-acceptance"},
            "example/parsar/pnpm-lock.yaml": {"example"},
            "packages/agents-client/pnpm-lock.yaml": {"web", "web-acceptance", "example"},
            "packages/claude-sdk-adapter/pnpm-lock.yaml": {"harness", "native", "distribution"},
            "packages/claude-sdk-adapter/package.json": {"harness", "native", "distribution"},
            "packages/tsconfig/base.json": {"harness", "native"},
            "pnpm-lock.yaml": set(),
        }
        for path, jobs in cases.items():
            with self.subTest(path=path):
                self.assertEqual(self.jobs(path), jobs | {"hygiene"})
        self.assertEqual(self.jobs("website/package.json", "website/pnpm-lock.yaml",
                                  "website/.vitepress/theme/components/Mermaid.vue"),
                         {"hygiene", "website"})
        self.assertEqual(self.jobs("website/pnpm-lock.yaml", "apps/web/pnpm-lock.yaml"),
                         {"hygiene", "website", "web", "web-acceptance"})

    def test_ci_tests_and_metrics_do_not_trigger_product_checks(self):
        for path in ("scripts/ci_plan_test.py", "scripts/ci_metrics.py", "scripts/ci_metrics_test.py"):
            self.assertEqual(self.jobs(path), {"hygiene"})

    def test_workflow_and_code_changes_accumulate(self):
        self.assertEqual(self.jobs(".github/workflows/ci-review.yml", "services/core/internal/store/sessions.go"),
                         {"hygiene", "lint", "backend", "api", "compose"})
        self.assertEqual(self.jobs(".github/workflows/native.yml", "apps/web/src/app.tsx"),
                         {"hygiene", "lint", "native", "web", "web-acceptance"})

    def test_every_job_has_a_plan_condition(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/check.yml").read_text()
        bodies = dict(re.findall(r"^  ([a-z-]+):\n(.*?)(?=^  [a-z-]+:|\Z)", workflow, re.M | re.S))
        for job in ci.JOBS:
            with self.subTest(job=job):
                self.assertIn(f"contains(fromJSON(needs.plan.outputs.jobs || '[]'), '{job}')", bodies[job])

    def test_mixed_changes_accumulate(self):
        self.assertEqual(self.jobs("docs/maintainers.md", "deploy/install.sh", "apps/web/src/app.tsx"),
                         {"hygiene", "distribution", "web", "web-acceptance", "website"})

    def test_installer_pr_300_replay(self):
        self.assertEqual(self.jobs(
            "deploy/install.sh", "deploy/README.md", "deploy/node/node_install.py",
            "deploy/node/install_display.py", "deploy/node/node_payload.py",
            "docs/getting-started/install.md"), {"hygiene", "distribution", "website"})

    def test_workflow_graph_cannot_silently_omit_or_add_a_gate_dependency(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/check.yml").read_text().split("jobs:\n", 1)[1]
        jobs = set(re.findall(r"^  ([a-z-]+):$", workflow, re.M))
        self.assertEqual(jobs, set(ci.JOBS) | {"plan", "check"})
        gate = workflow.split("  check:\n", 1)[1]
        dependencies = re.search(r"needs: \[(.+)\]", gate).group(1).split(", ")
        self.assertEqual(set(dependencies), set(ci.JOBS) | {"plan"})

    def test_only_matching_directory_and_suffix_trigger_product_checks(self):
        for path in ("services/core/notes.md", "apps/daemon/design.md", "internal/architecture.md",
                     "apps/web/notes.md", "scripts/build-core.sh.md", "new-component/source.rs",
                     "services/core/code.go.bak"):
            self.assertEqual(self.jobs(path), {"hygiene"}, path)
        self.assertEqual(self.jobs("services/core/code.go"), {"hygiene", "backend", "api", "compose"})
        self.assertEqual(self.jobs("apps/web/src/style.css"), {"hygiene", "web", "web-acceptance"})
        self.assertEqual(self.jobs("services/core/migrations/123.sql"), {"hygiene", "backend", "api", "compose"})

    def test_fixture_and_embedded_resources_keep_checks_regardless_of_suffix(self):
        for path in ("services/core/tests/testdata/prompt.md", "services/core/tests/testdata/image.jpg"):
            self.assertTrue({"backend", "api"} <= self.jobs(path))
        self.assertTrue({"backend", "native"} <= self.jobs("apps/daemon/internal/agent/testdata/prompt.md"))
        self.assertTrue({"backend", "api", "native", "distribution"} <= self.jobs("services/core/internal/nativeinstaller/assets/archive"))
        self.assertTrue({"web", "web-acceptance"} <= self.jobs("apps/web/public/logo.svg"))
        self.assertTrue({"distribution", "compose", "web", "web-acceptance"} <= self.jobs("services/web/Dockerfile"))

    def test_tracked_program_sources_have_a_matching_rule(self):
        root = Path(__file__).resolve().parents[1]
        paths = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
        for path in filter(None, paths):
            if Path(path).suffix in {".go", ".sql", ".ts", ".tsx", ".mjs", ".sh", ".ps1"} or path.endswith("Dockerfile"):
                self.assertNotEqual(self.jobs(path), {"hygiene"}, path)

    def test_verified_empty_diff_only_needs_hygiene(self):
        self.assertEqual(self.jobs(), {"hygiene"})

    def test_actionlint_config_selects_lint(self):
        self.assertEqual(self.jobs(".github/actionlint.yaml"), {"hygiene", "lint"})

    def test_ci_and_deployment_have_distinct_triggers(self):
        root = Path(__file__).resolve().parents[1]
        check = (root / ".github/workflows/check.yml").read_text()
        website = (root / ".github/workflows/website.yml").read_text()
        review = (root / ".github/workflows/ci-review.yml").read_text()
        self.assertIn("  pull_request:", check)
        self.assertIn("  workflow_dispatch:", check)
        self.assertNotIn("  push:", check)
        self.assertIn("  push:", website)
        self.assertNotIn("  pull_request:", website)
        self.assertIn("pull_request.merged == true", review)
        self.assertIn("ref: ${{ github.event.pull_request.merge_commit_sha }}", review)
        self.assertNotIn("workflow_run", review)

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
                self.assertEqual(set(plan["jobs"]), {"hygiene", "backend", "api", "compose", "web", "web-acceptance", "website"})
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

    def test_compose_smoke_must_succeed_when_selected(self):
        plan = ci.select(["deploy/compose/compose.yaml"])
        needs = {job: {"result": "success" if job in plan["jobs"] else "skipped"} for job in ci.JOBS}
        needs["plan"] = {"result": "success"}
        ci.check_results(plan, needs)
        for state in ("failure", "cancelled", "skipped"):
            with self.subTest(state=state), self.assertRaises(ValueError):
                ci.check_results(plan, needs | {"compose": {"result": state}})

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
