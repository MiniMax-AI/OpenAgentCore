# Hosted Sandbox Manager

One Core execution owner can manage sandbox nodes on its own host and on other
Linux hosts, or provision cloud sandboxes directly through E2B. A deployment selects
exactly one provider: `e2b`, `docker` or `microsandbox`. PostgreSQL owns that
selection, the per-sandbox resource limits and the immutable Runtime release.
Docker/microsandbox nodes must match its installation, generation and specification. These nodes
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
Hosted Sandbox Manager first asks for **E2B cloud** or **Own machines**. Own machines
then choose Docker or microsandbox. Supply the per-sandbox resources and matched
Runtime release as part of initial setup. E2B takes an account API key, CPU/memory
limits and a qualified immutable Runtime template build (`template-id:build-uuid`);
its exact ready build must match those limits before the selection can be saved. The key is write-only,
encrypted by Core and never returned to the browser. E2B needs no node installation. It defaults to the
paired console origin, which forwards the required Core API and WebSocket routes.
Use advanced network settings only when nodes and guests need a different public
HTTPS origin. When the inferred address is loopback or is not HTTPS, setup
opens the network field and requires a non-loopback HTTPS origin before saving.
The API still accepts HTTP loopback for explicit local development; a guest's
loopback address cannot reach its Core host.

Saving validates and initializes the deployment without creating compute. A failed
candidate leaves the previous selection intact. Refresh after an uncertain response before
trying again. Selection persists in PostgreSQL and activates without a restart.
Removing all nodes does not reset it. E2B uses the same daemon, harness and workspace
Runtime as node-backed hosting. Configuration alone does not prove provider or model
readiness. To prepare the qualified E2B Runtime build, use
[the E2B build guide](deploy/e2b/README.md); it is not a public Environment Template.
Microsandbox suspends eligible idle Sessions after 300 seconds and retains their
snapshots for 86400 seconds. Docker and E2B have no memory snapshot policy.

For source/manual deployments using E2B, install the packaged helper and set
`AGENTS_API_E2B_PROVIDER_BIN` to its absolute executable path. Set
`AGENTS_API_E2B_STATE_DIR` to a persistent directory owned by the Core service user,
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

For own machines, click **Add node**, copy the installation command from the dialog, and run it on
the target Linux amd64 host. The command uses the saved deployment origin. Before
installing, it reads the active specification with its enrollment token; this read
does not consume the token. The local provider file is an installed copy of the
server configuration and cannot select a different Runtime or resource profile.
Closing the dialog discards its one-time command. An expired command requires
explicit regeneration; failed or uncertain writes are never retried automatically. The installer checks prerequisites, downloads the matched payload,
checks its hashes, prepares provider configuration and starts the existing node
program as a systemd user service. Web polls readiness and capacity while waiting.
It does not install software through SSH. Registration itself
does not create a Session, sandbox or model request. Hosted Session admission
fails until setup is complete and a ready node has capacity. E2B allocates directly without this node requirement.

For manual zero-node deployments, set `AGENTS_API_SANDBOX_INSTALLATION_ID` to a
stable UUID, configure `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`, and enable the daemon
gateway using `AGENTS_API_DAEMON_WS_URL`. Do not also set
`AGENTS_API_MANAGED_RUNTIMES_FILE`. The Web-selected origin supplies hosted Runtime
bootstrap and its public daemon WebSocket address; it never uses request Host or
forwarded headers. Preserve the installation UUID and database together.

The startup configuration API remains a startup snapshot. Use the live sandbox
deployment response for a selection made after startup.

## Resources and Runtime

The same deployment specification applies to every hosted sandbox. CPU count and
memory in MiB are required. Microsandbox also requires separate root and
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

An explicitly requested local node uses the ordinary node installer and service,
with the same database selection and registration checks as a remote node. Local
means the Core host, not the browser. The installation option requires a
non-loopback HTTPS `--public-url` reachable from sandbox guests. Node identity and
configuration live under `~/.parsar/nodes/<installation-id>/`; preserve that private
directory and its backend storage together. Core has no embedded local provider
configuration or node identity mount.

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

