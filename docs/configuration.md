# Configuration reference

Every setting of a Core installation has exactly one home. There are two kinds:

| Kind | Examples | Home | Change it with | Takes effect |
| --- | --- | --- | --- | --- |
| [Process settings](#process-settings-configjson) | Public URL, ports, logging, harnesses, execution concurrency, audit retention, OAuth origins, database pool, Runtime history export | `config.json` in the installation directory (default `~/.oac/core`) | Web domain setup or `oac domain` for managed HTTPS; otherwise edit the file, then run `oac apply` | `oac apply` restarts the services that read the changed settings |
| [Runtime settings](#runtime-settings-web) | Sandbox backend and size, nodes, Projects and keys, default models, executor credentials | Core's PostgreSQL database | Web, or the Core API (`/core/v1`) with the Core key | Saved without a Core restart; nodes prepare Runtime changes asynchronously |

Web's **System** page shows the installation's addresses, the default models, the sandbox configuration and, under **Startup settings**, the process settings read-only with the path of `config.json` and the apply command. Secrets live in [`secrets/`](#installation-directory), one copy each. The files in `generated/` are derived from `config.json`. No configuration file defines Projects or API keys.

## Process settings: config.json

The installer writes every setting that applies to the installation's [mode](getting-started/install-options.md#modes), so the file shows each value. Installer flags listed in [installation options](getting-started/install-options.md) only seed it. To change a setting, edit the file and apply it:

```sh
~/.oac/core/oac apply --dry-run   # show the changed settings, files and restarts
~/.oac/core/oac apply
```

### How oac apply works

1. It validates `config.json` and changes nothing if a value is invalid. `mode`, `native_core` and `ingress` are fixed after installation; to change them, install into a new directory. It also checks the listeners that a changed `host`, port or managed-ingress `public_url` adds ([ports](getting-started/install-options.md#ports)), and changes nothing if `host` is not an address of this machine or another program holds one of their ports; the installation's own listeners do not count.
2. It writes the files Core, Web and Compose read into `generated/`: `compose.json`, `core.env`, `core-key-digests.json`, `settings.json` and, when used, `runtime-history.json`, the managed `Caddyfile` and the native Core unit. Don't edit them. A generated file edited by hand stops `apply` until you move the change into `config.json` and run `oac apply --discard-edits`, which keeps the edited copy as `generated/<file>.edited-<time>`.
3. It compares what it wrote with what actually runs. Each container carries a digest of its inputs (the `io.oac.inputs` label; native Core carries `OAC_INPUTS`), and `apply` recreates or restarts exactly the services whose inputs differ: Core first, then Web. The **Restarts** column below says which services a setting affects; see [stop and restart](getting-started/operations.md#stop-and-restart) for what a restart interrupts.
4. While any service runs, `apply` also starts the stopped ones. After `oac stop`, it only writes the files and the installation stays stopped.
5. If Core refuses a value at startup, `apply` prints Core's startup error. If every service was running with the previous files, it restores them and starts the services again; otherwise it reports the failure, and the next `apply` finishes the work.

`oac status` reports changes to `config.json` that are not applied yet and generated files edited by hand.

### Changing the public URL

`public_url` is the one origin that applications, nodes, sandboxes and self-hosted executors use. Core derives the daemon WebSocket URL, the self-hosted `remote_url` and each sandbox's connection address from it. With `null`, Core uses its loopback origin. Until `public_url` is set, a managed installation serves only Web, over HTTP on the host's IP address at `ports.web`; afterwards that address redirects to `public_url`.

With managed ingress, change it in Web (**System** → **Configure domain and HTTPS**) or with `oac domain HOSTNAME`. Both take the installation lock, verify the new certificate and address, then write `config.json` and apply it. With external ingress, update your reverse proxy first, then edit `public_url` and run `oac apply`.

When nodes, hosted sandboxes or self-hosted executors are bound to the current address, `apply` lists them and asks you to type the new URL (`--confirm-public-url-change URL` without a terminal). Afterwards:

- Nodes on the old address get no new sandboxes: remove them in Web and add them again.
- Existing sandboxes and executors keep working only while the old address still reaches this Core. A managed domain change replaces the previous domain route.
- Self-hosted executors must restart with the new `remote_url`, and their installer refuses an installation made for the old address: create new self-hosted Sessions and connect their hosts again.

### Settings

`core.runtime_history.headers` may hold export credentials. They stay in the `0600` `config.json` and the generated file Core reads, and never appear in `oac` output or in Core's settings snapshot. Model providers are not process settings; see [Default models](#default-models).

<!-- BEGIN config-reference: generated by scripts/config-reference.py from deploy/install/config.schema.json -->
| Key | Type | Default | Modes | Change | Restarts | Meaning |
| --- | --- | --- | --- | --- | --- | --- |
| `$schema` | string | none | all | any time | none | Editor hint that points at the installed copy of this schema. Ignored. |
| `format` | `1` | none | all | fixed | none | Configuration format for this release. Fixed after installation. |
| `mode` | `"all"` \| `"core-only"` \| `"web-only"` | `"all"` | all | fixed | none | Which services this installation runs. |
| `native_core` | boolean | `false` | `all`, `core-only` | fixed | none | Run Core as a systemd user service instead of a container. |
| `public_url` | string or null (canonical origin; HTTP only on loopback) | `null` | all | `oac apply` | core, web | Canonical public origin of Core and Web. With managed ingress, set the DNS hostname in Web or run oac domain; certificates are automatic. With external ingress, configure your TLS reverse proxy before applying this value. |
| `host` | string (IPv4 or IPv6 address) | `"127.0.0.1"` | all | `oac apply` | core, web | Listener IP. With managed ingress only the gateway is public; Core stays on loopback. The default combined installer listens on all IPv4 interfaces. |
| `ports.core` | integer 1024–65535 | `8091` | `all`, `core-only` | `oac apply` | core (core, web with native Core) | Host port of the Core API. With native Core, Web follows it. |
| `ports.web` | integer 1024–65535 | `8080` | `all`, `web-only` | `oac apply` | web | Host port of Web. |
| `ports.database` | integer 1024–65535 | none | `all`, `core-only` | `oac apply` | database, core | Loopback port of PostgreSQL. Present exactly when native_core is true; the installer picks a free port. |
| `web.core_url` | string (canonical origin; HTTP only on loopback) | none | `web-only` | `oac apply` | web | Origin of the Core that this Web connects to: HTTPS, or HTTP on a loopback host. |
| `log.level` | `"debug"` \| `"info"` \| `"warn"` \| `"error"` | `"info"` | all | `oac apply` | core, web | Minimum log level of Core and Web. |
| `log.format` | `"auto"` \| `"text"` \| `"json"` | `"auto"` | all | `oac apply` | core, web | Log format. auto writes text to a terminal and JSON otherwise. |
| `log.add_source` | boolean | `false` | all | `oac apply` | core, web | Add the source file and line to each log record. |
| `core.execution_concurrency` | integer 1–1024 | `4` | `all`, `core-only` | `oac apply` | core | Concurrent execution work units in Core. Unrelated to node sandbox capacity. |
| `core.harnesses` | array of `"claude_sdk"` \| `"codex"` \| `"mcode"` | `["claude_sdk", "codex", "mcode"]` | `all`, `core-only` | `oac apply` | core | Harnesses that Sessions may select. |
| `core.default_harness` | `"claude_sdk"` \| `"codex"` \| `"mcode"` | `"codex"` | `all`, `core-only` | `oac apply` | core | Harness used when a Session names none. It must be listed in core.harnesses. |
| `core.write_audit_retention` | string (Go duration, at least `1h`) | `"2160h"` | `all`, `core-only` | `oac apply` | core | How long non-creation write history is kept, as a Go duration of at least 1h. |
| `core.oauth_trusted_origins` | array of string (canonical HTTPS origin) | `[]` | `all`, `core-only` | `oac apply` | core | Extra HTTPS origins trusted as private OAuth issuers. |
| `core.database_pool.max_conns` | integer or null ≥ 1 | `null` | `all`, `core-only` | `oac apply` | core | Maximum database connections. null keeps the driver default, max(4, CPU count). |
| `core.database_pool.min_conns` | integer or null ≥ 0 | `null` | `all`, `core-only` | `oac apply` | core | Minimum idle database connections. null keeps the driver default, 0. |
| `core.database_pool.max_conn_lifetime` | string or null (Go duration) | `null` | `all`, `core-only` | `oac apply` | core | Go duration. null keeps the driver default, 1h. |
| `core.database_pool.max_conn_idle_time` | string or null (Go duration) | `null` | `all`, `core-only` | `oac apply` | core | Go duration. null keeps the driver default, 30m. |
| `core.database_pool.health_check_period` | string or null (Go duration) | `null` | `all`, `core-only` | `oac apply` | core | Go duration. null keeps the driver default, 1m. |
| `core.runtime_history` | object or null | `null` | `all`, `core-only` | `oac apply` | core | Runtime history collection and OTLP export. null keeps local collection with Core's defaults. Core checks the values at startup. |
| `core.runtime_history.transport` | string | none | `all`, `core-only` | `oac apply` | core | OTLP export transport: `otlp_http`. Required with endpoint. |
| `core.runtime_history.endpoint` | string | none | `all`, `core-only` | `oac apply` | core | OTLP/HTTP metrics URL: a canonical absolute URL with a path and no credentials, query or fragment; HTTPS, or HTTP with insecure. Omit it to keep history local; transport, insecure and headers then must be absent. |
| `core.runtime_history.insecure` | boolean | none | `all`, `core-only` | `oac apply` | core | Export without TLS. Required for an HTTP endpoint and refused for HTTPS. |
| `core.runtime_history.headers` | object of string values | none | `all`, `core-only` | `oac apply` | core | Headers sent with each export, such as credentials. Host, Content-Length, Content-Type and Content-Encoding are refused. Never shown by oac or Core. Sensitive. |
| `core.runtime_history.queue_capacity` | integer | none | `all`, `core-only` | `oac apply` | core | Capacity of the history write queue and of the export queue, up to 4096. Omitted or 0 selects 256. |
| `core.runtime_history.timeout_seconds` | integer | none | `all`, `core-only` | `oac apply` | core | History write, export and query timeout in seconds, up to 30. Omitted or 0 selects 2. |
| `core.runtime_history.sample_interval_seconds` | integer | none | `all`, `core-only` | `oac apply` | core | Periodic sampling interval in seconds, 5 to 300. Omitted or 0 selects 30. |
| `ingress` | `"managed"` \| `"external"` | `"external"` | all | fixed | none | managed provides automatic HTTPS and Web domain setup for a combined Docker installation; install.sh selects it by default. external uses your existing proxy. Fixed after installation. |
<!-- END config-reference -->

The schema is [`deploy/install/config.schema.json`](../deploy/install/config.schema.json); each installation keeps a copy in `generated/config.schema.json` for editors. Core serves the non-secret settings snapshot, with the path of `config.json` and the apply command, at `GET /core/v1/installation`. How Core collects and keeps Runtime history is in [retained history](../contracts/agents-api/runtime-observability.md#retained-history-and-optional-export).

## Runtime settings: Web

Runtime settings live in Core's database. Change them in Web; scripts use the same Core API with the Core key.

| Setting | Where in Web | Core API | Notes |
| --- | --- | --- | --- |
| Sandbox backend: Docker, microsandbox or E2B | **System** → **Manage sandbox configuration**: the setup wizard, ending with **Save configuration** | `/core/v1/sandbox/deployment` | One backend per installation. `install.sh --sandbox` saves the first choice. Another backend needs **Reset deployment** first; see [change the sandbox configuration](getting-started/nodes.md#change-the-sandbox-configuration) |
| Sandbox size, Runtime release, E2B key and template build | **System** → **Manage sandbox configuration** → **Change resources** | `/core/v1/sandbox/deployment` | Web proposes the sizes in [`standard-sizes.json`](../apps/web/src/features/sandbox/standard-sizes.json). Existing sandboxes keep their size and release. The E2B key is write-only and encrypted |
| Nodes and their capacity | **Nodes**: **Add node**; **Edit node** and **Remove node** on a node's page | `/core/v1/sandbox/enrollment-tokens`, `/core/v1/sandbox/nodes` | See [Node capacity](#node-capacity) and the [nodes guide](getting-started/nodes.md) |
| Projects and API keys | **Projects and keys**: **Create project**, **Rename**, **Issue key**, **Revoke**, **Archive** | `/core/v1/projects` | Keys are shown once; Core stores digests |
| Default model per harness | **System** → **Default model configuration**: **Set** | `/core/v1/harnesses/{harness}/model-configuration` | See [Default models](#default-models) |
| Executor credentials of a self-hosted Session | **Session log**, then the **Session** page: **Executor credentials** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | See [self-hosted executors](getting-started/self-hosted.md) |

Which harnesses are enabled, and the default one, are process settings (`core.harnesses`, `core.default_harness`); System shows them read-only. The [API index](api/README.md#core-api) lists every Core API route, and the [deployment contract](../contracts/agents-api/sandbox-deployment.md) defines the sandbox fields, limits and change rules.

### Node capacity

Core approves a node's capacity when you generate its Add node command: **Sandboxes at once** (`max_active`, default 2) and, for microsandbox only, **Retained sandboxes** (`max_retained`, default 8), with `max_retained >= max_active >= 1`. Docker never suspends sandboxes, so Web doesn't ask for it and Core keeps `max_retained` equal to `max_active`. Change them later with **Edit node**. Reservations and cleanup that is not confirmed count against capacity; lowering a limit stops no running sandbox. A node's own files can't change its capacity, size or Runtime.

`core.execution_concurrency` is unrelated: it limits concurrent execution work in Core.

### Default models

Set a default in **System** → **Default model configuration**, or use `PUT /core/v1/harnesses/{harness}/model-configuration`. Core encrypts provider keys with `secrets/credential.key` and never returns them. [Model execution](../contracts/agents-api/model-execution.md#deployment-defaults) owns the request fields and replacement rules, and [precedence](../contracts/agents-api/model-execution.md#saved-defaults-and-precedence) says which Sessions use a default.

## Installation directory

The installer creates the installation directory, `~/.oac/core` by default, with mode `0700`; the files in `secrets/` are `0600`.

| Path | Content | Changed by |
| --- | --- | --- |
| `config.json` | [Process settings](#process-settings-configjson). The only file you edit | You, then `oac apply`; managed domain setup for `public_url` |
| `oac` | The [management command](getting-started/operations.md#the-oac-command) | The installer |
| `state.json` | Installation ID, Compose project name, image IDs, source commit and the digests of generated files | The tools only |
| `secrets/core.key` | The [Core key](getting-started/operations.md#core-key) | `oac rotate-core-key` |
| `secrets/credential.key` | Encryption key for what Core stores sealed in the database: model providers, the E2B key, Vault credentials, Skills, initial files and environment setup | Nothing. Keep it with the database; `oac apply` refuses a changed file |
| `secrets/database.password` | PostgreSQL password | Nothing. PostgreSQL reads it only when the database is created; `oac apply` refuses a changed file |
| `generated/` | Files derived from `config.json` | `oac apply` |
| `node-payload/` | The node installer and node files that Web serves at `/node-install/`, one directory per release | The installer |
| `native-installers/` | Self-hosted daemon installers for each platform, with their catalog, when the bundle carries them | The installer |
| `state/e2b/` | Private E2B receipts | Core |
| `ingress/` | Managed HTTPS: certificates and gateway state, domain setup status and the control sockets | The `gateway` and `installation` services |
| `native/` | Core binaries, with native Core | The installer |
| `.oac.lock` | The installation lock | The installer and mutating `oac` commands |

A Web-only installation has only `secrets/core.key`, a copy of its Core's key, and no `state/` or `native-installers/`. A Core-only installation has no `node-payload/`. Only managed ingress has `ingress/`. With native Core, the installation directory and the bundle must be canonical absolute paths without control characters, quotes, backslashes or wildcards.

In Docker, the Compose project is named `oac-<10 hex digits>` (`project` in `state.json`). Its services are `database`, `migrate`, `core` and `web`, plus `gateway` and `installation` with managed ingress. `gateway` routes to Core and Web and publishes `ports.web`, plus [80 and 443](getting-started/install-options.md#ports) once HTTPS is on; `installation` applies domain changes from Web and uses the Docker socket to do so. The volume `<project>_database` holds all data. Native Core runs Core as the systemd user unit `<project>-core.service` instead of a container. Apart from Docker's storage, nothing is written outside your home directory.

## Appendix: Core environment without the installer

Core reads only its environment. The installer renders `generated/core.env` from `config.json`; if you run Core yourself (see [Maintainers and advanced deployments](maintainers.md)), set these variables. Compose loads the file with `env_file` and systemd with `EnvironmentFile`, so Compose must be 2.26.0 or newer.

| Variable | Set from |
| --- | --- |
| `OAC_PUBLIC_URL` | `public_url`, or Core's loopback origin. Core derives the daemon WebSocket URL, the self-hosted `remote_url`, the hosted sandbox address and the deployment's read-only `core_url` from it, never from request headers. Without it, Core runs no Runtime gateway and executes no Sessions |
| `OAC_ADDR` | `ports.core` (native Core) or `:8091` in the container |
| `OAC_DATABASE_URL` | The installation's PostgreSQL without a password, plus `core.database_pool` as `pool_*` query parameters |
| `OAC_DATABASE_PASSWORD_FILE` | `secrets/database.password`. The URL must then carry no password; migrations and the maintenance commands read the file too |
| `OAC_CREDENTIAL_KEY_FILE` | `secrets/credential.key` |
| `OAC_CORE_KEY_DIGESTS_FILE` | `generated/core-key-digests.json`: a JSON array with the SHA-256 of the Core key |
| `OAC_INSTALLATION_ID` | The installation ID from `state.json`, a canonical UUID. It enables the sandbox deployment and node routes and requires `OAC_PUBLIC_URL` and `OAC_CORE_KEY_DIGESTS_FILE`. Core refuses an ID other than the one its database recorded, so keep the two together |
| `OAC_SETTINGS_FILE` | `generated/settings.json`, the snapshot Core serves at `GET /core/v1/installation`; Core does not act on it |
| `OAC_EXECUTION_CONCURRENCY`, `OAC_DEFAULT_HARNESS`, `OAC_HARNESSES`, `OAC_WRITE_AUDIT_RETENTION`, `OAC_OAUTH_TRUSTED_ORIGINS` | The matching `core.*` settings |
| `OAC_HISTORY_SETTINGS_FILE` | `generated/runtime-history.json`: the [`core.runtime_history`](#settings) object, when it is set |
| `OAC_LOG_LEVEL`, `OAC_LOG_FORMAT`, `OAC_LOG_ADD_SOURCE` | `log.*`; Web reads the same three |
| `OAC_E2B_STATE_DIR` | `state/e2b/`: an absolute directory owned by Core's user, with no group or other access. Back it up with the database and `credential.key`; don't mount it into Web or a Runtime |
| `OAC_E2B_PROVIDER_BIN` | The E2B helper: `/opt/oac/e2b/oac-e2b-provider` in the Core image, `native/e2b/oac-e2b-provider` for native Core. The E2B key and template live in the database |
| `OAC_NATIVE_INSTALLER_DIR` | `native-installers/`, served to self-hosted machines; the Core image has a copy at `/opt/oac/native-installers`, used when this is unset. Core checks the catalog against its own release before serving it |

Core logs the file paths it loads, never environment values or file contents.

A Web you run without the installer reads the variables in [Console server settings](web/console-server.md#settings), plus `OAC_WEB_NODE_PAYLOAD_DIR`: the absolute path of the matched distribution's node payload (the installer's `node-payload/`). Without it, Add node is unavailable.
