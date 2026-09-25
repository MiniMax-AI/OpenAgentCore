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

## Projects and API keys

Use the [administrator API](../../contracts/agents-api/admin-api.md) to create a
Project and issue its first key after installation. The Web management migration
is still pending; see [integration status](../web/README.md). No configuration file
defines Projects or application keys. API-key plaintext is returned once at issuance,
with only its digest stored in the database.

To rotate, issue another key within the same Project, update the application, then
revoke the old key. Renaming a Project or revoking a key preserves its assets and
execution principal. Archiving a Project disables all its keys while retaining
assets and already accepted execution. Administrators may inspect or delete retained
resources, or copy supported assets to an active Project; they cannot execute them
using the deployment credential.

## Data and upgrades

Retain together:

- the dedicated PostgreSQL volume, including Projects, API-key digests, provenance
  and large objects;
- `config/credential.key`, the database-owned deployment specification and installation identity;
- `admin/`, including on zero-node installations, containing the separate sandbox
  administrator key and Core's digest configuration;
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
specifications existed has an empty specification after migration. Current Core
loads it only to drain: nodes enrolled under the previous release reconnect with
their existing node service, and their sandboxes stay reachable for archive and
cleanup. Fresh sandbox creation, node configuration reads and enrollment are
refused. Back up as above, replace Core and Web, then use steps 1–4 with a Runtime
release from the new distribution. The replacement retires the old nodes. Stop each
retired node service, move its identity directory aside as a backup, and add the
host again with a new command from Web. Node IDs change; Session history and
persisted Files/Artifacts remain. E2B deployments from that period are not covered.

## Exposure and network policy

API and console bind to host loopback. With native Core, PostgreSQL publishes an
installation-specific loopback port; with container Core it has no published port.
The production Web proxy forwards only allowlisted administrator and sandbox
management routes after console login. Its deployment credential stays on the
server. All `/v1` requests, including requests with an explicit API key, return 404
at the console. Route public `/v1` and project executor-credential requests directly
to Core through the TLS reverse proxy. Route console pages, authentication,
administration and the existing node/daemon transport paths to Web. Those fixed
transport paths retain their own authentication. `/node-install/` serves only the
matched non-secret node payload. Web has no Docker or KVM authority.
It requires an independent administrator login and rejects untrusted browser origins.
New installations store the administrator password hash in private console state;
legacy installations retain Basic authentication. There is only one Web role,
with full console authority; Agent API caller keys remain separate. Back up the
console account state, and sign in again after a console restart.

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
authentication path. Console credentials are separate deployment credentials. See the
[query contract](../../contracts/agents-api/write-audit.md) for deployment-authenticated
Project-scoped batch ownership and cursor history endpoints. Administrator mutations
have a separate [audit log](../../contracts/agents-api/admin-api.md#monitoring-and-audit). These APIs do not log bodies or secrets.
