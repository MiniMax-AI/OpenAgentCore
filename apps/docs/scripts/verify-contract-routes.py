#!/usr/bin/env python3
"""Check the generated OpenAPI contracts against the routes the code registers.

The public contract is constrained by its pinned upstream baseline; Core and
machine contracts are generated from handler annotations. This reads the real
`chi` route table from services/core/internal/api and compares all three
contracts with the registered paths.

Two checks:
  [A] routes the code registers that no contract publishes
  [B] paths a contract publishes that the code never registers  <- the real bug

Scope resolution is brace-matched rather than line-based, because chi nests
`Route(prefix, func(r chi.Router) { ... })` and the same variable name is reused
at each level.

Read-only. Usage (from apps/docs):
    node scripts/check-python.mjs routes
"""

from __future__ import annotations

import json
import pathlib
import re
import sys


APP = pathlib.Path(__file__).resolve().parent.parent
REPO = APP.parent.parent
API = REPO / "services/core/internal/api"


VERBS = ("Get", "Post", "Put", "Patch", "Delete", "Head", "Options", "Trace")
ROUTE = re.compile(r"\b(\w+)\.(%s)\(\s*([^,)]+?)\s*[,)]" % "|".join(VERBS))
METHOD = re.compile(r"\b(\w+)\.Method\(\s*(\"[^\"]+\"|[\w.]+)\s*,\s*(\"[A-Za-z]+\"|http\.Method[A-Za-z]+)")
BLOCK = re.compile(r"\b(\w+)\.(Route|Group)\(\s*(?:\"([^\"]*)\"\s*,\s*)?func\(\s*(\w+)\s+chi\.Router")
CALL = re.compile(r"\bh\.(register\w+Routes)\(\s*(\w+)\s*\)")
FUNC = re.compile(r"^func\s+(?:\([^)]*\)\s+)?(register\w+Routes)\(\s*(\w+)\s+chi\.Router", re.M)
CONST = re.compile(r"\bconst\s+(\w+)\s*=\s*\"([^\"]*)\"")

IGNORED = {("/healthz", "GET"): "liveness probe, not part of the Agent API"}

# Differences between the contract and the registered route that the reference
# accepts on purpose, keyed (path, method) as the contract spells them.
ACCEPTED = {}

# A HEAD route wired to `methodNotAllowed` is an explicit 405 guard, not an API.
GUARD = re.compile(r"\.(Head|Options|Get|Post)\(\s*([^,]+),\s*methodNotAllowed\s*\)")


def brace_end(text: str, start: int) -> int:
    depth = 0
    for index in range(start, len(text)):
        if text[index] == "{":
            depth += 1
        elif text[index] == "}":
            depth -= 1
            if depth == 0:
                return index
    return len(text)


class Region:
    def __init__(self, text: str):
        self.text = text
        self.consts = {name: value for name, value in CONST.findall(text)}
        self.blocks = []
        for match in BLOCK.finditer(text):
            brace = text.find("{", match.end())
            if brace < 0:
                continue
            self.blocks.append(
                {
                    "outer": match.group(1),
                    "inner": match.group(4),
                    "prefix": match.group(3) or "",
                    "own": match.group(2) == "Route",
                    "start": match.start(),
                    "open": brace,
                    "end": brace_end(text, brace),
                }
            )

    def _enclosing(self, position: int):
        inside = [b for b in self.blocks if b["open"] < position < b["end"]]
        inside.sort(key=lambda b: b["open"])
        return inside

    def prefix_of(self, var: str, position: int, seed: dict[str, str]) -> str | None:
        """Prefix for a router variable at a position, or None if unknown."""
        for block in reversed(self._enclosing(position)):
            if block["inner"] == var:
                outer = self.prefix_of(block["outer"], block["start"], seed)
                if outer is None:
                    return None
                return outer + block["prefix"] if block["own"] else outer
        return seed.get(var)

    def literal(self, expr: str) -> str | None:
        parts = re.split(r"\s*\+\s*", expr.strip())
        out = ""
        for part in parts:
            part = part.strip()
            if len(part) >= 2 and part[0] == '"' and part[-1] == '"':
                out += part[1:-1]
            elif part in self.consts:
                out += self.consts[part]
            else:
                return None
        return out or None

    def routes(self, seed: dict[str, str]):
        found: list[tuple[str, str]] = []
        for match in ROUTE.finditer(self.text):
            var, verb, expr = match.group(1), match.group(2), match.group(3)
            path = self.literal(expr)
            if path is None or not path.startswith("/"):
                continue
            prefix = self.prefix_of(var, match.start(), seed)
            if prefix is None:
                continue
            found.append((prefix + path, verb.upper()))
        for match in METHOD.finditer(self.text):
            var, target, verb = match.group(1), match.group(2), match.group(3)
            path = self.literal(target)
            if path is None or not path.startswith("/"):
                continue
            prefix = self.prefix_of(var, match.start(), seed)
            if prefix is None:
                continue
            method = verb.split(".")[-1] if verb.startswith("http.") else verb.strip('"')
            found.append((prefix + path, method.upper()))
        return found

    def calls(self, seed: dict[str, str]):
        out = []
        for match in CALL.finditer(self.text):
            prefix = self.prefix_of(match.group(2), match.start(), seed)
            out.append((match.group(1), prefix if prefix is not None else ""))
        return out


