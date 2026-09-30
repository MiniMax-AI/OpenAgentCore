# Environment contract and implementation path

This assessment covers the fixed [Python SDK contract](upstream.json), with partial
coverage. Core-managed Docker runs the three qualified native harnesses. V1
user-managed `self_hosted` enrollment uses the same colocated Runtime for Codex,
Claude SDK and MiniMax. `/workspace` names the packaged workspace; a self-hosted
Session may instead select its exact canonical physical directory. The
[recorded deployment qualification](user-managed-runtime-v1.md) retains its historical
source, paths and tested capability scope.
For hosted compute, the deployment selects E2B, Docker or microsandbox through
[Hosted Sandbox Manager](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md).
For the separate caller-managed E2B path, the user owns allocation, renewal and
cleanup through the official SDK and [Runtime packaging](../../services/agents-api/deploy/e2b/README.md).
[Templates](environment-templates.md) provide reusable preparation; the Core extension also applies their execution configuration to user-managed machines;
[Files](environment-files.md) reuse the exact authorized local workspace.

The internal Store now owns a durable Environment association for newly created
`self_hosted` and `openai_hosted` snapshots, atomically with Session creation.
It derives configuration and tenant ownership from the Session; retries preserve
the existing identity. Scoped reads hide associations after Session deletion while
retaining the underlying record. The public text profile reuses this association
and the preparation/admission path below; additional provider profiles remain open.
Missing/`none` configurations and historical internal snapshots gain no backfill.


