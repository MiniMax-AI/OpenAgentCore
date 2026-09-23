# Hosted Sandbox Manager

One Core execution owner can manage sandbox nodes on its own host and on other
Linux hosts. The deployment selects exactly one provider: `docker` or
`microsandbox`. All nodes must use that provider and installation ID. These nodes
are distinct from user-managed `self_hosted` Environments, whose provisioning
remains the user's responsibility.

The Web console's **Hosted Sandbox Manager** page uses deployment administrator
authority, separate from project credentials. In a paired distribution, the
console server reads its own administrator key and forwards it only on sandbox
management routes after console login. No administrator key reaches the browser.
There is no second login or manual administrator-key form. Management uses the
same-origin paired console connection; direct remote project connections do not
grant deployment access. An unpaired console shows setup guidance. For manual or
Web-only deployments, an operator can configure the matching private `0600` token
file server-side through `CORE_CONSOLE_SANDBOX_ADMIN_TOKEN_FILE`.

The sandbox page and its setup, enrollment, status and diagnostic controls support
Chinese and English. Choose a language in System navigation; the preference is
saved, and otherwise the page follows the browser's first language. Nodes are the
main view, arranged around Core in a desktop topology with readiness and capacity
visible immediately. Select a node for allocation records, diagnostics and guarded
removal; deployment identifiers are available in secondary details. Animated links
indicate live connections, not measured traffic. Offline links are static, and
reduced-motion preferences disable decorative animation.

The installer creates the separate key under the private `admin/` directory,
including zero-node installs. Core receives its digest; the bundled Web server
receives the original private key. Neither is included in static assets or the
node installation payload. Project keys cannot register, edit or remove nodes.

## Start with zero nodes

Default installation starts Core, Web and PostgreSQL without local compute.
Hosted Sandbox Manager first asks for Docker or microsandbox. It defaults to the
paired console origin, which forwards the required Core API and WebSocket routes.
Use advanced network settings only when nodes and guests need a different public
HTTPS origin. When the inferred address is loopback or is not HTTPS, setup
opens the network field and requires a non-loopback HTTPS origin before saving.
The API still accepts HTTP loopback for explicit local development; a guest's
loopback address cannot reach its Core host.

Saving initializes the deployment once. An identical request may be retried;
a different provider or origin returns a conflict. Refresh after an uncertain
response before trying again. Selection persists in PostgreSQL, activates without
a restart and applies to every node. Removing all nodes does not reset it.
Microsandbox suspends eligible idle Sessions after 300 seconds and retains their
snapshots for 86400 seconds. Docker has no memory snapshot policy.

Click **Add node**, copy the installation command from the dialog, and run it on
the target Linux amd64 host. The command uses the saved deployment origin, or the
paired console origin for file-managed deployments, without a routine URL field.
Closing the dialog discards its one-time command. An expired command requires
explicit regeneration; failed or uncertain writes are never retried automatically. The installer checks prerequisites, downloads the matched payload,
checks its hashes, prepares provider configuration and starts the existing node
program as a systemd user service. Web polls readiness and capacity while waiting.
It does not install software through SSH. Registration itself
does not create a Session, sandbox or model request. Hosted Session admission
fails until setup is complete and a ready node has capacity.

For manual zero-node deployments, set `AGENTS_API_SANDBOX_INSTALLATION_ID` to a
stable UUID, configure `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`, and enable the daemon
gateway using `AGENTS_API_DAEMON_WS_URL`. Do not also set
`AGENTS_API_MANAGED_RUNTIMES_FILE`. The Web-selected origin supplies hosted Runtime
bootstrap and its public daemon WebSocket address; it never uses request Host or
forwarded headers. Preserve the installation UUID and database together.

The startup configuration API remains a startup snapshot. Use the live sandbox
deployment response for a selection made after startup.

## Configure a local or file-managed deployment

Keep the existing `AGENTS_API_MANAGED_RUNTIMES_FILE` JSON. Existing Docker and
microsandbox configurations participate through an embedded local node, using
the same protocol as remote nodes. Local means the Core server, not the browser.
Its durable identity is stored beside the configuration in a private directory,
or at the absolute `AGENTS_API_SANDBOX_NODE_STATE_DIR` you supply. Preserve this
directory across service/container restarts. Do not share it between hosts.

Set `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` to a JSON array of SHA-256 hex digests
of administrator bearer credentials. Keep the original randomly generated bearer
credential in the operator's password manager. This file is separate from
`AGENTS_API_KEYS_FILE`; omitting it disables deployment administration while
preserving existing local execution. Serve the API through HTTPS for remote
nodes. The reverse proxy must support the WebSocket endpoint
`/core/v1/sandbox/node/connect` and preserve Authorization headers.

An optional `nodes` object makes local participation and reservation limits
explicit:

```json
"nodes": {"local": true, "max_active": 4, "max_retained": 16}
```

Without this object, local Docker defaults to four active and sixteen retained
allocations. Local microsandbox uses its existing `max_active` and `max_retained`
configuration. Tune capacity to the host and fixed guest resource settings.
Capacity counts pending allocations and uncertain cleanup as well as running
instances. Displayed host metrics are observations, not an overcommit guarantee.

A Core with no local compute can instead use this Docker configuration:

```json
{
  "core_url": "https://core.example/api/v1",
  "provider": "docker",
  "installation_id": "REPLACE_WITH_CANONICAL_UUID",
  "maintenance": false,
  "nodes": {"local": false, "max_active": 4, "max_retained": 16}
}
```

