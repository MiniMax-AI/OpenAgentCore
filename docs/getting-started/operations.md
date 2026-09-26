# Operate your Core

The installation operator owns the host, storage and service availability.
The default installation selects Docker sandboxes and has zero execution nodes.
After nodes are added in Web, the Core host included, their operators maintain the
node services, containers or microVMs on those hosts. Service
health and provider state are separate from a Session's public execution state.

Use [Configuration](../configuration.md) for the authoritative setting locations,
defaults and restart behavior. Process settings live in the installation's
`config.json` and take effect with `parsar apply`; Compose and systemd only
launch the processes from the files `apply` generates.

## Read service health

Each installation has its own `parsar` command; it does not need the bundle:

```sh
~/.parsar/core/parsar status
# For a separate installation:
~/.parsar/core-console/parsar status
```

The command reads only this installation's service status and health endpoints.
It prints service names, running/exit state, the native Core service state when
applicable, and available Docker health state. It also prints the public URL, API
base URL, source commit, the reverse-proxy routes to configure, `config.json`
changes that are not applied yet and generated files edited by hand. A Web-only
installation also checks that its Core accepts its Core key. It does not print
Compose configuration, environment variables, credentials or raw application
logs. It never creates a Session or calls a model.

Use these observations for distinct questions:

| Observation | What it establishes |
| --- | --- |
| PostgreSQL container health | The dedicated database accepts its readiness check |
| Core `/healthz` | Core process liveness |
| Authenticated API resource read | Caller authentication and the requested resource operation |
| Environment connection | Runtime transport observation |
| Terminal Turn and queried results | The requested task's recorded execution outcome |

Container liveness alone is not a healthy native harness or an available model.
Use public Session, Turn, Items, Environment and Usage reads for execution. Reuse
Core's Runtime observations for sandbox details when available; do not infer
execution truth from Docker or invent a second lifecycle collector. Use Web's
**Nodes** page for node connection, provider readiness and placement.

## Stop and restart

Settle active work before a planned restart. Then:

```sh
~/.parsar/core/parsar stop
~/.parsar/core/parsar start
```

This stops the control-plane services and retains the database, Runtime state and
credentials. Local and remote nodes run their own services; stopping Core does not stop those
services. The node service uses `KillMode=process`, so a service restart also
preserves resident microsandbox processes; microVMs and their work may remain running. Docker-owned Runtime
containers likewise remain Provider resources. The command does not promise to
stop all compute. Confirm resource cleanup before a full
shutdown; do not kill Provider processes directly. A Core restart does not promise
transparent continuation of an interrupted native tool. Query the same
Session after reconnecting; do not create a replacement Session to replay uncertain
work. The official SSE stream is live, and recovery uses durable resource queries.

`parsar start` uses the last applied files; it warns about unapplied `config.json`
changes. The generated Compose file and `core.env` hold no secrets; secrets reach
the services as read-only file mounts from `secrets/`. Still, do not paste
`docker compose config`, `docker inspect` or raw logs into public issue reports.
For local diagnosis, use the exact installation file:

```sh
docker compose -f "$HOME/.parsar/core/generated/compose.json" ps --all
```

Do not edit files under `generated/`; `parsar apply` refuses to overwrite hand
edits until you move them into `config.json`.

## Core key

Each installation has one management credential, the Core key. The installer
generates a 64-character random key in `<install dir>/secrets/core.key` (default
install dir `~/.parsar/core`). `parsar apply` writes its SHA-256 digest to
`generated/core-key-digests.json`. Core reads the digest file named by
`AGENTS_API_CORE_KEY_DIGESTS_FILE`; Web reads the key file named by
`CORE_CONSOLE_CORE_KEY_FILE`. Both read them only at startup.

A Core key must have at least 32 characters and no whitespace. Web and the
Web-only installer refuse a shorter key; Core sees only digests, so it cannot
check the length. Web limits failed sign-ins, but the correct key always signs in.

The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes `/core/v1` management requests sent to Core as
  `Authorization: Bearer <Core key>`;
- never authorizes `/v1`. Applications use Project API keys, which in turn cannot
  call `/core/v1`.

Keep it private. `secrets/` stays mode `0700` and its files `0600`, owned by the
installation user. Of the services, only Web reads `core.key`; Core reads only the
digest file. Don't copy the key into scripts, shell history, logs or issue reports.
Operator scripts run on the Core host, call Core's loopback port and read the key
from its file. This example keeps the key off the command line:

