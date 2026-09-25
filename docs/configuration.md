# Configuration

This is the configuration reference for Core, hosted nodes and the execution
daemon. Installation and operations guides describe workflows and link here for
parameters. Each setting has one owner; reinstalling does not restore defaults.

## Ownership and changes

| Settings | Authoritative source | How changes take effect |
| --- | --- | --- |
| Core database/pool, listener, execution concurrency, logging, audit retention | `<installation>/config/core.env` | Edit the private file; restart Core |
| Hosted provider, uniform guest resources, immutable Runtime specification, E2B credentials | Core PostgreSQL | Web or deployment API; existing maintenance/generation checks |
| Node registration and sandbox capacity | Core PostgreSQL | Administrator enrollment and node updates |
| Node host paths, Docker socket and local network wiring | Node `provider.json` | Edit host-local fields; restart the node |
| Downloaded deployment specification | `specification` in node `provider.json` | Validated copy only; must match Core's digest |
| Daemon connection, identity and execution preparation | Hosted bootstrap or self-hosted registration | Generated automatically; reuse identity across restart |

There is no node enable/disable switch. Registration and guarded removal control
membership. Core execution concurrency limits concurrent execution and directory/file
work; node capacity limits reserved/running sandboxes. They are independent.

## Core process file

Both Compose and native systemd installations load the same private `core.env`.
The installer writes it once. Keep its directory mode `0700` and the file `0600`,
owned by the service account. Reinstallation validates and preserves existing
content; missing, unsafe or conflicting identity files fail rather than regenerate.
The installation record stores packaging and identity receipts, not a second
editable set of process parameters.

Use one double-quoted literal value per line:

```dotenv
AGENTS_API_EXECUTION_CONCURRENCY="4"
PARSAR_LOG_LEVEL="info"
AGENTS_API_WRITE_AUDIT_RETENTION="2160h"
```

Comments and blank lines are allowed. Escape backslashes, double quotes and dollar
signs with a backslash. Do not use `export`, duplicate names, variable references,
command substitution, unquoted values or multiline values. Nothing is expanded by
the installer's reader. Compose requires version **2.26.0 or newer** for the same
literal escaping behavior. Do not source this file as a shell script.

Compose uses `env_file` for Core and migrations; the native service uses
`EnvironmentFile`, and native migrations read the same persisted file. Do not add
parallel Core values in Compose `environment`, systemd `Environment` or installation
flags. Changing `core.env` does not require editing those launcher files.

| Parameter | Default / unit | Restrictions and effect |
| --- | --- | --- |
| `AGENTS_API_DATABASE_URL` | Required PostgreSQL connection string; generated for the dedicated database | Includes pool settings; keep credentials private |
| Pool options in the URL | Existing pgx defaults: `pool_max_conns=max(4, CPU count)`, `pool_min_conns=0`, `pool_max_conn_lifetime=1h`, `pool_max_conn_idle_time=30m`, `pool_health_check_period=1m` | For example append `&pool_max_conns=16`; counts are integers and times are Go durations; no separate pool env layer |
| `AGENTS_API_ADDR` | Binary: `127.0.0.1:8091`; installer: container `:8091` or native loopback installation port | Listen address; container port publishing and reverse-proxy routing are deployment wiring and must match |
| `AGENTS_API_EXECUTION_CONCURRENCY` | `4` concurrent execution work units | Integer `1..1024`; explicit empty/invalid values fail startup; unrelated to node sandbox capacity |
| `PARSAR_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `PARSAR_LOG_FORMAT` | Automatic for the output destination | `text` or `json` |
| `PARSAR_LOG_ADD_SOURCE` | Disabled | `1` enables source locations; `0` disables |
| `AGENTS_API_WRITE_AUDIT_RETENTION` | `2160h` (90 days) | Go duration, minimum `1h`; permanent creation ownership is retained |
| `AGENTS_API_ENGINE` | `codex` | Default harness; accepted Session choices remain frozen |
| `AGENTS_API_HARNESSES` | Binary: default harness; installer: `codex,claude_sdk,mcode` | Comma-separated supported harness IDs; unknown IDs fail startup |
| `AGENTS_API_DAEMON_WS_URL` | Installer-generated reachable WebSocket URL | Enables daemon transport and advertises self-hosted connectivity; hosted bootstrap uses the saved deployment origin |
| `AGENTS_API_CONFIG_FILE` | Installer-generated absolute loaded-file path | Diagnostic marker only, not a loader; preserve it |
| `AGENTS_API_SANDBOX_INSTALLATION_ID` | Installer-generated canonical UUID | Stable identity pinned to the database, not provider selection; preserve it |
| `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` | Generated private digest file path | Administrator authority; original bearer key stays separately on the console server |
| `AGENTS_API_CREDENTIAL_KEY_FILE` | Generated private encryption-key path | Preserve with the database; never regenerate to repair credentials |
| `AGENTS_API_EXECUTION_OPTIONS_FILE` | Unset | Optional existing adapter-options JSON; see [model execution](../contracts/agents-api/model-execution.md) and [harness selection](../contracts/agents-api/harness-selection.md) |
| `AGENTS_API_RUNTIME_HISTORY_FILE` | Unset | Optional existing Runtime history/export JSON; local collection defaults to 30 seconds, retention to 7 days; see [history contract](../contracts/agents-api/runtime-history-api.md) |
| `AGENTS_API_E2B_PROVIDER_BIN` / `AGENTS_API_E2B_STATE_DIR` | Matching helper / private persistent receipt directory from installer | Paths only; the E2B account credential and template belong to the database |
| `AGENTS_API_OAUTH_TRUSTED_ORIGINS` | Public HTTPS origins | Additional exact trusted HTTPS origins for private issuers; use the [OAuth contract](../services/agents-api/oauth-credentials.md) |

Core reports loaded configuration paths on startup, without environment values or
file contents. Existing adapter-options and history JSON formats remain separate
specialized files referenced from `core.env`; this change does not introduce a
new loader or consolidate secrets into one file. Internal polling/queue controls
remain internal. Runtime history retention remains its existing fixed policy.

A process-only change requires restart. In Compose, recreate Core so it rereads
`env_file` (a container restart alone retains its old environment):

```sh
docker compose -f "$HOME/.parsar/core/compose.json" up -d --no-deps --force-recreate core
```

Native installation paths must be canonical absolute paths without control
characters, quotes, backslashes or wildcard characters.

For native Core, restart the installation's generated `parsar-<id>-core.service`
with `systemctl --user restart`. Do not change installation UUID or backend paths
as a substitute for provider maintenance. Core refuses a missing installation
setting when its database already has a claimed deployment.

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
| `resources.memory_mib` | Docker/E2B `2048`, microsandbox `4096` | MiB per sandbox, 512 through 1048576 |
| `resources.root_disk_mib`, `environment_disk_mib` | Microsandbox `8192` each | At least 1024 MiB; omitted for Docker/E2B |
| `runtime` | Matching distribution manifest | Source revision, exact image ID/manifest/ref and runtime/firmware hashes; no mutable tags |
| Idle interval / snapshot retention | `300` / `86400` seconds | Database-owned microsandbox policy; no node-file override |

E2B also requires CPU/memory. Core verifies them against the ready immutable
`template-id:build-uuid`; omit `runtime`. Its account key is encrypted and write-only.
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

The generated private `provider.json` includes the provider, installation identity,
Core address, generation, approved specification copy and one adapter object:

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
upgrade or cross-provider migration tool.

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
