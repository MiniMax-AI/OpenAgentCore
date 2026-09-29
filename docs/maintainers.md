# Maintainers and advanced deployments

This page is for people who build and publish OpenAgentCore, or run Core without the
installer. To install Core and Web, use the
[installation guide](getting-started/install.md) instead.

## Build a distribution

Release builders need the repository's full Linux toolchain and Docker. Build from
clean, committed source, with the pinned Codex platform package and a matching MiniMax
companion prepared through the existing Runtime build instructions:

```sh
export AGENTS_RUNTIME_CODEX_PACKAGE=/absolute/path/to/codex-linux-package
export MCODE_HARNESS_BUILD_DIR=/absolute/path/to/mcode-harness-artifact
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://downloads.example/releases/COMMIT
make build-core-distribution
```

The builder reuses the existing Core, Runtime, SDK and Web build scripts. It records
the commit, immutable image identities, microsandbox binary hashes and the actual
Runtime OCI manifest digest. Output goes to `~/.oac/build/core-distribution/`; it is
not published anywhere automatically. Qualify the exact bundle before distributing it.
See the [contributor guide](../CONTRIBUTING.md).

The release base must serve the generated asset file names over HTTPS. Set
`CORE_DISTRIBUTION_OFFLINE=1` to also produce the offline bundle; a build without a
release base must select offline mode. Nodes obtain bootstrap metadata from the
console that generated their command. The console serves local artifacts or redirects
missing ones to the pinned HTTPS release URL. Published assets download anonymously.

Native daemon/Harness installers are separate `oac-native-<source>-<platform>.tar.gz`
Release assets with checksum files. The default Core archive and image carry only
`native-installers/catalog.json`; Session bootstrap downloads the machine's platform
on demand. The explicit offline archive includes these installers once, outside the
Core image. Core installation retains them locally for the same bootstrap endpoint.
The tag workflow assembles the catalog from matching native CI outputs. For a local
build with native onboarding, first run `scripts/build-native-catalog.mjs INPUT OUTPUT`
and set `OAC_NATIVE_INSTALLER_BUILD_DIR=OUTPUT`; missing or foreign native assets
prevent release publication. No Release or registry download occurs when Core starts.

A bundle carries a fixed set of docs (the build lists them). Links between them stay
relative; every other relative link is rewritten to the same file on GitHub at the
bundle's commit, and the build fails if a link or anchor does not resolve.

## Publish a version

Push a version tag on the reviewed commit to run `core-release`:

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

Tags use `vMAJOR.MINOR.PATCH`, optionally with a prerelease suffix such as
`-rc.1` and build metadata such as `+build.1`. A prerelease suffix creates a
GitHub prerelease. Tag creation is the maintainer's release decision.

The workflow checks that exact source with the shared `make check` workflow
while building the Linux amd64 Core, Web, Runtime and database images, installation
archives and versioned Runtime assets. Tag builds include the offline archive.
It uploads the matched files as an Actions artifact and automatically publishes
them in the same tag's GitHub Release. Downloads in the manifest refer to that
tag. Each Release also includes the standalone `install.sh` bootstrap and its
checksum. It defaults to the latest stable release and accepts `--version`;
the [installation guide](getting-started/install.md#install) owns its usage.
Images are shipped as archives; this workflow does not push an image registry.
Publishing a Release does not change the repository's visibility.

Checks and builds run concurrently, and publication requires both jobs to succeed.
Both check out the same full commit SHA (the tag event's commit or the explicit
manual input). A failed check never permits publication, even if its build succeeds.
The build may consume runner time before another job fails; this trades some failed-run
cost for shorter successful releases.

Both jobs use the same Go module and compiler-cache directories under
`~/.oac/cache/`. Cache keys include runner OS/architecture, all Go module manifests
and checksums (including the toolchain version), and the checked-out commit.
A dependency-matched older cache is only a compiler/download seed: Go resolves
inputs again, and all checks still run with their existing assertions and timeouts.
No test result, installation state or release archive is accepted from this cache.
Main-branch checks can populate the default-branch cache for later release runs;
GitHub's branch/tag cache visibility rules still apply. New keys are saved only
after successful jobs; concurrent writers for the same key may retain either
job's valid cache. Missing or evicted entries affect speed, not correctness.

Build and check jobs have read-only repository permissions. Only the publication
job receives `contents: write`. Before publication it verifies archive checksums
and confirms that the remote lightweight or annotated tag still resolves to the
built commit after uploading the draft assets. Publication sends one request for
that fixed Release ID. An upload failure cannot expose an incomplete public Release;
an ambiguous publication response leaves the Release intact for inspection.

Do not move release tags or overwrite published assets. A rerun refuses an
existing Release, including a partial draft, rather than replacing files. If the
publication job fails, inspect the Release first: it may have completed despite
a lost response. Leave a complete published Release intact. For an incomplete
draft, reconcile or remove only that draft before rerunning the failed publication
job, which reuses the original Actions artifact. Do not rerun the successful build
or recreate the tag to recover a failed upload.

Automated checks establish build and test results, not real-model qualification.
Keep live execution evidence separate and assess it before pushing the version
tag. No model credentials or private certificate authorities belong in CI inputs.

### Build a candidate without publishing

Manual runs accept a full source commit SHA. They run the same checks and build
steps, default to offline output, and never publish automatically:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/parsar-core --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

With `draft_release=true`, the result is an unpublished `build-<full SHA>`
Release. With `draft_release=false`, files remain in the Actions artifact only.
Use the exact matched asset set; do not mix builds or resolve components through
`latest`. The historical batch qualification tools are not part of tag publication.

## Run Core without the installer

These paths give you Core alone, without Web, the `oac` command or `config.json`.
They are for development, testing and operators who manage Core's process themselves.
They are not an installation path for new users.

- [Standalone Core archive](../services/agents-api/RELEASE.md)
  (`make build-agents-api-release`): Core, its migrator and operator commands for your
  own PostgreSQL.
- [Standalone container](../services/agents-api/CONTAINER.md)
  (`make docker-build-agents-api`): the same in a Linux container image.
- [Service guide](../services/agents-api/README.md): building and running Core from
  source.

Core reads only its environment; the
[configuration appendix](configuration.md#appendix-core-environment-without-the-installer)
lists the variables. The Docker-hosted variant of the standalone archive is retired: it
could not describe a complete Runtime release by itself. Docker-hosted deployments use
the Core distribution and its installer, whose manifest carries the complete release.
