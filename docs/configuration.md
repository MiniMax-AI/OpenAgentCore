---
title: "Configuration reference"
---

Every setting of a Core installation, where it lives and how to change it. The installer writes the initial values; use this reference afterwards. Each setting has exactly one home, in one of three categories:

| Category | Examples | Home | Change it with | Takes effect |
| --- | --- | --- | --- | --- |
| [Process settings](#process-settings) | Public URL, ports, logging, Harnesses, execution concurrency, audit retention, OAuth origins, Runtime history | `.env` in the installation directory (default `~/.oac/core`) | Edit `.env`, then run `oac apply` | `oac apply` recreates the services that read the changed settings |
| [Secrets](#compose-installations) | Database password, credential encryption key, installation ID, Core key and the Core key digest derived from it | `secrets/` in the Compose data volume, one copy each | Initialization generates them once; `oac rotate-core-key` replaces the Core key and its digest | `oac rotate-core-key` restarts Core and Web |
| [Runtime settings](#runtime-settings-web) | Sandbox backend and size, nodes, Projects and keys, default models, executor credentials | Core's PostgreSQL database | Web, or the Core API (`/core/v1`) with the Core key | Saved without a Core restart; nodes prepare Runtime changes asynchronously |

**Settings** is Web's single entry for platform configuration, with **Sandbox and scheduling**, **Workspace storage**, **Models and credentials** and **Service and deployment** groups. Each setting has one editor; monitoring and node pages link to it. Forms save independently through their owning API, without a cross-resource save. **Service and deployment** shows the installation's addresses and the process settings Core loaded; process settings remain read-only in Web. Projects, API keys and Session executor credentials remain on their resource pages. No configuration file defines Projects or API keys.

## Process settings

Installer flags in [Installation options](./getting-started/install-options.md#flags) write `.env` once. To change a setting, edit `.env` and apply it:

```sh
~/.oac/core/oac apply
```

### Settings

Model providers are not process settings; see [Default models](#default-models).

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | Required; `compose.yaml` sets `http://localhost:` followed by `OAC_WEB_PORT` | Origin applications, nodes, sandboxes and self-hosted executors use. See [Change the public URL](./getting-started/public-address.md#changing-the-public-url) |
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

### How oac apply works {#how-oac-apply-works}

1. It runs the `init` service, which checks that the data volume belongs to the installed release and finishes initializing it if the first start stopped early.
2. It runs `oac-core check-config` with the `.env` you edited and changes no service if a value is invalid.
3. It runs `docker compose up -d --wait` with the images already on the host. Compose recreates only the services whose configuration changed and removes containers the release no longer defines. If a service does not become healthy, the message names it and the command that prints its log.

Use `docker compose ps` to check the services. See [stop and restart](./getting-started/operations.md#stop-and-restart) for what a restart interrupts.

## Runtime settings: Web

Runtime settings live in Core's database. Change them in Web; scripts use the same Core API with the Core key.

| Setting | Where in Web | Core API | Notes |
| --- | --- | --- | --- |
| Sandbox backend: Docker, microsandbox or E2B | **Settings** → **Sandbox and scheduling**: the setup wizard, ending with **Save configuration** | `/core/v1/sandbox/deployment` | One backend per installation, chosen after the first sign-in. Another backend needs **Reset deployment** first; see [change the sandbox configuration](./getting-started/nodes.md#change-the-sandbox-configuration) |
| Sandbox size, Runtime release, E2B key and template build | **Settings** → **Sandbox and scheduling** → **Change resources** | `/core/v1/sandbox/deployment` | Web proposes the [default size the Provider declares](./sandbox-provider.md#register-the-provider-kind). Existing sandboxes keep their size and release. The E2B key is write-only and encrypted |
| Nodes and their capacity | **Nodes**: **Add node**, **Edit node** and removal | `/core/v1/sandbox/enrollment-tokens`, `/core/v1/sandbox/nodes` | See [Node capacity](#node-capacity) and [Add and manage nodes](./getting-started/nodes.md) |
| Idle suspension | **Settings** → **Sandbox and scheduling** → **Automatic suspension** → **Idle before suspension (seconds)** → **Save** | `PATCH /core/v1/sandbox/deployment` | Deployment-wide; see [suspension policy](../contracts/agents-api/sandbox-deployment.md#suspension-policy) |
| Independent workspace storage and tenant capacity | **Settings → Workspace storage** | `/core/v1/workspace-storage` | See [Set up workspace storage](./getting-started/workspace-storage.md); existing workspaces retain their configuration |
| Projects and API keys | **Projects and keys**: **Create project**, **Rename**, **Issue key**, **Revoke**, **Archive** | `/core/v1/projects` | Keys are shown once; Core stores digests |
| Default model per Harness | **Settings** → **Models and credentials**: **Set** | `/core/v1/harnesses/{harness}/model-configuration` | See [Default models](#default-models) |
| Executor credentials of a self-hosted Session | **Session log**, then the **Session** page: **Executor credentials** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | See [self-hosted executors](./getting-started/self-hosted.md) |

Which Harnesses are enabled, and the default one, are process settings (`OAC_HARNESSES`, `OAC_DEFAULT_HARNESS`); **Settings → Service and deployment** lists them under **Startup settings**. **Settings → Models and credentials** marks each Harness **Ready** once it is enabled and has a default model, and otherwise names the missing step: **Model provider not set** or **Disabled**. The [Core administration API](../contracts/agents-api/admin-api.md) lists every Core API route, and the [deployment contract](../contracts/agents-api/sandbox-deployment.md) defines the sandbox fields, limits and change rules.

### Node capacity

Core approves a node's capacity when you generate its Add node command: **Sandboxes at once** (`max_active`, default 2) and, for microsandbox only, **Retained sandboxes** (`max_retained`, default 8), with `max_retained >= max_active >= 1`. Docker never suspends sandboxes, so Web doesn't ask for it and Core keeps `max_retained` equal to `max_active`. Change them later on **Nodes** with **Edit node**. Reservations and cleanup that is not confirmed count against capacity; lowering a limit stops no running sandbox. A node's own files can't change its capacity, size or Runtime.

`OAC_EXECUTION_CONCURRENCY` is unrelated: it limits concurrent execution work in Core.

### Default models

Set a default in **Settings** → **Models and credentials**, or use `PUT /core/v1/harnesses/{harness}/model-configuration`. Core encrypts provider keys with `secrets/core/credential.key` and never returns them. [Model execution](../contracts/agents-api/model-execution.md#deployment-defaults) owns the request fields and replacement rules, and [precedence](../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) says which Sessions use a default.

### Tenant capacity

`tenant_capacity_mib` is the installation's single tenant capacity policy, written only together with the selection by `PUT /core/v1/workspace-storage` and stored in Core's database. A positive value requires a quota mode configuration, and a quota mode configuration requires a positive value. It bounds the sum of admitted capacities of one tenant's workspaces that are not yet confirmed deleted, across configuration changes; [capability validation](./workspace-provider.md#capability-validation) owns the counting rules. Lowering the value refuses new workspaces only; deleting Sessions frees capacity once their storage is removed.

Session creation can succeed before this asynchronous workspace admission. While capacity is exhausted, Core retries without allocating the refused workspace or its compute; pending input keeps its original [Environment input deadline](../contracts/agents-api/environments.md#reservations). An idle Session created without input has no input deadline and waits for capacity.

Workspaces created without quota mode remain unbounded; nothing resizes or migrates them. While one is retained, its tenant cannot admit new workspaces under a positive policy until that Session is deleted.

The policy bounds what each tenant can commit, not the filesystem. Size the XFS filesystem for the number of tenants times the policy plus headroom for unbounded workspaces, identities, deletion markers, the journal and XFS metadata, or oversubscribe knowingly and monitor free space. Keep each node's `checkpoint_root` on separate storage. Capacity bounds provide no I/O fairness.

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

The node installer writes Docker’s host settings into the `native` object of the node’s configuration file, which only the Docker adapter reads. Deployment resources, the Runtime release and capacity remain in [Core’s database](#runtime-settings-web).

| Field | Installer value | Meaning |
| --- | --- | --- |
| `host` | `unix:///var/run/docker.sock` | Explicit Docker Engine socket |
| `image` | The Runtime image’s local ID after loading | The release’s `image_id` or `image_manifest_digest`. The host’s image store decides which digest names the loaded image, so the value is node-local; the adapter accepts only these two |
| `network` | `oac-node-<installation-id>` | Runtime container network |
| `seccomp_file` | `<node-root>/runtime/seccomp.json` | Matched distribution’s seccomp profile |
| `nested_sandbox` | `true` | Enables the Docker adapter’s init process and proc-mask configuration |

The [Docker adapter](./sandbox-provider.md#docker-adapter) owns container isolation, volume layout and lifecycle behavior.

## Installation directory

The installer creates `~/.oac/core` by default (`$HOME/.oac/core` on Windows). Its files contain process settings and the native operator command; persistent service data lives in the [Compose data volume](#compose-installations).

| Path | Content | Changed by |
| --- | --- | --- |
| `.env` | Process settings and the stable Compose project name | You, then `oac apply` |
| `compose.yaml` | Verified release service definition | The installer, when it installs or [upgrades](./getting-started/operations.md#upgrade) |
| `compose.override.yaml` | Optional local Compose entries. For a source bundle it is JSON and also holds the four service images and Web's `/node-payload` bind | You. `install.py` replaces only its own entries and requires JSON. `oac install --upgrade` removes the `/node-payload` bind and the host `node-payload/` directory, because a release serves its payload from the data volume |
| `oac` (`oac.exe` on Windows) | Native management command | The installer |

The sibling `<install-dir>.lock` directory remains for synchronization; `<install-dir>.staging` holds unpublished installation files. Neither contains service data. On Unix the installer creates private directories with mode `0700` and configuration files with mode `0600`.

The Compose project is named `oac-<10 hex digits>`. Its services are `init`, `database`, `core` and `web`. Core applies database migrations when it starts. Web serves the console and is the only service with a published port, `OAC_WEB_PORT`; see [Request boundary](./web/console-server.md#request-boundary) for what it forwards. No service receives a Docker socket, and Web and Core have no KVM access.

## Independent workspace storage {#independent-workspace-storage}

The supported independent filesystem combination is microsandbox with the [kernel NFS adapter](./workspace-provider.md#kernel-nfs-adapter). Select it in **Settings → Workspace storage** (`/core/v1/workspace-storage`, see the [Core administration API](../contracts/agents-api/admin-api.md#workspace-storage)); the selection and its tenant capacity apply to new workspaces, and existing workspaces keep their configuration. [Set up workspace storage](./getting-started/workspace-storage.md) covers the NFS export, the `oac-node` account, the Compose mount, the selection payloads and the quota mode storage server.

## Appendix: Core environment without the installer

Core reads its process environment. Compose interpolates `.env` into it and mounts secrets at the container paths below. When running Core directly, set the file variables to absolute paths readable by the Core process; see the [service guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md).

| Variable | Set from |
| --- | --- |
| `OAC_PUBLIC_URL` | Required. The [public URL](#settings). Core validates it once and derives the Agents API base, the daemon WebSocket URL, the self-hosted `remote_url`, the installer downloads, the hosted sandbox address and the deployment's read-only `core_url` from it, never from request headers |
| `OAC_ADDR` | The image sets `:8091`. Independently started Core defaults to `127.0.0.1:8091` when unset or empty. `oac-core healthcheck` probes its `/healthz` on `127.0.0.1` when its host is empty or unspecified |
| `OAC_DATABASE_URL` | Required. PostgreSQL without a password |
| `OAC_DATABASE_PASSWORD_FILE` | `/run/database/password`. The URL must then carry no password |
| `OAC_CREDENTIAL_KEY_FILE` | Required. `/run/oac/credential.key`: a base64-encoded random 32-byte key. Core seals stored credentials with it |
| `OAC_CORE_KEY_DIGESTS_FILE` | Required. `/run/oac/core-key-digests.json`: a JSON array with the SHA-256 of the Core key |
| `OAC_INSTALLATION_ID_FILE` | Required. `/run/oac/installation.id`: the installation ID, a canonical UUID. Core refuses an ID other than the one its database recorded |
| `OAC_EXECUTION_CONCURRENCY`, `OAC_DEFAULT_HARNESS`, `OAC_HARNESSES`, `OAC_WRITE_AUDIT_RETENTION`, `OAC_OAUTH_TRUSTED_ORIGINS`, `OAC_HISTORY_SETTINGS_FILE`, `OAC_LOG_LEVEL`, `OAC_LOG_FORMAT`, `OAC_LOG_ADD_SOURCE` | The matching [process settings](#settings). Web reads the three log settings too |
| `OAC_PROVIDER_ROOT` | Absolute adapter artifact root. The Core image sets `/opt/oac`. Each adapter owns its helper paths beneath this root. Core serves self-hosted daemon installers from its `native-installers/` directory when that holds a `catalog.json`, after checking the catalog against its own release. Adapter state lives at `/state`, the data volume's [`state/`](#compose-installations) |

Core logs the history file path it loads, never environment values or file contents.

Invalid explicit OAuth trusted origins stop Core at startup. Entries must be HTTPS origins without credentials, query or a non-root path. [Vaults](../contracts/agents-api/vaults.md) owns refresh and network policy. A private issuer also needs a trusted CA: independently managed Unix Core can use Go’s `SSL_CERT_FILE` PEM CA-bundle override, which preserves certificate verification. Managed installation has no custom-CA setting.

## Appendix: Web environment without the installer

Compose sets these for Web. Set them yourself only when you run the console without Compose. Compose mounts the data volume's `secrets/web/` at `/run/oac` and sets `OAC_WEB_CORE_KEY_FILE=/run/oac/core.key`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | Listener address. `oac-web healthcheck` probes its `/healthz` on `127.0.0.1` when its host is empty or unspecified |
| `OAC_PUBLIC_URL` | Required | The [public URL](#settings): the exact browser-facing origin, HTTP or HTTPS, without a path. Host and origin checks use it; HTTPS makes the session cookie `Secure` |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core's origin, HTTP or HTTPS, without credentials, query or path |
| `OAC_WEB_CORE_KEY_FILE` | Required | Absolute path of a regular file with no group or other permissions, holding the Core key: at least 32 characters, no whitespace, at most 4 KiB |
| `OAC_WEB_DIST` | `/www` | Absolute directory of the built console; must contain `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | unset | Absolute path of the matched distribution's node payload (the installer's `node-payload/`). Unset, `/node-install/*` is not served and Add node is unavailable |

An unset or empty variable selects its default. An invalid value stops the console at startup with a message naming the variable. The console also reads the three log [process settings](#settings) and rejects the values Core rejects. Use HTTPS for any browser that is not on the same machine.

## Appendix: Runtime history file {#runtime-history-file}

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

## Appendix: Runtime resource directories {#runtime-resource-directories}

The Runtime resolves its resource directories before discovering Harness adapters. `<RuntimeHome>` is the Runtime home selected by `OAC_RUNTIME_HOME`. `OAC_RUNTIME_STATE_DIRECTORY` defaults only when unset; an empty, relative or noncanonical explicit value is rejected. Directory settings have no secondary file or environment fallback.

| Runtime process setting | Default | Packaged Linux images |
| --- | --- | --- |
| `OAC_RUNTIME_INITIALIZATION_DIRECTORY` | `<RuntimeHome>/initialization` | `/environment/initialization` |
| `OAC_RUNTIME_PACKAGE_DIRECTORY` | `<RuntimeHome>/packages` | `/environment/packages` |
| `OAC_RUNTIME_STATE_DIRECTORY` | `<RuntimeHome>` | `/environment/runtime-state` |

The state directory contains retained native Harness history and capability-installation completion records. It is separate from the declared workspace directory. Selecting a separate state directory does not relocate Runtime device credentials or connection identity from their instance-private paths in the Runtime home; they must not be copied with native history. [Harness onboarding](../contracts/agents-api/harness-onboarding.md#register-the-adapter) defines the adapter boundary, and [Environment preparation](../contracts/agents-api/environments.md) owns initialization and package behavior.

Placing state on an independent filesystem preserves those files when compute is replaced. Native continuation still requires a qualified Harness, matching ownership and confirmed stop of the previous writer. It does not preserve process memory or arbitrary files on the compute root disk.
