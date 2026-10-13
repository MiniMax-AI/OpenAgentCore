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
MAX_SECTION = 900
REPORT_FIELDS = {
    "code": {"changes": "实际行为变化，1–3 条 Markdown 列表", "review": "代码正确性与仓库规则审查结论", "ci": "已有 CI 结果；失败或未完成时给出具体检查项"},
    "docs": {"consistency": "文档与代码一致性", "organization": "文档矛盾、重复与归属；未改文档时写本次未修改文档"},
}


def report_schema(kind):
    properties = {"status": {"type": "string", "enum": list(STATUSES)}}
    properties.update({name: {"type": "string", "minLength": 1, "maxLength": MAX_SECTION, "description": description}
                       for name, description in REPORT_FIELDS[kind].items()})
    return {"type": "object", "properties": properties, "required": list(properties), "additionalProperties": False}


def incomplete(reason, kind):
    fields = dict.fromkeys(REPORT_FIELDS[kind], "未完成。")
    fields[next(iter(fields))] = reason
    return {"status": "incomplete", **fields}


def parse_report(raw, kind):
    try:
        report = json.loads(raw)
    except (ValueError, TypeError):
        return incomplete("未收到有效报告，请查看审查日志。", kind)
    if (not isinstance(report, dict) or set(report) != {"status", *REPORT_FIELDS[kind]}
            or not isinstance(report["status"], str) or report["status"] not in STATUSES
            or any(not isinstance(report[name], str) or not report[name].strip()
                   or len(report[name]) > MAX_SECTION for name in REPORT_FIELDS[kind])):
        return incomplete("报告格式不完整，请查看审查日志。", kind)
    return report


def collect(outcome, raw, kind):
    if outcome != "success":
        return incomplete("审查未成功结束，请查看审查日志。", kind)
    return parse_report(raw, kind)


def read_report(directory, kind):
    try:
        return parse_report((directory / f"review-{kind}.json").read_text(), kind)
    except (OSError, UnicodeError):
        return incomplete("审查报告缺失，请查看审查日志。", kind)


def markdown_text(value):
    return re.sub(r"([\\`*_{}\[\]()<>!])", r"\\\1", value)


def build_card(event, reports, run_url):
    pr = event["pull_request"]
    statuses = {report["status"] for report in reports.values()}
    color = "red" if "issues" in statuses else "yellow" if "incomplete" in statuses else "green"
    heading = "❌ 审查发现问题" if "issues" in statuses else "⚠️ 审查未完成" if "incomplete" in statuses else "✅ 审查通过"
    code, docs = reports["code"], reports["docs"]

    def text(value):
        # Feishu mentions use HTML-like tags; reports are ordinary Markdown.
        return value.replace("<", "&lt;").replace(">", "&gt;")

    sections = [
        ("1. 哪个 PR", f"**github id:** {markdown_text(pr['user']['login'])}\n"
         f"**标题:** {markdown_text(pr['title'][:200])}\n**链接:** [PR #{pr['number']}]({pr['html_url']})"),
        ("2. 改了什么", text(code["changes"])),
        ("3. 代码与仓库规则", text(code["review"])),
        ("4. CI 结果", text(code["ci"])),
        (f"5. 文档审查 · {STATUSES[docs['status']]}",
         f"**文档与代码一致性**\n{text(docs['consistency'])}\n\n"
         f"**文档矛盾、重复与归属**\n{text(docs['organization'])}"),
    ]
    elements = []
    for title, content in sections:
        if elements:
            elements.append({"tag": "hr"})
        elements.append({"tag": "markdown", "content": f"**{title}**\n\n{content}"})
    elements.append({"tag": "markdown", "content": f"[查看审查日志]({run_url})"})
    return {
        "msg_type": "interactive",
        "card": {
            "schema": "2.0",
            "header": {"template": color, "title": {"tag": "plain_text", "content": f"{heading} · PR #{pr['number']}"}},
            "body": {"elements": elements},
        },
    }


def card_markdown(card):
    return "\n\n".join("---" if element["tag"] == "hr" else element["content"]
                        for element in card["card"]["body"]["elements"]) + "\n"


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
    commands.add_parser("schema").add_argument("kind", choices=REPORT_FIELDS)
    collector = commands.add_parser("collect")
    collector.add_argument("kind", choices=REPORT_FIELDS)
    collector.add_argument("output", type=Path)
    commands.add_parser("notify").add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "schema":
        print("schema=" + json.dumps(report_schema(args.kind), separators=(",", ":")))
    elif args.command == "collect":
        report = collect(os.environ.get("REVIEW_OUTCOME"), os.environ.get("REVIEW_RESULT", ""), args.kind)
        args.output.write_text(json.dumps(report, ensure_ascii=False))
    else:
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        reports = {kind: read_report(args.directory, kind) for kind in ("code", "docs")}
        run_url = f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}/actions/runs/{os.environ['GITHUB_RUN_ID']}"
        card = build_card(event, reports, run_url)
        summary = os.environ.get("GITHUB_STEP_SUMMARY")
        if summary:
            with open(summary, "a") as output:
                output.write(card_markdown(card))
        send_card(os.environ.get("FEISHU_WEBHOOK_URL"), os.environ.get("FEISHU_WEBHOOK_SECRET"), card)
        print("代码与文档审查结果已合并发送至飞书群。")


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        raise SystemExit(str(error)) from None
