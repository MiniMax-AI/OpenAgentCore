# Codex Runtime

The Codex adapter ([`agent/codex`](../../../../apps/daemon/internal/agent/codex)) runs the native Codex CLI through its app-server protocol inside the daemon. This page holds the Codex-specific adapter rules, the Codex Runtime image and the Docker sandbox settings that every Docker Runtime uses. [Harness onboarding](../../../../contracts/agents-api/harness-onboarding.md) owns the obligations shared by all adapters.

Native tools run with the daemon user's permissions; the outer sandbox provides isolation ([Runtime and outer isolation](../../../../docs/design-principles.md#runtime-and-outer-isolation)).

## Native pin

The adapter accepts only Codex `0.153.4`: installation and recovery checks require `codex --version` to report `codex-cli 0.153.4` ([`installation.go`](../../../../apps/daemon/internal/agent/codex/installation.go), [`recovery.go`](../../../../apps/daemon/internal/agent/codex/recovery.go)). The Runtime image carries the official Linux amd64 package of that version with its matching `codex-resources`; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds it.

## Model provider and native state

Codex accepts only the `responses` model protocol ([`harnessconfig/codex`](../../../../internal/harnessconfig/codex/configuration.go)). A Chat Completions or Anthropic provider is rejected; nothing converts between protocols. [Model execution](../../../../contracts/agents-api/model-execution.md#deployment-defaults) owns provider selection, including the per-Harness deployment default.

Each Session has its own `CODEX_HOME` at `$OAC_RUNTIME_HOME/daemon/agent-sessions/<state key>/` (`OAC_RUNTIME_HOME` defaults to `~/.oac`). Native history stays there. The adapter regenerates that directory's `config.toml` on every prompt: the Session's frozen provider bundle becomes the `[model_providers.oac]` block, with `wire_api` `responses`, and the thread is pinned to that provider, so Codex never falls back to its built-in `openai` provider.

## Native failure classification

The adapter classifies a failed Turn only from the exact root terminal Turn's `codexErrorInfo` ([`error_classification.go`](../../../../apps/daemon/internal/agent/codex/error_classification.go)); error notifications, including retry notifications, never classify it. The [Runtime protocol](../../../../docs/runtime-protocol.md#native-failure-classification) defines the codes.

| `codexErrorInfo` | Code |
| --- | --- |
| `unauthorized` | `authentication_error` |
| `usageLimitExceeded` | `usage_limit_exceeded` |
| `rateLimitExceeded` | `rate_limit_exceeded` |
| `contextWindowExceeded` | `context_length_exceeded` |
| `serverOverloaded` | `server_overloaded` |
| `internalServerError` | `server_error` |
| `badRequest` | `invalid_request` |
| `cyberPolicy` | `cyber_policy` |
| `httpConnectionFailed`, `responseStreamConnectionFailed`, `responseStreamDisconnected` | `connection_failed`, with the upstream `httpStatusCode` |
| `responseTooManyFailedAttempts` | `rate_limit_exceeded` when `httpStatusCode` is 429, otherwise `connection_failed` |

Every other variant stays unclassified.

## Runtime image

[`Dockerfile`](Dockerfile) builds the Codex Runtime image from a prepared context that holds only `oac-daemon`, the unmodified `codex` executable and `codex-resources`.

| Item | Value |
| --- | --- |
| Base | Digest-pinned `debian:bookworm-slim` with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip`, `nodejs`, `npm` and `ripgrep` |
| Programs | `/usr/local/bin/oac-daemon`, `/usr/local/bin/codex` (mode 0555) and `/usr/local/codex-resources` |
| User | `runtime`, UID/GID 1000, home `/home/runtime` |
| Environment | `OAC_RUNTIME_HOME=/home/runtime/.oac`, `OAC_RUNTIME_CODEX_BIN=/usr/local/bin/codex`, `OAC_RUNTIME_WORKSPACE=/environment/workspace`, `OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization`, `OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages` |
| Entry point | `oac-daemon connect --profile default`, working directory `/environment/workspace` |

The build fails unless `codex --version` reports the pinned version. The image holds no credentials, workspace data or product software. The distribution copies the Codex executable and resources into the combined Runtime image; see the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers).

## Docker sandbox settings

The Docker Sandbox Provider ([`sandbox/docker`](../../../../services/core/internal/sandbox/docker)) runs every Runtime image, whichever Harness it serves, with the same container settings ([`container_options.go`](../../../../services/core/internal/sandbox/docker/container_options.go)):

- user 1000:1000, read-only root filesystem, all capabilities dropped, `no-new-privileges`, this directory's `seccomp.json` and AppArmor `unconfined`;
- the Docker network from the node's provider configuration (`oac-node-<installation-id>` from the node installer), plus any `extra_hosts` there;
- CPU and memory from the deployment specification, a 128-process limit and a 128 MiB `/tmp` tmpfs;
- two named volumes labelled with the installation, tenant, Environment and allocation: `<name>-home` at `/home` and `<name>-environment` at `/environment`, whose `workspace` subdirectory is also mounted at `/workspace`. The Docker Engine must support volume subpath mounts;
- with `nested_sandbox`, which the node installer sets, Docker's `/proc` masks are lifted (`/sys/firmware` and `/sys/devices/virtual/powercap` stay masked) and the container runs an init process.

Create refuses to reuse retained volumes that have no container. It copies the [Runtime bootstrap](../../../../docs/runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600, UID 1000) and the `/environment` workspace, staging, initialization and package directories into the container, then starts `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json`. When the created container does not have the configured CPU, memory and exact image, Create returns the error with `CreateSettled`. Docker has no lease, so Renew only reads the container state. Kill checks the ownership labels of the container and both volumes before removing any of them, then confirms that all three are gone.

The node uses the explicit Unix socket in its provider configuration (`unix:///var/run/docker.sock` from the node installer) and ignores `DOCKER_HOST`. No Docker socket, host home or Core credential is mounted into a Runtime.

### Seccomp profile

`seccomp.json` is the Moby default profile at [revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json) (Apache-2.0, see `seccomp.LICENSE`; upstream file SHA-256 `785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`) with one appended rule that allows `clone`, `unshare`, `setns`, `mount`, `umount2` and `pivot_root`. The distribution ships this file to every Docker node as `runtime/seccomp.json`.
