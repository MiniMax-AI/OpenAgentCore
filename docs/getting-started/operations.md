# Operate your Core

The API abstracts execution environments for clients. The installation operator
also owns the host, container or microVM provider, storage and service availability.
Those are separate from a Session's public execution state. This guide adds that
self-deployment view without adding a second Runtime controller.

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
execution truth from Docker or invent a second lifecycle collector. The existing
Web is unchanged by this installation batch.

## Stop and restart

Settle active work before a planned restart. Then:

```sh
./install.sh --stop
./install.sh  # Use the same component/provider/port flags as the initial install.
```

This stops the control-plane services and retains the database, Runtime state and
credentials. For microsandbox, the systemd user unit uses `KillMode=process`:
only Core stops; microVMs and their work may remain running. Docker-owned Runtime
containers likewise remain Provider resources. The command does not promise to
stop all compute. Release resources through the existing Core API before a full
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

## Data and upgrades

Retain together:

- the dedicated PostgreSQL volume, including large objects;
- `config/credential.key`, caller identity configuration and provider identity;
- microsandbox's private state/cache/disks/snapshots, or Docker-owned Runtime
  volumes and histories;
- the exact distribution and private deployment configuration needed to recover.

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

Provider replacement is an operator operation, not a new `--provider` value on an
existing install. Follow the [maintenance and provider-switch procedure](https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/deploy/microsandbox/README.md#change-the-deployment-provider).
The installer never migrates Sessions between providers or deletes old compute.

## Exposure and network policy

API and console bind to host loopback. With native Core, PostgreSQL publishes an
installation-specific loopback port; with container Core it has no published port.
The production Web proxy only forwards the public `/v1` surface to its configured
Core; it never receives the Docker socket, KVM device or provider/model secrets.
It requires an independent console password and rejects untrusted browser origins.
This is a single-operator console deployment, not a multi-user identity system.

A native Core restart preserves resident microVM processes. Host reboot, user
manager termination and loss of a running microVM are not equivalent to that
restart and are not qualified cold-recovery workflows. Completed idle snapshots
retain their existing recovery contract.

microsandbox uses an explicit policy: public egress, the Core/DNS host ports needed
for the colocated Runtime, and denied inbound/private-network access. Private
model/MCP endpoints require an explicit operator policy change. Native tool network
policy remains the Session's separate public configuration.

Docker uses the existing qualified nested-sandbox Runtime policy. Only Core can
access the selected host Docker daemon. Install on a trusted service host and do
not share its Docker authority with untrusted users.

Host virtualization, credentials, tenant isolation, durable state and actual
execution are release acceptance requirements. Other missing operational screens
or low-frequency improvements belong in the backlog; they do not turn this batch
into a Web redesign or a complete protocol-compatibility claim.
