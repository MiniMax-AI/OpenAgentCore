"""Pure CI policy: affected consumers, execution prerequisites and plan shape."""

from pathlib import PurePosixPath

JOBS = ("hygiene", "distribution", "backend", "harness", "example", "web", "web-acceptance", "api", "native", "lint")
# Execution prerequisites are separate from path-to-consumer rules.
PREREQUISITES = {"web-acceptance": ("web",), "image": ("api",)}

# Rules accumulate: shared inputs exercise every declared consumer. This is the
# only authored path map; workflows consume the resulting plan.
RULES = (
    (("scripts/ci_",), ("hygiene", "lint")),
    (("apps/web/", "playwright.config.ts", ".env.example"), ("web", "web-acceptance")),
    (("services/web/",), ("distribution", "web", "web-acceptance")),
    (("example/",), ("example",)),
    (("services/core/",), ("backend", "api")),
    (("services/core/internal/sandbox/testdata/node-diagnostics.json",), ("web", "web-acceptance", "example")),
    (("services/core/internal/sandbox/testdata/deployment-contract.json",
      "services/core/internal/sandbox/e2b/testdata/configuration-selectors.json"), ("distribution",)),
    (("services/core/internal/nativeinstaller/",), ("native", "distribution")),
    (("services/core/deploy/", "services/core/tools/"), ("distribution",)),
    (("apps/daemon/",), ("backend", "native")),
    (("internal/",), ("backend", "api", "native", "distribution")),
    (("internal/harnessconfig/",), ("web", "web-acceptance", "example", "harness")),
    (("contracts/",), ("backend", "api", "native", "web", "web-acceptance", "example", "distribution")),
    (("packages/agents-client/",), ("backend", "api", "web", "web-acceptance", "example")),
    (("packages/claude-sdk-adapter/", "packages/mcode-harness/"), ("harness", "native", "backend", "distribution")),
    (("packages/tsconfig/",), ("web", "web-acceptance", "example", "harness", "native")),
    (("deploy/install/", "deploy/install-release.sh", "scripts/install-release.", "scripts/publish-core-release.",
      "scripts/core-distribution-manifest.", "scripts/build-core-distribution.sh", "scripts/config-reference.py",
      "scripts/build-web.sh"), ("distribution",)),
    (("scripts/build-native-", "scripts/native-"), ("native", "backend", "distribution")),
    (("scripts/build-core.sh", "scripts/build-core-image-context.sh", "deploy/distribution/"), ("backend", "api", "distribution", "native")),
    (("scripts/build-e2b-provider.sh",), ("backend", "api", "distribution")),
    (("scripts/build-claude", "scripts/check-claude", "scripts/build-mcode", "scripts/prepare-release-runtimes.sh"),
     ("harness", "native", "backend", "distribution")),
    (("scripts/build-agents-runtime.sh",), ("backend", "native", "distribution")),
    (("scripts/generate-harness-catalog", "scripts/harness-catalog/", "scripts/openapi-split/", "scripts/patch-agents-openapi.py",
      "scripts/extract-agents-api-upstream.py"), tuple(job for job in JOBS if job != "lint")),
    (("scripts/check-sqlc.py",), ("backend",)),
    (("scripts/check-names", "scripts/name-allowlist.json"), ("hygiene",)),
)
# Exact root and workflow inputs are never inferred from a filename prefix.
ROOT_RULES = {
    **{path: ("backend", "api", "native", "distribution") for path in ("go.mod", "go.sum", "go.work", "go.work.sum")},
    **{path: ("web", "web-acceptance", "example", "harness", "native") for path in
       ("package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "tsconfig.base.json", ".npmrc")},
    "Makefile": tuple(job for job in JOBS if job != "lint") + ("image",),
    ".gitignore": ("hygiene",),
    ".gitattributes": ("backend", "distribution", "native", "web", "example", "harness"),
    ".dockerignore": ("image", "distribution"),
    ".github/workflows/check.yml": ("hygiene", "lint"),
    ".github/workflows/release.yml": ("hygiene", "lint"),
    ".github/workflows/actionlint.yml": ("lint",),
    ".github/workflows/api-acceptance.yml": ("api", "image", "lint"),
    ".github/workflows/native.yml": ("native", "lint"),
    ".github/actions/node/action.yml": ("web", "web-acceptance", "example", "harness", "native", "lint"),
    **{f".github/workflows/ci-{job}.yml": (job, "lint") for job in
       ("backend", "distribution", "harness", "example", "web", "web-acceptance")},
}
RULES += ((("scripts/build-core", "scripts/build-e2b-provider", "deploy/distribution/",
            "services/core/tools/e2b-provider/", "services/core/deploy/e2b/"), ("image",)),)
# Generated outputs retain freshness checks even when the file is documentation.
GENERATED_OUTPUTS = {"contracts/agents-api/harness-catalog.md", "packages/agents-client/src/harness-catalog.ts",
                     "services/core/internal/engine/catalog_generated.go", "docs/configuration.md",
                     "docs/getting-started/install-options.md"}


def documentation(path):
    p = PurePosixPath(path)
    if p.name in {"README.md", "README.zh-CN.md", "AGENTS.md", "CONTRIBUTING.md", "LICENSE"} and (len(p.parts) == 1 or path.startswith(("docs/", "contracts/", "apps/", "services/", "internal/", "packages/", "deploy/", "example/"))):
        return True
    return (path.startswith(("docs/", "contracts/")) and p.suffix in {".md", ".png", ".jpg", ".jpeg", ".svg", ".webp"}) or path in {
        "apps/web/PRODUCT.md", "apps/web/DESIGN.md", "services/core/IMPLEMENTATION.md"}


def expand(checks):
    selected = set(checks)
    if not selected <= set(JOBS) | {"image"}:
        raise ValueError("Unknown CI check")
    pending = list(selected)
    while pending:
        for dependency in PREREQUISITES.get(pending.pop(), ()):
            if dependency not in selected:
                selected.add(dependency)
                pending.append(dependency)
    return selected


def make_plan(checks, reasons):
    selected = expand({"hygiene", *checks})
    return {"version": 1, "jobs": [job for job in JOBS if job in selected], "image": "image" in selected, "reasons": reasons}


def full(reason):
    return make_plan((*JOBS, "image"), [reason])


def select(paths):
    checks, reasons = set(), []
    for path in paths:
        if not path or path.startswith("/") or ".." in PurePosixPath(path).parts:
            raise ValueError("Invalid path in diff")
        matches = set(ROOT_RULES.get(path, ()))
        if documentation(path):
            matches.add("hygiene")
        else:
            matches.update(job for prefixes, targets in RULES if path.startswith(prefixes) for job in targets)
        if path in GENERATED_OUTPUTS:
            matches.add("distribution")
        if not matches:
            raise ValueError(f"Unclassified input: {path}; add its consumers to scripts/ci_policy.py")
        checks.update(matches)
        reasons.append(f"{path}: {', '.join(sorted(matches))}")
    return make_plan(checks, reasons or ["Empty diff: repository integrity only"])
