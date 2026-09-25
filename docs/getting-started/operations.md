# Operate your Core

The installation operator owns the host, storage and service availability.
The default installation has zero execution nodes. After nodes are added in Web,
their operators maintain the node services, containers or microVMs on those hosts.
A local node requested during installation runs as the same separate node service
on the Core host. Service
health and provider state are separate from a Session's public execution state.

Use [Configuration](../configuration.md) for the authoritative setting locations,
defaults and restart instructions. Core process parameters live in `config/core.env`;
Compose and systemd only launch the process.

## Read service health

Run from the extracted bundle:

```sh
./install.sh --status
# For a separate installation:
./install.sh --status --install-dir "$HOME/.parsar/core-console"
```

The command reads only this installation's service status and health endpoints.
It prints service names, running/exit state, the native Core service state when
applicable, and available Docker health state;
it does not print Compose configuration, environment variables, credentials or
raw application logs. It never creates a Session or calls a model.

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
**Hosted Sandbox Manager** for node connection, provider readiness and placement.

## Stop and restart

Settle active work before a planned restart. Then:

```sh
./install.sh --stop
./install.sh  # Use the same component/packaging/port flags as the initial install.
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

The generated Compose file is private because it contains database connection
credentials. Do not paste `docker compose config`, `docker inspect` or raw logs into
public issue reports. For local diagnosis, use the exact installation file:

```sh
docker compose -f "$HOME/.parsar/core/compose.json" ps --all
```

## Core key

Each installation has one management credential, the Core key. The installer
generates a 64-character random key in `<install dir>/admin/core.key` (default
install dir `~/.parsar/core`) and writes its SHA-256 digest to
`admin/core-key-digests.json`. Core reads the digest file named by
`AGENTS_API_CORE_KEY_DIGESTS_FILE` in `config/core.env`; Web reads the key file
named by `CORE_CONSOLE_CORE_KEY_FILE`. Both read them only at startup.

A Core key must have at least 32 characters and no whitespace. Web and the
Web-only installer refuse a shorter key; Core sees only digests, so it cannot
check the length. Web limits failed sign-ins, but the correct key always signs in.

The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes `/core/v1` management requests sent to Core as
  `Authorization: Bearer <Core key>`;
- never authorizes `/v1`. Applications use Project API keys, which in turn cannot
  call `/core/v1`.

Keep it private. `admin/` stays mode `0700` and both files `0600`, owned by the
installation user. Of the services, only Web reads `core.key`; Core reads only the
digest file. Don't copy the key into scripts, shell history, logs or issue reports.
Operator scripts run on the Core host, call Core's loopback port and read the key
from its file. This example keeps the key off the command line:

```sh
curl -fsS -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.parsar/core/admin/core.key")") \
  http://127.0.0.1:8091/core/v1/projects