```sh
curl -fsS -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.parsar/core/secrets/core.key")") \
  http://127.0.0.1:8091/core/v1/projects
```

### Rotate the Core key

```sh
~/.parsar/core/parsar rotate-core-key
```

The command asks for confirmation (`--yes` skips it), stops Web, writes a new
64-character key to `secrets/core.key`, regenerates the digest file, restarts Core
and waits until it is healthy, then starts Web. It checks that Core accepts the
new key and rejects the old one. The old key stops working at once, and every Web
sign-in session ends: sign in again with the new key and update your scripts. If
the command stops early, `secrets/core.key` holds the key to use and `parsar status`
reports what does not match it; run `parsar apply` to finish the rotation.

A separate Web-only installation keeps its own copy of the key. After rotating,
copy `secrets/core.key` from the Core host to that installation's
`secrets/core.key` (mode `0600`) and run its `parsar apply`; its `parsar status`
reports a key that Core rejects. `rotate-core-key` refuses to run on a Web-only
installation, because Core owns the key.

### Upgrade an existing installation

Installations made before `config.json` move to the current layout with
[`install.sh --convert`](install.md#convert-an-earlier-installation). Conversion
expects the Core key names below. Earlier releases used other names for these
files and settings; current Core and Web refuse to start while an old setting is
present, and the error names the replacement. For such an installation, stop the
services and rename them first:

| Old | New | Where |
| --- | --- | --- |
| `admin/sandbox-admin.key` | `admin/core.key` | File; Web bind mount in `compose.json` |
| `admin/digests.json` | `admin/core-key-digests.json` | File; Core bind mount in `compose.json` |
| `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` | `AGENTS_API_CORE_KEY_DIGESTS_FILE` | `config/core.env`; its value names the renamed file |
| `CORE_CONSOLE_ADMIN_TOKEN_FILE` | `CORE_CONSOLE_CORE_KEY_FILE` | Web `environment` in `compose.json` |
| `--admin-token-file` | `--core-key-file` | `install.sh --web-only` flag; the installer rejects the old flag |
| `AGENTS_API_DAEMON_WS_URL` (a `wss://…/api/v1/agent-daemon/ws` URL) | `AGENTS_API_PUBLIC_URL` (the origin only, such as `https://core.example`) | `config/core.env` |
| `AGENTS_API_CONFIG_FILE` | None; delete the line | `config/core.env` |

Also remove `CORE_CONSOLE_AUTH_MODE`, `CORE_CONSOLE_STATE_DIR` and
`CORE_CONSOLE_PASSWORD_FILE` from Web's environment, with their `state/console`
and `config/console.password` mounts. Web no longer has accounts, passwords or
Basic authentication, and it refuses to start while those settings are present.
The old account state is not read; delete it after the upgrade or keep it as a
backup. Then start the services and sign in with the Core key.

The deployment no longer stores the Core address, and nodes keep the one they
enrolled with. For an installation made before `AGENTS_API_PUBLIC_URL` existed,
`install.sh --convert` takes the public URL from the sandbox deployment's
`core_url` when the installation had none, and stops when the two differ, so
existing nodes stay bound. Later installations already name the address in
`config/core.env`, and conversion keeps it. It also moves the database password into
`secrets/database.password`. For a Core you run without the installer, read
`GET /core/v1/sandbox/deployment` with the Core key before upgrading and set
`AGENTS_API_PUBLIC_URL` to its `core_url`; with any other value, every existing
node counts as bound to another address and must be re-added. Optionally move the
password out of `AGENTS_API_DATABASE_URL` into the file named by
`AGENTS_API_DATABASE_PASSWORD_FILE`.

## Projects and API keys

Use Web, or the [Core API](../../contracts/agents-api/admin-api.md) under
`/core/v1/projects` with the Core key, to create a Project and issue its first key
after installation. No configuration file
defines Projects or application keys. API-key plaintext is returned once at issuance,
with only its digest stored in the database.

To rotate, issue another key within the same Project, update the application, then
revoke the old key. Renaming a Project or revoking a key preserves its assets and
execution principal. Archiving a Project disables all its keys while retaining
assets and already accepted execution. Administrators may inspect or delete retained
resources; they cannot execute them using the Core key.

## Data and upgrades

Retain together:

- the dedicated PostgreSQL volume, including Projects, API-key digests, provenance
  and large objects;
- the installation directory: `config.json`, `state.json` (installation identity)
  and `secrets/`, whose `credential.key` must stay with the database and whose
  `core.key` is the [Core key](#core-key), including on zero-node installations;
- the database-owned deployment specification;
- each separately installed node's private configuration and persistent identity
  directory on its host (`/var/lib/parsar-node/.parsar/nodes/<installation-id>/` for a
  node added with sudo, `~/.parsar/nodes/<installation-id>/` for one added as a normal
  user), as described in the
  [node guide](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host);
- E2B's private `state/e2b` receipts and SDK connection materials when selected;
- microsandbox's private state/cache/disks/snapshots, or Docker-owned Runtime
  volumes and histories;
- the exact distribution and private deployment configuration needed to recover.

Do not replace a missing node identity directory with a fresh registration;
restore its original saved state alongside the database and provider storage.
Restarting the same installation preserves the directory.

Never regenerate the encryption key to resolve an error: stored Session/model and
Vault credentials require it. Never prune Docker volumes or delete native history
to make a retry pass. Public Session deletion acknowledgement does not prove that
all physical provider resources have been reclaimed.

The installer supports fresh installation, same-release repair and
[conversion](install.md#convert-an-earlier-installation) of an installation made
before `config.json`. It refuses automatic replacement of an installed revision. For a reviewed upgrade,
back up the coordinated state, retain the previous distribution, settle execution,
apply the existing Core migration workflow and replace matched service/Runtime
artifacts while retaining identities and backend paths. Qualify recovery before
claiming the upgrade complete; there is no downgrade or history migration promise.

Upgrading to a release that serves node connections at `/api/v1/sandbox-node/*`:

1. Core and its nodes must come from the same distribution; the node installer
   refuses a mismatched release. Nodes from releases that used the removed
   `/core/v1/sandbox/enroll` and `/core/v1/sandbox/node/*` paths cannot connect to
   the new Core. For a Docker or microsandbox deployment, drain with the previous
   release while its nodes are still connected (steps 1–2 below).
2. When the new Core and Web go live, change the reverse proxy so that `/api/v1/*`,
   including WebSocket upgrades, goes directly to Core instead of Web (see
   [Expose Core and Web](install.md#expose-core-and-web)). The new Web returns 404
   for `/api/v1`. With the old routing, node enrollment fails and so does every
   Runtime daemon connection (`/api/v1/agent-daemon/ws`), for Docker, microsandbox,
   self-hosted and E2B sandboxes alike.
3. Complete steps 3–4 below with the new distribution's Runtime release. On each
   node host, stop the old node service and move `~/.parsar/nodes/<installation-id>/`
   aside as a backup before running the new command from Web.

The installer refuses flags on an existing installation; change process settings
in `config.json` with `parsar apply`. Rerunning it does not resize sandboxes or
replace the database selection. Use Web
or the administrator API for the initial selection and all later provider,
per-sandbox resource or Runtime changes:

1. Read the current deployment generation and enter global maintenance. Existing
   work, reads and cleanup remain available; fresh hosted admission stops.
2. Verify both allocation and pending-Environment counts are zero. Stopped
   compute, retained snapshots and uncertain cleanup still count. Explicitly
   [archive each retained hosted Session](../../contracts/agents-api/admin-api.md#administrative-session-archive)
   through the administrator API with the current generation, read until its
   resource disposition is `released`, then recheck both counts. Archive retains
   history and persisted Files/Artifacts, discards unpersisted workspace contents
   and prevents the original Session from resuming. Unknown cleanup still blocks
   replacement; an active Turn may finalize after its resources are released.
3. Submit the complete replacement with the current generation and unchanged Core
   origin. A failed candidate keeps the previous configuration. A successful
   changed commit retires old nodes and enrollment tokens while retaining history.
4. Explicitly resume with the returned generation, then enroll matching nodes where
   needed. Node enrollment requires maintenance to be off; a node-backed deployment
   admits hosted work only after a ready node has capacity.

See the [deployment contract](../../contracts/agents-api/sandbox-deployment.md) for
exact requests and errors. After an uncertain response, read the deployment before
another write. Editing a node file cannot change its generation, Runtime or resource
profile; a mismatch fails without replacing identity or resources.

Older installations using `AGENTS_API_MANAGED_RUNTIMES_FILE` must retain their
previous release and backend while settling and draining work. Current Core rejects
that setting and does not automatically adopt the old database after it is removed.
Keep the original business data, credential key, Runtime history and private provider
state. There is no automatic old-database conversion, force reset or resource deletion.
The supported current path uses a database-managed deployment.

A Web-selected Docker or microsandbox deployment saved before deployment
specifications existed has an empty specification after migration. Its nodes use
the removed `/core/v1/sandbox` node paths, so draining it needs a Core that has the
pre-specification drain mode (pull request #114) but still serves those paths. No such release
has been published: build a distribution from main commit
`7b66be236a627246c85658722314285e6b39d9b8`, or any commit that contains #114 but not
the move to `/api/v1/sandbox-node`. That Core loads the deployment only to drain:
nodes enrolled under the previous release reconnect with their existing node
service, and their sandboxes stay reachable for archive and cleanup. Fresh sandbox
creation, node configuration reads and enrollment are refused. Back up as above,
replace Core and Web with that build and complete steps 1–2. Only then upgrade to
this release, change the reverse proxy as described above and use steps 3–4 with
its Runtime release. The replacement retires the old nodes. Stop each retired node
service, move its identity directory aside as a backup, and add the host again
with a new command from Web. Node IDs change; Session history and
persisted Files/Artifacts remain. E2B deployments from that period are not covered.

## Exposure and network policy

API and console bind to host loopback. With native Core, PostgreSQL publishes an
installation-specific loopback port; with container Core it has no published port.
After console login and same-origin checks, the production Web proxy forwards every
`/core/v1` request to Core with the Core key, which stays on the server; Core
decides which routes exist. The TLS reverse proxy routes `/v1` (applications) and
`/api/v1` (node and Runtime daemon connections) directly to Core. Everything else,
including console pages, authentication and `/core/v1`, goes to Web. Operator
scripts call `/core/v1` with the Core key on Core's loopback port. Web returns 404 for
`/v1` and `/api/v1`, whatever credential a request carries, and forwards no node or
daemon traffic. Machine routes keep their own enrollment and connection credentials.
`/node-install/` serves only the matched non-secret node payload. Web has no Docker
or KVM authority.
It requires [Core key](#core-key) sign-in and rejects untrusted browser origins.
There is only one Web role, with full console authority; Agent API caller keys
remain separate. Sessions live in Web's memory, so sign in again after a Web
restart or Core key rotation.

A Core restart leaves the separately supervised node and resident microVM processes alone. Host reboot, user
manager termination and loss of a running microVM are not equivalent to that
restart and are not qualified cold-recovery workflows. Completed idle snapshots retain their existing recovery contract. When native restore
omits a configured root-disk size, the exact verified full snapshot must prove the
inherited capacity; CPU, memory and environment-disk limits still need to match.
A missing or mismatched proof does not authorize resize or replacement.

microsandbox uses an explicit policy: public egress, the Core/DNS host ports needed
for the colocated Runtime, and denied inbound/private-network access. Private
model/MCP endpoints require an explicit operator policy change. Native tool network
policy remains the Session's separate public configuration.

The Docker node uses the existing nested-sandbox Runtime policy. Its service account
needs access to the selected host Docker daemon; Core has no Docker socket mount.
Install on a trusted node host and do
not share its Docker authority with untrusted users. The default installation
does not mount the Docker socket or host devices into Core, import Runtime images
or generate managed Provider configuration.

Host virtualization, credentials, tenant isolation, durable state and actual
execution are release acceptance requirements. Other missing operational screens
or low-frequency improvements belong in the backlog; they do not turn this batch
into a Web redesign or a complete protocol-compatibility claim.

### API-key write history

Core records committed public resource writes and their key ownership for the
administrator console. Set `core.write_audit_retention` in `config.json` (Go
duration, minimum `1h`, default `2160h`) to control non-creation history. Creation ownership
remains permanently; removing keys or resources does not cascade-delete records.
Projects and their API keys are database records. Keys within one Project share
assets and the same execution principal, while provenance identifies the actual
key that made each write. Existing resources without recorded provenance return
null. Historical key-kind metadata remains audit evidence, not a configuration
authentication path. The Core key is separate from Project API keys. See the
[query contract](../../contracts/agents-api/write-audit.md) for deployment-authenticated
Project-scoped batch ownership and cursor history endpoints. Administrator mutations
have a separate [audit log](../../contracts/agents-api/admin-api.md#monitoring-and-audit). These APIs do not log bodies or secrets.
