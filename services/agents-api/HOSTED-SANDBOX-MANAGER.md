# Hosted Sandbox Manager

One Core execution owner can manage sandbox nodes on its own host and on other
Linux hosts, or provision cloud sandboxes directly through E2B. A deployment selects
exactly one provider: `e2b`, `docker` or `microsandbox`. PostgreSQL owns that
selection, the per-sandbox resource limits and the immutable Runtime release.
Docker/microsandbox nodes must match its installation, generation and specification. These nodes
are distinct from user-managed `self_hosted` Environments, whose provisioning
remains the user's responsibility.

The Web console's **Nodes** page (shown as **Sandbox backend** when the deployment
uses E2B) uses the Core key, separate from
Project API keys. After console sign-in, the console server forwards the page's
`/core/v1/sandbox` requests, like every other `/core/v1` request, with the Core
key it reads from its private file. The Core key never reaches the browser. There
is no second login or manual key form. For manual or Web-only deployments, an
operator configures the matching private `0600` Core key file server-side through
`CORE_CONSOLE_CORE_KEY_FILE`; Web refuses to start without it. Core serves the
deployment and node routes only when `AGENTS_API_SANDBOX_INSTALLATION_ID` is set.

The sandbox page and its setup, enrollment, status and diagnostic controls support
Chinese and English. Choose a language in System navigation; the preference is
saved, and otherwise the page follows the browser's first language. Nodes are the
main view, arranged around Core in a desktop topology with readiness and capacity
visible immediately. Select a node for allocation records, diagnostics and guarded
removal; deployment identifiers are available in secondary details. Animated links
indicate live connections, not measured traffic. Offline links are static, and
reduced-motion preferences disable decorative animation.

The installer creates the separate key under the private `secrets/` directory,
including zero-node installs. Core receives its digest; the bundled Web server
receives the original private key. Neither is included in static assets or the
node installation payload. Project keys cannot register, edit or remove nodes.

## Start with zero nodes

Default installation starts Core, Web and PostgreSQL without local compute, and
selects Docker sandboxes at Web's Standard size through the route below;
`install.sh --sandbox` chooses microsandbox, E2B or none instead. Without a
selection, setup on the **Nodes** page first asks for **E2B cloud** or **Own machines**. Own
machines then choose Docker or microsandbox. Supply the per-sandbox resources and
matched Runtime release as part of initial setup. E2B takes an account API key and
a qualified immutable Runtime template build (`template-id:build-uuid`) that must be
ready before the selection can be saved. E2B CPU/memory limits are optional: Web
sends none, and Core adopts the build's size; supplied limits must match the build.
The key is write-only, encrypted by Core and never returned to the browser. E2B
needs no node installation.
Setup takes no Core address. Nodes and guests use the installation public URL,
where the reverse proxy sends `/api/v1`, including WebSocket upgrades, directly to
Core; Web does not forward it. The deployment reports it as read-only `core_url`.
E2B requires a public URL that is not loopback; a loopback public URL serves only
local development, because a guest's loopback address cannot reach its Core host.

