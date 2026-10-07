#!/usr/bin/env python3
"""Collect independent review reports and deliver one Feishu card."""

import argparse
import base64
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import time
import urllib.error
import urllib.request


STATUSES = {"ok": "未发现问题", "issues": "发现问题", "incomplete": "未完成"}
MAX_SUMMARY = 2000
SCHEMA = {
    "type": "object",
    "properties": {
        "status": {"type": "string", "enum": list(STATUSES)},
        "summary": {"type": "string", "minLength": 1, "maxLength": MAX_SUMMARY},
    },
    "required": ["status", "summary"],
    "additionalProperties": False,
}


def incomplete(reason):
    return {"status": "incomplete", "summary": reason}


def parse_report(raw):
    try:
        report = json.loads(raw)
    except (ValueError, TypeError):
        return incomplete("未收到有效报告，请查看审查日志。")
    if (not isinstance(report, dict) or set(report) != set(SCHEMA["required"])
            or not isinstance(report["status"], str) or report["status"] not in STATUSES
            or not isinstance(report["summary"], str) or not report["summary"].strip()
            or len(report["summary"]) > MAX_SUMMARY):
        return incomplete("报告格式不完整，请查看审查日志。")
    return report


def collect(outcome, raw):
    if outcome != "success":
        return incomplete("审查未成功结束，请查看审查日志。")
    return parse_report(raw)


def read_report(directory, kind):
    try:
        return parse_report((directory / f"review-{kind}.json").read_text())
    except (OSError, UnicodeError):
        return incomplete("审查报告缺失，请查看审查日志。")


def markdown_text(value):
    return re.sub(r"([\\`*_{}\[\]()<>!])", r"\\\1", value)


def build_card(event, reports, run_url):
    pr = event["pull_request"]
    statuses = {report["status"] for report in reports.values()}
    color = "red" if "issues" in statuses else "yellow" if "incomplete" in statuses else "green"
    elements = [{"tag": "markdown", "content": (
        f"**{markdown_text(pr['title'][:200])}**\n"
        f"{markdown_text(pr['user']['login'])} · [PR #{pr['number']}]({pr['html_url']})"
    )}]
    for kind, title in (("code", "代码审查与 CI"), ("docs", "文档审查")):
        report = reports[kind]
        # Feishu mentions use HTML-like tags; reports are ordinary Markdown.
        summary = report["summary"].replace("<", "&lt;").replace(">", "&gt;")
        elements.append({"tag": "markdown", "content": f"**{title}：{STATUSES[report['status']]}**\n{summary}"})
    elements.append({"tag": "markdown", "content": f"[查看审查日志]({run_url})"})
    return {
        "msg_type": "interactive",
        "card": {
            "schema": "2.0",
            "header": {"template": color, "title": {"tag": "plain_text", "content": f"CI 审查 · PR #{pr['number']}"}},
            "body": {"elements": elements},
        },
    }


def send_card(webhook, secret, card):
    if not webhook:
        raise ValueError("FEISHU_WEBHOOK_URL is not configured")
    payload = dict(card)
    if secret:
        timestamp = str(int(time.time()))
        key = f"{timestamp}\n{secret}".encode()
        payload.update(timestamp=timestamp, sign=base64.b64encode(hmac.new(key, b"", hashlib.sha256).digest()).decode())
    data = json.dumps(payload, ensure_ascii=False).encode()
    if len(data) > 20000:
        raise ValueError("Review card exceeds the webhook message size limit")
    request = urllib.request.Request(webhook, data=data, headers={"Content-Type": "application/json"}, method="POST")
    # A timeout may follow successful delivery; retrying could send a duplicate.
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.load(response)
    except (OSError, ValueError, urllib.error.URLError):
        raise ValueError("Feishu delivery failed; delivery status is unknown") from None
    if not isinstance(result, dict) or type(result.get("code")) is not int or result["code"] != 0:
        raise ValueError("Feishu did not confirm delivery")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("schema")
    commands.add_parser("collect").add_argument("output", type=Path)
    commands.add_parser("notify").add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "schema":
        print("schema=" + json.dumps(SCHEMA, separators=(",", ":")))
    elif args.command == "collect":
        report = collect(os.environ.get("REVIEW_OUTCOME"), os.environ.get("REVIEW_RESULT", ""))
        args.output.write_text(json.dumps(report, ensure_ascii=False))
    else:
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        reports = {kind: read_report(args.directory, kind) for kind in ("code", "docs")}
        run_url = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/{os.environ['GITHUB_RUN_ID']}"
        card = build_card(event, reports, run_url)
        summary = os.environ.get("GITHUB_STEP_SUMMARY")
        if summary:
            with open(summary, "a") as output:
                output.write("\n\n".join(element["content"] for element in card["card"]["body"]["elements"]) + "\n")
        send_card(os.environ.get("FEISHU_WEBHOOK_URL"), os.environ.get("FEISHU_WEBHOOK_SECRET"), card)
        print("代码与文档审查结果已合并发送至飞书群。")


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        raise SystemExit(str(error)) from None
