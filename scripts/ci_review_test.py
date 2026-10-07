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
        self.ok = {"status": "ok", "summary": "未发现问题"}

    def card(self, code=None, docs=None):
        return review.build_card(self.event, {"code": code or self.ok, "docs": docs or self.ok}, self.run_url)

    @patch("ci_review.urllib.request.urlopen")
    def test_two_reports_make_one_delivery(self, post):
        post.return_value = io.BytesIO(b'{"code":0}')
        card = self.card(docs={"status": "issues", "summary": "docs/install.md:12 与代码不符"})
        review.send_card("https://example.invalid/webhook", "", card)
        post.assert_called_once()
        payload = json.loads(post.call_args.args[0].data)
        self.assertEqual(payload["card"]["schema"], "2.0")
        self.assertEqual(payload["card"]["header"]["template"], "red")
        text = "\n".join(item["content"] for item in payload["card"]["body"]["elements"])
        self.assertIn("代码审查与 CI：未发现问题", text)
        self.assertIn("文档审查：发现问题", text)
        self.assertIn("docs/install.md:12", text)
        self.assertIn(self.run_url, text)
        self.assertNotIn("sign", payload)

    def test_failed_action_cannot_publish_a_success_report(self):
        for outcome in ("failure", "cancelled", "skipped", None):
            with self.subTest(outcome=outcome):
                result = review.collect(outcome, json.dumps(self.ok))
                self.assertEqual(result["status"], "incomplete")
                self.assertEqual(self.card(code=result)["card"]["header"]["template"], "yellow")

    def test_invalid_or_missing_reports_are_incomplete(self):
        for raw in ("", "not json", "null", "[]", '{"status":"ok"}', '{"status":[],"summary":"x"}', '{"status":"ok","summary":" "}', json.dumps({"status": "ok", "summary": "x" * 2001})):
            with self.subTest(raw=raw[:50]):
                self.assertEqual(review.collect("success", raw)["status"], "incomplete")
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
        report = {"status": "issues", "summary": "<at id=all>所有人</at>" + "问" * 1950}
        review.send_card("https://example.invalid/webhook", "", self.card(report, report))
        data = post.call_args.args[0].data
        self.assertLess(len(data), 20000)
        self.assertNotIn(b"<at", data)


if __name__ == "__main__":
    unittest.main()