```

### Rotate the Core key

1. Generate a new 64-character key and replace both files. The subshell keeps
   `umask 077` and the key variable out of your shell:

   ```sh
   (
     cd "$HOME/.parsar/core/admin"
     umask 077
     key=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
     printf '%s\n' "$key" > core.key.new
     printf '["%s"]\n' "$(printf '%s' "$key" | sha256sum | cut -d' ' -f1)" > core-key-digests.json.new
     mv core.key.new core.key && mv core-key-digests.json.new core-key-digests.json
   )
   ```

2. Restart Core and Web so they read the new files:

   ```sh
   docker compose -f "$HOME/.parsar/core/compose.json" up -d --no-deps --force-recreate core web
   ```

   With native Core, run `systemctl --user restart parsar-<id>-core.service`, then
   the same Compose command with only `web`. A core-only installation has no `web`
   service: recreate only `core` (or restart the native service). A separate Web
   installation keeps its own copy in its `admin/core.key`; replace that file and
   recreate its `web`.
3. Sign in to Web again with the new key and update your scripts. The restart
   ends every Web session, and Core rejects the old key.

The digest file is a JSON array, and Core accepts every digest it lists. To give
scripts time to switch, you can list the old and new digests, restart Core, and
then remove the old digest and restart Core again. Web holds only one key.

### Upgrade an existing installation

Earlier releases used other names for these files and settings. Current Core and
Web refuse to start while an old setting is present, and the error names the
replacement. Stop the services, then:

| Old | New | Where |
| --- | --- | --- |
| `admin/sandbox-admin.key` | `admin/core.key` | File; Web bind mount in `compose.json` |
| `admin/digests.json` | `admin/core-key-digests.json` | File; Core bind mount in `compose.json` |
| `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` | `AGENTS_API_CORE_KEY_DIGESTS_FILE` | `config/core.env`; its value names the renamed file |
| `CORE_CONSOLE_ADMIN_TOKEN_FILE` | `CORE_CONSOLE_CORE_KEY_FILE` | Web `environment` in `compose.json` |

Also remove `CORE_CONSOLE_AUTH_MODE`, `CORE_CONSOLE_STATE_DIR` and
`CORE_CONSOLE_PASSWORD_FILE` from Web's environment, with their `state/console`
and `config/console.password` mounts. Web no longer has accounts, passwords or
Basic authentication, and it refuses to start while those settings are present.
The old account state is not read; delete it after the upgrade or keep it as a
backup. Then start the services and sign in with the Core key.

## Projects and API keys

Use Web, or the [Core API](../../contracts/agents-api/admin-api.md) under
`/core/v1/projects` with the Core key, to create a Project and issue its first key
after installation. The Web management migration
is still pending; see [integration status](../web/README.md). No configuration file
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
- `config/credential.key`, the database-owned deployment specification and installation identity;
- `admin/`, including on zero-node installations, containing the
  [Core key](#core-key) and its digest file;
- each separately installed node's private configuration and persistent identity
  directory on its host (`~/.parsar/nodes/<installation-id>/` for the Web-generated
  installer), as described in the
  [node guide](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#register-a-host);
- the same `~/.parsar/nodes/<installation-id>/` identity for a local node, including
  its credential, generation, specification digest and highest accepted owner epoch;
- E2B's private `state/e2b` receipts and SDK connection materials when selected;
- microsandbox's private state/cache/disks/snapshots, or Docker-owned Runtime
  volumes and histories;
- the exact distribution and private deployment configuration needed to recover.

Do not replace a missing local node identity directory with a fresh registration;
restore its original saved state alongside the database and provider storage.
Restarting the same installation preserves the directory.

Never regenerate the encryption key to resolve an error: stored Session/model and
Vault credentials require it. Never prune Docker volumes or delete native history
to make a retry pass. Public Session deletion acknowledgement does not prove that
all physical provider resources have been reclaimed.

This first installer supports fresh installation and same-release restart. It
refuses automatic replacement of an installed revision. For a reviewed upgrade,
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

The installer refuses component/packaging flag changes on an existing installation.
Rerunning it does not resize sandboxes or replace the database selection. Use Web
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
administrator console. Configure `AGENTS_API_WRITE_AUDIT_RETENTION` (Go duration,
minimum `1h`, default `2160h`) to control non-creation history. Creation ownership
remains permanently; removing keys or resources does not cascade-delete records.
Projects and their API keys are database records. Keys within one Project share
assets and the same execution principal, while provenance identifies the actual
key that made each write. Existing resources without recorded provenance return
null. Historical key-kind metadata remains audit evidence, not a configuration
authentication path. The Core key is separate from Project API keys. See the
[query contract](../../contracts/agents-api/write-audit.md) for deployment-authenticated
Project-scoped batch ownership and cursor history endpoints. Administrator mutations
have a separate [audit log](../../contracts/agents-api/admin-api.md#monitoring-and-audit). These APIs do not log bodies or secrets.
