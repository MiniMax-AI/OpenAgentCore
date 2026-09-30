# Build and release OpenAgentCore

This guide is for maintainers who build and publish OpenAgentCore. To install Core and Web, use the [installation guide](getting-started/install.md). The rules the installer code follows are in [Installer design rules](../deploy/install/README.md); required checks are in [CONTRIBUTING](../CONTRIBUTING.md#required-checks).

## Build a distribution

A distribution is the matched set of Linux amd64 release assets built from one commit: the control archive (the installer, the `oac` command, and the Core, Web, gateway and PostgreSQL images), the Runtime image and node artifacts as separate files, and the native installers.

Build on Linux x86_64 with a glibc compatible with Debian 12, Docker, the Go version in `go.mod`, a C compiler (the microsandbox helper is a CGO build), Node, pnpm, Python 3.9 or newer, curl, tar and sha256sum. The source must be clean and committed. First prepare the pinned Codex package and MiniMax Code companion, then build:

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

The control archive carries no Runtime image or node execution artifacts; the offline archive carries them. The [download contract](../deploy/install/README.md#download-contract) describes how nodes obtain them.

A distribution carries the docs listed in `BUNDLED_DOCS` in `scripts/core-distribution-manifest.py`. Links between bundled docs stay relative; every other relative link is rewritten to the same file on GitHub at the bundle's commit. The build fails when a link or anchor does not resolve, and `make check-distribution` runs the same check on every tracked Markdown file outside `example/` and `provenance/`. Update the list when you add or move a doc that the installer or its output refers to.

### Native installers

Self-hosted machines install `oac-daemon` from per-platform native installers: Linux amd64, macOS arm64 and Windows amd64. Each is built on its own OS by the `native-check` workflow (`scripts/build-native-installer.mjs`, whose `pins` object fixes the Node.js and Harness versions) and uploaded as `oac-native-installer-<OS>-<ARCH>.tar.gz`. For a local distribution, download the three artifacts from a `native-check` run on that exact commit (a manual run or the release run; pull-request runs build the merge commit and do not match), then assemble the catalog from that checkout:

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

Docker builds the Linux amd64 helper with the pinned CPython and Debian 12 image. The Python dependency closure, including PyInstaller, is hash-locked in `services/core/tools/e2b-provider/requirements.lock`; no E2B account key is needed. Set `E2B_PROVIDER_BUILD_DIR` for another output directory and `E2B_SOURCE_REVISION` when building from an exported source tree. The output is `oac-e2b-provider-linux-amd64.tar.gz` with its `.sha256`; it extracts to `oac-e2b-provider/` with the executable, `_internal/`, `licenses/`, `requirements.lock` and `manifest.json`. The Core image and native Core use the same tree; the host needs a compatible glibc and CA certificates, not Python.

**microsandbox helper.** Linux only, with a C compiler:

```sh
make build-microsandbox-provider
make check-microsandbox-provider
```

The helper is written to `~/.oac/build/microsandbox-provider/oac-microsandbox-provider`. Its separate Go module pins the microsandbox Go SDK v0.7.2 and embeds the matching FFI library; never build production with the SDK's `microsandbox_ffi_path` tag. Core itself stays a CGO-disabled build. The helper needs glibc and runs only on nodes.

**microsandbox runtime.** The distribution uses the official [v0.7.2 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2) archive `microsandbox-linux-x86_64.tar.gz`, SHA256 `47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b` (`RUNTIME_ARCHIVE_SHA256` in `scripts/core-distribution-manifest.py`). The build verifies the checksum before extracting `msb` and `libkrunfw.so.5.6.1` and records both files' hashes. The helper checks those hashes on every call and never installs or upgrades them.

### Standalone Core builds

`make build-core` builds `oac-core`, `oac-core-migrate`, `oac-core-device`, `oac-core-environment-key` and `oac-node` into `${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core` (`OAC_DEV_CORE_BUILD_DIR` selects another absolute directory). The build copies only the source set listed in `scripts/build-core.sh` (the Core service, its contracts, the shared packages it needs and the root Go module files) into a temporary context and builds with CGO disabled, read-only modules and trimmed paths. It needs no Node, Docker or other application. When Core gains a shared dependency, add that package to the list; never copy the whole repository to make it compile.

`make docker-build-core` builds the image `oac-core:dev` (`OAC_DEV_CORE_IMAGE` selects another name) from those five commands and the E2B helper. The base is the digest-pinned `debian:bookworm-slim` with CA certificates and the glibc runtime the helper needs; the default user is UID/GID 65532 and Core listens on `:8091`. The image is Linux amd64 only and is not pushed to a registry. Changes to the image or its build need `make check-core-container` in addition to `make check`: it runs the official-client suite against the image with a read-only root filesystem and needs Linux Docker, a non-root user, and the [test database and pinned SDK](../services/core/README.md#official-client-verification) of the service checks (`OAC_TEST_DATABASE_URL` naming an `oac_*_tests` database with the migrations applied, and `OAC_TEST_OFFICIAL_SDK_PYTHON`).

`make build-core-release` packages the same five commands into `oac-core-<commit>-linux-amd64.tar.gz` and its `.sha256` under `~/.oac/build/oac-core-release` (`OAC_DEV_RELEASE_DIR`). Beside `bin/`, the archive holds the [archive README](../services/core/RELEASE.md), the license, `manifest.json` (commit, tree, platform, Go version, upstream protocol and binary hashes) and `SHA256SUMS`, which lists every packaged file. The build needs clean committed source and Python 3.9 or newer, and packages deterministically. It carries no configuration, credentials, Web or Runtime. Test archive changes by extracting a fresh copy and running its commands.

## Publish a version

Push a version tag on the reviewed commit to run the `core-release` workflow:

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

Tags use `vMAJOR.MINOR.PATCH`, optionally with a prerelease suffix such as `-rc.1` and build metadata such as `+build.1`. A prerelease suffix creates a GitHub prerelease. Pushing the tag is the release decision. Automated checks establish build and test results, not real-model qualification: assess live execution evidence before you push the tag. Model credentials and private certificate authorities never enter CI or release inputs, including acceptance images that contain them.

The workflow runs three jobs on the tagged commit: `check` (the full `make check` workflow), `native` (the `native-check` matrix) and `build`, which starts after `native` succeeds. `build` prepares the pinned Runtime inputs, assembles the native catalog and builds the distribution with the offline archive, and adds `deploy/install-release.sh` as `install.sh` with its checksum. The `release` job runs only after `check` and `build` succeed. It is the only job with `contents: write`. It verifies the archive checksums and the native installer checksums against the catalog, resolves the repository's current name from GitHub before any write (Actions can keep an old name after a rename), refuses an existing Release or draft for the tag, uploads everything to a new draft on `uploads.github.com` bound to that draft's ID without retrying failed uploads, confirms the tag still points at the built commit, and publishes that draft by its ID. Images ship as archives; no registry is pushed. Downloads are anonymous.

`install.sh` resolves the latest stable release once, or the release named by `--version`, verifies the control archive and runs that bundle's installer; the [installation guide](getting-started/install.md#install) covers its use.

The `check` and `build` jobs share Go module and build caches under `~/.oac/cache/`, keyed by runner OS and architecture, the Go module files and the commit. An older cache only seeds downloads and compilation; every check still runs. New keys are saved only after a successful job.

Never move a release tag or overwrite published assets. If the `release` job fails, inspect the Release first: publication may have completed despite a lost response. Leave a complete published Release as it is. For an incomplete draft, delete that draft (the job refuses any existing Release or draft for the tag), then rerun the failed `release` job, which reuses the original Actions artifact. Do not rerun the build or recreate the tag to recover a failed upload.

### Build a candidate without publishing

A manual run takes a full commit SHA, runs the same checks and builds, defaults to the offline archive, and never publishes:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/OpenAgentCore --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

With `draft_release=true` the result is an unpublished `build-<full SHA>` draft Release; with `draft_release=false` the files stay in the Actions artifact. Use the exact matched asset set; never mix builds or resolve components through `latest`.

### Promote a qualified candidate

`scripts/promote-qualified-release.py` qualifies a candidate on a supervised host and publishes it once main reaches the reviewed promotion commit. Pass the candidate's flat files, built for the `build-<full SHA>` release base: the thin and offline archives and the native installers, each with its `.sha256`, and the Runtime and node assets. Take them from a local build with that release base and `CORE_DISTRIBUTION_OFFLINE=1`, or from the Actions artifact of a manual `core-release` run with `draft_release=false` after removing `install.sh` and `install.sh.sha256`. The command creates the draft Release itself and refuses any other file, so a draft created by `draft_release=true` cannot be promoted. Its module docstring lists the inputs, the qualification stages and the publication checks.

## Continuous integration

| Workflow | Runs on | Covers |
| --- | --- | --- |
| `core-check` (`check.yml`) | Pushes to `main`, every pull request, releases | `make check` with a PostgreSQL service and the Playwright browser, then a daemon build |
| `api-acceptance` | Pushes to `main` and pull requests that touch Core, its contracts, clients, shared Go code or build scripts | Standalone commands and migration, the pinned official client over HTTP, and the standalone container |
| `native-check` (`native.yml`) | Pull requests that touch native sources, shared dependencies or packaging inputs; manual runs; releases | Daemon, process lifecycle, Harness protocols and the installer bundle on Linux, macOS and Windows; uploads the native installers |
| `actionlint` | Changes to workflows | Workflow syntax |
| `core-release` | Version tags and manual runs | See [Publish a version](#publish-a-version) |

Changes limited to Web or to documentation outside `contracts/agents-api` do not start `native-check`. A newer `core-check`, `api-acceptance` or `native-check` run on the same branch or pull request cancels the older one.

## Run Core without the installer

The standalone archive and container give you Core alone: no Web, no `oac` command and no `config.json`. They suit development, testing and operators who supervise Core themselves. Core reads only its environment; the [configuration appendix](configuration.md#appendix-core-environment-without-the-installer) lists the variables. `OAC_DATABASE_URL` and `OAC_CORE_KEY_DIGESTS_FILE` are required; set `OAC_PUBLIC_URL` to the origin machines use to reach Core, or Core runs without the daemon transport.

- The [archive README](../services/core/RELEASE.md) covers the standalone archive.
- The [service guide](../services/core/README.md) covers building and running Core from source.

To run the container, create a private directory (mode 0700) with `api.env` (`OAC_DATABASE_URL` for a dedicated database, reachable from the container, `OAC_CORE_KEY_DIGESTS_FILE=/run/core-key-digests.json`, and `OAC_PUBLIC_URL`) and `core-key-digests.json`, a JSON array with the lowercase hex SHA-256 digest of your Core key. Keep both files mode 0600 and the Core key itself elsewhere. Run the migrations, then start Core:

```sh
config_dir="$HOME/.oac/oac-core-deployment"
docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --env-file "$config_dir/api.env" \
  oac-core:dev /usr/local/bin/oac-core-migrate
docker run --name oac-core --detach --read-only \
  --cap-drop=ALL --security-opt=no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8091:8091 \
  --env-file "$config_dir/api.env" \
  --mount "type=bind,source=$config_dir/core-key-digests.json,target=/run/core-key-digests.json,readonly" \
  oac-core:dev
curl --fail http://127.0.0.1:8091/healthz
```

`--user` lets the container read the key digest file as your non-root host user; alternatively grant UID 65532 read access and omit it. Put a TLS reverse proxy in front for remote clients. `/healthz` reports liveness only. Keep credentials out of the image. All state is in PostgreSQL, so the container needs no writable volume; stop and start it with `docker stop` and `docker start`, and never remove the database to replace it. One Core process serves each database; replicas add no availability. After startup, use the Core key with the [administrator API](../contracts/agents-api/admin-api.md) to create Projects and issue application keys.

The image also contains `oac-core-device` for an [internal execution device](../services/core/README.md#internal-execution-device-connection) and `oac-core-environment-key`, the [break-glass credential command](../contracts/agents-api/environment-executor-credentials.md#break-glass-command).
