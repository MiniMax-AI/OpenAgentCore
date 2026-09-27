# Nodes and sandbox backends: operator reference

This reference is for operators who need the details behind Web's **Nodes** page
(shown as **Sandbox backend** when the deployment uses E2B): the node protocol,
manual registration, placement, maintenance and failure handling. To add, remove or
troubleshoot a node, start with the [nodes guide](../../docs/getting-started/nodes.md).

One Core execution owner manages sandbox nodes on its own host and on other Linux
hosts, or provisions cloud sandboxes directly through E2B. A deployment selects
exactly one provider: `e2b`, `docker` or `microsandbox`. PostgreSQL owns that
selection, the per-sandbox resource limits and the immutable Runtime release.
Docker and microsandbox nodes must match its installation, generation and
specification. These nodes are distinct from user-managed `self_hosted` Environments,
whose provisioning remains the application's responsibility.

The Nodes page uses the Core key, separate from Project API keys. After console
sign-in, the console server forwards the page's `/core/v1/sandbox` requests, like
every other `/core/v1` request, with the Core key it reads from its private file; the
key never reaches the browser. For a Web you run without the installer, configure the
private `0600` key file through `OAC_WEB_CORE_KEY_FILE`; Web refuses to start
without it. Core serves the deployment and node routes only when
`OAC_INSTALLATION_ID` is set. Project keys cannot register, edit or
remove nodes. The node installation payload that Web serves contains no secret.

## Sandbox backend selection

A new installation selects microsandbox at Web's Standard size unless
`install.sh --sandbox` chose Docker, E2B or none. Without a selection, the
**Nodes** page first asks for **E2B cloud** or **Own machines**; own machines then
choose Docker or microsandbox, the per-sandbox resources and the matched Runtime
release. E2B takes an account API key and a qualified immutable Runtime template build
(`template-id:build-uuid`) that must be ready before the selection can be saved. E2B
CPU and memory limits are optional: Web sends none, and Core adopts the build's size;
supplied limits must match the build. The key is write-only, encrypted by Core and
never returned to the browser. E2B needs no node installation.

Setup takes no Core address. Nodes and guests use the installation public URL, where
the reverse proxy sends `/api/v1`, including WebSocket upgrades, directly to Core; Web
does not forward it. The deployment reports it as the read-only `core_url`. E2B
requires a public URL that is not loopback; a loopback public URL serves only local
development, because a guest's loopback address cannot reach its Core host.

