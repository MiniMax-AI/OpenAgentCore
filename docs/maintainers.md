---
title: "Build and release OpenAgentCore"
---

This guide is for maintainers who build and publish OpenAgentCore. To install Core and Web, use the [installation guide](./getting-started/install.md). The rules the installer code follows are in [Deployment](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md) and [Node installer](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/node/README.md); required checks are in [CONTRIBUTING](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#required-checks).

## Build a distribution

A distribution is the matched set of Linux amd64 release assets built from one commit: the control archive (the installer, the `oac` command, and the Core, Web, ingress and PostgreSQL images), the Runtime image and node artifacts as separate files, and the native installers.

Build on Linux x86_64 with a glibc compatible with Debian 12, Docker, the Go version in `go.mod`, a C compiler (the microsandbox helper is a CGO build), Node, pnpm, Python 3.9 or newer, curl, tar, pigz and sha256sum. The source must be clean and committed. First prepare the pinned Codex package and MiniMax Code companion, then build:

```sh
bash scripts/prepare-release-runtimes.sh
inputs="$HOME/.oac/build/release-inputs/inputs.json"
export AGENTS_RUNTIME_CODEX_PACKAGE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["codex"])' "$inputs")"
export MCODE_HARNESS_BUILD_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mcode"])' "$inputs")"
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://github.com/MiniMax-AI/OpenAgentCore/releases/download/v1.2.3
make build-core-distribution
```

`prepare-release-runtimes.sh` refuses an existing `~/.oac/build/release-inputs`; use a fresh build host.

| Variable | Effect |
| --- | --- |
| `CORE_DISTRIBUTION_RELEASE_BASE_URL` | Versioned HTTPS directory that will serve the generated asset file names (never `latest`). Required unless `CORE_DISTRIBUTION_OFFLINE=1` |
| `CORE_DISTRIBUTION_OFFLINE` | `1` also builds the offline archive |
| `AGENTS_RUNTIME_CODEX_PACKAGE`, `MCODE_HARNESS_BUILD_DIR` | Pinned Runtime inputs from `prepare-release-runtimes.sh` |
| `CORE_DISTRIBUTION_CODEX_IMAGE`, `CORE_DISTRIBUTION_CLAUDE_IMAGE`, `CORE_DISTRIBUTION_MCODE_IMAGE` | Use existing Harness images, given as immutable `sha256:` image IDs, instead of building them; set all three or none. Each must contain the daemon built from this commit |
| `OAC_NATIVE_INSTALLER_BUILD_DIR` | Native installer catalog directory; see [Native installers](#native-installers) |
| `CORE_DISTRIBUTION_BUILD_DIR` | Output directory under `~/.oac`. Default: `~/.oac/build/core-distribution` |
| `CORE_DISTRIBUTION_BUILD_NETWORK` | Docker build network: `default`, `host` or `none` |
| `CORE_DISTRIBUTION_MICROSANDBOX_ARCHIVE` | Cached microsandbox release archive. Default: `~/.oac/cache/microsandbox-v0.7.2-linux-x86_64.tar.gz`, downloaded when missing |
| `CORE_DISTRIBUTION_DATABASE_IMAGE` | PostgreSQL 16 image; the default is pinned by its linux/amd64 manifest digest |

The build reuses the Core, Web, Runtime, SDK and helper builders. The manifest records the commit and source tree, image config and OCI manifest digests, the Runtime OCI manifest digest, the microsandbox runtime and firmware hashes, and the size and SHA-256 of every Runtime and node artifact; native installers carry only their SHA-256 in the [catalog](#native-installers). Output is the control archive and its `.sha256`, the optional offline archive, and the versioned Runtime, node and native installer assets. Nothing is published. Rebuilding into a directory that already holds this commit's distribution is refused.

The ingress image carries only `oac` and four node metadata files: `manifest.json`, `SHA256SUMS`, `node-install.pyz` and `runtime/seccomp.json`. The build prepares their manifest from the same Runtime and artifact identities as the distribution, then verifies initialization with networking disabled. The node manifest omits control-plane image identities so it can be packaged before the ingress image itself exists.

The control archive carries no Runtime image or node execution artifacts; the offline archive carries them. The [download contract](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/node/README.md#download-contract) describes how nodes obtain them.

A distribution carries the docs listed in `BUNDLED_DOCS` in `scripts/core-distribution-manifest.py`. Links between bundled docs stay relative; every other relative link is rewritten to the same file on GitHub at the bundle's commit. The build fails when a link or anchor does not resolve, and `make check-distribution` runs the same check on every tracked Markdown file outside `example/`. Update the list when you add or move a doc that the installer or its output refers to.

### Native installers

Self-hosted machines install `oac-daemon` from per-platform native installers: Linux amd64, macOS arm64 and Windows amd64. Each is built on its own OS by the `native-check` workflow (`scripts/build-native-installer.mjs`, whose `pins` object fixes the Node.js and Harness versions) and verified on every selected native check. Manual packaging runs and release checks upload `oac-native-installer-<OS>-<ARCH>.tar.gz` for seven days; ordinary PR and main checks do not upload successful packages. For a local distribution, download the three artifacts from a `native-check` run on that exact commit (a manual run or the release run; pull-request runs build the merge commit and do not match), then assemble the catalog from that checkout:

```sh
node scripts/build-native-catalog.mjs INPUT_DIR OUTPUT_DIR
export OAC_NATIVE_INSTALLER_BUILD_DIR=OUTPUT_DIR
```

The catalog records the commit, the Runtime protocol version, each archive's SHA-256 and, with a release base, its versioned URL. The control archive and Core image carry only `native-installers/catalog.json`; the archives become separate `oac-native-<commit>-<platform>.tar.gz` release assets, and the offline archive holds one copy of each outside the Core image. Without a catalog, Sessions report the install command as unavailable, and the release workflow refuses to publish.

### Runtime images and helpers

`make build-core-distribution` builds all of these. Build one on its own to test a Harness image or a helper. Run every command from the repository root; default outputs go under `${OAC_DEV_HOME:-$HOME/.oac}/build`.

**Codex Runtime image.** Extract the official npm package `@openai/codex@0.153.4-linux-x64` under `~/.oac` (for example with `npm pack --ignore-scripts` and `tar -xzf`), then:

```sh
export AGENTS_RUNTIME_CODEX_PACKAGE=/absolute/path/to/package
make build-agents-runtime
docker build --platform linux/amd64 -t oac-runtime:codex "${OAC_DEV_HOME:-$HOME/.oac}/build/agents-runtime"
```

The script checks the package version, builds `oac-daemon` for Linux amd64 and prepares a context with only the daemon, the unmodified native executable, its resources and `services/core/deploy/codex/Dockerfile`.

**Claude Code Runtime image.** Node 20 or newer and pnpm are required.

```sh
make build-claude-sdk-runtime
make build-claude-runtime
docker build --platform linux/amd64 -t oac-runtime:claude "${OAC_DEV_HOME:-$HOME/.oac}/build/claude-runtime"
```

The first step exports the adapter with the pinned Claude Agent SDK (`packages/claude-sdk-adapter/package.json`) as a checksummed archive for the host platform; the second verifies it and adds the daemon. The image step needs the `linux-x64-glibc` archive, so build both on Linux x86_64 with glibc. Keep the exported archive unchanged.

**MiniMax Code Runtime image.** Build the companion from a checkout of the revision pinned in `packages/mcode-harness/source.json`, with the `@minimax-ai/code` npm package of the same version for native dependencies. The companion build runs on Linux x86_64 or macOS arm64 into a new directory; for the Linux Runtime image, build it on Linux x86_64 (macOS arm64 serves only the native installer):

```sh
MCODE_NATIVE_SOURCE=/absolute/minimax-code \
MCODE_CLI_DIR=/absolute/node_modules/@minimax-ai/code \
MCODE_HARNESS_BUILD_DIR=/absolute/mcode-harness bash scripts/build-mcode-harness.sh
MCODE_HARNESS_BUILD_DIR=/absolute/mcode-harness bash scripts/build-mcode-runtime.sh
docker build --platform linux/amd64 -t oac-runtime:mcode "${OAC_DEV_HOME:-$HOME/.oac}/build/mcode-runtime"
```

`scripts/prepare-release-runtimes.sh` runs the companion build from the pins.

The distribution combines the three Harness images into one Runtime image (`deploy/distribution/Runtime.Dockerfile`): the MiniMax Code image, which carries the daemon, with the Codex executable and resources and the Claude SDK bundle copied in. It verifies that each image carries the daemon built from the same commit.

**E2B helper.**

```sh
make build-e2b-provider
```

Docker builds the Linux amd64 helper with the pinned CPython and Debian 12 image. The Python dependency closure, including PyInstaller, is hash-locked in `services/core/tools/e2b-provider/requirements.lock`; no E2B account key is needed. Set `E2B_PROVIDER_BUILD_DIR` for another output directory. The build is a pure function of the helper sources, `LICENSE` and the build script, so it is cached under `~/.oac/cache/e2b-provider/` by their hash and rebuilt only when they change. The output is `oac-e2b-provider-linux-amd64.tar.gz` with its `.sha256`; it extracts to `oac-e2b-provider/` with the executable, `_internal/`, `licenses/`, `requirements.lock` and `manifest.json`. The Core image uses that tree; the host needs a compatible glibc and CA certificates, not Python.

**microsandbox helper.** Linux only, with a C compiler:

```sh
make build-microsandbox-provider
make check-microsandbox-provider
```

The helper is written to `~/.oac/build/microsandbox-provider/oac-microsandbox-provider`. Its separate Go module pins the microsandbox Go SDK v0.7.2 and embeds the matching FFI library; never build production with the SDK's `microsandbox_ffi_path` tag. Core itself stays a CGO-disabled build. The helper needs glibc and runs only on nodes.

**microsandbox runtime.** The distribution uses the official [v0.7.2 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2) archive `microsandbox-linux-x86_64.tar.gz`, SHA256 `47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b` (`RUNTIME_ARCHIVE_SHA256` in `scripts/core-distribution-manifest.py`). The build verifies the checksum before extracting `msb` and `libkrunfw.so.5.6.1` and records both files' hashes. The helper checks those hashes on every call and never installs or upgrades them.

### Standalone Core builds

`make build-core` builds `oac-core`, `oac-core-device`, `oac-core-environment-key` and `oac-node` into `${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core` (`OAC_DEV_CORE_BUILD_DIR` selects another absolute directory). The build copies only the source set listed in `scripts/build-core.sh` (the Core service, its contracts, the shared packages it needs and the root Go module files) into a temporary context and builds with CGO disabled, read-only modules and trimmed paths. It needs no Node, Docker or other application. When Core gains a shared dependency, add that package to the list; never copy the whole repository to make it compile.

`make docker-build-core` builds the image `oac-core:dev` (`OAC_DEV_CORE_IMAGE` selects another name) from those five commands and the E2B helper. The base is the digest-pinned `debian:bookworm-slim` with CA certificates and the glibc runtime the helper needs; the default user is UID/GID 65532 and Core listens on `:8091`. The image is Linux amd64 only and is not pushed to a registry. Changes to the image or its build need `make check-core-container` in addition to the relevant source checks: it runs the official-client suite against the image with a read-only root filesystem and needs Linux Docker, a non-root user, and the [test database and pinned SDK](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#official-client-verification) of the service checks (`OAC_TEST_DATABASE_URL` naming an `oac_*_tests` database with the migrations applied, and `OAC_TEST_OFFICIAL_SDK_PYTHON`).

## Publish a version

Push a version tag on the reviewed commit to run the `core-release` workflow:

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

Tags use `vMAJOR.MINOR.PATCH`, optionally with a prerelease suffix such as `-rc.1` and build metadata such as `+build.1`. A prerelease suffix creates a GitHub prerelease. Pushing the tag is the release decision. Automated checks establish build and test results, not real-model qualification: assess live execution evidence before you push the tag. Model credentials and private certificate authorities never enter CI or release inputs, including acceptance images that contain them.

The workflow runs `check` on the tagged commit, including the full local gate, official-client and image acceptance, and the native matrix with its packaging artifacts enabled. After checks succeed, one `build` job on `blacksmith-4vcpu-ubuntu-2204` prepares the pinned Runtime inputs, reuses the native installers, builds the distribution and publishes directly from its local files. This combined job has `contents: write` and `packages: write`; checkout does not persist credentials. It retains the assets as an uncompressed Actions artifact only when publication fails, for recovery, or when a manual build does not publish.

Distribution and Runtime archives use `pigz` level 6 with at most four compression workers and no filename or timestamp in the gzip header. The publisher verifies archive and native installer checksums, resolves the repository identity, refuses an existing Release or draft for the tag and creates one draft with a fixed ID. Up to four assets upload concurrently, largest first, without retries. After confirming the complete remote inventory, the publisher validates all image archives and existing registry tags before pushing up to four images concurrently. Each image config and registry manifest is verified; any error leaves the Release unpublished. In-flight transfers finish before a failed operation returns. The publisher rechecks the version tag before publishing the draft by its ID.

### Container registry

Version releases and manual `build-<full SHA>` drafts publish Linux amd64 images as `ghcr.io/minimax-ai/openagentcore/<component>:<version>`, where `<component>` is `core`, `web`, `runtime` or `ingress`. For example, `ghcr.io/minimax-ai/openagentcore/core:v1.2.3`. A draft uses the tag `build-<full SHA>`. PostgreSQL uses its upstream image and is not republished. The registry images are loaded from the release archives without rebuilding. Existing version tags are reused only when their image config digest matches the release; a different image stops publication. A stable release also moves each component's `latest` tag to that image. Prereleases and drafts leave `latest` unchanged. SemVer build metadata uses `_` in place of `+` in container tags; version strings longer than 128 characters cannot be published to GHCR. After the images are verified, the publisher uploads the single `compose.yaml` and its checksum list, rendered for that release. Compose pins the ingress image by its registry digest; initialization rejects an image whose build revision differs from the Compose revision. A draft Release stays unpublished.

The combined build/publication job uses `GITHUB_TOKEN` with `packages: write`. On the first publication, GitHub creates each container package as private: a package administrator must change all four packages to **Public** in their package settings before users can pull anonymously. See [GitHub container visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry). Verify an unauthenticated pull after changing visibility. Repository visibility alone does not make a new container package public.

GHCR and GitHub Releases do not share a transaction. A failed release may leave some matching version tags in GHCR; preserve those images and follow the draft recovery procedure below using the original artifacts. Registry failures other than a missing manifest stop publication. The job summary records digest-pinned references. These images and the rendered Compose files still require the configuration, secrets and routing described in [Configuration](./configuration.md).

`install.sh` downloads the Compose files for the latest stable release, or the release named by `--version`, verifies their SHA-256 and starts that release. The [installation guide](./getting-started/install.md#install) covers its use.

Go check and build jobs share Go module and compiler-cache directories under `~/.oac/cache/`, keyed by runner OS and architecture, all Go module files, the check/build partition and the commit. Partitioned keys prevent concurrent jobs from saving different compiler subsets under one key. Release builds can seed their cache from backend checks as well as earlier release builds. An older cache only seeds downloads and compilation; every check still runs. Release jobs also cache npm package downloads and the pinned microsandbox archive, whose checksum is verified on every build. Actions cache visibility follows GitHub ref scoping; a tag-specific cache is not shared with other release tags. New keys are saved only after a successful job.

Never move a release tag or overwrite published assets. If publication fails, inspect the Release first: publication may have completed despite a lost response. Leave a complete published Release as it is. For an incomplete draft, delete that draft only after inspection, download the original `core-release-<revision>` Actions artifact with `gh run download RUN_ID --name core-release-REVISION --dir ASSET_DIRECTORY`, and use a checkout of that exact source revision to run `python3 scripts/publish-core-release.py --assets ASSET_DIRECTORY`. Set `GH_REPO`, `GH_TOKEN`, `RELEASE_REVISION`, `RELEASE_TAG` and `RELEASE_MODE` to the original publication inputs and sign Docker into GHCR. The script revalidates the assets and refuses existing releases. Do not rerun the combined build job or recreate the tag to recover a failed upload.

### Build a candidate without publishing

A manual run takes a full commit SHA, runs the same checks and builds, defaults to the offline archive, and leaves the Release unpublished:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/OpenAgentCore --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

With `draft_release=true` the result is an unpublished `build-<full SHA>` draft Release whose images are pushed under that tag; with `draft_release=false` the files stay in the Actions artifact. Use the exact matched asset set; never mix builds or resolve components through `latest`.

## Continuous integration

Every PR runs `core-check` and reports the required status `check`. Main uses GitHub branch protection requiring this check and an up-to-date branch before merging, so merging does not start another copy of the test suite. Changes must enter through checked PRs; an administrator bypass does not establish CI success. Main pushes publish the website when its inputs change and run `cache-warm`, which builds the MiniMax companion, the E2B helper and the pnpm store without running tests, because only caches saved on main can be restored by every PR and release tag. Version tags and manual release builds run the full release gate at their exact source commit. `scripts/ci_plan.py` owns the only input-to-check map. Component rules require both a matching directory or script prefix and a matching file suffix; exact dependency, workflow and shared build inputs have explicit rules. Rules accumulate across shared consumers and mixed changes. Paths with no matching build/test rule run hygiene only. Add the corresponding rule when introducing a new component, language, build input or resource location.

The planner compares the PR event's tested merge commit with its verified first parent. NUL-delimited Git output and disabled rename detection retain both old and new paths. The plan and reasons appear in the run summary. Missing or inconsistent history, mismatched checkouts, invalid paths, planner/orchestration changes and shared build inputs select the full gate. A verified empty diff selects hygiene only. Release, manual and explicit-ref calls always select every group.

| Group | Checks and consumers |
| --- | --- |
| `hygiene` | Names, repository links, bundled documentation integrity, and CI planner/gate tests; runs for every change |
| `distribution` | Harness catalog and installer schema, install/apply/recovery/cleanup tests, Compose parsing and initialization fixtures, release/download and bundle contracts, Go console tests and build; needs Docker Compose for template parsing, no pnpm install or browser |
| `compose` | Real startup from empty volumes using images built from the checkout, sign-in and API access, file upload and node installer download, then URL reconfiguration and container recreation with preserved credentials and data; needs Docker, Go and network access, no model credentials |
| `backend` | Parallel parts, each with a dedicated PostgreSQL guard: `runtime` (sqlc freshness, Runtime/shared Go tests, Linux microsandbox helper, daemon build), `core` (standalone Core build, Core service and client tests) and three `store` shards of the serial Core persistence integration package |
| `harness` | Claude SDK tests and packaging, MiniMax companion scripts |
| `example` | Optional application typecheck, tests, build and isolated browser acceptance |
| `web` | TypeScript, Web/client tests and Web build |
| `website` | Website build and output checks for website, published documentation and dependency changes |
| `web-acceptance` | Full Web browser suite in four isolated shards after Web unit/build success; each keeps one worker |
| `api` | Reusable official-client acceptance against standalone commands and migrations; image acceptance when image/build/helper inputs change, and in every full gate |
| `native` | Reusable Linux, macOS and Windows builds, filesystem/process/Harness checks and native installation; all three platforms can run concurrently |
| `lint` | Reusable actionlint check, including local composite actions |

`.github/actionlint.yaml` selects hygiene and lint. Known workflow changes select their consumers: the CI review and actionlint workflows run hygiene and lint; native workflow changes add native checks; API acceptance workflow changes add API checks with container acceptance enabled; website workflow changes add website checks. The shared Node action selects every job that uses it plus lint. A new or unclassified workflow/action selects the full gate until its consumers are declared in the planner. Planner tests and CI measurement scripts run hygiene; changing the planner itself runs the full gate.

Compose template and Compose test changes select both `distribution` fixtures and the `compose` smoke job; Core, Web, shared Go packages and the image Dockerfiles also select the smoke job. Run `python3 scripts/compose-smoke.py` locally with Docker available to repeat it. The script uses a unique project, an automatically assigned loopback port and artifacts under `~/.oac/tests/`; it removes its containers and volumes on exit. CI also performs cleanup after a failed or interrupted smoke step. Diagnostics show container status without printing HTTP response bodies or sign-in keys. Core, Web and the ingress image are built from the checkout; Web serves a placeholder page instead of the console build. Build-time node metadata comes from the release pinned in `deploy/compose/smoke-pins.json`; the initialization container runs with networking disabled. This checks generic Compose behavior; it does not run a Dokploy/Coolify instance or execute a model.

Go module and workspace inputs select backend, API (including the container), native and distribution checks. Each Node module owns its manifest and lockfile. Website dependencies select website checks; Web dependencies select Web and browser checks; example dependencies select example checks; shared TypeScript client dependencies select Web, browser and example checks; Claude adapter dependencies select Harness, native and distribution checks. Shared package-manager configuration selects all Node consumers. The root TypeScript configuration selects Web and example checks; the adapter TypeScript configuration selects Harness and native checks. Each selected set includes hygiene. Mixed changes accumulate their consumers, and every job reads the same plan instead of maintaining its own path list. For example, a notification-only PR skips database, browser and native jobs, while a notification plus Core change adds backend and API checks.

Published documentation and assets under `docs/` and `contracts/`, plus `docs.json`, select hygiene and website. Other ordinary Markdown, including documentation inside source directories, selects hygiene only. Generated catalog files and configuration reference sections retain their distribution freshness checks. Core `.go`, `.sql`, helper scripts and configuration inputs select backend/API checks; Web source, styles and assets select Web checks. Embedded native assets and declared test fixture directories select their consumers regardless of suffix, including Markdown prompts and extensionless data. Installer changes add distribution checks. Web changes add Web checks and all browser shards; Core/DB changes add backend and official-client acceptance. Shared contracts, SDKs, Runtime inputs and dependencies propagate to their consumers according to the planner. Generated catalog and protocol inputs include the installer, client and UI consumers. Do not duplicate path lists in reusable workflows or put a `paths` filter on the required workflow.

The final `check` runs even when planning or a dependency fails. It requires a successful, valid plan, every selected job to be successful, and every unselected job to be skipped. Failure, cancellation, a missing job, an unexpected skip or an unexpected execution fails the gate. API/native reusable workflows are direct dependencies of this gate. A newer run on the same PR cancels its predecessor. Release checks run at their requested immutable ref; native packaging executes once inside those checks, and the distribution build waits for them.

Use **Actions → core-check → Run workflow** for a manual full check. For a transient failure, use GitHub's **Re-run failed jobs** so successful jobs remain completed. PR updates cancel the superseded run through Actions concurrency. Build and dependency caches speed execution; they do not stand in for successful tests. Native release installers are passed between jobs using Actions artifacts, within the same release workflow.

The `CI review and Feishu notification` workflow runs once after a PR merges into main. It checks out the merged commit, reads that PR's existing checks and logs, and reports their actual status. It does not trigger another test run. Closing an unmerged PR does not invoke the review. The workflow uses `pull_request_target` only for the merged event and never checks out an unmerged PR head with notification credentials.

Browser jobs own separate fixtures and servers; increasing workers against the shared mutable fixture is unsafe. Failed browser jobs retain reports/traces for seven days. Native failure phase summaries are retained for seven days and detailed output stays in the Actions logs; credentials and temporary installation trees are not uploaded. Successful native archives are uploaded only for explicit manual packaging or releases, without recompressing the compressed archive. Release distribution artifacts retain their existing recovery policy; failed publication can reuse the original build as described above.

The local Node composite action installs the pinned pnpm and caches its package store by lockfile, OS, architecture, Node version and pnpm version. It caches downloaded packages, not `node_modules`; installs remain frozen. Go partitions retain the existing module/compiler caches described under [publication](#publish-a-version). Cache hits seed work and never replace tests. The three native platforms run independently; parallel execution reduces elapsed time without reducing total machine time.

For a local change, inspect the selected groups and choose focused checks according to [Checks for a change](../CONTRIBUTING.md#checks-for-a-change):

```sh
python3 scripts/ci_plan.py plan --base origin/main --head HEAD
make check-ci
```

`make check` remains the full local entry point with an unsharded Web suite and an unsharded store package. `make check-web-unit` and `make check-web-acceptance OAC_WEB_TEST_SHARD=1/4` expose the Web parts; `make check-core-packages` and `make check-core-store OAC_CORE_STORE_SHARD=1/3` expose the Core parts, with store tests assigned to shards by a stable hash of their names. The selection tests cover mixed changes, shared consumers, renames/deletions, unknown inputs, shallow merge checkouts and failed/cancelled/missing results. For changes to the selection map, replay representative diffs for the affected rules. For workflow changes, run actionlint and validate the changed scheduling or partition behavior. Use real component runs only when needed to validate behavior affected by the change.

Measure completed runs with `python3 scripts/ci_metrics.py RUN_ID ...`. It reports the latest attempt's summed runner minutes, elapsed time and initial queue delay from that attempt's start, peak concurrent jobs, platform breakdown and job outcomes/failure fraction. Only jobs assigned a runner in that attempt contribute machine time and execution concurrency; jobs cancelled while queued retain their outcome and wall time. Earlier attempts are not included. Failed-job reruns can carry earlier successful results: their outcomes appear separately and their old execution time is excluded. A missing rerun start timestamp stops measurement because reused jobs cannot be separated reliably. Keep run/head/attempt identities with comparisons, and report cancellations and unfinished runs separately. Raw runner minutes are not billed minutes; use each platform's published conversion and allowance rules before estimating cost. A small successful sample is not a long-term failure-rate estimate. Scheduled full runs are outside this policy.

### CI runners and free allowance

Linux check jobs use Blacksmith's 2-vCPU Ubuntu 22.04 or 24.04 runners; native Windows uses its 2-vCPU Windows 2025 runner. Blacksmith has no 2-vCPU macOS runner, so native macOS uses the standard GitHub `macos-15` ARM64 runner. Release building and publication always share one Blacksmith `blacksmith-4vcpu-ubuntu-2204` runner, independent of the runner switch. Custom runner labels used directly in `runs-on` are declared in `.github/actionlint.yaml` for workflow validation.

Set the repository Actions variable `OAC_USE_GITHUB_RUNNERS` to `true` to run check jobs on standard GitHub-hosted runners instead. Linux keeps its matching Ubuntu version, Windows uses `windows-2025`, and macOS continues using `macos-15`. Remove the variable or set it to `false` to return switchable check jobs to Blacksmith's 2-vCPU defaults. For example, maintainers can switch when the organization's free allowance is used up, then restore Blacksmith after the allowance resets:

```sh
gh variable set OAC_USE_GITHUB_RUNNERS --body true --repo MiniMax-AI/OpenAgentCore
```

This is an explicit operator switch, not an automatic billing balance probe. Runner selection applies to newly scheduled runs. Check current allowance and platform conversion rates in [Blacksmith's runner documentation](https://docs.blacksmith.sh/blacksmith-runners/overview) before treating 2-vCPU usage as free; Windows minutes consume more allowance than Linux minutes. Standard GitHub runner usage follows the repository's visibility and GitHub plan. The release build uses a 4-vCPU runner; these workflows request no paid cache add-on.