**Save configuration** validates and initializes the deployment without creating
compute. A failed candidate leaves the previous selection intact. Refresh after an uncertain response before
trying again. Selection persists in PostgreSQL and activates without a restart.
Removing all nodes does not reset it. E2B uses the same daemon, harness and workspace
Runtime as node-backed hosting. Configuration alone does not prove provider or model
readiness. To prepare the qualified E2B Runtime build, use
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
the target Linux amd64 host. The command uses the installation public URL. Before
installing, it reads the active specification with its enrollment token; this read
does not consume the token. The local provider file is an installed copy of the
server configuration and cannot select a different Runtime or resource profile.
Closing the dialog discards its one-time command. An expired command requires
explicit regeneration; failed or uncertain writes are never retried automatically. The installer checks prerequisites, downloads the matched payload,
checks its hashes, prepares provider configuration and starts the existing node
program as a service (see [Register a host](#register-a-host)). Web polls readiness
and capacity while waiting.
It does not install software through SSH. Registration itself
does not create a Session, sandbox or model request. Hosted Session admission
fails until setup is complete and a ready node has capacity. E2B allocates directly without this node requirement.

For manual zero-node deployments, set `AGENTS_API_SANDBOX_INSTALLATION_ID` to a
stable UUID, configure `AGENTS_API_CORE_KEY_DIGESTS_FILE`, and enable the daemon
gateway with `AGENTS_API_PUBLIC_URL`. Do not also set
`AGENTS_API_MANAGED_RUNTIMES_FILE`. Core derives hosted Runtime bootstrap and the
public daemon WebSocket address from that URL; it never uses request Host or
forwarded headers. Preserve the installation UUID and database together.

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
`~/.parsar/nodes/<installation-id>/` in the home of the account that runs the node
(`/var/lib/parsar-node` for a node added with sudo); preserve that private directory
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

The paired console provides a complete installation command. It downloads only
a matched bootstrap from `/node-install/`, then checksum-verified prebuilt assets
from the same console's payload, never from a release URL the build recorded. The
console must hold them: install from the offline bundle, or place the release assets
in the bundle's `artifacts/` directory first; `/console/config` lists the providers
whose assets it holds (`node_artifacts`). It reuses verified cache
entries and exact imported images, then waits for Core to confirm connection and
provider readiness. The enrollment token
is transient and never a console/project credential; the command passes it to the
installer on standard input (`--enrollment-token-stdin`), never in a process argument,
an environment variable or sudo's log. A download that brings less than 64 KiB in a
minute stops; the downloaded part is kept, and running a new command resumes it.

The installer chooses how the node runs from the user that runs it:

- **With sudo or as root (the default command).** It prepares the host itself. It
  creates the system user `parsar-node` (home `/var/lib/parsar-node`) if missing,
  or adopts an existing one with that home and a nologin shell. It adds the user to
  the `docker` group (Docker) or the `kvm` group (microsandbox), whichever owns the
  device; any other device group is refused. The node runs as the root-owned system
  service `/etc/systemd/system/parsar-node-<installation-id>.service` with
  `User=parsar-node`, enabled for boot. No lingering or login session is needed;
  logs are in `sudo journalctl -u parsar-node-<installation-id>.service`. Node state
  lives in `/var/lib/parsar-node/.parsar/nodes/<installation-id>/`, and what the
  installer created or changed is recorded in `/etc/parsar-node/`. Files the service
  user owns are written and deleted only with that user's credentials; root handles
  the account, the group, the unit and the Docker network. It never installs
  Docker, KVM or packages, never changes device permissions and refuses
  SELinux-enforcing hosts; a missing prerequisite stops it with a one-line hint
  before anything changes. A foreign account named `parsar-node` is refused. Sudo
  mode serves **one Core per host**: every sudo-mode node shares `parsar-node`, so a
  node for a second Core is refused. A node for the same installation installed
  without sudo is found in the invoking user's home or, for Docker, by its network
  on the same engine; other users' homes are not searched. The steps that run as
  `parsar-node` start in their own session with no terminal, so nothing they run
  can reach the administrator's terminal, and their output appears only as plain
  text. Interrupting the installer or closing its terminal stops those steps as
  well.

  **Docker mode is root-equivalent.** Membership in the `docker` group lets
  `parsar-node`, and so anything that controls the node, act as root on that host.
  This is inherent to running sandboxes on Docker and equally true in the no-sudo
  mode. Add Docker nodes only on hosts dedicated to running sandboxes. Microsandbox
  nodes need only the `kvm` group.

  Pass the token on standard input, as the command does. In sudo mode the installer
  refuses `PARSAR_NODE_ENROLLMENT_TOKEN`, because `sudo VAR=… python3` records the
  variable in sudo's log. A sudoers policy with `log_input` records standard input
  as well; the token is single-use and expires after ten minutes.
- **As a normal user (for hosts without sudo).** The node runs as that user's
  systemd user service with state under `~/.parsar/nodes/<installation-id>/`. The
  user needs lingering and Docker or KVM access, which an administrator grants
  beforehand. The installer finds the user's systemd manager even from `su` or
  `sudo -iu`, where no login session sets `XDG_RUNTIME_DIR`.

Python 3.9+, curl and sha256sum, and Docker Engine or KVM with microsandbox's
native libraries must already exist on the target host. Rerunning the same command preserves the
node's private identity. A registered retry reads configuration with its retained
node credential and `X-Parsar-Node-ID`; it does not enroll again. Changes to the
Core origin, installation, generation, specification or release are refused
without rewriting state. Use a newly generated token if an unconsumed one expires.

For a public paired endpoint, use `install.sh --public-url https://core.example`
and an operator-managed TLS reverse proxy preserving Host and WebSocket Upgrade.
The proxy routes `/api/v1` directly to Core; see
[Expose Core and Web](../../docs/getting-started/install.md#expose-core-and-web).
Nodes use only these machine connection routes, authenticated by their own
credentials; Web does not forward them and no administrator credential applies:

| Route | Credential |
| --- | --- |
| `GET /api/v1/sandbox-node/configuration` | Enrollment token, or node credential with `X-Parsar-Node-ID` |
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
in [Data and upgrades](../../docs/getting-started/operations.md#data-and-upgrades).
Manual registration remains available for operator-managed payloads:

Build/install `parsar-sandbox-node` from the same Core release. On the host,
first read `GET /api/v1/sandbox-node/configuration` with the enrollment Bearer
token. Build the private provider JSON from its `provider`, `installation_id`,
`core_url` (the installation public URL), `generation` and `specification`, using the existing local backend
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

On the **Nodes** page, generate a single-use registration token. Save it in
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
After removal, clean up the host with the installer's `--uninstall`, using the same
checked download as the add command:

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\n' '<node_installer_sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
$s python3 "$d/node-install.pyz" --uninstall --installation-id '<installation-id>')
```

For a node installed as a normal user, run it as that user; it does not use sudo
then. The installer's checksum is `node_installer_sha256` in the console's
`/console/config`. Uninstall proceeds only when Core answers 401 to the node's
credential, or with `--force` for a Core that no longer exists. It stops and removes
the service, the node state and the Docker network. The `parsar-node` account is
deleted only when the installer created it and no node remains. An adopted account
and its home directory stay; uninstall removes only the groups the installer added. Uninstall never removes
sandboxes, volumes or images: it keeps the Runtime image and a microsandbox node's
store (`/var/lib/parsar-node/.parsar/m/<hash>`, its images and any sandbox state),
prints how to remove them (`sudo -u parsar-node rm -rf <store>`), and keeps a
created account until that store is gone.
With `--force`, microVMs that `KillMode=process` left running may still use the
store, so check `pgrep -u parsar-node` first. The host can then be added again with
a new command.
The node program then exits with status 78 when Core answers 401 to its credential,
and the installed service does not restart it. Other failures, including an
unreachable Core or a 403 from a proxy in front of it, restart the service every
5 seconds without a start limit.
Ordinary disconnects and host restarts reuse the original identity.

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
