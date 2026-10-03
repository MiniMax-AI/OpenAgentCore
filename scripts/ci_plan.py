#!/usr/bin/env python3
"""Select checks from verified diffs using directory and file-suffix rules."""

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import subprocess

JOBS = ("hygiene", "distribution", "compose", "backend", "harness", "example", "web", "web-acceptance", "website", "api", "native", "lint")
NODE_JOBS = ("harness", "example", "web", "web-acceptance", "website", "native")
GO_JOBS = ("distribution", "backend", "api", "native")
# Exact file matches keep new workflows/actions conservative until classified.
CI_INPUTS = {
    ".github/workflows/check.yml": JOBS,
    ".github/workflows/release.yml": JOBS,
    ".github/workflows/api-acceptance.yml": ("api", "lint"),
    ".github/workflows/native.yml": ("native", "lint"),
    ".github/workflows/actionlint.yml": ("lint",),
    ".github/actionlint.yaml": ("lint",),
    ".github/workflows/ci-review.yml": ("lint",),
    ".github/workflows/website.yml": ("website", "lint"),
    ".github/actions/node/action.yml": (*NODE_JOBS, "lint"),
    "scripts/ci_plan.py": JOBS,
    "scripts/ci_plan_test.py": ("hygiene",),
    "scripts/ci_metrics.py": ("hygiene",),
    "scripts/ci_metrics_test.py": ("hygiene",),
}
DEPENDENCY_INPUTS = {
    **dict.fromkeys(("go.mod", "go.sum", "go.work", "go.work.sum"), GO_JOBS),
    **dict.fromkeys(("package.json", "pnpm-workspace.yaml", ".npmrc"), NODE_JOBS),
    "pnpm-lock.yaml": ("hygiene",),
    "apps/web/pnpm-lock.yaml": ("web", "web-acceptance"),
    "example/parsar/pnpm-lock.yaml": ("example",),
    "website/pnpm-lock.yaml": ("website",),
    "website/pnpm-workspace.yaml": ("website",),
    "packages/claude-sdk-adapter/pnpm-workspace.yaml": ("harness", "native", "distribution"),
    "packages/agents-client/pnpm-lock.yaml": ("web", "web-acceptance", "example"),
    "packages/agents-client/package.json": ("web", "web-acceptance", "example"),
    "packages/claude-sdk-adapter/pnpm-lock.yaml": ("harness", "native", "distribution"),
    "packages/claude-sdk-adapter/package.json": ("harness", "native", "distribution"),
    "docs.json": ("website",),
    "tsconfig.base.json": ("web", "web-acceptance", "example"),
}
# Each rule requires BOTH a path prefix and a file suffix. Rules accumulate
# across shared consumers. Exact dependency and workflow inputs are above.
GO = (".go", ".mod", ".sum", ".work")
WEB = (".ts", ".tsx", ".mts", ".js", ".jsx", ".mjs", ".cjs", ".json", ".css", ".scss", ".html", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".ico", ".woff", ".woff2")
CORE = (*GO, ".sql", ".py", ".json", ".yaml", ".yml", ".sh", ".ps1", ".txt", ".in", ".lock", "Dockerfile")
SCRIPTS = (".py", ".sh", ".mjs", ".go", ".json")
RULES = (
    (("apps/web/",), WEB, ("web", "web-acceptance")),
    (("services/web/",), (*GO, "Dockerfile"), ("distribution", "web", "web-acceptance")),
    (("example/",), WEB, ("example",)),
    (("docs/", "contracts/"), ("",), ("website",)),
    (("website/",), (*WEB, ".vue", ".md"), ("website",)),
    (("services/core/",), CORE, ("backend", "api")),
    (("services/core/internal/nativeinstaller/",), GO, ("native", "distribution")),
    (("services/core/deploy/", "services/core/tools/"), CORE, ("distribution",)),
    (("apps/daemon/",), GO, ("backend", "native")),
    (("internal/",), (*GO, ".json"), ("backend", "api", "native", "distribution")),
    (("internal/harnessconfig/",), (*GO, ".json"), ("web", "web-acceptance", "example", "harness")),
    (("contracts/",), (*GO, ".json", ".yaml", ".yml"), ("backend", "api", "native", "web", "web-acceptance", "example", "distribution")),
    (("packages/agents-client/",), (*GO, *WEB), ("backend", "api", "web", "web-acceptance", "example")),
    (("packages/claude-sdk-adapter/", "packages/mcode-harness/"), WEB, ("harness", "native", "backend", "distribution")),
    (("packages/tsconfig/",), (".json",), ("harness", "native")),
    (("deploy/install/",), (".py", ".json", ".sh"), ("distribution",)),
    (("deploy/compose/",), (".yaml", ".toml"), ("distribution", "compose")),
    (("scripts/compose-smoke.py", "deploy/install/test_compose.py"), (".py",), ("distribution", "compose")),
    (("deploy/install-release.sh", "scripts/install-release.", "scripts/publish-core-release.",
      "scripts/core-distribution-manifest.", "scripts/build-core-distribution.sh", "scripts/config-reference.py",
      "scripts/build-web.sh"), SCRIPTS, ("distribution",)),
    (("scripts/build-native-", "scripts/native-"), SCRIPTS, ("native", "backend", "distribution")),
    (("scripts/build-core.sh", "scripts/build-core-image-context.sh"), (".sh",), ("backend", "api", "distribution", "native")),
    (("deploy/distribution/",), ("Dockerfile",), ("backend", "api", "distribution", "native")),
    (("scripts/build-e2b-provider.sh",), (".sh",), ("backend", "api", "distribution")),
    (("scripts/build-claude", "scripts/check-claude", "scripts/build-mcode", "scripts/prepare-release-runtimes.sh"),
     SCRIPTS, ("harness", "native", "backend", "distribution")),
    (("scripts/build-agents-runtime.sh",), (".sh",), ("backend", "native", "distribution")),
    (("scripts/generate-harness-catalog", "scripts/harness-catalog/", "scripts/openapi-split/", "scripts/patch-agents-openapi.py",
      "scripts/extract-agents-api-upstream.py"), SCRIPTS, JOBS),
    (("scripts/check-sqlc.py", "scripts/go-test-shard.py"), (".py",), ("backend",)),
    (("scripts/check-names", "scripts/name-allowlist.json", "scripts/ci_"), (".py", ".json"), ("hygiene",)),
    ((".github/workflows/", ".github/actions/"), (".yml", ".yaml"), JOBS),
)
# Embedded files and test fixtures are executable inputs regardless of suffix.
# These narrow directories may contain Markdown prompts or extensionless data.
RESOURCE_RULES = (
    (("services/core/internal/nativeinstaller/assets/",), ("backend", "api", "native", "distribution")),
    (("services/core/",), ("backend", "api")),
    (("apps/daemon/",), ("backend", "native")),
    (("internal/",), ("backend", "api", "native", "distribution")),
    (("packages/agents-client/",), ("backend", "api", "web", "web-acceptance", "example")),
    (("packages/claude-sdk-adapter/", "packages/mcode-harness/"), ("harness", "native", "backend", "distribution")),
)
EXACT_INPUTS = {
    "services/core/internal/sandbox/testdata/node-diagnostics.json": ("web", "web-acceptance", "example"),
    "services/core/internal/sandbox/testdata/deployment-contract.json": ("distribution",),
    "services/core/internal/sandbox/e2b/testdata/configuration-selectors.json": ("distribution",),
}
FULL_INPUTS = {"Makefile", ".gitignore", ".gitattributes", ".dockerignore"}
IMAGE_FILES = {"go.mod", "go.sum", "go.work", "go.work.sum", ".github/workflows/api-acceptance.yml"}
IMAGE_INPUTS = ("scripts/build-core", "scripts/build-e2b-provider", "deploy/distribution/", "services/core/tools/e2b-provider/",
                "services/core/deploy/e2b/")
