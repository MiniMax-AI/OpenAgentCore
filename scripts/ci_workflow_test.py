"""Keep trigger ownership and workflow adapters aligned with the CI policy."""

from pathlib import Path
import re
import unittest

from ci_policy import JOBS, select

ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / ".github/workflows"


class WorkflowTests(unittest.TestCase):
    def test_daily_entry_has_no_push_or_schedule_trigger(self):
        text = (WORKFLOWS / "check.yml").read_text()
        trigger = text.split("on:\n", 1)[1].split("\npermissions:", 1)[0]
        self.assertEqual(set(re.findall(r"^  ([a-z_]+):", trigger, re.M)), {"pull_request", "workflow_call"})
        self.assertIn("cancel-in-progress: true", text)
        self.assertIn("github.ref", text.split("concurrency:", 1)[1].split("jobs:", 1)[0])
        self.assertIn("attempt: ${{ steps.select.outputs.attempt }}", text)
        self.assertIn("PLAN_ATTEMPT: ${{ needs.plan.outputs.attempt }}", text)

    def test_check_adapters_are_callable_and_select_their_own_group(self):
        text = (WORKFLOWS / "check.yml").read_text()
        for job in set(JOBS) - {"hygiene"}:
            block = re.search(r"^  " + job + r":\n(.*?)(?=^  (?:[a-z-]+:|#)|\Z)", text, re.M | re.S).group(1)
            self.assertIn(f"'{job}')", block)
            target = re.search(r"uses: \./(\S+)", block).group(1)
            self.assertIn("ref: ${{ inputs.ref || github.sha }}", block)
            adapter = (ROOT / target).read_text()
            trigger = adapter.split("on:\n", 1)[1].split("\npermissions:", 1)[0]
            self.assertIn("  workflow_call:", trigger)
            self.assertNotRegex(trigger, r"(?m)^  (push|pull_request|schedule):")
            self.assertIn("ref: ${{ inputs.ref", adapter)
            self.assertIn(job, select([target])["jobs"])

    def test_release_requests_full_checks_before_build_and_publication(self):
        text = (WORKFLOWS / "release.yml").read_text()
        check = text.split("  check:\n", 1)[1].split("  build:\n", 1)[0]
        self.assertIn("scope: full", check)
        self.assertIn("native-artifacts: true", check)
        self.assertIn("ref: ${{ inputs.ref || github.sha }}", check)
        self.assertIn("    needs: check", text.split("  build:\n", 1)[1])
        self.assertIn("needs: [check, build]", text.split("  release:\n", 1)[1])
        native = (WORKFLOWS / "native.yml").read_text()
        self.assertIn("overwrite: true", native)
        self.assertIn("if: inputs.upload-artifacts", native)

    def test_browser_and_native_limits_remain_owned_by_their_adapters(self):
        web = (WORKFLOWS / "ci-web-acceptance.yml").read_text()
        self.assertIn("shard: [1, 2]", web)
        self.assertIn("OAC_WEB_TEST_SHARD=${{ matrix.shard }}/2", web)
        self.assertIn("workers: 1", (ROOT / "playwright.config.ts").read_text())
        native = (WORKFLOWS / "native.yml").read_text()
        self.assertIn("max-parallel: 2", native)
        for platform in ("linux-amd64", "darwin-arm64", "windows-amd64"):
            self.assertIn(platform, native)


if __name__ == "__main__":
    unittest.main()