**Save configuration** validates and initializes the deployment without creating
compute. A failed candidate leaves the previous selection intact. Refresh after an
uncertain response before trying again. The selection persists in PostgreSQL and
activates without a restart; removing all nodes does not reset it. E2B uses the same
daemon, harness and workspace Runtime as node-backed hosting. Configuration alone does
not prove provider or model readiness. To prepare the qualified E2B Runtime build, use
[the E2B build guide](deploy/e2b/README.md); it is not a public Environment Template.
Microsandbox suspends eligible idle Sessions after 300 seconds and retains their
snapshots for 86400 seconds. Docker and E2B have no memory snapshot policy.
A node's allocation list reports `compute_phase_changed_at`, the time each
allocation entered its current `compute_phase`, or null when unknown; an allocation
that existed before Core recorded it reports null until its next phase change. A
suspended allocation's age, combined with this retention, tells roughly when Core
reclaims it.
See [what each field means per sandbox provider](../../contracts/agents-api/sandbox-deployment.md#what-each-field-means-per-sandbox-provider)
for fields that differ between E2B, Docker and microsandbox.

For deployments without the installer that use E2B, install the packaged helper and set
`OAC_E2B_PROVIDER_BIN` to its absolute executable path. Set
`OAC_E2B_STATE_DIR` to a persistent directory owned by the Core service user,
mode `0700`. The standard distribution prepares both. Back up this private state
with the database and credential-encryption key; losing it can leave an uncertain
allocation that cannot safely be reclaimed. Do not mount it into Web or Runtime.

Drain and confirm cleanup before revoking the configured E2B account key. Replacing
that key currently uses the same zero-resource configuration guard; in-place key
rotation with retained resources is not supported. If the key is revoked early,
Core keeps unverifiable allocations and blocks switching, even if compute was
removed through the provider console. Do not clear database allocations or private
receipts to bypass this check. A future credential-repair operation must verify
account/resource ownership before accepting a replacement key; an inaccessible
sandbox or empty listing from another account is not proof of cleanup.

For own machines, **Add node** generates a one-time command that uses the
installation public URL. Before installing, the node installer reads the active
specification with its enrollment token; this read does not consume the token. The
local provider file is an installed copy of the server configuration and cannot select
a different Runtime or resource profile. Closing the dialog discards its one-time
command. An expired command requires explicit regeneration; failed or uncertain writes
are never retried automatically. Registration itself creates no Session, sandbox or
model request. Hosted Session admission fails until setup is complete and a ready node
has capacity. E2B allocates directly without this node requirement.

For deployments without the installer and with zero nodes, set
`OAC_INSTALLATION_ID` to a stable UUID, configure
`OAC_CORE_KEY_DIGESTS_FILE`, and enable the daemon gateway with
`OAC_PUBLIC_URL`. Core derives the hosted Runtime bootstrap and the public
daemon WebSocket address from that URL; it never uses the request Host or forwarded
headers. Preserve the installation UUID and database together.

Use the live sandbox deployment response (`GET /core/v1/sandbox/deployment`) for
the current selection, including one made after startup.

## Resources and Runtime

The same deployment specification applies to every hosted sandbox. CPU count and
memory in MiB are required for Docker and microsandbox; E2B may omit them and adopt
its template build's size. Microsandbox also requires separate root and
`/environment` disk capacities. Docker and E2B reject nonzero independent disk
limits because this contract does not enforce those hard quotas. Node active and
retained reservation limits are separate controls; they do not resize a sandbox.

For Docker/microsandbox, select one verified distribution containing its source
commit, Docker image ID, OCI manifest digest, microsandbox image reference,
Runtime binary SHA-256 and firmware SHA-256. The node installer compares these
identities with the downloaded manifest and checks installed provider settings.
E2B selects its immutable Runtime through the template build instead. See the
[deployment protocol](../../contracts/agents-api/sandbox-deployment.md) for fields,
validation and safe response shapes. The response's `specification.resources`
contains limits; its top-level `resources` contains cleanup counts.

The Core installer never enrolls its own host. The Core host joins like any other
host, through **Add node**, with the same database selection and registration
checks. Node identity and configuration live under
`~/.oac/nodes/<installation-id>/` in the home of the account that runs the node
(`/var/lib/oac-node` for a node added with sudo); preserve that private directory
and its backend storage together. Core has no embedded local provider configuration or node
identity mount.

## Older file-managed installations

Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`. It does not automatically convert
an old file-managed database into a Web-managed deployment. Removing the setting
alone is insufficient: the database retains its original configuration ownership.

Keep the previous release, original database and backend available to settle work
and confirm cleanup. Stopped compute, retained snapshots, pending Environments and
unknown operations remain owned until their normal cleanup completes. Preserve
business data, Runtime history, private receipts and node state; never clear rows
or prune provider storage to bypass the guard. Retire the old file configuration
only as part of an explicit deployment transition. Automatic old-database adoption,
cross-provider Session migration and a force-reset operation are not supported.
The current installation path uses a database-managed deployment.

## Register a host

Web's **Add node** command is the supported way to register a host; the
[nodes guide](../../docs/getting-started/nodes.md) describes its sudo and no-sudo
modes, what they create, uninstalling and troubleshooting. The command downloads only a
matched bootstrap from `/node-install/`, then checksum-verified prebuilt assets from
the same console's payload, never from a release URL the build recorded. The console
must hold them: install from the offline bundle, or place the release assets in the
bundle's `artifacts/` directory first; `/console/config` lists the providers whose
assets it holds (`node_artifacts`). The enrollment token is transient and never a
console or Project credential; the command passes it to the installer on standard
input (`--enrollment-token-stdin`).

Nodes use only these machine connection routes, authenticated by their own
credentials. Web does not forward them, and no administrator credential applies. The
reverse proxy routes `/api/v1`, with WebSocket upgrades, directly to Core; see
[HTTPS and the reverse proxy](../../docs/getting-started/install.md#https-and-the-reverse-proxy).

| Route | Credential |
| --- | --- |
| `GET /api/v1/sandbox-node/configuration` | Enrollment token, or node credential with `X-OAC-Node-ID` |
| `POST /api/v1/sandbox-node/enroll` | One-use enrollment token |
| `GET /api/v1/sandbox-node/identity?node_id=` | Node credential |
| WebSocket `GET /api/v1/sandbox-node/connect?node_id=` | Node credential |

Enrollment sends the Core origin the node stores (`core_url`, from `register
--core-url`). Core refuses one that is not the installation public URL with 409
`sandbox_node_address_mismatch`, before consuming the token, so a node never records
an address Core no longer uses. `POST /core/v1/sandbox/enrollment-tokens` also returns
a non-secret `enrollment_id`; the node it registers reports the same value in
`/core/v1/sandbox/nodes`, and nodes enrolled before Core recorded it report null.

A node and its Core must come from the same distribution. Nodes from releases
that used the removed `/core/v1/sandbox` node paths cannot connect to this Core:
drain with the previous release, then upgrade and enroll new nodes, as described
in [Upgrade notes](../../docs/getting-started/operations.md#node-connections-at-apiv1).
Rerunning the same command preserves the node's private identity. A registered retry
reads configuration with its retained node credential and `X-OAC-Node-ID`; it does
not enroll again. Changes to the Core origin, installation, generation, specification
or release are refused without rewriting state.

### Manual registration

Manual registration remains available for operator-managed payloads. Build or install
`oac-node` from the same Core release. On the host, first read
`GET /api/v1/sandbox-node/configuration` with the enrollment Bearer token. Build the
private provider JSON from its `provider`, `installation_id`, `core_url` (the
installation public URL), `generation` and `specification`, plus one adapter object
for the host:

- Docker: an explicit Unix socket `host`, the locally imported `image`, `network`,
  `extra_hosts` and an absolute `seccomp_file`, with the existing nested-sandbox
  security settings. The imported image must match the approved Runtime release.
- Microsandbox: absolute `helper_path`, `runtime_home`, `runtime_path` and
  `firmware_path`, the host network policy and validated resource and Runtime copies.
  The runtime home is an existing private backend namespace. The native Unix socket
  limit requires `$HOME/.oac/m/<12-character installation hash>` to fit within 48
  encoded bytes; the installer rejects a longer path before creating node state, so
  use a service account with a shorter persistent home.

The node file has no `max_active`, `max_retained`, idle-policy or maintenance
override, and a node never receives arbitrary host paths from the browser. Editing the
downloaded specification does not change Core: the node validates its content and file
hashes, and Core compares its digest at enrollment and on every connection. A mismatch
rejects work until the approved configuration is restored. Node capacity is approved by
Core when the administrator creates an enrollment token and retained in PostgreSQL;
registration and node files can't overwrite it. See
[Node capacity](../../docs/configuration.md#node-capacity).

Save the single-use registration token in a `0600` file on the host; it expires after
the duration shown by Core. Run, with real absolute paths:

```sh
oac-node register \
  --config /var/lib/parsar/provider.json \
  --state-dir /var/lib/parsar/node \
  --core-url https://core.example \
  --name worker-1 \
  --enrollment-token-file /var/lib/parsar/enrollment-token
oac-node run \
  --config /var/lib/parsar/provider.json \
  --state-dir /var/lib/parsar/node
```

Run the second command under the host's service supervisor. The node initiates
its connection to Core; Core does not need SSH or an exposed Docker TCP daemon.
Registration persists the node identity before contacting Core, allowing a lost
registration response to be recovered without replacing the identity. Keep the
state directory on persistent storage, private to the node service account.
One process owns it at a time. Do not copy a node identity into another state
directory or onto another host. Core rejects duplicate connections while the
original connection is opening, live or finishing disconnect cleanup. A half-open
connection must reach its heartbeat timeout before a reconnect can be admitted;
the node retries with bounded backoff. These checks do not attest physical host
identity. The enrollment token is not the node credential. Reconnection reads the
current capacity from the database; it never writes local limits back to Core.

## Placement and recovery

Session creation chooses an available node automatically; callers cannot select
one. Placement commits with Session creation and remains fixed across creation
retries, later Turns and resume. Administrators see each node's allocations under
`/core/v1/sandbox/nodes`.

Microsandbox suspends only after a Turn has finished, no work is pending, and the
idle interval has passed. Core measures terminal activity from its database's
first committed completion observation, so clock differences between hosts do not
shorten or extend that idle interval. Public native timestamps remain unchanged;
repeated completion observations and heartbeats do not reset the idle timer.
Idle eligibility and its final transaction check use the database observation
clock, including when PostgreSQL runs on a different host from Core. Snapshot
retention starts from the same clock.
Its full snapshot preserves guest state, files and configuration; a completed
Agent process is not recreated as a resident process. Native restore can omit an
explicit root-disk size because that disk is inherited from the verified snapshot.
Core requires matching snapshot resource proof and exact target identity for this
case; other CPU, memory and environment-disk limits must still match. This is not
permission to resize a retained sandbox.
Docker remains supported without promising memory snapshots. Disconnecting a
node does not delete or move its Sessions. An hour without internal observation
keepalives no longer authorizes cleanup of a node-managed allocation. Reconnect the original node to observe
and continue its existing resources. Unknown creation, initialization, capture
and restore results do not authorize request replay or a replacement sandbox.

## Removal and maintenance

There is no per-node drain switch. Online, eligible nodes participate
automatically. A node with instances, retained snapshots, pending allocations,
unknown operations or cleanup records cannot be removed. Resolve those resources
through their normal lifecycle and then retry; the API reports the conflict.
Offline resources remain owned and visible. Explicit removal permanently retires
the node identity; adding that host again requires a fresh private state directory.
After removal, clean up the host with the installer's `--uninstall`, which Web shows
as **Clean up the host**; see [Remove a node](../../docs/getting-started/nodes.md#remove-a-node).
Uninstall proceeds only when Core answers 401 to the node's credential, or with
`--force` for a Core address that no longer answers. It never removes sandboxes,
volumes or images. The node program exits with status 78 when Core answers 401 to its
credential, and the installed service does not restart it. Other failures, including
an unreachable Core or a 403 from a proxy in front of it, restart the service every
5 seconds without a start limit. Ordinary disconnects and host restarts reuse the
original identity.

Provider, resource-limit and Runtime changes share one deployment-wide procedure:

1. Enter maintenance. This blocks new hosted Sessions and allocations while allowing
   existing work, queries and cleanup.
2. Verify the deployment's allocation and pending-Environment counts are zero.
   Stopped compute, snapshots, unknown creates and pending cleanup still block
   switching. Explicitly archive retained Sessions as described below.
3. Once cleanup is verified, submit the complete replacement. Core validates the
   candidate before changing the database or draining current workers. It then
   drains existing manager calls and repeats the resource/generation checks in the
   commit transaction. A changed selection advances the generation and retires old
   nodes and unused enrollments atomically; history remains intact.
4. Explicitly resume with the returned generation. A rejected candidate preserves
   the old configuration and maintenance state. After an uncertain response, refresh
   before another write. Saving never automatically deletes compute.

During maintenance, explicitly archive each retained Core-managed hosted Session
through `POST /core/v1/projects/{project_id}/sessions/{session_id}/archive`
with the current `expected_generation`. This requests cancellation and revokes
Runtime authority; the existing lifecycle releases compute and snapshots after
provider verification. Poll GET on the same path for `released`, then verify both
deployment counts are zero. Unknown cleanup remains a blocker. Resource release
does not prove that an active Turn has finalized.

Archive preserves Session history and persisted Files/Artifacts. Unpersisted
workspace contents are lost and the original Session cannot resume. Public Session
deletion has different retention behavior and is not needed for this workflow.
After an uncertain archive response, read its disposition before another explicit
write; never automatically retry. See the
[administrator archive contract](../../contracts/agents-api/admin-api.md#administrative-session-archive).
Maintenance and configuration changes alone perform no cleanup.

`GET /core/v1/sandbox/deployment` reports the safe configuration, `generation` and
`resources` counts. `PATCH /core/v1/sandbox/deployment/maintenance` takes
`maintenance` and `expected_generation`; `PUT /core/v1/sandbox/deployment` takes
the provider, complete `resources`/`runtime` selection, any E2B input and
`expected_generation`. Both require deployment admin
authority. Neither request takes a Core address. E2B is not
combined with own-machine nodes, and existing Sessions never migrate providers.

This release has one Core execution owner. It does not add Core multi-active,
cross-node snapshot restore, automatic failover, Kubernetes or autoscaling.

## Node and host failures

A restarted node service reuses its saved identity and checks the original
resources. After a host reboot, Core observes the exact instance or already
authorized snapshot; it does not silently cold-start a replacement. Missing or
corrupt resources remain owned and appear with a sanitized diagnostic. Restore
the original host/storage or explicitly resolve the affected Session. Preserve
the identity directory alongside backend storage backups: losing that directory
is a recovery incident, not permission to register over existing resources.

Each registered node has an independent serial lifecycle worker, including its
resource scans, pending creation, initialization and direct provisioning. A slow
or stuck helper on one node does not hold another node's lifecycle gate or scan
page. Offline workers retain their resource records and resume observation after
reconnection. Lifecycle concurrency is one operation per node, so its total grows
with the registered node count; there is no fixed global provider concurrency
limit. The single Core execution lease and atomic per-node capacity checks remain
in force. Losing the execution lease stops every worker; an ordinary provider
failure affects only its node. Shutdown cancels and drains workers and direct
provisioning before releasing that lease.

Connections use heartbeats and bounded reconnect backoff. Operation timeouts are
relative budgets measured locally on receipt, so host wall-clock skew cannot
misclassify a fresh operation as expired. Temporary database or
network failures remain retryable; invalid credentials and identity mismatches
require operator intervention. Old connection and execution epochs cannot
authorize new work after replacement. These controls do not recover lost disks
or migrate a Session to another host.

Core runs node authentication, ownership and storage callbacks outside the Hub's
connection-state mutex, with cancellation and a five-second limit. Hub shutdown
cancels opening and live connections without waiting for database callbacks.
A node's connection reservation remains held until its fenced disconnect cleanup
finishes; a slow database must not allow a competing connection to take its place.

## Node readiness diagnostics

Each heartbeat reports whether the node's local provider is ready. When it is
not, the node list and node detail (`GET /core/v1/sandbox/nodes` and
`GET /core/v1/sandbox/nodes/{node_id}`) carry one fixed `diagnostic` code. The
field is absent while the provider is ready. An offline node keeps its last
reported value, so check `online` first.

The codes, their causes and fixes are listed in the
[nodes guide](../../docs/getting-started/nodes.md#readiness-codes).

A node reports only its first failed check. Checks run from the provider platform
(Docker daemon or KVM), through Docker limit support and host capacity, to the
installed Runtime content. An unreachable Docker daemon therefore hides a missing
image, and missing KVM hides missing artifacts or insufficient capacity. After a
fix, the next heartbeat (about ten seconds) checks again and clears or replaces
the code. Repaired Runtime artifacts, a pulled image or a started Docker daemon
recover this way. A new Docker or KVM group membership applies only to a new
process: restart the node service (`sudo systemctl restart
oac-node-<installation_id>.service` in sudo mode, `systemctl --user restart …`
without sudo). The service journal's warning includes the local error behind the code;
that text never leaves the host.

Only the code crosses the node connection. Probe errors can name host paths or
contain daemon messages; they are not sent to Core, stored or returned. Core stores
any other reported value as `provider_unavailable`. Nodes from older releases send
`provider_unavailable` or no code and keep working unchanged. Upgrade Core before
its nodes: an older Core rejects the new codes and closes an unready newer node's
connection until its provider is ready again.

## Runtime observations

The existing Runtime observation and history APIs route managed reads to the
Session's original node. Reading metrics does not wake a suspended sandbox or
reset its idle timer. An offline node or a provider without an observation source
returns unavailable telemetry. Provider timestamps retain their existing
validation, so excessive node/Core clock skew can also make a sample unavailable;
suspension eligibility continues to use database time.
