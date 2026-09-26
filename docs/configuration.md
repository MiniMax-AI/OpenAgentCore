# Configuration

This is the configuration reference for Core, Web, hosted nodes and the execution
daemon. Installation and operations guides describe workflows and link here for
parameters. Each setting has one home; reinstalling does not restore defaults.

## Ownership and changes

| Settings | Home | How changes take effect |
| --- | --- | --- |
| Core and Web process settings: public URL, ports, logging, execution concurrency, harnesses, audit retention, OAuth origins, database pool, Runtime history | `<installation>/config.json` | Edit the file, then run `<installation>/parsar apply` |
| Core key | `<installation>/secrets/core.key` | `parsar rotate-core-key` ([rotation](getting-started/operations.md#rotate-the-core-key)) |
| Credential encryption key and database password | `<installation>/secrets/` | Fixed after installation; `parsar apply` refuses a changed file |
| Hosted provider, uniform guest resources, immutable Runtime specification, E2B credentials | Core PostgreSQL | Web or deployment API; existing maintenance/generation checks |
| Node registration and sandbox capacity | Core PostgreSQL | Administrator enrollment and node updates |
| Node host paths, Docker socket and local network wiring | Node `provider.json` | Edit host-local fields; restart the node |
| Downloaded deployment specification | `specification` in node `provider.json` | Validated copy only; must match Core's digest |
| Daemon connection, identity and execution preparation | Hosted bootstrap or self-hosted registration | Generated automatically; reuse identity across restart |

There is no node enable/disable switch. Registration and guarded removal control
membership. Core execution concurrency limits concurrent execution and directory/file
work; node capacity limits reserved/running sandboxes. They are independent.

## Installation directory

The installer creates the installation directory (default `~/.parsar/core`) with
mode `0700`. Keep every file in it private to the installation user.

| Path | Content |
| --- | --- |
| `config.json` | Operator settings; the only file you edit |
| `parsar` | Management command: `status`, `start`, `stop`, `apply`, `rotate-core-key` |
| `secrets/` | `core.key`, `credential.key` and `database.password`, one copy each |
| `state.json` | Installation ID, Compose project, images and the digests of written files; written by tools only |
| `generated/` | Files derived from `config.json`: `compose.json`, `core.env`, `core-key-digests.json`, `settings.json`, the native unit and `runtime-history.json` when set |
| `state/e2b/`, `native/`, `node-payload/` | E2B receipts, native Core binaries and the public node payload |

`parsar apply` overwrites `generated/` and records the digests of the files it
writes. A generated file that matches neither those digests nor what `config.json`
renders now was edited by hand: `apply` refuses to run until you move the change
into `config.json` and run `parsar apply --discard-edits`, which keeps the edited
copy as `generated/<file>.edited-<time>`. A missing generated file is simply
written again. `parsar status` reports edited files, `config.json` changes that are
not applied yet and services that run with other inputs than `config.json` renders.

## config.json

The installer writes every setting that applies to the installation's mode, so
the file shows each value. Installation flags such as `--public-url`,
`--core-port` and `--web-port` only seed it; `install.sh --config FILE` seeds it
from a prepared file instead. Afterwards edit it and run:

```sh
~/.parsar/core/parsar apply --dry-run   # show changed settings, files and restarts
~/.parsar/core/parsar apply
```

`apply` validates the file first and changes nothing when a value is invalid. It
then writes the generated files and compares them with what actually runs: each
Compose container carries the digest of its inputs in the `io.parsar.inputs`
label, and native Core carries it as `PARSAR_INPUTS` in its process environment.
`apply` recreates or restarts exactly the services whose running inputs differ,
Core first, then Web; a Web restart ends every Web sign-in session. When no service
runs (after `parsar stop`), `apply` only writes the files and the installation
stays stopped; while any service runs, `apply` also starts the ones that are
stopped. Because the comparison is with what runs, an interrupted `apply`,
rotation or rollback is finished by the next `apply`. If Core rejects a value at
startup and every service was running with the previous files, `apply` restores
those files, converges on them again and prints Core's startup error line;
otherwise it reports the failure and leaves the next `apply` to finish. `mode` and
`native_core` are fixed; install into a new directory to change them. `state.json`
records both at installation and wins: `apply` refuses a `config.json` that differs,
and `parsar status` reports it.

Changing `public_url` moves everything Core derives from it: the daemon
WebSocket URL, the self-hosted `remote_url`, the hosted sandbox bootstrap URL and
node configuration. When nodes, hosted sandboxes or self-hosted executors are bound
to the current address, `apply` lists them and asks you to type the new URL
(`--confirm-public-url-change URL` when non-interactive); it also asks when the
applied value can't be read. Nodes on the old address
then get no new sandboxes and must be removed and added again. With
`public_url: null`, Core uses `http://127.0.0.1:<ports.core>` and only local access
works; set a real HTTPS URL later without reinstalling.

`core.runtime_history.headers` may hold export credentials. They stay in the 0600
`config.json` and the generated file Core reads, and never appear in `parsar`
output or in Core's settings snapshot. Model provider settings are not part of
`config.json`.

<!-- BEGIN config-reference: generated by scripts/config-reference.py from deploy/install/config.schema.json -->
| Key | Type | Default | Modes | Change | Restarts | Meaning |
| --- | --- | --- | --- | --- | --- | --- |
| `$schema` | string | none | all | any time | none | Editor hint that points at the installed copy of this schema. Ignored. |
| `format` | `1` | none | all | fixed | none | Configuration format. Only an upgrade changes it. |
| `mode` | `"all"` \| `"core-only"` \| `"web-only"` | `"all"` | all | fixed | none | Which services this installation runs. Install flag: `--core-only` or `--web-only`. |
| `native_core` | boolean | `false` | `all`, `core-only` | fixed | none | Run Core as a systemd user service instead of a container. Install flag: `--native-core`. |
| `public_url` | string or null (canonical origin; HTTP only on loopback) | `null` | all | `parsar apply` | core, web | Public origin of Core and Web behind your TLS reverse proxy, such as https://core.example. Nodes, sandboxes and self-hosted executors use it. null means local access only through http://127.0.0.1. Install flag: `--public-url`. |
| `ports.core` | integer 1024–65535 | `8091` | `all`, `core-only` | `parsar apply` | core (core, web with native Core) | Loopback port of the Core API. With native Core, Web follows it. Install flag: `--core-port`. |
| `ports.web` | integer 1024–65535 | `8080` | `all`, `web-only` | `parsar apply` | web | Loopback port of Web. Install flag: `--web-port`. |
| `ports.database` | integer 1024–65535 | none | `all`, `core-only` | `parsar apply` | database, core | Loopback port of PostgreSQL. Present exactly when native_core is true; the installer picks a free port. |
| `web.core_url` | string (canonical origin; HTTP only on loopback) | none | `web-only` | `parsar apply` | web | Origin of the Core that this Web connects to: HTTPS, or HTTP on a loopback host. Install flag: `--core-url`. |
| `log.level` | `"debug"` \| `"info"` \| `"warn"` \| `"error"` | `"info"` | all | `parsar apply` | core, web | Minimum log level of Core and Web. |
| `log.format` | `"auto"` \| `"text"` \| `"json"` | `"auto"` | all | `parsar apply` | core, web | Log format. auto writes text to a terminal and JSON otherwise. |
| `log.add_source` | boolean | `false` | all | `parsar apply` | core, web | Add the source file and line to each log record. |
| `core.execution_concurrency` | integer 1–1024 | `4` | `all`, `core-only` | `parsar apply` | core | Concurrent execution work units in Core. Unrelated to node sandbox capacity. |
| `core.harnesses` | array of `"claude_sdk"` \| `"codex"` \| `"mcode"` | `["claude_sdk", "codex", "mcode"]` | `all`, `core-only` | `parsar apply` | core | Harnesses that Sessions may select. |
| `core.default_harness` | `"claude_sdk"` \| `"codex"` \| `"mcode"` | `"codex"` | `all`, `core-only` | `parsar apply` | core | Harness used when a Session names none. It must be listed in core.harnesses. |
| `core.write_audit_retention` | string (Go duration, at least `1h`) | `"2160h"` | `all`, `core-only` | `parsar apply` | core | How long non-creation write history is kept, as a Go duration of at least 1h. |
| `core.oauth_trusted_origins` | array of string (canonical HTTPS origin) | `[]` | `all`, `core-only` | `parsar apply` | core | Extra HTTPS origins trusted as private OAuth issuers. |
| `core.database_pool.max_conns` | integer or null ≥ 1 | `null` | `all`, `core-only` | `parsar apply` | core | Maximum database connections. null keeps the driver default, max(4, CPU count). |
| `core.database_pool.min_conns` | integer or null ≥ 0 | `null` | `all`, `core-only` | `parsar apply` | core | Minimum idle database connections. null keeps the driver default, 0. |
| `core.database_pool.max_conn_lifetime` | string or null (Go duration) | `null` | `all`, `core-only` | `parsar apply` | core | Go duration. null keeps the driver default, 1h. |
| `core.database_pool.max_conn_idle_time` | string or null (Go duration) | `null` | `all`, `core-only` | `parsar apply` | core | Go duration. null keeps the driver default, 30m. |
| `core.database_pool.health_check_period` | string or null (Go duration) | `null` | `all`, `core-only` | `parsar apply` | core | Go duration. null keeps the driver default, 1m. |
| `core.runtime_history` | object or null | `null` | `all`, `core-only` | `parsar apply` | core | Runtime history collection and OTLP export. null keeps local collection with Core's defaults. Core checks the values at startup. |
| `core.runtime_history.transport` | string | none | `all`, `core-only` | `parsar apply` | core | OTLP export transport. |
| `core.runtime_history.endpoint` | string | none | `all`, `core-only` | `parsar apply` | core | OTLP collector endpoint. Omit it to keep history local. |
| `core.runtime_history.insecure` | boolean | none | `all`, `core-only` | `parsar apply` | core | Export without TLS. |
| `core.runtime_history.headers` | object of string values | none | `all`, `core-only` | `parsar apply` | core | Headers sent with each export, such as credentials. Never shown by parsar or Core. Sensitive. |
| `core.runtime_history.queue_capacity` | integer | none | `all`, `core-only` | `parsar apply` | core | Export queue capacity. |
| `core.runtime_history.timeout_seconds` | integer | none | `all`, `core-only` | `parsar apply` | core | Export and query timeout in seconds. |
| `core.runtime_history.sample_interval_seconds` | integer | none | `all`, `core-only` | `parsar apply` | core | Periodic sampling interval in seconds. |
<!-- END config-reference -->

The schema is `deploy/install/config.schema.json`; the installation keeps a copy
in `generated/config.schema.json` for editors. Core serves the non-secret settings
snapshot, with the path of `config.json` and the apply command, at
`GET /core/v1/installation`.

## Core environment for standalone Core

Core reads only its environment. The installer generates `generated/core.env`
from `config.json`; operators who run Core without the installer set these names
themselves. Compose loads the file with `env_file` and systemd with
`EnvironmentFile`, so Compose must be **2.26.0 or newer**.

| Variable | Set from |
| --- | --- |
| `AGENTS_API_PUBLIC_URL` | `public_url`, or Core's loopback origin. The one origin applications, nodes, sandbox guests and self-hosted executors use; Core derives the daemon WebSocket URL, the self-hosted `remote_url`, the hosted bootstrap and the deployment's read-only `core_url` from it. Required with `AGENTS_API_SANDBOX_INSTALLATION_ID` |
| `AGENTS_API_ADDR` | `ports.core` (native Core) or the container port |
| `AGENTS_API_DATABASE_URL` | The installation's PostgreSQL without a password, plus `core.database_pool` as `pool_*` query parameters |
| `AGENTS_API_DATABASE_PASSWORD_FILE` | `secrets/database.password`. `AGENTS_API_DATABASE_URL` must then not contain a password; migrations and the maintenance commands read the file too |
| `AGENTS_API_CREDENTIAL_KEY_FILE` | `secrets/credential.key`; never regenerate it to repair credentials |
| `AGENTS_API_CORE_KEY_DIGESTS_FILE` | `generated/core-key-digests.json`, the SHA-256 of the [Core key](getting-started/operations.md#core-key) |
| `AGENTS_API_SANDBOX_INSTALLATION_ID` | `state.json`; pinned to the database |
| `AGENTS_API_SETTINGS_FILE` | `generated/settings.json`, the snapshot Core serves at `GET /core/v1/installation`; Core does not act on it |
| `AGENTS_API_EXECUTION_CONCURRENCY`, `AGENTS_API_ENGINE`, `AGENTS_API_HARNESSES`, `AGENTS_API_WRITE_AUDIT_RETENTION`, `AGENTS_API_OAUTH_TRUSTED_ORIGINS` | The matching `core.*` settings |
| `AGENTS_API_RUNTIME_HISTORY_FILE` | `generated/runtime-history.json` when `core.runtime_history` is set; see the [history contract](../contracts/agents-api/runtime-history-api.md) |
| `PARSAR_LOG_LEVEL`, `PARSAR_LOG_FORMAT`, `PARSAR_LOG_ADD_SOURCE` | `log.*`; Web reads the same three |
| `AGENTS_API_E2B_STATE_DIR`, `AGENTS_API_E2B_PROVIDER_BIN` | Receipt directory and, for native Core, the bundled helper; the E2B account credential and template belong to the database |

`AGENTS_API_DAEMON_WS_URL` is retired in favor of `AGENTS_API_PUBLIC_URL`,
`AGENTS_API_CONFIG_FILE` with no replacement, and `AGENTS_API_EXECUTION_OPTIONS_FILE`
in favor of [deployment model providers](#deployment-model-providers); Core fails at
startup while any of them is set and says what to do. Core reports loaded file
paths on startup, without environment values or file contents. Internal
polling/queue controls remain internal. Runtime history retention remains its
existing fixed policy.

Native installation paths must be canonical absolute paths without control
characters, quotes, backslashes or wildcard characters. Do not change the
installation UUID or backend paths as a substitute for provider maintenance. Core
refuses a missing installation setting when its database already has a claimed
deployment.

## Deployment model providers

Each harness has at most one deployment default model provider, stored encrypted in
PostgreSQL and set with the Core key in Web or through
`PUT /core/v1/harnesses/{harness}/model-provider`. It takes the same complete
bundle as `x_agents_core.model_provider` (`protocol`, HTTPS `base_url`, write-only
`api_key` and, for MiniMax Code, `context_window` and `max_output_tokens`). New
`openai_hosted` and `none` Sessions without a Session or Agent bundle freeze it;
`self_hosted` Sessions never use it, and a hosted or self-hosted Session with no
bundle is rejected with 400 `model_provider_required`. Reads never return the key.
See [model execution](../contracts/agents-api/model-execution.md#deployment-defaults).

## Database-owned deployment

Choose exactly one provider through Web or `/core/v1/sandbox/deployment`:
Docker, microsandbox or E2B. Core packaging is independent: `--native-core` does
not select microsandbox or install a local execution node.

Setup requests supply `resources` and, for Docker/microsandbox, the matching
`runtime` release. Core saves them as `specification` with a digest and generation.
Web proposes defaults from its distribution; PostgreSQL owns the submitted values.
See the [deployment contract](../contracts/agents-api/sandbox-deployment.md) for
request shapes, immutable identities and provider validation.

| Setting | Initial Web proposal | Meaning |
| --- | --- | --- |
| `resources.cpus` | `2` | Integer vCPUs per sandbox, 1 through 255 |
| `resources.memory_mib` | Docker `2048`, microsandbox `4096` | MiB per sandbox, 512 through 1048576 |
| `resources.root_disk_mib`, `environment_disk_mib` | Microsandbox `8192` each | At least 1024 MiB; omitted for Docker/E2B |
| `runtime` | Matching distribution manifest | Source revision, exact image ID/manifest/ref and runtime/firmware hashes; no mutable tags |
| Idle interval / snapshot retention | `300` / `86400` seconds | Database-owned microsandbox policy; no node-file override |

E2B resources are optional, and Web sends none: Core then adopts the CPU and memory
of the ready immutable `template-id:build-uuid`. Supplied values must match that
build. Omit `runtime`. The E2B account key is encrypted and write-only.
E2B template storage remains native; Docker has no independent hard disk quota.
A submitted limit must be enforceable by its provider.

Changing provider, Runtime or guest specification uses the existing explicit
maintenance procedure: stop fresh hosted admission, settle all retained and pending
resources, then save against the current generation. A failed check changes
nothing and deletes nothing. Successful replacement retires old nodes and tokens.
Explicitly resume, then issue enrollment tokens and register nodes for the new
generation; maintenance blocks enrollment as well as hosted admission. Hosted
requests need an online eligible node before they can be accepted. Existing Sessions do not
migrate between providers. See [maintenance](../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance).

## Node configuration and capacity

Use Web's **Add node** command on a Linux host, including the Core host. Core
approves capacity when issuing its enrollment token: defaults are **2 active** and
**8 retained** sandboxes. An administrator may update capacity later through the
node API. `max_retained >= max_active >= 1`; reservations and uncertain cleanup
also consume capacity. Lowering a limit does not kill existing resources.
Microsandbox uses both limits. Docker never suspends, so its `max_retained` always
equals `max_active`; Core replaces any submitted value.

The generated private `provider.json` includes the provider, installation identity,
Core address (the installation public URL when the node enrolled), generation,
approved specification copy and one adapter object:

- Docker: explicit Unix `host`, locally imported `image`, `network`, `extra_hosts`
  and absolute `seccomp_file`, with the existing nested-sandbox security settings.
  The imported image must match the approved Runtime release.
- Microsandbox: absolute `helper_path`, `runtime_home`, `runtime_path` and
  `firmware_path`, host network policy and validated resource/Runtime copies.
  The runtime home is an existing private backend namespace.

The node has no `max_active`, `max_retained`, idle-policy or maintenance override.
Editing the downloaded spec does not change Core. The node validates the content
and file hashes, and Core compares its digest on enrollment and every connection.
Mismatch rejects work until the approved configuration is restored. Reconnect
reads current database capacity; it never writes local limits back to Core.

Keep the private identity/state directory and backend storage together. Do not
copy node identities, replace a backend directory, or delete snapshots to fix a
connection problem. Reinstall retains local configuration and identity; changes
in provider, installation, Core address or release are refused. This is not an
upgrade or cross-provider migration tool. Core records the address each node
enrolled with. After the installation public URL changes, such a node receives no
new sandboxes: remove it in Web and add it again with a fresh state directory.

## Daemon and identity

Hosted Runtime initialization supplies the daemon's connection and identity;
execution preparation supplies the selected harness configuration through its
adapter. Self-hosted installation registers once, stores its restricted identity
and reuses it on restart. Administrators do not maintain another daemon config
file. `environment.json` records identity and ownership; do not hand-edit it into
an execution configuration source. Retain credentials, native history, private
receipts and files independently of process configuration.

The old `AGENTS_API_MANAGED_RUNTIMES_FILE`, embedded Core node and local-node Core
environment overrides are retired and rejected. Installation may connect an
explicitly requested local node through the same administrator API and registration
used for remote hosts. Provider flags are one-time setup inputs, never a persisted
override. No old file-managed deployment is migrated or adopted.