# Generated outputs retain freshness checks even when the file is documentation.
GENERATED_OUTPUTS = {"contracts/agents-api/harness-catalog.md", "contracts/agents-api/zh/harness-catalog.md", "packages/agents-client/src/harness-catalog.ts",
                     "services/core/internal/engine/catalog_generated.go", "docs/configuration.md",
                     "docs/getting-started/install-options.md"}


def full(reason):
    return {"version": 1, "jobs": list(JOBS), "image": True, "reasons": [reason]}


def select(paths):
    if not paths:
        return {"version": 1, "jobs": ["hygiene"], "image": False, "reasons": ["Verified empty diff"]}
    jobs = {"hygiene"}
    image = False
    reasons = []
    for path in paths:
        if not path or path.startswith("/") or ".." in PurePosixPath(path).parts:
            return full("Invalid path in diff")
        if path in FULL_INPUTS:
            return full(f"Shared build or CI input: {path}")
        if path in CI_INPUTS:
            matches = set(CI_INPUTS[path])
        elif path in DEPENDENCY_INPUTS:
            matches = set(DEPENDENCY_INPUTS[path])
        else:
            matches = {job for prefixes, suffixes, targets in RULES
                       if path.startswith(prefixes) and path.endswith(suffixes) for job in targets}
            matches.update(EXACT_INPUTS.get(path, ()))
            if "/testdata/" in path or "/fixtures/" in path or path.startswith("services/core/internal/nativeinstaller/assets/"):
                matches.update(job for prefixes, targets in RESOURCE_RULES if path.startswith(prefixes) for job in targets)
        if path in GENERATED_OUTPUTS:
            matches.add("distribution")
        if path in IMAGE_FILES or (matches - {"hygiene"} and path.startswith(IMAGE_INPUTS)):
            matches.add("api")
            image = True
        jobs.update(matches)
        image = image or matches == set(JOBS)
        reasons.append(f"{path}: {', '.join(sorted(matches)) or 'hygiene (no matching build/test rule)'}")
    return {"version": 1, "jobs": [job for job in JOBS if job in jobs], "image": image, "reasons": reasons}


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True).stdout