def main() -> int:
    files = [f for f in sorted(API.glob("*.go")) if not f.name.endswith("_test.go")]
    texts = {f.name: f.read_text(encoding="utf-8").replace("\r\n", "\n") for f in files}

    helpers = {}
    for name, text in texts.items():
        for match in FUNC.finditer(text):
            brace = text.find("{", match.end())
            helpers[match.group(1)] = (name, match.group(2), brace, brace_end(text, brace))

    # Pass 1: everything except helper bodies.
    code: dict[tuple[str, str], str] = {}
    calls: list[tuple[str, str]] = []
    for name, text in texts.items():
        masked = list(text)
        for fn, (hfile, _, brace, end) in helpers.items():
            if hfile != name:
                continue
            for index in range(brace, min(end + 1, len(masked))):
                if masked[index] != "\n":
                    masked[index] = " "
        region = Region("".join(masked))
        root = {"router": "", "r": ""}
        for path, method in region.routes(root):
            code.setdefault((path, method), name)
        calls.extend(region.calls(root))

    # Follow nested registration calls with the actual prefix from their caller.
    seen = set()
    while calls:
        fn, prefix = calls.pop()
        if (fn, prefix) in seen:
            continue
        seen.add((fn, prefix))
        if fn not in helpers:
            raise ValueError("Unknown route registration helper: " + fn)
        name, param, brace, end = helpers[fn]
        region = Region(texts[name][brace : end + 1])
        for route, method in region.routes({param: prefix}):
            code.setdefault((route, method), name)
        calls.extend(region.calls({param: prefix}))

    documented: dict[tuple[str, str], str] = {}
    # YAML is decoded by the declared Node dependency; Python uses only stdlib.
    for contract in json.load(sys.stdin):
        document, label = contract["document"], contract["label"]
        base = (document.get("basePath") or "").rstrip("/")
        for route, item in (document.get("paths") or {}).items():
            for method in item:
                if method in ("get", "post", "put", "patch", "delete", "head", "options"):
                    documented.setdefault(((base + route) or "/", method.upper()), label)

    guard_paths = set()
    for name, text in texts.items():
        for match in GUARD.finditer(text):
            guard_paths.add(match.group(2).strip().strip('"'))

    unpublished = sorted(k for k in code if k not in documented)
    missing = sorted(k for k in documented if k not in code)

    print("  Registered (method, path): %d" % len(code))
    print("  Contract operations:          %d" % len(documented))
    print()
    guards = [k for k in unpublished if k[1] == "HEAD" and any(k[0].endswith(suffix) for suffix in guard_paths)]
    real = [k for k in unpublished if k not in guards]
    undocumented = [k for k in real if k not in IGNORED]
    print("  [A] Registered but not published: %d  (%d explicit HEAD guards)" % (len(unpublished), len(guards)))
    for path, method in real:
        note = IGNORED.get((path, method), "")
        print("      %-6s %-72s %s%s" % (method, path, code[(path, method)], ("  <- " + note) if note else ""))
    print()
    accepted = [k for k in missing if k in ACCEPTED]
    real = [k for k in missing if k not in ACCEPTED]

    print("  [B] Published but not registered: %d  (%d accepted differences)" % (len(missing), len(accepted)))
    for path, method in real:
        print("      %-6s %-72s [%s]" % (method, path, documented[(path, method)]))
    for path, method in accepted:
        print("      ok     %-72s %s" % (path, ACCEPTED[(path, method)]))
    return 1 if real or undocumented else 0


if __name__ == "__main__":
    sys.exit(main())
