#!/usr/bin/env python3
"""Reject retired identifiers in tracked text, with reasoned span-level exceptions."""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import fnmatch
import json
from pathlib import Path
import re
import subprocess
import sys

# These spellings are detection inputs, not supported compatibility aliases.
FORBIDDEN = re.compile(
    r"(?i:parsar)|\bAGENTS_CORE_WEB_[A-Z][A-Z0-9_]*|\bAGENTS_API_[A-Z][A-Z0-9_]*|\bCORE_CONSOLE_[A-Z][A-Z0-9_]*"
    r"|\bagents-api(?:-(?:migrate|device|environment-key|e2b-provider|microsandbox-provider"
    r"|tool-root|codex-directory|codex-write|workspace-export|runtime-initialize|claude-shell-prefix))?\b"
    r"|\bcore-console\b|\bagents-runtime-|(?i:\bAgents? Core(?: Web)?\b)|@agents-core-web/",
)


@dataclass(frozen=True)
class ExceptionRule:
    path: str
    regex: re.Pattern[str]
    reason: str


def load_rules(path: Path) -> list[ExceptionRule]:
    entries = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(entries, list):
        raise ValueError("allowlist must be a list")
    rules = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict) or set(entry) != {"path", "regex", "reason"}:
            raise ValueError(f"allowlist entry {index} needs path, regex and reason")
        if any(not isinstance(entry[key], str) or not entry[key].strip() for key in entry):
            raise ValueError(f"allowlist entry {index} has an empty path, regex or reason")
        regex = re.compile(entry["regex"])
        if regex.search("") is not None:
            raise ValueError(f"allowlist entry {index} matches empty text")
        rules.append(ExceptionRule(entry["path"], regex, entry["reason"]))
    return rules


def violations(path: str, content: str, rules: list[ExceptionRule]) -> list[tuple[int, int, str]]:
    allowed = [match.span() for rule in rules if fnmatch.fnmatchcase(path, rule.path)
               for match in rule.regex.finditer(content)]
    result = []
    for match in FORBIDDEN.finditer(content):
        if any(start <= match.start() and match.end() <= end for start, end in allowed):
            continue
        line = content.count("\n", 0, match.start()) + 1
        column = match.start() - content.rfind("\n", 0, match.start())
        result.append((line, column, match.group()))
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--allowlist", type=Path, default=Path(__file__).with_name("name-allowlist.json"))
    args = parser.parse_args()
    try:
        rules = load_rules(args.allowlist)
        names = subprocess.check_output(["git", "-C", str(args.root), "ls-files", "-z"]).split(b"\0")
        failures = []
        for raw in names:
            if not raw:
                continue
            name = raw.decode("utf-8")
            path = args.root / name
            # Never follow a tracked symlink into files outside this repository.
            if path.is_symlink() or not path.is_file():
                continue
            data = path.read_bytes()
            if b"\0" in data:
                continue
            content = data.decode("utf-8")
            failures.extend((name, *item) for item in violations(name, content, rules))
    except (OSError, ValueError, re.error, subprocess.CalledProcessError) as error:
        print(f"Name guard failed: {error}", file=sys.stderr)
        return 2
    for name, line, column, token in failures:
        print(f"{name}:{line}:{column}: retired identifier {token!r}")
    if failures:
        print(f"Name guard found {len(failures)} unapproved identifiers.", file=sys.stderr)
        return 1
    print("OpenAgentCore name guard passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