Do not include a `docker` or `microsandbox` backend object in a remote-only Core
configuration. For remote-only microsandbox, set `provider` to `microsandbox`
and add positive `idle_seconds` and `retention_seconds` to `nodes`. These are the
deployment's idle suspension policy. Local microsandbox continues to read the
policy from its existing `microsandbox` object.

`AGENTS_API_DAEMON_WS_URL` and `core_url` must be reachable from the guests.
The embedded node connects to Core's loopback listener. If Core listens only on
a non-loopback address, set `AGENTS_API_SANDBOX_NODE_CORE_URL` to its HTTPS origin.
Do not expose a plaintext remote node connection.

## Upgrade an existing single-host deployment

Keep the original backend and configuration when upgrading. Matching installation
IDs and socket/runtime paths alone do not prove that resources are on this host.
Before admitting work, Core verifies each unreleased allocation against its actual
container, exact compute instance or verified full snapshot. Stopped resources can
provide ownership evidence; missing resources cannot. One failed or uncertain
check rejects the complete adoption without assigning nodes or deleting resources.
Errors identify the allocation and a safe reason such as missing resources,
incorrect ownership or an unconfirmed snapshot.

Restore access to the original backend before retrying. Docker volume-only remnants
and unfinished transitions that have already consumed a restore lack sufficient
read-only evidence for this upgrade; use the previous Core to finish or resolve
that lifecycle first. There is no force-adopt or cross-host migration switch.
Pending Environments with no allocation receive their first local placement.
Released historical allocations remain unassigned. Successful first adoption starts
the idle interval from Core's database clock; subsequent restarts preserve it and
do not extend snapshot retention.

## Register a host

The paired console provides a complete installation command. It downloads only
fixed public distribution artifacts from `/node-install/`; the enrollment token
is transient and never a console/project credential. Python 3.9+, a systemd user
session with lingering, and Docker access or KVM/native-library prerequisites
must already exist on the target host. Rerunning the same command preserves the
node's private identity; changes to its Core, installation, provider or release
are refused. Use a newly generated token if an unconsumed one expires.

For a public paired endpoint, use `install.sh --public-url https://core.example`
and an operator-managed TLS reverse proxy preserving Host and WebSocket Upgrade.
The console passes node and daemon credentials unchanged on a fixed route list;
Core authenticates them. No administrator credential is used on these routes.
Manual registration remains available for operator-managed payloads:


Build/install `parsar-sandbox-node` from the same Core release. On the host,
provide a private node provider JSON using the existing Docker or microsandbox
backend schema. Match Core's provider and installation UUID; use that host's
own socket, runtime paths and immutable image. Docker needs access to its local
Unix socket and pinned image. Microsandbox needs its qualified runtime, helper,
firmware and KVM. A node never receives arbitrary host paths from the browser.

In Hosted Sandbox Manager, generate a single-use registration token. Save it in
a `0600` file on the host. The token expires after the duration shown by Core.
Run the displayed command with real absolute paths, for example:

```sh
parsar-sandbox-node register \
  --config /var/lib/parsar/provider.json \
  --state-dir /var/lib/parsar/node \
  --core-url https://core.example \
  --name worker-1 \
  --max-active 4 --max-retained 16 \
  --enrollment-token-file /var/lib/parsar/enrollment-token
parsar-sandbox-node run \
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
identity. The enrollment token is not the node credential.

## Placement and recovery

Session creation chooses an available node automatically. The advanced node
selector requests a particular node; unavailable or full selection fails rather
than silently falling back. API callers can use the explicit Core extension:

```json
"x_agents_core": {"sandbox_node_id": "NODE_UUID"}
```

Placement commits with Session creation and remains fixed across creation
retries, later Turns and resume. Session details show that placement. The narrow
project-authenticated node directory and Session placement endpoint do not grant
administration privileges or expose other tenants' resources.

Microsandbox suspends only after a Turn has finished, no work is pending, and the
idle interval has passed. Core measures terminal activity from its database's
first committed completion observation, so clock differences between hosts do not
shorten or extend that idle interval. Public native timestamps remain unchanged;
repeated completion observations and heartbeats do not reset the idle timer.
Idle eligibility and its final transaction check use the database observation
clock, including when PostgreSQL runs on a different host from Core. Snapshot
retention starts from the same clock.
Its full snapshot preserves guest state, files and configuration; a completed
Agent process is not recreated as a resident process.
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
Ordinary disconnects and host restarts reuse the original identity. The configured embedded local node
also cannot be removed until its local configuration is disabled through the
maintenance transition. That transition currently changes the deployment backend
configuration and requires resources on all nodes to be cleared; it is not a
per-node drain operation.

Provider changes remain an explicit deployment maintenance operation: start the
old configuration in maintenance, resolve all old resources, then start the new
provider configuration in maintenance before reopening admission. Merely changing
a configuration file or disconnecting hosts does not clear resources. Preserve
old node state and backend storage until cleanup is confirmed. A new backend
requires its own node identity. Existing Sessions do not migrate across nodes or
providers. Switching local participation also changes backend configuration and
is subject to the same resource guard; it is not an automatic migration.

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

## Runtime observations

The existing Runtime observation and history APIs route managed reads to the
Session's original node. Reading metrics does not wake a suspended sandbox or
reset its idle timer. An offline node or a provider without an observation source
returns unavailable telemetry. Provider timestamps retain their existing
validation, so excessive node/Core clock skew can also make a sample unavailable;
suspension eligibility continues to use database time.
