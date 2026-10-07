---
title: "Configuration reference"
---

Every setting of a Core installation has exactly one home, in one of three categories:

| Category | Examples | Home | Change it with | Takes effect |
| --- | --- | --- | --- | --- |
| [Process settings](#process-settings) | Public URL, ports, logging, harnesses, execution concurrency, audit retention, OAuth origins, Runtime history | `.env` in the installation directory (default `~/.oac/core`) | Edit `.env`, then run `oac apply` | `oac apply` recreates the services that read the changed settings |
| [Secrets](#compose-installations) | Database password, credential encryption key, installation ID, Core key and the Core key digest derived from it | `secrets/` in the Compose data volume, one copy each | Initialization generates them once; `oac rotate-core-key` replaces the Core key and its digest | `oac rotate-core-key` restarts Core and Web |
| [Runtime settings](#runtime-settings-web) | Sandbox backend and size, nodes, Projects and keys, default models, executor credentials | Core's PostgreSQL database | Web, or the Core API (`/core/v1`) with the Core key | Saved without a Core restart; nodes prepare Runtime changes asynchronously |

Web's **System** page shows the installation's addresses, the default models, the sandbox configuration and, under **Startup settings**, the process settings Core loaded. No configuration file defines Projects or API keys.

## Process settings

Installer flags in [installation options](./getting-started/install-options.md) write `.env` once. To change a setting, edit `.env` and apply it:

```sh
~/.oac/core/oac apply
```

### How oac apply works {#how-oac-apply-works}

1. It runs `oac-core check-config` with the `.env` you edited and changes nothing if a value is invalid.
2. It runs `docker compose up -d --wait`. Compose recreates only the services whose configuration changed.

Use `docker compose ps` to check the services. See [stop and restart](./getting-started/operations.md#stop-and-restart) for what a restart interrupts.

### Changing the public URL {#changing-the-public-url}

`OAC_PUBLIC_URL` is the one origin that applications, nodes, sandboxes and self-hosted executors use. Core derives the daemon WebSocket URL, the self-hosted `remote_url` and each sandbox's connection address from it. It is an http or https origin: the address browsers and nodes use. The installation serves Web over HTTP on `OAC_WEB_PORT`; a reverse proxy or hosting platform terminates HTTPS when you put one in front.

To change it, point the reverse proxy at the new address first, then edit `OAC_PUBLIC_URL` and run `oac apply`. Afterwards:

- Nodes on the old address get no new sandboxes: remove them in Web and add them again.
- Existing sandboxes and executors keep working only while the old address still reaches this Core.
- Self-hosted executors must restart with the new `remote_url`, and their installer refuses an installation made for the old address: create new self-hosted Sessions and connect their hosts again.

### Settings

Model providers are not process settings; see [Default models](#default-models).

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | `http://localhost:8080`, set by `compose.yaml`. Core started without it runs no Runtime gateway and executes no Sessions | Origin applications, nodes, sandboxes and self-hosted executors use. See [changing the public URL](#changing-the-public-url) |
| `OAC_HOST` | `127.0.0.1` | Web bind address published by `compose.yaml`. The installer sets `0.0.0.0` |
| `OAC_WEB_PORT` | `8080` | Host port of Web |
| `OAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `OAC_LOG_FORMAT` | `auto` | `auto`, `text` or `json` |
| `OAC_LOG_ADD_SOURCE` | unset | `1` adds source locations |
| `OAC_EXECUTION_CONCURRENCY` | `4` | Concurrent execution work, from 1 to 1024 |
| `OAC_DEFAULT_HARNESS` | `codex` | Harness used when a request does not name one |
| `OAC_HARNESSES` | Every registered Harness | Comma-separated Harnesses to enable besides the default one. Unknown names stop startup |
| `OAC_WRITE_AUDIT_RETENTION` | `2160h` | Minimum `1h` |
| `OAC_OAUTH_TRUSTED_ORIGINS` | unset | Comma-separated HTTPS origins |
| `OAC_HISTORY_SETTINGS_FILE` | unset | Optional [Runtime history file](#runtime-history-file). Sensitive; Core reports only whether it is configured |

An unset or empty value selects the default. Edit `.env`, then run `oac apply`. Core reads every process setting, and every file a setting names, once at startup and reports what it loaded at `GET /core/v1/installation`. `oac-core check-config` loads and validates the same settings and files without starting Core. The native installer catalog under `OAC_PROVIDER_ROOT` is not a setting; Core checks it only when it starts. Errors name the variable, never its value. Sensitive settings report only whether they are configured.

### Runtime history file

`OAC_HISTORY_SETTINGS_FILE` names a JSON file that tunes [retained history](../contracts/agents-api/runtime-observability.md#retained-history-and-optional-export) and adds an optional OTLP export. Without it, Core keeps history in its database with the defaults below. In a Compose installation, put the file in the data volume's `secrets/core/` directory, owned by UID 65532 with mode `0600`, and set `OAC_HISTORY_SETTINGS_FILE=/run/oac/<file name>`: Core mounts that directory read-only at `/run/oac`. Headers may hold export credentials; they never appear in `oac` output or in the installation report. Unknown fields are rejected.

| Field | Default | Meaning |
| --- | --- | --- |
| `sample_interval_seconds` | `30` | Periodic sampling interval, from 5 to 300 |
| `queue_capacity` | `256` | Records each exporter queues, at most 4096 |
| `timeout_seconds` | `2` | Export and history query timeout, at most 30 |
| `endpoint` | unset | Absolute OTLP/HTTP metrics URL, such as `https://collector.example/v1/metrics`. Unset, Core exports nothing and the other export fields must be unset |
| `transport` | unset | `otlp_http`; required with `endpoint` |
| `insecure` | `false` | `true` is required for an `http` endpoint and rejected for `https` |
| `headers` | none | Request headers for the endpoint. `Host`, `Content-Length`, `Content-Type` and `Content-Encoding` are reserved |

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

Set a default in **System** → **Default model configuration**, or use `PUT /core/v1/harnesses/{harness}/model-configuration`. Core encrypts provider keys with `secrets/core/credential.key` and never returns them. [Model execution](../contracts/agents-api/model-execution.md#deployment-defaults) owns the request fields and replacement rules, and [precedence](../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) says which Sessions use a default.

## Compose installations

The [standalone Compose file](./getting-started/install-options.md#docker-compose-and-hosting-platforms) takes process settings from the platform's environment. Set [`OAC_PUBLIC_URL`](#settings) to the exact public origin, without a trailing slash, and recreate Core and Web before adding nodes or executors. The platform terminates TLS and routes to `web:8080`.

The initialization service generates secrets and the installation ID once, then verifies them on subsequent deployments. Each secret has one persistent source; Core's key digest is derived from Web's sign-in key. Initialization never replaces missing or changed secrets on an existing installation. Core reads the process environment from `.env`.

| Data directory path | Content | Readers |
| --- | --- | --- |
| `database/` | PostgreSQL data | PostgreSQL; initialization checks whether it is empty |
| `secrets/database/` | Generated database password | PostgreSQL and Core |
| `secrets/core/` | Credential encryption key, installation ID and Core key digest | Core |
| `secrets/web/` | Generated Core sign-in key | Web |
| `state/` | Private Provider state, mounted in Core at `/state`. Each adapter owns a subdirectory; E2B uses `e2b/`, with no group or other access | Core |
| `node-payload/` | Verified node installation metadata | Web |

Initialization prepares this directory; application services receive their secret directories read-only. `docker compose exec web oac-web core-key` prints the Core key to the operator terminal without writing it to container logs. Database passwords and credential encryption keys are never printed.

The named Docker volume `<project>_data` contains these paths. Docker manages Linux ownership on every host; each service mounts only its required subdirectories. Preserve this volume together with the project definition and public URL. Removing only the secret directories does not reset an installation; initialization refuses to start over an existing database. Core also binds the installation ID to its database. Runtime settings continue to live in [Core's database](#runtime-settings-web).

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

## Agent-host container

The agent host runs each Session's Harness outside the sandbox, in a view of its own ([Run in an agent-host view](../contracts/agents-api/harness-onboarding.md#run-in-an-agent-host-view)). Its container runs the [agent-host image](./maintainers.md#runtime-images-and-helpers) as root and needs Linux 5.14 or newer with cgroup v2:

| Requirement | Docker flag | Used for |
| --- | --- | --- |
| A private cgroup namespace | `--cgroupns=private` | Makes the container's own cgroup the root of a cgroup v2 mount inside it |
| `CAP_SYS_ADMIN` | `--cap-add SYS_ADMIN` | That cgroup v2 mount, the views' mount namespaces and their FUSE world |
| `CAP_NET_ADMIN` | `--cap-add NET_ADMIN` | Each view's loopback-only network |
| `/dev/fuse` | `--device /dev/fuse` | The view's world, the sandbox's files served over the File access protocol |
| No AppArmor profile | `--security-opt apparmor=unconfined` | Docker's default AppArmor profile denies these mounts |

Docker's default seccomp profile stays: with `CAP_SYS_ADMIN` it allows `clone3`, `mount` and `unshare`. The container gets no Docker socket and publishes no port.

The agent host needs a cgroup v2 directory delegated to it. It starts each view in a cgroup of its own there, ends the view with `cgroup.kill` and removes the cgroup. When it starts it ends and removes every cgroup in the directory, because each counts as a view's, so nothing else may use it. The directory must be writable and must not contain the agent host's own process. Docker mounts the container's cgroup read-only. With the flags above, cgroup v2 mounted again inside the container (`mount -t cgroup2 cgroup2 <dir>`) is the container's own cgroup and is writable, and a new directory in it is the delegated directory. Without that delegation the agent host does not start: it fails with `agenthost.ErrUnsupported`, and nothing falls back.

Never set `GODEBUG=http2debug` for the agent host. With it, Go's HTTP/2 implementation logs every header it encodes, including the model and MCP credentials the agent host adds.

## Installation directory

The installer creates `~/.oac/core` by default (`$HOME/.oac/core` on Windows). Its files contain process settings and the native operator command; persistent service data lives in the [Compose data volume](#compose-installations).

| Path | Content | Changed by |
| --- | --- | --- |
| `.env` | Process settings and the stable Compose project name | You, then `oac apply` |
| `compose.yaml`, `compose-sha256sums.txt` | Verified release service definition | The release |
| `oac` (`oac.exe` on Windows) | Native management command | The installer |

The sibling `<install-dir>.lock` directory remains for synchronization; `<install-dir>.staging` holds unpublished installation files. Neither contains service data. On Unix the installer creates private directories with mode `0700` and configuration files with mode `0600`.

The Compose project is named `oac-<10 hex digits>`. Its services are `init`, `database`, `core` and `web`. Core applies database migrations when it starts. Web serves the console and forwards `/v1` and `/api/v1` to Core; it is the only service with a published port, `OAC_WEB_PORT`. No service receives a Docker socket.

## Appendix: Core environment without the installer

Core reads its process environment. Compose interpolates `.env` into it and mounts secrets at the container paths below. When running Core directly, set the file variables to absolute paths readable by the Core process; see the [service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md).

| Variable | Set from |
| --- | --- |
| `OAC_PUBLIC_URL` | The [public URL](#settings). Core validates it once and derives the Agents API base, the daemon WebSocket URL, the self-hosted `remote_url`, the installer downloads, the hosted sandbox address and the deployment's read-only `core_url` from it, never from request headers |
| `OAC_ADDR` | The image sets `:8091`. Independently started Core defaults to `127.0.0.1:8091` when unset or empty |
| `OAC_DATABASE_URL` | Required. PostgreSQL without a password |
| `OAC_DATABASE_PASSWORD_FILE` | `/run/database/password`. The URL must then carry no password |
| `OAC_CREDENTIAL_KEY_FILE` | `/run/oac/credential.key` |
| `OAC_CORE_KEY_DIGESTS_FILE` | Required. `/run/oac/core-key-digests.json`: a JSON array with the SHA-256 of the Core key |
| `OAC_INSTALLATION_ID_FILE` | `/run/oac/installation.id`: the installation ID, a canonical UUID. It enables the sandbox deployment and node routes and requires `OAC_PUBLIC_URL`. Core refuses an ID other than the one its database recorded |
| `OAC_EXECUTION_CONCURRENCY`, `OAC_DEFAULT_HARNESS`, `OAC_HARNESSES`, `OAC_WRITE_AUDIT_RETENTION`, `OAC_OAUTH_TRUSTED_ORIGINS`, `OAC_HISTORY_SETTINGS_FILE`, `OAC_LOG_LEVEL`, `OAC_LOG_FORMAT`, `OAC_LOG_ADD_SOURCE` | The matching [process settings](#settings). Web reads the three log settings too |
| `OAC_PROVIDER_ROOT` | Absolute adapter artifact root. The Core image sets `/opt/oac`. Each adapter owns its helper paths beneath this root. Core serves self-hosted daemon installers from its `native-installers/` directory when that holds a `catalog.json`, after checking the catalog against its own release. Adapter state lives at `/state`, the data volume's [`state/`](#compose-installations) |

Core logs the history file path it loads, never environment values or file contents.

Invalid explicit OAuth trusted origins stop Core at startup. Entries must be HTTPS origins without credentials, query or a non-root path. [Vaults](../contracts/agents-api/vaults.md) owns refresh and network policy. A private issuer also needs a trusted CA: independently managed Unix Core can use Go’s `SSL_CERT_FILE` PEM CA-bundle override, which preserves certificate verification. Managed installation has no custom-CA setting.

## Appendix: Web environment without the installer

Compose sets these for Web. Set them yourself only when you run the console without Compose. Compose mounts the data volume's `secrets/web/` at `/run/oac` and sets `OAC_WEB_CORE_KEY_FILE=/run/oac/core.key`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | Listener address. The healthcheck probes it on `127.0.0.1` when its host is empty or unspecified |
| `OAC_PUBLIC_URL` | Required | The [public URL](#settings): the exact browser-facing origin, HTTP or HTTPS, without a path. Host and origin checks use it; HTTPS makes the session cookie `Secure` |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core's origin, HTTP or HTTPS, without credentials, query or path. The healthcheck probes its `/healthz` |
| `OAC_WEB_CORE_KEY_FILE` | Required | Absolute path of a regular file with no group or other permissions, holding the Core key: at least 32 characters, no whitespace, at most 4 KiB |
| `OAC_WEB_DIST` | `/www` | Absolute directory of the built console; must contain `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | unset | Absolute path of the matched distribution's node payload (the installer's `node-payload/`). Unset, `/node-install/*` is not served and Add node is unavailable |

An unset or empty variable selects its default. An invalid value stops the console at startup with a message naming the variable. The console also reads the three log [process settings](#settings) and rejects the values Core rejects. Use HTTPS for any browser that is not on the same machine.