def changed_paths(base, head):
    # Disable rename detection: both the deleted path and new path affect checks.
    fields = git("diff", "--no-renames", "--name-status", "-z", base, head, "--").decode("utf-8").split("\0")
    if fields.pop() != "" or len(fields) % 2:
        raise ValueError("Malformed git diff")
    if any(status not in {"A", "D", "M", "T"} for status in fields[::2]):
        raise ValueError("Unresolved diff status")
    return fields[1::2]


def event_plan(event_name, event, requested_ref=""):
    if requested_ref:
        return full("Explicit ref: full gate")
    if event_name != "pull_request":
        return full("Manual or reusable run: full gate")
    try:
        pr = event["pull_request"]
        base, head = pr["base"]["sha"], pr["head"]["sha"]
        # Checkout tests the event's merge commit, not an arbitrary head or ref.
        # Its first-parent diff includes the integrated PR changes, with no API
        # pagination/truncation or merge-base assumptions in a shallow clone.
        commit = git("cat-file", "-p", "HEAD").decode("utf-8")
        parents = [line[7:] for line in commit.split("\n\n", 1)[0].splitlines() if line.startswith("parent ")]
        if parents != [base, head]:
            raise ValueError("Checkout does not match the PR merge parents")
        return select(changed_paths(base, "HEAD"))
    except (KeyError, TypeError, ValueError, UnicodeError, subprocess.CalledProcessError) as err:
        return full(f"Diff unavailable ({type(err).__name__}); full gate")


def validate_plan(plan):
    if not isinstance(plan, dict) or type(plan.get("version")) is not int or plan.get("version") != 1 or type(plan.get("image")) is not bool:
        raise ValueError("Invalid check plan")
    selected = plan.get("jobs")
    if not isinstance(selected, list) or any(not isinstance(j, str) for j in selected):
        raise ValueError("Invalid selected jobs")
    if len(selected) != len(set(selected)) or not set(selected) <= set(JOBS) or "hygiene" not in selected:
        raise ValueError("Invalid selected jobs")
    if plan["image"] and "api" not in selected:
        raise ValueError("Image checks require API acceptance")
    return set(selected)


def check_results(plan, needs):
    selected = validate_plan(plan)
    if set(needs) != set(JOBS) | {"plan"} or needs["plan"].get("result") != "success":
        raise ValueError("Missing jobs or unsuccessful plan")
    failed = [job for job in JOBS if needs[job].get("result") != ("success" if job in selected else "skipped")]
    if failed:
        raise ValueError("Check results do not match the plan: " + ", ".join(failed))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    plan_parser = sub.add_parser("plan")
    plan_parser.add_argument("--base")
    plan_parser.add_argument("--head", default="HEAD")
    sub.add_parser("gate")
    args = parser.parse_args()
    if args.command == "gate":
        check_results(json.loads(os.environ["PLAN"]), json.loads(os.environ["RESULTS"]))
        print("All checks selected by the plan passed.")
        return
    if args.base:
        plan = select(changed_paths(args.base, args.head))
    else:
        try:
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        except (OSError, ValueError, KeyError):
            event = {}
        plan = event_plan(os.environ.get("GITHUB_EVENT_NAME"), event, os.environ.get("REQUESTED_REF", ""))
    print(json.dumps(plan, indent=2))
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a") as f:
            f.write("plan=" + json.dumps(plan, separators=(",", ":")) + "\n")
            f.write("jobs=" + json.dumps(plan["jobs"]) + "\n")
            f.write("image=" + json.dumps(plan["image"]) + "\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as f:
            f.write("### Selected checks\n\n```json\n" + json.dumps(plan, indent=2) + "\n```\n")


if __name__ == "__main__":
    main()
