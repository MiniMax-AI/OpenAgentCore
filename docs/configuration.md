---
title: "Configuration reference"
---

Every setting of a Core installation has exactly one home. There are two kinds:

| Kind | Examples | Home | Change it with | Takes effect |
| --- | --- | --- | --- | --- |
| [Process settings](#process-settings-configjson) | Public URL, ports, logging, harnesses, execution concurrency, audit retention, OAuth origins, Runtime history export | `.env` in the installation directory (default `~/.oac/core`) | Edit `.env`, then run `oac apply` | `oac apply` recreates the services that read the changed settings |
| [Runtime settings](#runtime-settings-web) | Sandbox backend and size, nodes, Projects and keys, default models, executor credentials | Core's PostgreSQL database | Web, or the Core API (`/core/v1`) with the Core key | Saved without a Core restart; nodes prepare Runtime changes asynchronously |

Web's **System** page shows the installation's addresses, the default models, the sandbox configuration and, under **Startup settings**, the process settings Core loaded. Secrets live in [`data/secrets/`](#installation-directory), one copy each. No configuration file defines Projects or API keys.

## Process settings {#process-settings-configjson}

Installer flags in [installation options](./getting-started/install-options.md) write `.env` once. To change a setting, edit `.env` and apply it:

```sh
~/.oac/core/oac apply
```

### How oac apply works {#how-oac-apply-works}

1. It runs `oac-core check-config` with the `.env` you edited and changes nothing if a value is invalid.
2. It runs `docker compose up -d --wait`. Compose recreates only the services whose configuration changed.
3. If the check fails, no container is recreated. See [stop and restart](./getting-started/operations.md#stop-and-restart) for what a restart interrupts.

`docker compose ps` shows the services. Domain state is `data/domain/status.json`.

### Changing the public URL {#changing-the-public-url}

`OAC_PUBLIC_URL` is the one origin that applications, nodes, sandboxes and self-hosted executors use. Core derives the daemon WebSocket URL, the self-hosted `remote_url` and each sandbox's connection address from it. It is an http or https origin: the address browsers and nodes use. The installation serves Web over HTTP on `OAC_WEB_PORT`; a reverse proxy or hosting platform terminates HTTPS when you put one in front.

To change it, point the reverse proxy at the new address first, then edit `OAC_PUBLIC_URL` and run `oac apply`. Afterwards:

- Nodes on the old address get no new sandboxes: remove them in Web and add them again.
- Existing sandboxes and executors keep working only while the old address still reaches this Core.
- Self-hosted executors must restart with the new `remote_url`, and their installer refuses an installation made for the old address: create new self-hosted Sessions and connect their hosts again.

### Settings

`OAC_HISTORY_SETTINGS_FILE` may point at a file whose headers hold export credentials. The file stays mode `0600`, and those headers never appear in `oac` output or in the installation report. Model providers are not process settings; see [Default models](#default-models).

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | `http://localhost:8080` | Origin applications, nodes, sandboxes and self-hosted executors use. See [changing the public URL](#changing-the-public-url) |
| `OAC_HOST` | `127.0.0.1` | Address published by `ports.yaml`. `install.sh` sets `0.0.0.0` |
| `OAC_WEB_PORT` | `8080` | Host port of Web |
| `COMPOSE_FILE` | `compose.yaml:ports.yaml` | The Compose files. `ports.yaml` publishes Web; hosting platforms omit it |
| `OAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `OAC_LOG_FORMAT` | `auto` | `auto`, `text` or `json` |
| `OAC_LOG_ADD_SOURCE` | unset | `1` adds source locations |
| `OAC_EXECUTION_CONCURRENCY` | `4` | Concurrent execution work, from 1 to 1024 |
| `OAC_DEFAULT_HARNESS` | `codex` | Harness used when a request does not name one |
| `OAC_HARNESSES` | Every registered Harness | Comma-separated Harnesses to enable besides the default one. Unknown names stop startup |
| `OAC_WRITE_AUDIT_RETENTION` | `2160h` | Minimum `1h` |
| `OAC_OAUTH_TRUSTED_ORIGINS` | unset | Comma-separated HTTPS origins |
| `OAC_HISTORY_SETTINGS_FILE` | unset | Optional Runtime history file. Sensitive; Core reports only whether it is configured |

An unset or empty value selects the default. Edit `.env`, then run `oac apply`. Core reports the process settings it loaded at `GET /core/v1/installation`. `oac-core check-config` validates the same environment without starting Core. Sensitive settings report only whether they are configured. How Core collects and keeps Runtime history is in [retained history](../contracts/agents-api/runtime-observability.md#retained-history-and-optional-export).

## Runtime settings: Web

Runtime settings live in Core's database. Change them in Web; scripts use the same Core API with the Core key.

| Setting | Where in Web | Core API | Notes |
| --- | --- | --- | --- |
| Sandbox backend: Docker, microsandbox or E2B | **System** → **Manage sandbox configuration**: the setup wizard, ending with **Save configuration** | `/core/v1/sandbox/deployment` | One backend per installation, chosen after the first sign-in. Another backend needs **Reset deployment** first; see [change the sandbox configuration](./getting-started/nodes.md#change-the-sandbox-configuration) |
| Sandbox size, Runtime release, E2B key and template build | **System** → **Manage sandbox configuration** → **Change resources** | `/core/v1/sandbox/deployment` | Web proposes the sizes in [`standard-sizes.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/src/features/sandbox/standard-sizes.json). Existing sandboxes keep their size and release. The E2B key is write-only and encrypted |
| Nodes and their capacity | **Nodes**: **Add node**; **Edit node** and **Remove node** on a node's page | `/core/v1/sandbox/enrollment-tokens`, `/core/v1/sandbox/nodes` | See [Node capacity](#node-capacity) and the [nodes guide](./getting-started/nodes.md) |
| Projects and API keys | **Projects and keys**: **Create project**, **Rename**, **Issue key**, **Revoke**, **Archive** | `/core/v1/projects` | Keys are shown once; Core stores digests |
| Default model per harness | **System** → **Default model configuration**: **Set** | `/core/v1/harnesses/{harness}/model-configuration` | See [Default models](#default-models) |
| Executor credentials of a self-hosted Session | **Session log**, then the **Session** page: **Executor credentials** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | See [self-hosted executors](./getting-started/self-hosted.md) |

Which harnesses are enabled, and the default one, are process settings (`core.harnesses`, `core.default_harness`); System shows them read-only. The [Core administration API](../contracts/agents-api/admin-api.md) lists every Core API route, and the [deployment contract](../contracts/agents-api/sandbox-deployment.md) defines the sandbox fields, limits and change rules.

### Node capacity

Core approves a node's capacity when you generate its Add node command: **Sandboxes at once** (`max_active`, default 2) and, for microsandbox only, **Retained sandboxes** (`max_retained`, default 8), with `max_retained >= max_active >= 1`. Docker never suspends sandboxes, so Web doesn't ask for it and Core keeps `max_retained` equal to `max_active`. Change them later with **Edit node**. Reservations and cleanup that is not confirmed count against capacity; lowering a limit stops no running sandbox. A node's own files can't change its capacity, size or Runtime.

`core.execution_concurrency` is unrelated: it limits concurrent execution work in Core.

### Default models

Set a default in **System** → **Default model configuration**, or use `PUT /core/v1/harnesses/{harness}/model-configuration`. Core encrypts provider keys with `secrets/credential.key` and never returns them. [Model execution](../contracts/agents-api/model-execution.md#deployment-defaults) owns the request fields and replacement rules, and [precedence](../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) says which Sessions use a default.

## Compose installations

The [standalone Compose file](./getting-started/install-options.md#docker-compose-and-hosting-platforms) takes process settings from the platform's environment. An empty `OAC_PUBLIC_URL` selects `http://localhost:8080`. Set it to the exact public origin, without a trailing slash, and recreate Core and Web before adding nodes or executors. The platform terminates TLS and routes to `web:8080`.

The initialization service generates secrets and the installation ID once, then verifies them on subsequent deployments. Each secret has one persistent source; Core's key digest is derived from Web's sign-in key. Initialization never replaces missing or changed secrets on an existing installation. Core reads the process environment from `.env`.

| Data directory path | Content | Readers |
| --- | --- | --- |
| `database/` | PostgreSQL data | PostgreSQL; initialization checks whether it is empty |
| `secrets/database/` | Generated database password | PostgreSQL and Core |
| `secrets/core/` | Credential encryption key, installation ID and Core key digest | Core |
| `secrets/web/` | Generated Core sign-in key | Web |
| `state/` | Private Provider state | Core |
| `node-payload/` | Verified node installation metadata | Web |

Initialization prepares this directory; application services receive their secret directories read-only. `docker compose exec web oac-web core-key` prints the Core key to the operator terminal without writing it to container logs. Database passwords and credential encryption keys are never printed.

`OAC_DATA_DIR` selects the directory and defaults to `./data` beside the Compose file. Preserve it together with that project's definition and public URL. Removing only the secret directories does not reset an installation; initialization refuses to start over an existing database. Core also binds the installation ID to its database. Runtime settings continue to live in [Core's database](#runtime-settings-web).

## Docker node configuration

The node installer writes Docker’s provider configuration into the node’s configuration file. Deployment resources, Runtime images and capacity remain in [Core’s database](#runtime-settings-web).

| Field | Installer value | Meaning |
| --- | --- | --- |
| `host` | `unix:///var/run/docker.sock` | Explicit Docker Engine socket |
| `network` | `oac-node-<installation-id>` | Runtime container network |
| `seccomp_file` | `<node-root>/runtime/seccomp.json` | Matched distribution’s seccomp profile |
| `nested_sandbox` | `true` | Enables the Docker adapter’s init process and proc-mask configuration |
| `extra_hosts` | Optional | Additional container host mappings |

The [Docker adapter](./sandbox-provider.md#docker-adapter) owns container isolation, volume layout and lifecycle behavior.

## Installation directory

The installer creates the installation directory, `~/.oac/core` by default, with mode `0700`. Secret files are `0600`.

| Path | Content | Changed by |
| --- | --- | --- |
| `.env` | [Process settings](#process-settings-configjson). The file you edit | You, then `oac apply` |
| `compose.yaml`, `ports.yaml` | The release's service definition. Do not edit them | The release |
| `oac` | The [management command](./getting-started/operations.md#the-oac-command), copied from the Core image | The installer |
| `data/secrets/web/core.key` | The [Core key](./getting-started/operations.md#core-key) | `oac rotate-core-key` |
| `data/secrets/core/credential.key` | Encryption key for what Core stores sealed in the database | Nothing. Keep it with the database |
| `data/secrets/core/core-key-digests.json` | SHA-256 of the Core key | `oac rotate-core-key` |
| `data/secrets/database/password` | PostgreSQL password | Nothing. PostgreSQL reads it only when the database is created |
| `data/database/` | PostgreSQL data | PostgreSQL |
| `data/node-payload/` | Node files Web serves at `/node-install/` | Initialization |
| `data/state/` | Private Provider state, including E2B receipts | Core |
| `.oac.lock` | The installation lock | Mutating `oac` commands |

The Compose project is named `oac-<10 hex digits>`. Its services are `init`, `database`, `core` and `web`. Core applies database migrations when it starts. `web` serves the console and forwards `/v1` and `/api/v1` to Core, and it is the only service with a published port, `OAC_WEB_PORT`. No service receives a Docker socket. Apart from Docker's storage, nothing is written outside the installation directory.

## Appendix: Core environment without the installer

Core reads only its environment. Compose interpolates `.env` into the service environment. Compose must be 2.26.0 or newer. If you run Core yourself, set these variables; see the [service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md).

| Variable | Set from |
| --- | --- |
| `OAC_PUBLIC_URL` | The public origin. Core derives the daemon WebSocket URL, the self-hosted `remote_url`, the hosted sandbox address and the deployment's read-only `core_url` from it, never from request headers. Without it, Core runs no Runtime gateway and executes no Sessions |
| `OAC_ADDR` | The image sets `:8091`. Independently started Core defaults to `127.0.0.1:8091` when unset or empty |
| `OAC_DATABASE_URL` | PostgreSQL without a password |
| `OAC_DATABASE_PASSWORD_FILE` | `data/secrets/database/password`. The URL must then carry no password |
| `OAC_CREDENTIAL_KEY_FILE` | `data/secrets/core/credential.key` |
| `OAC_CORE_KEY_DIGESTS_FILE` | `data/secrets/core/core-key-digests.json`: a JSON array with the SHA-256 of the Core key |
| `OAC_INSTALLATION_ID_FILE` | `data/secrets/core/installation.id`: the installation ID, a canonical UUID. It enables the sandbox deployment and node routes and requires `OAC_PUBLIC_URL` and `OAC_CORE_KEY_DIGESTS_FILE`. Core refuses an ID other than the one its database recorded |
| `OAC_EXECUTION_CONCURRENCY`, `OAC_DEFAULT_HARNESS`, `OAC_HARNESSES`, `OAC_WRITE_AUDIT_RETENTION`, `OAC_OAUTH_TRUSTED_ORIGINS` | The matching [process settings](#settings). `oac-core check-config` validates them without starting Core |
| `OAC_HISTORY_SETTINGS_FILE` | Optional Runtime history file. Sensitive; the installation report says only whether it is set |
| `OAC_LOG_LEVEL`, `OAC_LOG_FORMAT`, `OAC_LOG_ADD_SOURCE` | Logging; Web reads the same three |
| `OAC_PROVIDER_ROOT` | Absolute adapter artifact root. The Core image sets `/opt/oac`. Each adapter owns its helper paths beneath this root |
| `OAC_PROVIDER_STATE_ROOT` | Absolute private state root: `/state` in the Core image. Each adapter owns its subdirectory; E2B uses `e2b/`, owned by Core's user with no group or other access. Back it up with the database and `credential.key`; don't mount it into Web or a Runtime |
| `OAC_NATIVE_INSTALLER_DIR` | Self-hosted daemon installers: `/opt/oac/native-installers` in the Compose file. Unset, Core serves none. Core checks the catalog against its own release before serving it |

Core logs the file paths it loads, never environment values or file contents.

Invalid explicit OAuth trusted origins stop Core at startup. Entries must be HTTPS origins without credentials, query or a non-root path. [Vaults](../contracts/agents-api/vaults.md) owns refresh and network policy. A private issuer also needs a trusted CA: independently managed Unix Core can use Go’s `SSL_CERT_FILE` PEM CA-bundle override, which preserves certificate verification. Managed installation has no custom-CA setting.

## Appendix: Web environment without the installer

Compose sets these for Web. Set them yourself only when you run the console without Compose. Of the installation's secrets, Web receives only `data/secrets/web/core.key`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | Listener address |
| `OAC_WEB_ORIGIN` | `http://127.0.0.1:8080` | The exact browser-facing origin, HTTP or HTTPS, without a path. Host and origin checks use it; HTTPS makes the session cookie `Secure` |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core's origin, HTTP or HTTPS, without credentials, query or path |
| `OAC_WEB_CORE_KEY_FILE` | `/admin/core.key` | Absolute path of a regular file with no group or other permissions, holding the Core key: at least 32 characters, no whitespace, at most 4 KiB |
| `OAC_WEB_DIST` | `/www` | Absolute directory of the built console; must contain `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | unset | Absolute path of the matched distribution's node payload (the installer's `node-payload/`). Unset, `/node-install/*` is not served and Add node is unavailable |

Defaults apply when a variable is absent; an explicitly empty value is validated as supplied. An invalid `OAC_WEB_*` value stops the console at startup with a message naming the variable. The console also reads `OAC_LOG_LEVEL`, `OAC_LOG_FORMAT` and `OAC_LOG_ADD_SOURCE` ([Core environment](#appendix-core-environment-without-the-installer)); unknown values fall back to their defaults. Use HTTPS for any browser that is not on the same machine.
