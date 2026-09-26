# Maintainers and advanced deployments

This page is for people who build and publish Parsar Core, or run Core without the
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
Runtime OCI manifest digest. Output goes to `~/.parsar/build/core-distribution/`; it is
not published anywhere automatically. Qualify the exact bundle before distributing it.
See the [contributor guide](../CONTRIBUTING.md).

The release base must serve the generated asset file names over HTTPS. Set
`CORE_DISTRIBUTION_OFFLINE=1` to also produce the offline bundle; a build without a
release base must select offline mode. Nodes always download their files from the
console that generated their command, whatever release base the build recorded.

A bundle carries a fixed set of docs (the build lists them). Links between them stay
relative; every other relative link is rewritten to the same file on GitHub at the
bundle's commit, and the build fails if a link or anchor does not resolve.

## Produce and qualify a release

The `core-release` GitHub Actions workflow builds production assets from a full
committed source SHA with the pinned Runtime builders. Acceptance credentials and
private test certificate authorities must never enter its inputs. Run it from the
repository's Actions page, or:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/parsar-core --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

The workflow uploads the matched files as an Actions artifact and, with
`draft_release`, creates a draft Release tagged `build-<full SHA>`. The manifest records
the same tag in every asset URL. Don't mix files across releases or resolve components
through `latest`.

Download the draft assets with repository access, verify their checksums, and qualify a
fresh installation plus the node and self-hosted connection paths before publishing the
draft. A workflow build alone is not live acceptance. Publish exactly the tested assets;
never rebuild or replace files under the same release identity. Publishing a Release
does not change the repository's visibility.

## Run Core without the installer

These paths give you Core alone, without Web, the `parsar` command or `config.json`.
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
