#!/usr/bin/env python3
"""Exercise combined notification and incomplete-review behavior without sending messages."""

import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import ci_review as review


class ReviewTests(unittest.TestCase):
    event = {"pull_request": {"number": 485, "title": "Review docs", "html_url": "https://github.com/org/repo/pull/485", "user": {"login": "author"}}}
    run_url = "https://github.com/org/repo/actions/runs/123"

    def setUp(self):
        self.ok = {"status": "ok", "changes": "- 更新安装流程", "review": "未发现问题", "ci": "全部通过 ✅"}
        self.docs = {"status": "ok", "consistency": "与代码一致", "organization": "无矛盾或重复"}

    def card(self, code=None, docs=None):
        return review.build_card(self.event, {"code": code or self.ok, "docs": docs or self.docs}, self.run_url)

    @patch("ci_review.urllib.request.urlopen")
    def test_two_reports_make_one_delivery(self, post):
        post.return_value = io.BytesIO(b'{"code":0}')
        card = self.card(docs={**self.docs, "status": "issues", "consistency": "docs/install.md:12 与代码不符"})
        review.send_card("https://example.invalid/webhook", "", card)
        post.assert_called_once()
        payload = json.loads(post.call_args.args[0].data)
        self.assertEqual(payload["card"]["schema"], "2.0")
        self.assertEqual(payload["card"]["header"]["template"], "red")
        text = review.card_markdown(payload)
        self.assertIn("3. 代码与仓库规则", text)
        self.assertIn("文档审查 · 发现问题", text)
        self.assertIn("docs/install.md:12", text)
        self.assertIn(self.run_url, text)
        self.assertNotIn("sign", payload)

    def test_fixed_sections_preserve_markdown_and_separators(self):
        card = self.card()
        elements = card["card"]["body"]["elements"]
        self.assertEqual(sum(element["tag"] == "hr" for element in elements), 4)
        text = review.card_markdown(card)
        for heading in ("1. 哪个 PR", "2. 改了什么", "3. 代码与仓库规则", "4. CI 结果", "5. 文档审查"):
            self.assertIn(heading, text)
        self.assertIn("- 更新安装流程", text)
        self.assertIn("全部通过 ✅", text)
        self.assertIn("文档与代码一致性", text)
        self.assertIn("文档矛盾、重复与归属", text)
        self.assertIn("---", text)
        self.assertEqual(card["card"]["header"]["template"], "green")
        self.assertIn("✅ 审查通过", card["card"]["header"]["title"]["content"])

    def test_pending_ci_does_not_label_the_code_review_incomplete(self):
        card = self.card(code={**self.ok, "status": "incomplete", "ci": "backend 仍在运行"})
        text = review.card_markdown(card)
        self.assertIn("**3. 代码与仓库规则**", text)
        self.assertIn("未发现问题", text)
        self.assertIn("backend 仍在运行", text)
        self.assertEqual(card["card"]["header"]["template"], "yellow")

    def test_each_kind_requires_its_own_sections(self):
        for kind, report in (("code", self.ok), ("docs", self.docs)):
            self.assertEqual(review.parse_report(json.dumps(report), kind), report)
            self.assertEqual(set(review.report_schema(kind)["required"]), set(report))
            for field in review.REPORT_FIELDS[kind]:
                for value in (None, " ", "x" * (review.MAX_SECTION + 1)):
                    invalid = {**report, field: value}
                    self.assertEqual(review.parse_report(json.dumps(invalid), kind)["status"], "incomplete")
                missing = {key: value for key, value in report.items() if key != field}
                self.assertEqual(review.parse_report(json.dumps(missing), kind)["status"], "incomplete")

    def test_failed_action_cannot_publish_a_success_report(self):
        for outcome in ("failure", "cancelled", "skipped", None):
            with self.subTest(outcome=outcome):
                result = review.collect(outcome, json.dumps(self.ok), "code")
                self.assertEqual(result["status"], "incomplete")
                self.assertEqual(self.card(code=result)["card"]["header"]["template"], "yellow")

    def test_invalid_or_missing_reports_are_incomplete(self):
        for raw in ("", "not json", "null", "[]", '{"status":"ok"}', '{"status":[],"summary":"x"}', '{"status":"ok","summary":" "}', json.dumps({"status": "ok", "summary": "x" * 2001})):
            with self.subTest(raw=raw[:50]):
                self.assertEqual(review.collect("success", raw, "code")["status"], "incomplete")
        root = Path.home() / ".oac/tests"
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=root) as directory:
            path = Path(directory)
            (path / "review-code.json").write_text(json.dumps(self.ok))
            self.assertEqual(review.read_report(path, "code"), self.ok)
            self.assertEqual(review.read_report(path, "docs")["status"], "incomplete")

    @patch("ci_review.urllib.request.urlopen")
    def test_http_success_requires_feishu_confirmation(self, post):
        for body in (b'{"code":19021}', b'{}', b'{"code":false}', b'not json'):
            with self.subTest(body=body):
                post.reset_mock()
                post.return_value = io.BytesIO(body)
                with self.assertRaises(ValueError):
                    review.send_card("https://example.invalid/webhook", "", self.card())
                post.assert_called_once()

    @patch("ci_review.urllib.request.urlopen", side_effect=TimeoutError("private-webhook"))
    def test_ambiguous_delivery_is_not_retried_or_logged_with_secrets(self, post):
        with self.assertRaisesRegex(ValueError, "delivery status is unknown") as error:
            review.send_card("https://example.invalid/private-webhook", "private-signing-key", self.card())
        self.assertNotIn("private", str(error.exception))
        post.assert_called_once()

    @patch("ci_review.time.time", return_value=1700000000)
    @patch("ci_review.urllib.request.urlopen")
    def test_optional_signing(self, post, _clock):
        post.return_value = io.BytesIO(b'{"code":0}')
        review.send_card("https://example.invalid/webhook", "test-secret", self.card())
        payload = json.loads(post.call_args.args[0].data)
        self.assertEqual(payload["timestamp"], "1700000000")
        self.assertEqual(payload["sign"], "mbm4Y4oluIPQ00qlBIhX8vAZ0EKv3nw0LuTb91jPL84=")

    @patch("ci_review.urllib.request.urlopen")
    def test_bounded_unicode_reports_and_mentions(self, post):
        post.return_value = io.BytesIO(b'{"code":0}')
        reports = {kind: {"status": "issues", **dict.fromkeys(fields, "<at id=all>所有人</at>" + "问" * (review.MAX_SECTION - 20))}
                   for kind, fields in review.REPORT_FIELDS.items()}
        review.send_card("https://example.invalid/webhook", "", self.card(reports["code"], reports["docs"]))
        data = post.call_args.args[0].data
        self.assertLess(len(data), 20000)
        self.assertNotIn(b"<at", data)


if __name__ == "__main__":
    unittest.main()