The paired console provides a complete installation command. It downloads only
a matched bootstrap from `/node-install/`, then checksum-verified prebuilt assets
from the manifest release URL or offline console payload. It reuses verified cache
entries and exact imported images, then waits for Core to confirm connection and
provider readiness. The enrollment token
is transient and never a console/project credential. Python 3.9+, a systemd user
session with lingering, and Docker access or KVM/native-library prerequisites
must already exist on the target host. Rerunning the same command preserves the
node's private identity. A registered retry reads configuration with its retained
node credential and `X-Parsar-Node-ID`; it does not enroll again. Changes to the
Core origin, installation, generation, specification or release are refused
without rewriting state. Use a newly generated token if an unconsumed one expires.

For a public paired endpoint, use `install.sh --public-url https://core.example`
and an operator-managed TLS reverse proxy preserving Host and WebSocket Upgrade.
The console passes node and daemon credentials unchanged on a fixed route list;
Core authenticates them. No administrator credential is used on these routes.
Manual registration remains available for operator-managed payloads:


Build/install `parsar-sandbox-node` from the same Core release. On the host,
first read `GET /core/v1/sandbox/node/configuration` with the enrollment Bearer
token. Build the private provider JSON from its `provider`, `installation_id`,
`core_url`, `generation` and `specification`, using the existing local backend
schema for the host's socket, runtime paths and network policy. The Runtime image
and resource limits must match the server specification. Docker needs access to its local
Unix socket and pinned image. Microsandbox needs its qualified runtime, helper,
firmware and KVM. The native Unix socket limit also requires
`$HOME/.parsar/m/<12-character installation hash>` to fit within 48 encoded bytes.
The installer rejects a longer path before creating node state; use a service
account with a shorter persistent home. A node never receives arbitrary host
paths from the browser.

Node capacity is approved by Core when the administrator creates an enrollment
token, then retained in PostgreSQL. Registration and node files cannot overwrite
it. See [Configuration](../../docs/configuration.md#node-configuration-and-capacity)
for limits, defaults and changes.

In Hosted Sandbox Manager, generate a single-use registration token. Save it in
a `0600` file on the host. The token expires after the duration shown by Core.
Run the displayed command with real absolute paths, for example:

```sh
parsar-sandbox-node register \
  --config /var/lib/parsar/provider.json \
  --state-dir /var/lib/parsar/node \
  --core-url https://core.example \
  --name worker-1 \
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
Ordinary disconnects and host restarts reuse the original identity. A local node
uses the same removal and resource checks as any other enrolled node.

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
through `POST /core/v1/admin/projects/{project_id}/sessions/{session_id}/archive`
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
authority. The public Core origin cannot change in this operation. E2B is not
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

| Code | Cause detected by the node | Action |
| --- | --- | --- |
| `docker_unavailable` | The configured Docker socket is unreachable or not accessible, or the daemon fails its info or image request | Start Docker and give the node service account access to the configured socket |
| `docker_limits_unsupported` | Docker reports no CPU quota or memory limit support | Use a Docker host whose cgroups enforce CPU quotas and memory limits |
| `runtime_image_unavailable` | Docker does not have the pinned Runtime image | Rerun the node installer, or import the image from the matched release |
| `kvm_unavailable` | The node cannot open `/dev/kvm` for reading and writing, or the host is not Linux | Enable hardware virtualization and give the service account KVM access, for example through the `kvm` group |
| `microsandbox_artifacts_unavailable` | The pinned Runtime or firmware is missing or fails its SHA-256 check, or the helper is missing or not executable | Rerun the node installer from the matched release |
| `capacity_insufficient` | The host has fewer CPUs or less memory than one sandbox of the deployment specification | Use a larger host, or change the per-sandbox resources through the maintenance procedure |
| `provider_unavailable` | Any other failure, such as a missing private microsandbox state directory, and every failure reported by an older node | Read the local error in the node's service journal warning and fix that cause |

A node reports only its first failed check. Checks run from the provider platform
(Docker daemon or KVM), through Docker limit support and host capacity, to the
installed Runtime content. An unreachable Docker daemon therefore hides a missing
image, and missing KVM hides missing artifacts or insufficient capacity. After a
fix, the next heartbeat (about ten seconds) checks again and clears or replaces
the code. Repaired Runtime artifacts, a pulled image or a started Docker daemon
recover this way. A new Docker or KVM group membership applies only to a new
process: restart the node with `systemctl --user restart
parsar-node-<installation_id>.service`. The service journal's warning includes
the local error behind the code; that text never leaves the host.

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