The private daemon gateway authenticates the enrolled Environment executor key and
exact dedicated device. Existing Worker connection observations retain generation
and revision fencing; registration and connectivity do not establish native
readiness. Rotation/revocation, Session deletion and ownership loss deny further
access without promising immediate cessation of native effects. Native history
remains local to the bound Runtime and cannot be replaced on retry. There is no
registry/Noise relay or transient service-side harness credential.
See the [enrollment guide](../../services/agents-api/README.md#user-managed-runtime-enrollment).

## Basic public Docker-hosted profile

An explicitly configured default managed Docker provider enables `type=openai_hosted`
for the qualified Codex, Claude Code and MiniMax Code profiles. Each uses the same
Runtime lifecycle and workspace interfaces with its native adapter. The outer
Environment provides managed isolation; the daemon adds no inner sandbox.
See the [engine profile guides](README.md#public-engine-profiles) for setup and limits.
The standalone [operator configuration](../../services/agents-api/deploy/codex/README.md#standalone-operator-configuration)
selects the qualified immutable Runtime image; advertised capabilities alone do
not enable admission. An idle or initial-text creation commits Session, Environment
and retry identity before the existing leased Worker provisions its allocation.
A committed creation interrupted before bootstrap is recovered without replaying
an existing allocation's Create.

Omitted/null network defaults to enabled. The daemon does not enforce disabled
or restricted networking. Current execution profiles reject both before Session
creation and resource allocation.
Templates and inline configuration share initial files, env, packages, ordered setup
and inline or tenant-owned referenced Skills through ordered Environment initialization.
The same authenticated daemon handles initial files, tool configuration, packages,
setup, Skill/Plugin import and directory snapshot finalization through
`runtime_prepare`. Providers place, create, bootstrap, inspect, renew and reclaim
compute. npm/Python installation and setup use the host's existing network and
starting account. System dependencies must be preinstalled; `packages.system`
rejects explicitly, including null and empty lists, and never invokes apt, sudo
or elevated execution. Package responses include the official required
`system: []` field as empty metadata; it does not enable installation.
Unsupported hostname forms and installation combinations reject explicitly; see
the [Template coverage and limits](environment-templates.md). Empty/null installation
defaults produce safe empty metadata, not a live workspace inventory. Service-origin
hosted MCP remains unsupported; Environment Plugin MCP has its
own qualified transport matrix.

Initial provisioning leaves a Session idle until a Turn starts, with no caller
connection action. The managed scan records authenticated, exactly bound daemon
connections through existing fenced generations. Native preparation remains
separate. Core restart preserves allocation/workspace/native identity; it does not
blindly replay uncertain work. Terminal cleanup revokes authority and settles
pending input atomically before external reclamation. Matching retries preserve
outcomes; new inputs reject terminal Environments. Expiry has no invented SSE
variant. Local failure codes and exact event ordering remain unverified upstream
semantics; this profile does not establish complete Environment compatibility.

## User-managed E2B profile

The application creates, renews and destroys its E2B sandbox through the official
SDK. It deploys the shared Runtime, then enrolls that Runtime into a `self_hosted`
Session. Core neither keeps an E2B allocation nor issues Provider renew/kill calls.
The [E2B guide](../../services/agents-api/deploy/e2b/README.md) owns packaging and
user-side lifecycle instructions. Expiry or lost workspace/history must not trigger
transparent replacement or replay. The [new enrollment qualification](user-managed-runtime-v1.md)
records its own real deployment evidence.
The [prior E2B qualification](README.md#e2b-v1-qualification) records a historical
Core-managed route; it does not establish this caller-managed enrollment result.
Current deployment-managed E2B is a separate hosted configuration in the manager guide.

## Initial public self-hosted profile

Create a Session with `environment.type=self_hosted`,
a clean absolute `workspace_directory` and optional absolute local
`capability_directories`. `/workspace` maps to the Runtime-bound workspace. Creation
accepts initial text as a string or ordered user-message array. Omitted/null input
creates no Turn or connection action. Configured execution, an enabled harness and
the exact local profile are validated
before persistence. Supported optional functions remain engine-specific.

Initial text commits a reservation and connection action, then returns the Session
and Environment connection target while offline. Streamed creation sends the same
committed projection as its `created` snapshot, already showing `requires_action`
and the connection action, then the committed `requires_action` event. Like
every fresh creation stream it ends right after the idle recorded when the
admitted Turn ends or the reservation stops being pending, or after a failure;
the connection alone clearing the action does not end it. A creation without
input ends right after its created snapshot, and a same-key stream retry ends at
once without events. The existing Worker prepares and admits the input; closing the stream
leaves committed work intact.
Initial expiry leaves a failed Session, safe error and empty actions without a
Turn or an Environment failure. Creation retries preserve the original identity,
deadline and input. Later live observers do not replay creation events.

Later text-only batches recover their original reservation or direct receipt under
the Session lock. New active messages append to the current Turn through existing
ordered admission and native delivery; they create no Turn, preparation or reservation.
If no Turn is active, reserve and wait for the existing Worker to retain native
preparation, admit and claim. Return 204 only after that
transaction commits. Connection actions precede a Turn and clear on connection;
connection alone is not readiness. Retries preserve identity and the five-minute
database deadline. HTTP disconnect retains the reservation. Local expired/cancelled
outcomes return 409, lost ownership 503, and deletion 404; exact hosted error
status/body and pending-input crash recovery are unverified. See the
[canonical wait rules](#ownership-and-placement-decision).

Cancellation-only batches use the existing locked admission and native delivery.
An idle cancellation creates no Turn, and retry identity preserves the original
target during later work. Pending pre-Turn reservations still block new cancellation.
HTTP 204 confirms admission, not native completion or OS quiescence. A cancellation
before native Session transfer can still lack a final Outcome and conservatively
fail; complete cancellation settlement remains open.
Homogeneous function-result batches reuse scoped locked admission and native
application receipts, without creating a Turn or preparation. Definitions use the
existing function parser and remain fixed through native preparation and continuation;
output/error field presence and ordered content keep their existing semantics.
Retries retain the original call, including during later work. New results cannot
bypass pending input. Function callbacks do not populate Environment installations.
Mixed events and non-text input remain unsupported. Local capability directories
use the same Runtime parser and installed snapshot as managed Skills and Plugins;
preparation must finish before native execution. Reconnect reuses installed bytes,
while new Sessions capture their own sources. Recursive references to the installed snapshot and escaping directory
entries are rejected. Platform support and isolation follow
[Platforms and isolation](#platforms-and-isolation). Enrollment must match either the `/workspace`
logical alias or the exact canonical directory bound by the Runtime; selecting an
arbitrary path does not grant access. `remote_url` is the configured daemon WebSocket URL,
returned unchanged. This is our private connection contract and does not claim
stock `exec-server` compatibility. Service-origin HTTP MCP is explicitly rejected
on `self_hosted`; `none` MCP and hosted Template Plugin MCP retain their own scope.

Mechanism tests cover enrollment, credential checks and exact-device dispatch. They
do not establish real public qualification. Before claiming that qualification,
exercise fixed SDK/raw HTTP creation/input, actual native tools, Files/Artifacts,
second-Turn history, Core/Runtime restart, cancellation and credential rotation/
revocation/deletion on each declared deployment.

User-managed onboarding creates a `self_hosted` Session first, then passes its
Environment ID and unchanged `remote_url` to our Runtime with connect-only
authorization. This is our daemon connection contract, not stock OpenAI
`exec-server` transport compatibility. Validate the pinned public HTTP/SDK
resources, state transitions and lifecycle separately; do not infer complete
compatibility from a working connection. User-side tooling owns local Runtime or
E2B allocation, renewal and cleanup. Session deletion and credential revocation do
not transfer ownership of user compute to Core or prove process quiescence.

Keep prerequisites specific to the public operation being implemented. Native
harnesses execute; adapters translate protocols and fill demonstrated capability
gaps; Core owns public semantics, authorization and resources. Before adding a
mechanism, identify the current operation it enables and why existing native
capabilities or interfaces do not suffice. Durable metadata queries need no live
runtime. Live file reads require an authorized, isolated view of the exact workspace
and bounded operation ownership, but not a complete file-write, environment
replacement or placement-retirement implementation. Apply mutation fencing and
retirement guarantees where an operation can write, replace or retire that owner.
Read-only access still requires tenant/resource checks, path isolation and safe
failure when the authorized workspace cannot be reached; it never grants public
admission merely because an adapter advertises a capability.

Use distinct authorization for callers, devices and environment connections. A
managed outer Environment must exclude broader application credentials and other
tenants' secrets. The daemon does not hide its own state from same-user tools.
Directory bindings and process identities do not provide filesystem isolation.
Preserve or demonstrably restore native history across
compute replacement; never silently move a bound Session or replay unknown work.
Self-hosted compute/files remain caller-owned, with explicit cleanup separate from
Session deletion. The full Environment implementation remains pending; follow the
[pinned contract and acceptance sequence](environments.md)
and the [workspace placement map](workspace-placement.md).
For co-location, qualify the outer deployment boundary and the shared Runtime
lifecycle. Native tools use the starting account's permissions; do not claim a
daemon or harness sandbox. Host operators choose their own outer isolation.
Never enable an execution combination whose required outer behavior is unverified.

The internal Store creates one Environment with an environment-bearing Session in
its creation transaction. The Session upsert selects the retry winner; retries
never create or repair associations. Environment identity/state live in their own
table. Tenant ownership and immutable configuration come from the owning Session,
without duplicated JSON, tenant columns or generated IDs in the creation hash.
Environment reads join that Session and exclude deleted Sessions; deletion retains
ownership for later settlement/cleanup. Existing `none` and legacy missing
configuration create no Environment, and historical internal snapshots are not
backfilled. Creation and recorded-intent retry snapshots load the Environment with
the Session row/cursor in the same transaction, without borrowing subsequent
activity or Turn state. Initial state is `pending`; authenticated connection observations follow
the lifecycle rules below.
Public creation supports `self_hosted` on the three enabled native profiles when
the daemon gateway is configured. The workspace is a clean absolute host path matching the bound Runtime root;
`/workspace` denotes that root through the existing logical mapping. Optional
capability directories are clean absolute host-local selections. Supported non-deferred functions keep their engine-specific
validation and native callback bridge. Enrollment binds the dedicated Runtime;
Session output uses the owned Environment association. Public Files reuse the exact
local workspace; populated self-hosted installation metadata remains unsupported.

Environment retrieval uses the existing tenant-scoped join to a live owning Session
and its durable connection status, independently of a live Runtime or gateway setup.
It preserves project-shared read access and exposes only the pinned resource fields.
The current closed self-hosted configuration has no API-managed file, plugin or skill
installations, so those required arrays are empty. They are not a filesystem listing
or a claim about native discovery. Reuse the strict Environment configuration parser
and reject unsupported installation fields/capabilities or resource states instead
of treating unknown inventory as empty. No read initiates native work or changes
connection state; connection does not establish readiness or process quiescence.


The private Environment input reservation stores one canonical message batch before
Turn admission, with a five-minute deadline from the database clock. It requires
an Environment-bearing Session without active work. Reservation and direct input
paths share the Session lock and retry identity; pending or settled keys cannot
bypass the reservation through direct admission. A pending reservation blocks new
direct batches, including cancellation, while successful earlier retries remain
readable. Promotion commits the original inputs, history, reservation settlement
and execution claim (`queued` to `in_progress`) together; expiration and targeted
cancellation retain the terminal identity. Session deletion
is rejected while input is pending and changes nothing. A terminal reservation retry must not
affect a later reservation or Turn. Evaluate deadlines after acquiring the Session
lock, and return terminal storage outcomes without rolling their transaction back.

A validated Runtime `failed` response with `preparation_failed` and no Run settles
the pending input immediately with `runtime_preparation_failed`. Before admission,
a rejection of the current `execution_prepare` request with no Run and code
`invalid_configuration`, `unsupported_configuration` or `unsupported_preparation`
settles through that same failure path. It records a
safe Session failure before any Turn exists and releases the input gate; fixing
the local cause allows new input. Transport loss, capacity rejection and
unconfirmed cleanup remain retryable within the original deadline. Core uses
these common control states, never Harness-specific error text.

Initial messages for a newly created Environment-bearing Session use that same
reservation in the creation transaction, including its connection-action event.
The creation winner alone inserts it; the stream cursor still precedes that
event, and creation retries never re-insert it. A durable initial/later flag
defaults historical rows to later input without inferring origin. Initial expiry
projects a failed Session and safe error before any Turn exists; later expiry
retains idle semantics. Failure
events capture the settled activity and Usage atomically. Late connections and
creation retries cannot reset or replay expired input, and newer work supersedes
old activity without changing its event snapshots. The Environment itself is not
failed by an input deadline. This Store rule covers actual self-hosted and internal
hosted associations; it does not enable hosted providers. None/absent Environment initial input retains
immediate Turn admission. Cancellation/deletion keep their existing semantics.

Ordinary and streamed public self-hosted creation accept initial text through this
transaction after configuration and new-work lease checks. They return the owned
Environment ID and executor URL while offline, without waiting for admission.
The creation stream's `created` snapshot is the committed JSON 201 projection,
including the connection action; the committed action event then follows from the
creation cursor. A disconnected observer leaves committed input intact;
only the existing Worker prepares, promotes and starts it. Saved-Agent retries with recorded intent
recover before fresh execution admission or source resolution; inline retries keep
their existing resolved-snapshot validation.
Later live subscribers observe only future events and recover history through queries.

The public self-hosted input profile accepts message-only batches. Under the same
Session lock, recover the original reservation or direct receipt before choosing
current active input or idle reservation. Active messages use existing ordered input
receipts without a new Turn, preparation or reservation; idle messages retain the
readiness and promotion path, including already-connected environments. Pending
reservations keep their gate and deadline. An unlocked activity read or retry after
a conflict must never choose a different admission path. Mixed inputs remain gaps;
these restrictions do not narrow the pinned protocol target. Cancellation-only batches
use the existing direct admission after configuration and execution-ownership checks.
Only the Session-locked transaction chooses the active Turn or an idle receipt;
matching retries retain that target even during later work. Cancellation cannot
create a Turn or bypass preparation. A new cancellation still conflicts with a
pending reservation; it does not cancel pre-Turn input. Its 204 response confirms
durable admission, not native completion or process exit. Homogeneous function-result
batches also use direct admission after those same checks: their explicit Turn/call
identity selects an existing pending call, never new work. Reuse function validation,
Session-locked whole-batch receipts, preserved output/error fields and native
application acknowledgements. Matching retries remain bound to their original calls
after completion or during later work; new results cannot bypass a pending reservation.
Definitions remain fixed through preparation and cold native continuation. These
callbacks are not installed Environment metadata. Mixed result/cancel publication
and exact hosted action-removal timing remain separate gaps.
Promotion requires the current leased execution writer and the caller's
retained native preparation; never hold a database lock during external preparation. Only
the first successful non-replay receipts authorize Start on that same preparation.
An admitted retry returns the original receipts without reclaiming execution; a
read or uncertain commit never authorizes another Start. A crash after promotion
but before Start uses existing claimed-Turn reconciliation (`execution_interrupted`),
including unbound or deleted Sessions, rather than ordinary queued dispatch. Deletion
after claim is rejected like any active Turn.
The Worker expires at most 32 due reservations on each existing tick, after
checking ownership and before checking devices or execution slots. The sweep
requires the leased Store and uses its connection with the existing transaction
timeout; it never falls back to a pooled writer. A partial deadline index and
Session row locks with SKIP LOCKED let unrelated work proceed around contention.
The candidate cutoff is statement time; settlement rechecks the database clock
after acquiring the Session lock. This bounds mutations and transaction time, not
the number of examined locked rows. Restart resumes expiry on normal ticks without
a separate scheduler or backlog-draining loop. No failed Turn may stand in for a
pre-Turn connection failure.

Session activity before a Turn is derived from the latest relevant reservation and
authenticated connection state. Offline input requests `environment_connection`;
connection arrival clears that action to `idle`, while the prepared Worker still
owns native readiness and admission. An idle offline Environment alone requests no
connection. Reservation/connection changes commit immutable Session activity and
usage snapshots in the same transaction; SSE must not substitute a later Turn or
action set. A newer or active Turn owns subsequent activity. Settled non-initial
reservations clear their action to `idle` until newer work exists, even after an
earlier failed Turn. This local settlement policy does not establish hosted expiry
errors or initial-input asynchronous failure semantics; those remain unverified.

Session GET/list/metadata responses and live SSE share the safe `self_hosted`
output projection. Its `remote_url` comes only from the daemon gateway
configuration, never request headers or a daemon address. Include
the owned Environment ID, workspace and capability directories without exposing
private configuration. The standalone Environment resource remains separate.
Acceptance must pass that exact URL and ID to the
caller-started executor and observe real remote execution through the existing
Worker, daemon and harness, with fixed SDK and raw HTTP/SSE checks.

A self-hosted input HTTP request returns 204 only after durable admission. Its
wait uses bounded pooled operations, outside transactions and execution lease
ownership; it cannot prepare or start native work. Only that route extends its
response write deadline to six minutes for the original five-minute database
admission deadline plus response grace. Request/observer disconnect stops waiting,
not the durable reservation or execution; retries keep the original identity and
deadline. The Worker remains the readiness, promotion and Start owner. Local failure
mapping uses 409 `environment_input_expired` / `environment_input_cancelled`, 503
`execution_unavailable` for ownership loss, and existing 404 for deletion. New input
after a hosted provisioning failure returns the observed 409 `conflict_error`; other
exact hosted failure statuses/bodies and pending-input crash recovery remain unverified.
Principal acceptance must use public Session creation and input against the built
standalone service, including a wait exceeding its ordinary 30-second write timeout,
real remote commands/files and a second native-history Turn. Private provisioning
or injected API handlers cannot substitute for that workflow.

## Runtime capability preparation

This section is the single owner of capability preparation: initial files, tool
configuration, Skills, Plugins, Environment MCP, npm/Python packages, setup and
`packages.system`. Hosted and self-hosted Environments use one Runtime path.

### Ownership and lifetimes

Resource management retains provider placement, capacity, allocation and
Environment create/renew/reclaim. The authenticated Runtime connection carries
initialization, capability preparation and executor operations; it never
implicitly allocates or destroys compute.

Executor close, Turn cancellation and transport loss preserve the installed
snapshot, workspace and allocation. Reclamation is an explicit operation
coordinated with active work; a disconnected socket is not proof that native
effects have stopped. See
[Executor and Turn lifetimes](../../docs/runtime-protocol.md#executor-and-turn-lifetimes).

### Preparation order

Core freezes resource versions, metadata and source selections. Environment
initialization runs in this order, every step over the common `runtime_prepare`
exchange with the same daemon:

1. initial files and tool configuration;
2. Skill/Plugin bundle import;
3. npm/Python packages and ordered setup commands;
4. capability directory snapshot finalization.

Providers only place, create, bootstrap, inspect, renew and reclaim resources;
they never execute Core initialization commands. The common runner uses only
neutral Environment/Session identity and a Runtime peer, with no Provider,
deployment or OS branch. Harness differences belong to native adapters.

A missing authenticated Runtime connection waits before initialization is claimed.
The Environment owns durable `pending`, `running`, `complete` or `failed`
initialization state. The leased Worker processes both locations with the same
bounded initializer, independently of Provider maintenance. A running operation
whose process-local owner is lost is failed as unconfirmed; setup is never replayed.
A confirmed failure records only the safe step and exit status. Both failure paths
settle pending input through the common Environment failure transaction. Failure
alone does not destroy compute or remove a workspace.

The pinned self-hosted input retains `workspace_directory` and optional local
`capability_directories`. For portable preparation, applications may supply
`x_agents_core.environment` at Session creation in either location. This is a Core
extension, not an upstream self-hosted field. It accepts `environment_template_id`,
`files`, `env`, `packages`, `setup_commands`, `skills`, `plugins` and
`capability_directories`, with the same parsers and inheritance rules as hosted
input. A field supplied in both places rejects, including explicit null; there is
no hidden precedence. Machine location, sizing and network policy are not extension
preparation fields. A self-hosted selection cannot use a template requiring a
managed network restriction.

```json
{
  "agent_id": "agent_example",
  "environment": {
    "type": "self_hosted",
    "workspace_directory": "/home/user/project"
  },
  "x_agents_core": {
    "environment": {
      "environment_template_id": "env_template_example"
    }
  }
}
```

Switching `environment` to `{"type":"openai_hosted"}` reuses that same
preparation input. Resource resolution, Project authorization, concrete Skill
versions, encrypted file contents and confidential tool variables are frozen at
Session creation. Same-intent retries and reconnects reuse those snapshots;
new Sessions resolve new versions. User-managed Environments never require an
allocation record. Local directory discovery does not populate API-managed
installation arrays; managed Skills, Plugins and initial files do, in either
location. Session self-hosted responses retain their pinned connection shape;
the Environment resource exposes the safe installation metadata.

Transport `connected` is still a connection observation, not readiness. Execution
and live file access wait for initialization, then the common native preparation
owner validates the installed snapshot and Harness before admitting a Turn. The
model-provider authority rule is unchanged: deployment credentials are not
implicitly sent to user-owned machines.

### Transfer and paths

The private `runtime_prepare`/`runtime_prepare_result` exchange carries typed
initial files, configure/npm/python/setup operations, inert Skill/Plugin
archives and finalization selections, with canonical Session and Environment
identities.

- Files and setup working directories use logical `/workspace` addresses.
  Setup commands are explicit typed input; executable selection and physical
  destinations stay Runtime-owned. Core supplies no executable or host-platform field.
- Ordered 64 KiB chunks and SHA-256 receipts bound each file or archive to
  50 MiB, each control frame to 1 MiB and each connection to one transfer.
  Initialization and finalization headers carry no file data.
- Begin, chunk and commit are never retried. Rejected, failed and unknown effects
  stay distinct. Runtime owns transfer and filesystem work through settlement,
  including disconnect or cancellation.
- Source selections accept portable absolute Unix, Windows drive and UNC paths.
  Core never resolves them on its own host; the daemon applies its local path and
  access checks.

### Installed snapshot

Both origins use the common Runtime parser and `installed.json` manifest on
Linux, macOS and Windows. The operator chooses the capability root (by default
`capabilities` under `OAC_RUNTIME_HOME`); Core and transfer requests cannot.

- The manifest binds the Session and Environment to the ordered source-selection
  digest. The admitted asynchronous preparation owner verifies or creates it
  before native execution, including for an empty selection.
- Filesystem locking prevents concurrent installation. A private completion
  record keeps only the operator installation root, so a deleted snapshot is
  never mistaken for first preparation or recaptured.
- Missing, partial, conflicting or foreign snapshots fail without deleting data,
  automatic repair or replay.
- Reconnection and replacement Executors load installed contents without
  rereading sources. Source edits are seen only by a new Session with its own
  Environment and fresh snapshot.
- Read-only snapshot modes are integrity hints, not protection from the
  launching user.

### Executor admission

Synchronous Executor admission validates the frozen descriptor only. The
asynchronous preparation owner ensures capabilities are ready before invoking the
native factory. Adapters receive only the resolved Runtime-owned Skill paths and
MCP declarations; these remain Runtime-only fields. Reused Session Executors
keep their original configuration.

Files reads keep their separate readiness and authorization and do not require
capability or native execution readiness. MCP environment variables come only
from explicit immutable Runtime tool configuration; missing variables never fall
back to daemon credentials or ambient process variables.

### System dependencies and Runtime directories

The daemon runs as its launching account and never uses sudo or elevates its
permissions. Only user-directory dependencies install during preparation.

- System dependencies must be preinstalled in the managed image/template or by
  the self-hosted user.
- `packages.system` is rejected explicitly, in Templates and inline
  configuration, including a supplied null or empty list. It is never ignored or
  translated into a privileged operation.
- Runtime runs no apt, sudo, unprivileged system-root installer or other
  automatic system-package operation. No managed-only exception exists. Missing
  dependencies fail the consuming operation.
- npm and Python use local prefix/target directories. Setup requires Bash;
  Windows requires Git Bash, without a substitute shell.
- Package responses keep the official required `system: []` field as empty
  response metadata; it is not stored as an initialization option.

Initialization and package directories default under `OAC_RUNTIME_HOME` and may
be selected with `OAC_RUNTIME_INITIALIZATION_DIRECTORY` and
`OAC_RUNTIME_PACKAGE_DIRECTORY`. Packaged Linux images select their existing
`/environment` layout through those settings. `OAC_RUNTIME_TOOL_ENV_FILE`
selects explicit tool configuration. These are resource paths, never
Environment-source or OS switches in Core.

Runtime process ownership waits for exit and I/O settlement. Confirmed failures
keep only a bounded exit status, never command output.

### Platforms and isolation

The protocol and Go preparation implementation are shared by all three native
platforms. Managed Providers remain Linux-only. Commands run directly with the
starting account's permissions and host network; the daemon does not sandbox
tools, files or network access. Outer Environments own managed isolation, and
unsupported network restrictions reject instead of silently running unrestricted.

Native installation and validation limits are in the
[self-hosted guide](../../docs/getting-started/self-hosted.md#platforms). Historical acceptance evidence
stays limited to its recorded binaries and inputs.

On Windows, npm package installation and stdio MCP commands named `npm` or `npx`
(including their `.cmd` shims) run through the resolved npm installation's
JavaScript entrypoint with Node, without an extra shell.

### Environment initialization and compute wake

Environment initialization has pending, running, complete and failed states.
Authentication and connection publication are independent of preparation. Execution
bindings and live Files wait for initialization. The leased Worker's common
scheduler scans 32 Environments at a time, wraps at EOF and bounds concurrent
preparations by execution concurrency. Missing sockets do not consume a pending
attempt; unavailable Harnesses fail before installation. Each operation rechecks
current authority and the original socket. Completion rechecks the exact binding.
Provider bootstrap and resource cleanup retain their own owners and settlement.

After a next-Turn input is durably pending, a completed managed allocation in a
suspension/recovery phase may hint this loop. Initial inputs, cold creation,
running/disabled compute, terminal receipts, cancellation/tool-result events,
history and file operations do not use this hint. Eligibility lookup and delivery
are best effort; persisted work and the normal ticker remain authoritative.
Coalesce hints without blocking, and allow at most one extra scan per normal
five-second cycle. Keep the ticker independent of requests. A normal tick consumes
already queued hints before scanning; simultaneous tick/hint readiness is one
normal scan. Preserve hints arriving during a scan, the allocation cursor and all
ownership checks. Never close the hint channel while handlers may still send.
This bounds extra maintenance work but does not bypass capacity, a busy lifecycle
gate or multi-page scheduling, and does not guarantee a resume deadline.

A recovered or uncertain running installation fails without replaying writes.
Preparation failure is terminal for its Session. It does not destroy compute or
user files; explicit resource cleanup retains its existing owner. The Environment
transaction stores a safe reason with the failed Environment and records
`environment.failed`, `error` and one `agent.session.failed`; Session reads derive
`failed`, that reason and the failure time from the same record, and live streams end
after the failed event. The initializer reports only the integer exit status of a
failed initialization command. Core composes the reason from a fixed step label and that status,
never from command, package-manager or file output; unknown effects, timeouts and
receipts without a status keep a generic reason. New input then gets the observed 409
`conflict_error`; expiry and pending-input settlement keep their behavior.
Completed environments never reinstall initial files on reconnect or native recovery.
The typed Runtime preparation protocol carries bounded confidential input; the
daemon owns the common Go initialization implementation. Confidential env
and setup snapshots are encrypted independently of ordinary metadata. Adapters
apply explicit tool variables without automatically inheriting daemon credentials;
this does not prevent same-user tools from reading local Runtime state.
Reuse runtimefs atomic replacement and anchored logical-workspace paths for initial
files on every platform. Public Files creation keeps its own non-replacement rule.

### Skills, Plugins and Environment MCP

Skills and their immutable versions are Core-owned tenant resources, independent of
Sessions and native Skill installations. Serialize version allocation and pointer
mutations under the owning Skill row. Keep top-level name and description aligned
with the default version in the same transaction, including default-changing uploads;
nondefault uploads preserve that metadata. Preserve unique version identities across
concurrent uploads and deletion. Metadata reads never load or decrypt bundle bytes.
Encrypt bundle contents with a tenant, Skill and version binding using the existing
service cipher. Deleting a Skill reclaims its versions without affecting already
frozen Session initialization. Public reference metadata, unresolved template intent
and the resolved Runtime bundle are distinct; do not report a reference as inline
merely because it reuses the same installer. No compatibility reader, source
cache, extra lifecycle owner or per-harness resource implementation is required.
Resolve references inside the Session creation transaction, after the creation
upsert establishes ownership. Lock referenced resources in a stable order; freeze
the selected version, descriptive metadata and bytes together. Creation retries
recover the recorded intent before reading mutable templates or Skill sources.
Templates preserve default, latest and explicit version selectors. An omitted or
null reference version selects the default at Session creation and projects as
`version: null` in Template responses. Session responses contain concrete versions;
only validated installation metadata crosses the Runtime boundary. A supplied
Session Skill, Plugin or capability-directory list replaces its template list;
omission and null inherit, while an empty list clears that selection. This differs
from Template resource updates, where null clears lists and resets network to the
pinned enabled default. Preserve caller intent and frozen Session snapshots in
both cases. Public capability directories remain caller paths; adapter-owned
installation directories are not portable public paths.

Inline and referenced Skill ZIPs use the same confidential initialization snapshot and installer.
Core validates portable manifests and bounded regular-file archives, returns only
safe Skill metadata, and freezes content before native preparation. The Runtime
owns `skills/<name>` below its configured capability root; the manifest validates
the installed snapshot before reuse. This is state
consistency, not protection from the launching user. Public Plugin ZIPs preserve their complete package
layout and reuse the shared archive and portable Skill parsers. Core keeps safe
Plugin metadata separate from encrypted archives. Templates inherit or replace
Plugin and capability-directory lists through the same hosted resolver.

After ordered setup, the existing initializer snapshots declared workspace-contained
capability directories into Runtime storage and writes one installed manifest.
This is an initialization artifact, not a second lifecycle owner or database ledger.
Directory bytes are observed after setup; they are not frozen at Session creation.
The common daemon resolves the manifest only for executable preparation and passes
validated Runtime-owned Skill/package roots to adapters. Files reads do not require
that artifact. Reconnection and recovery read installed bytes, never mutable source
directories. Missing or inconsistent installations fail preparation without replay.

Adapters register only selected Skill roots without changing the execution loop.
Codex uses explicit extra roots; MiniMax projects its native catalog; Claude creates
one controlled envelope per package with real directories and immutable hardlinks
under content. Its explicit paths remain inside that envelope; original native
control files are not activated. Keep automatic native MCP discovery disabled.
Explicit Environment MCP declarations
follow the separately qualified transport path below; unsupported native activation
fails explicitly, without silent partial activation or a generic plugin framework.

Environment-origin MCP declarations use the shared Plugin parser and frozen
installed packages. The installation manifest retains selected MCP package roots;
Runtime re-parses those installed packages without another configuration copy,
credential cache or lifecycle ledger. Native adapters must explicitly qualify and
project supported declarations before public admission. Parent-directory Skill
discovery does not activate nested Plugin MCP configuration.

The common daemon stdio entry resolves the installed declaration and launches its
server under the same user permissions as the Harness. Unix replaces the helper
process; Windows forwards native stdio within the owned process tree. Explicit
initialized values override the selected declaration's variables. There is no
Python sandbox launcher, mount policy, shell prefix or separate credential sandbox.
Process groups and Windows Jobs own cancellation and descendant cleanup only.
Claude composes MCP identity and observation with the same workspace profile;
MiniMax uses the same effective bindings for stdio and HTTP. A public capability
advertisement must still match the installed engine's actual supported transport.

Runtime resolves public HTTP declarations and installed Plugin MCP through
`agent.ResolveMCPBindings` before adapter projection. Each transient binding
retains its connection origin, transport, nullable tool allowlist, required flag,
credential authority and installed stdio identity. Bindings are never persisted
or logged. Duplicate identities and unavailable selected credentials reject.
Public HTTP MCP uses the same binding path as installed Plugin MCP. Its explicit
`connection_origin` is retained from saved configuration through the Session
snapshot and private Runtime request; the exact Runtime wire version is required.
A service-origin request cannot silently become an Environment-origin connection.

### Public MCP connection origin

Core's Harness profile declares `MCPOrigins`; shared admission and dispatch check
the origin against the Environment and Runtime's advertised HTTP/bearer/required
capabilities. Runtime validates the same origin before invoking an adapter. No
Harness-name or Sandbox Provider branch selects a different connection path.

| Harness | `service` origin | `environment` origin | Optional policy |
| --- | --- | --- | --- |
| Codex | Service execution host, `environment:none` | Managed or self-hosted workspace | Nullable tool allowlist and required initialization |
| Claude SDK | Service execution host, `environment:none` | Managed or self-hosted workspace; packaged `workspace_mcp_http` feature required | Nullable tool allowlist and required initialization |
| MiniMax Code | Unsupported | Managed or self-hosted workspace | `allowed_tools` must be null/omitted; `required` must be false |

Both origins support the declared Harness's anonymous HTTP and selected HTTPS
bearer path. The existing attached-Vault selection freezes credential identity,
including a unique implicit URL match or an anonymous selection. Only that
Project-authorized credential may enter the transient Runtime request; Core
defaults and unrelated Vaults are not searched. Decryption failure or a missing
credential fails execution without an anonymous fallback. Public Environment
MCP retains `project_vault` authority; Plugin credentials retain
`environment_configuration` authority. Neither source overrides duplicate
server labels. Bearers never enter persisted native configuration or argv.

Omitted/null origin still means `service`, including on self-hosted requests;
it does not select the local network automatically. Service-origin requests with
a workspace remain rejected because they require separate service-side connection
forwarding. This implementation adds no proxy. Environment origin requires an
initialized workspace with enabled network access and is invalid on `none`.

Null/omitted `allowed_tools` permits all server tools; an empty list permits none.
MiniMax rejects every non-null allowlist, including an empty list, rather than
silently expanding it. Codex and Claude preserve native allowlists and initialize
required servers before releasing native input, including cold recovery.
Public MCP with native Subagents remains unqualified. Nonempty literal HTTP
headers, request metadata and public stdio declarations remain unsupported.
See [public MCP qualification](https://github.com/MiniMax-AI/parsar-core/blob/e974a7f880a2eb799f0dd39e6ba0870462854a53/contracts/agents-api/public-mcp-qualification.md) for actual model,
platform and infrastructure coverage; admission support is not a claim of
complete cross-platform/provider qualification.

Environment-origin literal HTTP headers remain rejected for the pinned Claude
and MiniMax clients because their cross-origin forwarding cannot preserve header
authority. MiniMax supports installed stdio and HTTP servers with anonymous or
explicit user-selected HTTPS bearer authentication. ACP HTTP declarations remain
Session-local native memory; tokens do not enter native configuration files or
process arguments. Required initialization and tool allowlists are not exposed
through the Plugin manifest. Public MiniMax HTTP uses the same transient ACP map
with the stricter admission limits above.
MiniMax reads the existing Session-private native runtime-name registry for exact
first-frame identities and cross-checks completed native results for both transports.
Reuse existing observation and cancellation settlement; never fabricate a delayed
start event, guess normalized identities or add a registry of our own.

See [capability qualification](environment-capabilities-qualification.md) for
real model evidence and the remaining public-origin and image gaps.

## Contract inventory

Paths below follow the SDK resource methods, before the service's `/v1` prefix.
The authoritative fields and unions are linked to the pinned source; this table
is an inventory, not a replacement schema.

| Resource | Operations | Contract distinctions |
| --- | --- | --- |
| [Environment](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/environments.py) | `GET /agents/environments/{id}` | Created through Session configuration, with no standalone create/list/update/delete method in this resource. Safe metadata includes files, plugins, skills, type and status. |
| [Template](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py) | `POST`, `GET /agents/environments/templates`; `GET`, `POST`, `DELETE /agents/environments/templates/{id}` | Reusable hosted configuration, resolved for each Session. List uses `after`, `limit` and `order`. Supplied update fields replace their value; omitted fields stay unchanged. Deletion includes confidential inputs. |
| [Files](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py) | `POST`, `GET /agents/environments/{id}/files` | Create accepts `file_id` or inline base64 data with an absolute path inside `/workspace`. List uses opaque `page`, not `after`, with stable path/order/limit across pages. |

These are eight operations, separate from Session creation and live events.
Templates use an `after` cursor and limit default 20, clamped to 1–100; file listing has nullable
limit/path, non-null order/page when supplied, and case-sensitive path-component
ordering. Both default to descending order. Do not reuse cursor decoding merely
because both endpoints paginate.

[Session environment input](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment_param.py)
and [output](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment.py)
have different shapes:

- `none` selects no execution environment.
- `self_hosted` input requires `type` and `workspace_directory`; its optional
  nullable `capability_directories` defaults to an empty list. `remote_url` is
  output-only. The output also includes the Environment ID and capability paths.
  The output description's `/workspace` default does not make the input field
  optional.
- `openai_hosted` can reference a template and supply capability paths, network,
  packages, files, plugins, skills, environment variables and setup commands.
  Omitted template-backed values inherit; Session overrides cannot broaden the
  template network policy. The service implementing this discriminator owns
  provisioning; it does not rename the public discriminator for its provider.

[Template responses](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_template.py)
expose safe metadata, retaining unresolved skill version selectors and file
references. They do not return inline file/archive contents, environment variables
or setup command bodies. Nullability and replacement behavior must be checked
against each request type, not inferred from these response models.

| Projection | State vocabulary |
| --- | --- |
| [Environment resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environment_info.py) | `pending`, `connected`, `disconnected`, `expired`, `failed` |
| [Session environment event state](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_session_environment_state.py) | `pending`, `ready`, `connected`, `disconnected`, `failed`; nullable error |

These projections cannot share an unchecked string cast. Session environment
notifications carry Session/Environment identity and optional Turn identity.
The Session's `required_actions` union includes `environment_connection` with an
Environment ID, separately from function calls. Environment readiness is distinct
from Session and Turn status.

## Ownership and placement decision

The canonical [architecture rules](#ownership-and-placement-decision)
keep Environment lifecycle common while leaving process placement and native
transport to adapters. Logical ownership does not require a machine per object.

| Object | Responsibility |
| --- | --- |
| API Session and Environment | Durable tenant ownership, configuration, pending interaction and connection observations. |
| Provider allocation | Compute and filesystem lifetime; caller-owned for `self_hosted`, service-owned for hosted provisioning. |
| Device and daemon connection | Authenticated engine-host identity and replaceable internal dispatch transport. |
| Harness process and native Session | Native model/tool loop, execution state and proven history/continuation path. |
| Runtime enrollment | Exact Environment/device/key binding for user-managed compute; no service-owned allocation. |

Daemon, harness, tools and workspace are colocated in V1. Our daemon fills the
executor role; a separate native executor and service-side harness are retired.
The pinned public resources remain the target, while stock executor wire
interoperability is explicitly outside this implementation.

Co-location needs a real credential and isolation design: generated code must not
gain the broader application credential or cross-tenant secrets through a shared
unrestricted process account. A directory binding alone is not isolation. Retain
native history independently of disposable compute, or prove native restoration;
never treat an Environment ID as a filesystem or history backup. Do not silently
move an existing Session away from its bound device.

Environment identity, tenant/Session association, configuration and lifecycle belong
in Agents API, independently of provider compute, authenticated device identity,
daemon sockets and native harness Sessions. Create an Environment association in
the same transaction as its Session and creation identity when this resource is
implemented. Keep mutable connection/registration state out of immutable
configuration; replacement ownership must fence stale observations.

The current hosted architecture is V1: Core runs independently; each Environment
sandbox contains its daemon, selected native harness, local tools and workspace.
Execution and Files use the same authorized workspace through the existing
Core/Runtime contract. Native tool calls stay local.
CLI discovery uses a bounded 15-second version probe per installed harness;
missing binaries fail immediately. A version result is availability, not Environment
readiness, and does not change initialization or connection ownership. Process placement and native
transport remain adapter responsibilities, without a second model/tool loop.
The former separated Runtime/harness and workspace executor topology is a distant
future V2 option, to revisit only after V1 is stable and concrete needs justify it.
Do not extend that topology for hosted delivery, maintain two current hosted routes,
or introduce dormant V2 compatibility scaffolding.

When an execution Session has a previously started Turn but no recorded native
Session ID, Core requires existing-history recovery through a verified Runtime
capability. Read that condition before claiming the next Turn. A supplied native ID
remains authoritative. The Codex adapter may recover only a unique, nonarchived
root in the exact Session-private native home and expected working directory, using
native listing and exact-ID resume. Missing, incomplete or ambiguous history must
fail without starting a fresh root. A recorded start can precede native work; that
uncertain case also fails conservatively. Recovery does not replay interrupted
inputs, erase prior outcomes or promise transparent continuation of running tools.
Keep Device identity and Environment scope in `ExecutionDevice`; native Session
identity and prior API Turn state belong to `SessionExecutionBinding`.

Platform-managed and user-managed deployment reuse this same Runtime. For platform
management, SandboxProvider creates and reclaims it. For user management, the user
starts the Runtime and its daemon authenticates and initiates the Core connection;
Core verifies principal ownership and the exact Environment binding. These are
management responsibilities, not separate execution architectures.

In V1, our daemon fills the user-side executor role. Users deploy daemon, the
selected harness, local tools and workspace together. Do not require Codex
`exec-server`, a service-side harness, registry/Noise transport or remote tool
forwarding. The explicit daemon-executor decision supersedes the previous native
executor interoperability requirement. The superseded execution route is removed;
retain reusable filesystem helpers,
necessary regressions and historical evidence without a compatibility layer.

### Explicit local tool environment

`--tool-env-file` supplies the Runtime operator's base tool variables. Common
preparation copies these explicit values into its private initialization snapshot;
explicit Session `env` keys override the base. Runtime does not rewrite the source
file or inherit unrelated ambient credentials. Setup, capability resolution and
Harness execution read the same prepared snapshot. Reconnect preserves that
snapshot even if the operator edits the source; a new installation for a new
Session reads the current source. Harness profiles may reference the Runtime-owned
environment file, but must not persist copies of its values. A missing or invalid explicitly configured file
fails preparation.
