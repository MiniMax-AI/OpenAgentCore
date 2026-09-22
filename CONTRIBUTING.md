# Contributing to Parsar Core

## Repository boundary

This repository is the standalone execution substrate copied from Parsar at the
revision in `provenance/source.json`. Keep the API, its migrations, protocol,
execution daemon, runtime adapters, shared execution packages, the standalone Core
Web console and build/test tools here. Product users, workspaces, model catalogs,
business assets, the Parsar product Web, product API and product migrations remain
in Parsar. Do not import `server/`, `apps/parsar/`, product CLI/plugin packages or
their deployment stack.

Preserve copied runtime and protocol behavior. Existing Go import paths, binary
names and runtime environment variables remain unchanged for this copy; they do
not require fetching the original repository. The source snapshot and per-file
hashes provide an audit trail; future Core development need not preserve those
hashes. Do not automatically sync or delete the original repository's Core.

## Workflow and quality

Develop in an isolated worktree on a feature branch and submit a PR. Do not edit
or commit implementation directly on main. An empty repository bootstrap commit
is only the comparison base for the first import PR. After validation, conduct an
independent blind review using only requirements, acceptance criteria, boundaries,
repository path and comparison baseline. Fix in-scope blockers before delivery.
Do not use `codex exec` as a substitute reviewer.

Keep runtime state, test artifacts and build output under `~/.parsar/`. Require
absolute user-supplied working directories. Keep credentials out of source and
logs. Update this guide when architecture, ownership or generated contracts change.
Comments and documentation are English. Reuse existing helpers and error mapping;
split oversized components before extending them. Use `internal/obs/log` for logs.

## Required checks

Run `make check` before completion. The standalone gate includes all daemon/shared
Go tests, Core contract/client/service tests, Core Web and TypeScript client
checks (including fixture-only Playwright acceptance), a real dedicated PostgreSQL test
database, byte-for-byte sqlc regeneration checks, standalone API builds, Claude SDK
tests and packaging, MiniMax companion checks, and Rust filesystem-helper
tests/format/Clippy. It intentionally has no product Web/server/installer gates. The full gate fails when the database variable is missing.

Use Go from `go.mod`, Node 22, pnpm 10.30.3, Python 3.9+, Rust 1.95.0 with rustfmt
and Clippy, and Linux OpenSSL development libraries. `make sqlc-generate` owns only
`services/agents-api/internal/db/sqlc` (sqlc v1.29.0). Do not rewrite landed
migrations. The public protocol schema is `contracts/agents-api/openapi.yaml`;
there is no product swaggo contract in this repository. Preserve its pinned types,
coverage ledgers and official SDK/raw HTTP tests when changing API behavior.
Run `make openapi` after handler annotation changes. It reuses the original
Core-only swaggo v1.16.4 generator and writes this schema, without product routes.

Core changes must retain the independent build and official-client workflow.
Native adapter changes require their applicable build/check targets and live provider
acceptance. Real execution checks require real models; do not count omitted
prerequisites or mocked responses as live acceptance. `packages/codex-executor`
retains only the directory, write and workspace-export Rust helpers. Its build and
check targets remain; the separate `packages/codex-harness`, its build/check scripts
and its CI/`make check` gate are retired. Historical remote native probes are not
current validation entrypoints.

## Architecture boundaries

The following execution rules are retained from the source contributor guide.
References to the product describe the external client boundary, not components
included in this repository.

### Product and execution service separation

Agents API is the primary infrastructure deliverable. Parsar is an ordinary client
and example application; its feature backlog must not dictate the execution
service's public protocol or internal model. Agents API must build, deploy and run
without the Parsar product service, frontend or database. An optional Compose
deployment may install both services with one PostgreSQL instance, but separate
databases, credentials and migrations. The product uses Core exclusively; it has no native daemon or HTTP Agent fallback.

#### Design and compatibility requirements

- The complete pinned `openai/openai-python` `beta/agents` protocol is the target,
  including its referenced resources and types. Match paths, methods, headers,
  field presence, nullability, discriminators, defaults, status transitions,
  pagination, errors and streaming behavior. Engine limitations are implementation
  gaps to solve, not grounds for narrowing or redefining the upstream contract.
- Preserve qualified native capability differences across harnesses. If a material
  difference from the official API has no clear mapping, pause that part and ask
  the user before changing its semantics. Explicit unsupported enablement rejects;
  ordinary requests retain native behavior with any official default discrepancy
  recorded in the coverage ledger. In particular, native programmatic tool calling
  is not currently qualified as the official default-on behavior. Do not build
  a separate executor or model loop to fabricate parity. This does not relax
  authentication, isolation, credential protection or data consistency.
- Pin upstream source and SDK versions in `contracts/agents-api/upstream.json`.
  Use official SDKs for clients and reuse upstream types or schemas where suitable.
  SDK deserialization alone is not server validation or proof of compatibility:
  test raw HTTP payloads and observable workflows as well. Synthetic data and mock
  model responses may support controlled tests; live execution acceptance must
  call a real model API through the service, daemon and harness. A real daemon
  with a synthetic model does not constitute live model validation. Keep provider
  credentials in private test configuration, outside source, logs and task records.
  Record unspecified or unverified behavior explicitly; never invent official
  semantics. Track partial
  coverage in `contracts/agents-api/README.md` until the complete target is verified.
  Reconcile current coverage summaries with merged routes and recorded acceptance;
  distinguish accepted profiles, partial implementation, missing operations and
  unverified semantics. Retain historical evidence with its original scope. Handler
  counts are not compatibility percentages, and an active provider probe is not
  deployment qualification.
- No legacy Agents API compatibility requirement takes precedence over this
  design. Replace an unsuitable implementation instead of growing compatibility
  branches. Preserve reusable, verified infrastructure rather than rewriting it
  merely for new names or directories. Replacements may retire obsolete private
  interfaces and history backfills in bounded PRs; this does not authorize deleting
  product data or changing unrelated product behavior.
- Every concrete harness interaction goes through the common Runtime contract
  and its adapter. Extend that contract minimally when a current operation cannot
  be expressed; never put native capability logic or transport conversion into
  Core handlers, storage or scheduling. Qualify public workflows through the same
  shared chain; direct native probes establish feasibility only.
- Keep engine-specific types, process management and protocol translation inside
  execution adapters. The public API and persistence/application core must not
  interpret Parsar product payloads or depend on one engine's native item types.
  Prefer maintained upstream SDKs and native execution protocols over a second
  hand-written model/tool loop or a general-purpose compatibility framework.
- Verify an independent official-client workflow before a Parsar integration.
  Parsar uses the same public contract as any other client, with no privileged
  endpoint or direct execution-table access. An OpenAI endpoint is a possible
  client target only where the requested capabilities and credentials support it.
- Maintain tasks, priorities and evidence in the Feishu board. Register issues
  discovered during a task without switching work or automatically selecting them
  next. Only a direct acceptance blocker justifies a minimal in-scope fix. After
  each bounded task passes checks/review and merges, mark its child entry done.
  Treat each selected concise Core TODO item as a delivery milestone. The long-term
  Goal retains the objective, constraints and standards; detailed-board entries are
  smaller tasks within that milestone. Batch related small tasks when they share a
  functional outcome and safety boundary, then validate and independently review
  the batch once stable. Do not manufacture one PR per internal wiring step.
  Finish the selected milestone before choosing another. The main agent selects
  tasks; subagents handle technical design, scoped collaboration and review, not
  prioritization. Stop after the complete milestone when the user sets that boundary;
  do not automatically claim the next one or mark the long-term objective complete. Product integration, UI and business Team work remain
  outside Core delivery. Prioritize a sound architecture skeleton
  and correct principal workflows with real API validation. Record and defer
  low-frequency corner cases when risk and ROI permit; do not let minor details
  delay the main work. Required checks and material correctness guarantees apply.

- Parsar owns users, workspaces, business authorization, Agent/Team definitions,
  capabilities, product conversations, IM/sharing, approval decisions and billing.
- Agents API owns protocol saved Agents, execution sessions/turns, effective
  configuration snapshots, dispatch/cancel, environments, vaults, raw usage,
  pending interactions, protocol subagents and durable events. Protocol saved
  Agents/vaults are execution resources, not Parsar marketplace or business roles.
  Neither service reads the other's tables. Parsar uses a versioned client contract.
- A product conversation may map to several execution sessions. An execution
  session is distinct from a live daemon socket, process or sandbox. Native engine
  session identifiers belong to the execution service.
- Establish single-Agent execution, approval, cancellation, idempotent submission,
  persisted recovery queries before Team orchestration. The upstream SSE stream
  is live-only; recover through Session/Turn/Items reads. Any additional product
  cursor replay must be documented as an extension, not upstream semantics.
  Team definitions, management and orchestration belong to Parsar. Agents API
  establishes single-Agent execution first; business Team loops are deferred.
  This does not exclude upstream `multi_agent` configuration or subagent resources
  from protocol coverage. Future business Team orchestration directly depends on
  `openai/openai-agents-python` in Parsar.
- Daemon Skill/SP authoring remains a product operation: forward through a scoped
  product callback with the original requester and workspace checks. A runtime
  credential alone must not grant business write permissions.

#### Environment ownership and placement

Environment identity, tenant/Session association, configuration and lifecycle belong
in Agents API, independently of provider compute, authenticated device identity,
daemon sockets and native harness Sessions. Create an Environment association in
the same transaction as its Session and creation identity when this resource is
implemented. Keep mutable connection/registration state out of immutable
configuration; replacement ownership must fence stale observations.

The current MVP covers Codex, Claude Code and MiniMax Code through the shared
single-Agent path: Session
creation, environment preparation, native execution, files/artifacts, cancellation,
reconnection/recovery queries, and standalone deployment acceptance. Select each
bounded child from the complete board within the selected concise-TODO task;
nonblocking local improvements stay queued.
Authentication, tenant/credential isolation, state consistency and data loss remain
material acceptance requirements. Optional feature equality is not required. After
the three profiles pass merged-main validation, publish the results, limitations
and backlog. The user has since renewed continuous Core delivery: select and
finish large tasks from the concise TODO, retaining bounded child acceptance.
Additional harness implementations remain queued. The separately authorized
Subagent batch targets the six read operations across these three harnesses and
does not change the complete pinned protocol target.

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
The private daemon wire protocol is 0.4.0. Initial, prepared and active input use
the same ordered MessageInput contract, replacing scalar prompts and attachments.
User-message boundaries and text/image order remain intact through Core and the
Runtime wire; adapters own native conversion and receipt aggregation. Text-only
transports reject image content rather than dropping it. Codex has a flat native
input list and uses blank-line separators between messages; this does not preserve
independent native user-message boundaries. No old wire fallback is maintained.
Deploy Core and daemon together; the existing major/minor WebSocket check rejects
older major/minor peers before dispatch rather than ignoring removed fields.
The independently packaged Claude bridge uses protocol 2 for ordered input;
readiness rejects packages reporting the old string-input protocol.
Image-bearing messages require a qualified profile/placement before persistence
and image support from the selected Runtime before native delivery. These checks
apply to that operation only; ordinary text retains offline queueing. Initial,
prepared and active paths use the same content and preserve receipt ownership.
The qualified public profile is inline PNG/JPEG on Codex/Claude `none` and
Core-managed Docker `openai_hosted`. Self-hosted/user-managed images, MiniMax
images and remote URLs remain explicit implementation gaps. Hosted images reuse
the existing preparation, active-input and workspace authority; they do not add
a downloader, a mount or a separate execution lifecycle. Core
does not fetch or transform media. See [message input coverage](contracts/agents-api/message-input.md).

User-managed onboarding creates a `self_hosted` Session first, then passes its
Environment ID and unchanged `remote_url` to our Runtime with connect-only
authorization. This is our daemon connection contract, not stock OpenAI
`exec-server` transport compatibility. Validate the pinned public HTTP/SDK
resources, state transitions and lifecycle separately; do not infer complete
compatibility from a working connection. User-side tooling owns local Runtime or
E2B allocation, renewal and cleanup. Session deletion and credential revocation do
not transfer ownership of user compute to Core or prove process quiescence.

Public Environment Templates belong to Core and its execution database, independently
of provider image/build templates. Resolve a tenant-owned reference once at Session
creation, freeze the effective ordinary hosted configuration and reuse inline
initialization. Do not pass template IDs into Provider or Runtime. Omitted network
inherits; overrides may only narrow policy. Preserve unresolved caller intent for
creation retries and recover committed results before reading mutable templates.
Updates and deletion cannot rewrite existing Session snapshots. Initial files use
one Core-owned installer for template and inline configurations. Keep confidential
bytes encrypted under the execution-service key and resource-bound AEAD, separately
from ordinary configuration and public metadata. Templates retain source references;
Session creation freezes tenant-authorized source bytes in the same commit, independent
of later source/template deletion. Public resource reads must not require decryption
or load encrypted file bodies. Record original creation intent before resolution.

Template parsing, persistence and resolution must not select a harness or Provider,
or depend on native tool names and private harness paths. The shared initializer
uses the packaged Runtime contract for trusted commands, workspace/staging paths,
confidential input and completion receipts. Each Provider supplies that same
Runtime and carries initialization commands through RunCommand; each adapter owns
native tool configuration. A new harness or Provider must not require template
business-logic changes. Reuse qualified shared helpers even when their executable
names have historical engine prefixes; renaming is not a boundary fix. Select real
regressions by the changed shared, Provider and adapter boundaries, rather than
repeating every deployment combination for each configuration field.

The allocation lifecycle owns pending/running/complete initialization. Authentication
may connect the daemon during initialization; execution bindings, native preparation,
live Files and connected publication wait for completion. Keep Provider bootstrap
settlement distinct. Advance at most one bounded initialization operation per full
maintenance scan,
using process-local progress and the existing lifecycle gate. A recovered or uncertain
running installation fails and uses existing cleanup, without replaying writes.
Completed environments never reinstall initial files on reconnect or native recovery.
Provider RunCommand carries bounded stdin, not confidential argv. Only fixed trusted
initializers may run with Runtime authority. User setup and package install hooks
run in the common packaged sandbox, without daemon credentials or native history.
Files, resolved Skills and inline Plugins precede system, npm/Python packages and ordered setup commands. Initialization has
provisioning network access; requested network restrictions apply to native tools
after setup. Confidential env and setup snapshots are encrypted independently of
ordinary metadata. Adapters apply tool env only after isolation, never to the
credential-bearing daemon/native harness launcher.
Reuse the packaged atomic file writer and anchored parent creation across all profiles.

System packages use one Runtime-owned tool root, separate from trusted daemon and
harness executables. Build its immutable seed from the base image before adding
Runtime/harness code or secrets; include the matching package database and base
tool symlink targets. The shared installer extracts independent inodes and runs
apt/dpkg inside an unprivileged namespace. Package scripts cannot access Runtime
credentials, native history or outer processes. Later setup and native tools enter
the installed root read-only, retaining the authorized workspace and adapter-owned
scratch. `/workspace` and `/environment/workspace` refer to the same authorized
workspace inside that root, preserving native working directories. Native adapters
own entry and existing process cancellation; Core never
selects an engine or Provider for package initialization. No live filesystem
snapshot, second lifecycle owner or package-manager framework is introduced.
Core preserves the system-package requirement in the common execution binding;
a missing installation receipt fails preparation instead of falling back to base
tools. This requirement does not add execution prerequisites to Files reads.

Skills and their immutable versions are Core-owned tenant resources, independent of
Sessions and native Skill installations. Serialize version allocation and pointer
mutations under the owning Skill row; preserve unique version identities across
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
Templates preserve omitted/default, latest and explicit version selectors. Session
responses contain concrete versions; only validated installation metadata crosses
the Runtime boundary. A supplied Session Skill list replaces the template list;
omission inherits. Explicit null reference selectors and null list overrides remain
unqualified and reject rather than silently changing selection.

Inline and referenced Skill ZIPs use the same confidential initialization snapshot and installer.
Core validates portable manifests and bounded regular-file archives, returns only
safe Skill metadata, and freezes content before native preparation. The Runtime
owns `/environment/initialization/capabilities/skills/<name>`; setup and native tools may read but
not modify this tree. Public Plugin ZIPs preserve their complete package
layout and reuse the shared archive and portable Skill parsers. Core keeps safe
Plugin metadata separate from encrypted archives. Templates inherit or replace
Plugin and capability-directory lists through the same hosted resolver.

After ordered setup, the existing initializer snapshots declared workspace-contained
capability directories into protected storage and writes one installed manifest.
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
Runtime re-parses those protected packages without another configuration copy,
credential cache or lifecycle ledger. Native adapters must explicitly qualify and
project supported declarations before public admission. Parent-directory Skill
discovery does not activate nested Plugin MCP configuration.

The packaged stdio entry enters the existing initialization sandbox before parsing
or executing a server. It mounts only the fixed static daemon helper, which resolves
the selected installed declaration, reads explicitly selected initialized user
variables, applies package-relative cwd and replaces itself with the server. Native
MCP stdin/stdout pass directly to the sandbox; no protocol forwarding loop,
Provider command or second Plugin parser is introduced. The existing Python stdio
entry remains a single-threaded launcher and monitors the native parent process
through Linux pidfds. Native Codex recycles spawning threads, so binding bwrap's
parent-death signal directly to those threads kills live MCP connections. The
launcher gives bwrap a stable parent, retains its parent-death/PID namespace cleanup,
and kills and reaps only that child when the native process exits. A pre-exec
parent-death signal and expected-parent PID check close the reproduced fork-to-exec
orphan window; bind libc before fork and keep this entry single-threaded. Missing
pidfd support fails closed. This fixes the OS process lifetime boundary without adding a
Core execution owner, retry or reconnect loop.
Model/daemon launch variables are never credential sources. Hosted stdio requires
enabled network; native HTTP requires its own qualified network and redirect behavior.
Claude composes the existing MCP identity/observation profile with its workspace
profile. Verify the complete native inventory before admitting tool identities;
MCP allowlist patterns never grant local Bash or file authority. Only declared
random bearer references enter native query env, with native Bash denial retained.
With system packages, Claude uses its packaged shell-prefix dispatcher. Only the
exact fixed Runtime stdio invocation bypasses the tool root; it immediately execs
the existing isolated launcher. All other full command strings pass unchanged to
the tool root. The native Bash sandbox and cwd receipt remain in their original
order. Do not move wrapping into Bash input hooks or add a directory-state owner.
Keep this native distinction in the adapter; Core and the common launcher do not
select an engine. Per-server env cannot disable the native global prefix because
Claude selects the executable before merging that env.
Environment-origin literal HTTP headers remain rejected for the pinned Claude
client because its interpolation and cross-origin forwarding change their meaning.
MiniMax accepts environment stdio only. Its adapter reads the existing Session-private
native runtime-name registry for exact first-frame identities and cross-checks
completed native results. Reuse existing observation and cancellation settlement;
never fabricate a delayed start event, guess normalized identities or add a registry
of our own. Its unqualified HTTP transport remains rejected.
These mechanisms alone do not establish public MCP support or full compatibility.

Name, enabled/disabled/exact-domain restricted network, initial files, inline/referenced Skills, Plugins, workspace capability directories and env/setup/system/npm/Python are
implemented independently of remaining installation fields. Reject unsupported
inputs rather than persisting them for silent
omission; expand inline and template initialization together in separately qualified
batches. Resource reads need only tenant authorization, not a live Runtime.
See the [Template coverage and unresolved semantics](contracts/agents-api/environment-templates.md).

SandboxProvider has five operations: Create, GetInfo, Renew, Kill and RunCommand.
Use maintained provider SDKs and thin adapters. The current hosted offering uses Docker.
Provider initialization creates the sandbox and starts its daemon/harness;
RunCommand is for initialization only. Daily execution and Files use Runtime and
native or bounded local capabilities. Docker's lack of a native renewable lease
does not remove service-owned hosted expiry and cleanup requirements.

The official `openai_hosted` discriminator means hosting by this independent Core
service, using Docker V1. Keep the public value unchanged; `parsar_hosted` is not
a new API type. Public Environment Templates apply only to this hosted path.
E2B onboarding follows the application-managed `self_hosted` resource workflow:
the application owns sandbox provisioning and cleanup, and our daemon connects
with the returned Environment ID, unchanged `remote_url` and scoped environment
authorization. Reuse the same Runtime and thin provider components. No OpenAI
executor process or additional execution architecture is required. Qualify
principal/tenant ownership, credentials and connection lifecycle using the pinned
client and actual execution; document our transport boundary explicitly.

The Core-managed E2B Provider, including its custom HTTP/Connect and envd protocol
implementation, is retired. User-side E2B tooling uses the official SDK and the
shared Runtime, not an additional execution architecture. The
[E2B guide](services/agents-api/deploy/e2b/README.md) owns packaging and user-managed
allocation, renewal and cleanup. Historical Core-managed E2B acceptance retains
only its original scope; it does not qualify the new enrollment path. Current
public Template acceptance uses Docker.

The independent Docker Provider consumes an immutable Runtime image and retains
one caller-owned allocation reference through partial creation and cleanup. Persist
that reference before Create and serialize its lifecycle; resolve a lost response
with observed state, without rewriting bootstrap credentials or replaying startup.
Provider state is compute state, not public Environment readiness. Named Runtime
volumes need explicit owned cleanup after container removal. A second mount of the
same workspace volume subdirectory provides the public `/workspace` path to native
tools; trusted staging and atomic rename retain the original parent mount. Do not
copy files or widen private-path reads to preserve an alias. Initialization command
timeouts can leave processes alive and require allocation cleanup before reuse.
The [managed Runtime build and operator configuration](services/agents-api/deploy/codex/README.md#managed-runtime-image-and-docker-adapter)
defines the explicit opt-in for basic hosted admission. Building an image alone
does not qualify its isolation or enable public creation.

Managed Runtime allocation, dedicated daemon credential hash and exact Session
binding commit atomically before Provider.Create, using the existing execution
lease and Session lock. Only the fresh allocation receipt permits Create; retries
and Core restart observe that same reference without replay or credential rotation.
The operator's stable provider key identifies one backend/installation; retain its
adapter for cleanup, and use a different key when changing the target. Never treat
absence on another backend as successful reclamation.
Disabling the default provider stops new hosted admission/bootstrap; it must not
block existing Session cancellation, tool results or input retry outcomes.
Input HTTP response budgets follow the persisted Environment type, covering the
admission wait for both hosted and self-hosted Sessions independently of operator
creation switches or remote executor configuration.

With an explicitly configured default managed provider, the same Worker scans
committed pending hosted Environments that have no allocation. This includes idle
Session creation and recovery after commit-before-bootstrap interruption; an
existing allocation never enters that startup path. Keep the scan bounded and
serialized by the existing lifecycle owner. Hosted provisioning requires no caller
connection action. An initial reservation without a Turn leaves its Session idle,
as allowed by the pinned contract; do not emit an in-progress event before a Turn
starts or treat a daemon connection as native readiness.

The same serialized scan publishes authenticated connection observations using
the existing durable generations after verifying the exact Session/device binding
and settled bootstrap. Socket loss remains observable during a provider outage;
Core restart fences old observations. Do not create a separate connection owner.

Terminal managed cleanup atomically revokes authority, persists Environment failure
or expiry, settles pending input and requests cancellation before external cleanup.
Preserve original input deadlines and retry outcomes. Temporary provider outages,
unknown Create results and stopped compute do not prove permanent failure. The
pinned stream has no Environment expired event; do not invent one. Exact hosted
failure codes and ordering remain explicitly unverified.

Allocation state is private compute ownership, separate from public Environment
connection/native readiness. Adapters qualify bootstrap completion; Core does not
infer it from an engine or provider name. Connected, observed compute receives
service keepalives between Turns. Keepalives cannot revive a one-hour lapse or a
cleanup request. Idle alone never requests shutdown. A stopped/missing container
does not authorize discarding retained workspace or history. Session deletion or
expiry requests cleanup, revokes the scoped device and cancels pending work before
Provider.Kill; the existing Worker serializes these lifecycle operations and drains
them before releasing its execution lease.

Keep the allocation after public Session deletion. Mark it released only after
owned compute/volume cleanup and evidence that its original Create has settled.
An unknown creation retains cleanup ownership even after an absence observation;
continue bounded scans for late resources without issuing another Create. This
conservative internal lifecycle does not define user-managed enrollment or prove
complete upstream expiry/error semantics.

Qualify the actual Docker/native sandbox before default cutover: real model
execution, file access, owned cancellation, restart with retained native history
and files, and rejection when required history is missing. Generated code and file
tools must not read daemon/model credentials, foreign Session history or another
tenant's workspace. Same-container placement, matching UID, mode bits, directory
bindings and capability flags do not prove isolation. Retain failed probes and
unverified limits; private functionality is not public hosted acceptance.

Before migration, archive existing edits and validation evidence. Reuse verified
authorization, resource/lifecycle ownership and safe filesystem primitives as
needed by V1. Stop work on separated-only enrollment, mirrored manifests and relay
mechanisms. Remove superseded unused code, configuration, tests, scripts and
task-owned temporary resources as each replacement is accepted. Preserve necessary
regressions, still-used official capabilities, product data and others' work.

The opt-in Codex deployment selector `PARSAR_CODEX_PERMISSION_PROFILE` chooses a
native named profile at harness startup and on both new/resumed threads, omitting
the legacy sandbox override. It is operator configuration, never a prompt option,
and rejects remote, none and temporary read preparations. Native managed
requirements own allowed profiles and deny-read enforcement. Keep the selector
unset for existing deployments. Managed native shells disable shell snapshots,
whose private files are inaccessible to tool execution; retain normal native shell
startup without granting tools access to harness state.
The [co-location qualification inputs](services/agents-api/deploy/codex/README.md)
record the pinned native/Docker prerequisites and limits; this switch alone does
not admit hosted Environments or authorize a workspace.

A managed Runtime's network policy is immutable deployment input, transferred
through the provider-neutral bootstrap and checked against execution preparation.
The shared policy includes enabled, disabled and an exact-host restricted allowlist.
Core preserves public spelling/order/duplicates and only permits Template overrides
that narrow authority. Adapters translate a normalized copy into native settings;
Core and Docker never select native profile names. Every hosted execution peer
must support `local_environment_network_policy` and receive the complete bound
policy; missing policy never falls back to enabled or an older peer path. Read-only
workspace access does not require execution network policy. A declaration alone
does not qualify an image or admit public hosted creation.

Codex restricted networking uses its native managed network requirements and proxy.
Its adapter preserves the image's filesystem, approval and hook requirements and
adds the frozen exact-host ceiling in Session-private state. The existing RPC
client owns a bubblewrap child that mounts those requirements read-only and runs
the stock native app-server in a PID namespace; teardown retains the same owner.
Preparation regenerates the non-secret requirements from the frozen policy. Keep
the file with Session state so cleanup cannot race a running child's mount.
Claude and MiniMax translate the same policy into their native sandbox allowlists.
These translations do not add a Core network service or model/tool loop.

A dedicated local Runtime uses one Environment-scoped device credential and an
immutable binding to that Environment's Session. It is excluded from general
device selection; another Session cannot claim it, including within the same
tenant. Deleting its Session invalidates credential lookup and heartbeat renewal.
Provision a new scoped device atomically rather than widening an existing shared
device credential. Revocation does not authorize silent placement replacement.

The private local Environment reference contains its identity and, for policy-aware
execution, its immutable network policy. Trusted Runtime deployment configuration
freezes the Environment, Session and workspace root;
requests cannot supply a replacement root. The V1 path uses only the exact local
Environment reference. Use the same preparation/start lifecycle for native execution and the
existing bounded workspace controls for directory access. Local idle directory
reads use the existing filesystem helper directly, with no model credentials or
temporary harness. These private capabilities alone do not authorize public requests or establish
Provider lifecycle. Self-hosted enrollment supplies the exact local binding.
Core rechecks the persisted Environment/device binding for preparation and active
reads; capability discovery cannot select or authorize a general device for this
placement. Local work uses the existing pending-input reservation and Worker
ownership without a remote connection resolver. The hosted profile supports `network.access: enabled`, `disabled` and exact-host
`restricted`; each image must qualify the supported policies before public deployment. Omitted
network settings mean enabled upstream and must not be silently treated as disabled.

Core and Runtime use common preparation, start, input-receipt, cancellation,
release and recovery semantics for Codex, Claude Code and MiniMax Code. Retain each
harness's native implementation behind its adapter. Core acts on verified capabilities and runtime
conditions; a capability declaration alone never grants public feature admission.
Extend existing interfaces during related functional work without introducing a
second framework or a broad rewrite. Codex, Claude Code and MiniMax Code have
qualified dedicated Docker profiles and historical Core-managed E2B evidence.
New user-managed enrollment requires separate real acceptance. Each harness has equal standing;
qualify each image/template with the common full-loop acceptance before deploying.
Additional engines remain separate work; V1 has no separate remote executor.
Later engines must satisfy the same applicable acceptance contract while keeping
their suitable native deployment layout.

Workspace reads may request the private `workspace_read_only` preparation profile
through the existing preparation factory and verified `workspace_read_preparation`
capability. It accepts only the bound Environment and resource identity; execution
options, model/MCP credentials, native Session continuation and model/tool input are excluded.
The Codex adapter creates temporary local state, reuses its native connection and
directory transport, and rejects Start. Its child inherits only process/transport
essentials. The private native read mode excludes system, managed, user and project
execution configuration and plugin startup while preserving native security
requirements. Ordinary execution keeps its stable state and configuration.
Reject the legacy mixed managed-config profile for reads rather than discarding
its enforced constraints together with execution settings.
For this read profile, `released` is published only after local Close succeeds;
cleanup errors retain ownership and report `cleanup_unconfirmed`. A failed factory
must return its resource with the error if cleanup remains unconfirmed; wrappers
must preserve both values. Successful cleanup retries publish confirmed release,
and stale status snapshots cannot publish success. Failed terminal status delivery
does not retry cleanup; ownership remains until an explicit release or shutdown retry.
A release request,
HTTP disconnect or remote socket closure alone is not cleanup confirmation. This
profile does not establish remote mutation quiescence or public Files admission.

Core directory reads reuse the Worker's Session scheduling reservation for idle
preparation and target the exact Run for active execution. Device selection uses
operation-specific capabilities; reading files never resolves model/MCP options
or creates a Turn. HTTP cancellation ends observation, not an admitted native read.
Keep the idle reservation through the bounded read and release attempt. Return
directory data only after confirmed Close; incomplete reads or uncertain cleanup
return unavailable without data. Release the Worker's scheduling reservation before
delivering the result so the caller can immediately request the next page.
Revoke the scoped read transport credential on
completion or failure. Runtime retains uncertain cleanup ownership and capacity;
this does not require a second durable Core owner registry or establish remote
write retirement. Public Files.list delegates workspace access to this reader;
the API owns tenant authorization, path validation and protocol pagination. Keep
partial directory coverage and unverified defaults explicit in the Files contract.

Source Files belong to the execution project and have an independent lifecycle
from copied workspace files. Store immutable source metadata and PostgreSQL large
objects in the execution database with the pinned pgx driver. Upload validation,
metadata insertion and bytes commit atomically; deletion removes metadata and
unlinks the object in one transaction. Keep OIDs private and authorize every
metadata/content/delete lookup by tenant before opening a body. Stream bounded
chunks; never hold an entire general Files upload in memory or use filenames as
filesystem paths. A read-only repeatable-read transaction preserves an admitted
source snapshot across concurrent deletion. Resolve that snapshot before entering
the existing Environment write path; deleting a source does not undo a completed
workspace copy. Bound request/transaction lifetimes, roll back incomplete bodies,
and never automatically retry ambiguous commits. Backups must include PostgreSQL
large objects; live deletion does not erase WAL or historical backups. Schema
rollback must not orphan existing source objects. Do not reuse product capability
tables or introduce a second destination writer for file_id.

Session Artifacts are immutable published output copies, separate from live
workspace files and general source Files. A private output exporter must reuse
the authorized workspace path boundary and stream bounded bytes. Require complete
capture and confirmed helper/transport success before publication; valid archive
syntax alone is insufficient. Never extract an output archive into Core's
filesystem or hold the global execution lease through a large transfer. Keep
publication ordered with Turn completion, and authorize stored reads independently
of Environment availability so published outputs can survive its expiration.
Capture bytes into private PostgreSQL large objects without a Session admission
lock; after confirmed export, lock and recheck the live Turn before staging metadata.
Before capture, seal native input under that lock using the private capture marker.
Later messages reuse the existing Environment input reservation and await the next
Turn; the public Turn stays in progress until publication settles. Directory reads
during capture use an independent authorized read-only preparation, not the released
native Run; they do not request model credentials or mutate the workspace. Cancellation
retains the existing Turn/reservation semantics. Do not introduce a second queue.
Publish metadata in the same transaction as Turn completion. Failed/cancelled Turns
discard private objects, and Session deletion removes both private and published
copies. Reuse the source-file snapshot reader pattern and common content response;
artifact deletion does not alter workspace files. Hosted execution requires the
Runtime's bounded output-export capability and exact read-only preparation binding;
capability advertisement alone does not qualify an operator's deployment.
Exporter component checks do not establish public Artifact compatibility.

Local inline file delivery uses the same authenticated daemon connection and exact
Environment/Session binding. The optional startup-owned `PARSAR_RUNTIME_WRITE_HELPER`
and `PARSAR_RUNTIME_STAGING` enable only the bounded installer primitive; they do
not grant public feature admission. Require a canonical executable outside the
Environment parent, canonical sibling workspace/staging directories on one mount,
and verified native tool denial of staging and its ancestors. Native credentials
and history remain outside that parent. Mode bits and path checks alone do not
qualify this layout. The read-only deployment needs neither writer setting.

Transfer a complete bounded body in acknowledged 64 KiB frames before invoking
the existing installer, verify the declared digest, and run no model for upload.
Keep the existing private 50 MiB bound distinct from upstream protocol limits.
The dedicated Runtime excludes execution while receiving or applying a write;
malformed, incomplete or expired transfers cannot reach the installer. Exact
commit/rejection receipts release the mutation owner. Missing or ambiguous
receipts retain uncertainty; observer cancellation and local process exit cannot
prove non-mutation. Before public admission, Core must durably reserve the write
under the Session lock and prevent successor mutation across restart until exact
settlement. Do not replay the request or introduce general replacement machinery.
Read-only operations retain their own authority and bounded ownership requirements.

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
co-located harness must not expose broader application credentials or other tenants'
secrets to generated code. Directory bindings and process identities do not provide
filesystem isolation. Preserve or demonstrably restore native history across
compute replacement; never silently move a bound Session or replay unknown work.
Self-hosted compute/files remain caller-owned, with explicit cleanup separate from
Session deletion. The full Environment implementation remains pending; follow the
[pinned contract and acceptance sequence](contracts/agents-api/environments.md)
and the [two-engine placement prerequisites](contracts/agents-api/workspace-placement.md).
For co-location, qualify both deployment isolation and native tool restrictions.
Bash sandbox settings alone do not establish file-tool or whole-harness isolation.
Never enable an environment profile before those boundaries are verified.

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
the daemon gateway is configured. V1 requires `/workspace` and empty/default
capability directories. Supported non-deferred functions keep their engine-specific
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
cancels pending input in the same transaction. A terminal reservation retry must not
affect a later reservation or Turn. Evaluate deadlines after acquiring the Session
lock, and return terminal storage outcomes without rolling their transaction back.

Initial messages for a newly created Environment-bearing Session use that same
reservation in the creation transaction, including its connection-action event.
The creation winner alone inserts it; the original pre-work snapshot and stream
cursor remain unchanged. A durable initial/later flag defaults historical rows to
later input without inferring origin. Initial expiry projects a failed Session and
safe error before any Turn exists; later expiry retains idle semantics. Failure
events capture the settled activity and Usage atomically. Late connections and
creation retries cannot reset or replay expired input, and newer work supersedes
old activity without changing its event snapshots. The Environment itself is not
failed by an input deadline. This Store rule covers actual self-hosted and internal
hosted associations; it does not enable hosted providers. None/absent Environment initial input retains
immediate Turn admission. Cancellation/deletion keep their existing semantics.

Ordinary and streamed public self-hosted creation accept initial text through this
transaction after configuration and new-work lease checks. They return the owned
Environment ID and executor URL while offline, without waiting for admission.
The creation stream sends its original pre-work `created` snapshot before the
committed connection action. A disconnected observer leaves committed input intact;
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
after claim requests cancellation under existing active-Turn semantics.
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
`execution_unavailable` for ownership loss, and existing 404 for deletion. Exact
hosted failure status/body and pending-input crash recovery remain unverified.
Principal acceptance must use public Session creation and input against the built
standalone service, including a wait exceeding its ordinary 30-second write timeout,
real remote commands/files and a second native-history Turn. Private provisioning
or injected API handlers cannot substitute for that workflow.


User-managed Runtime enrollment authenticates the existing principal executor key
against the exact live Session/Environment and its recorded creator. Keys retain
stable management IDs, immutable principals, optional exact-Environment restrictions,
rotation/revocation and digest-only storage. They grant connection authority, never
Session API access. No raw-token import or secret read-back is added.

Enrollment atomically creates or recovers one dedicated device and immutable Session
binding under the Session lock. The V1 workspace is `/workspace`; nonempty
self-hosted capability directories remain unsupported. No `runtime_allocation` is
created for user-owned compute. A retry cannot replace a device, change its bound
key or adopt another native history. Gateway authentication and dispatch recheck
current key authority; rotation/revocation and deletion deny further use.

The daemon, selected harness, local tools and workspace run together. The Dispatcher
passes the existing typed `LocalEnvironment` after exact tenant/Session/Environment/
device checks. Native preparation, Files and Artifacts reuse the same protected
local workspace and existing lifecycle owners. There is no registry/Noise relay,
transient harness credential, service-side harness or remote tool forwarding path.
Keep model credentials, daemon authorization and native histories private; connection
success alone establishes neither native readiness nor filesystem isolation.

The private daemon `workspace_read` control targets an existing preparation handle
or its transferred active Run on the same authenticated device connection. Require
the exact frozen Environment identity; callers cannot supply sockets, credentials
or workspace roots. Shared routing uses the optional `agent.WorkspaceReader`
interface, without selecting an engine by name.
The same control accepts `operation: directory` through the optional
`agent.WorkspaceDirectoryLister`, with mutually exclusive byte/entry limits and
typed directory metadata. Directory responses carry at most 1024 single-component
UTF-8 names of at most 255 bytes, so escaped metadata stays below the existing
frame bound. These are private transport limits, not public Files parameters.
Byte and directory operations share target checks, correlation, capacity and
retained operation waits; neither creates a Run or selects an engine by name.
This control does not itself authorize a public Files endpoint or placement.

The separate optional `agent.WorkspaceDirectoryLister` observes one workspace-relative
directory on the existing Prepared/Session owner; empty path selects its root.
Return single-component names, entry kind, regular-file byte size and explicit
truncation only after directory/metadata access and handle cleanup settle. Reuse
byte-read admission, uncertainty and caller-detach ownership where applicable.
Do not promise a snapshot, recursive traversal or public pagination through this
private interface. The Runtime invokes the retained directory helper against the
frozen local workspace. Keep the qualified helper outside writable paths, anchor
traversal to no-follow descriptors, bound enumeration and require a complete
validated result. Helper availability alone does not enable public Files admission.

Bound encoded request payloads to 8 KiB and correlation IDs to 128 bytes before
admission. Do not echo oversized IDs; omit oversized trace metadata in replies.
Bound raw control results to 1 MiB within the existing 4 MiB transport frame; the
limits are local policies, not pinned public protocol limits. Successful reads require complete bytes/truncation and acknowledged native
close. Safe native rejections carry no bytes; interrupted or ambiguous reads remain
unknown and stop further reads on that owner. Local RPC reap never establishes file
settlement. Retain a dispatched read's original bounded waiter across observer
cancellation and resource transfer/release; stop new admission on resource closure.
The gateway bounds subscriptions and never retries or replays on reconnect. Duplicate
pending operation IDs cannot start another read; this control does not promise durable
idempotency or result recovery. Preparation/Run ownership, public path authorization,
public file authorization and native cleanup retain their separate requirements.

The retained Rust directory, write and workspace-export helpers provide bounded
filesystem operations for the colocated Runtime. Their protected executable paths,
descriptor-relative traversal and exact workspace binding remain required. They
do not implement an executor transport or grant tenant authority.

Connection observations use the existing execution lease and Session lock. A
separate `environment_connections` row retains the current generation and revision;
`environments.status` and its Session Environment-event snapshot commit together.
The producer serializes replacements, then numbers socket observations within each
generation. Duplicate or older revisions and superseded generations are inert.
Replacement retires a previously connected observation before publishing its new
registration. Registration alone creates no connected event. Event payloads contain
only public Environment identity/type/status and nullable error, never configuration,
credentials, registration IDs or revisions. Transport observations have no asserted
Turn association. `connected`/`disconnected` are distinct from native preparation
readiness; do not cast resource `expired` into the event vocabulary or emit `ready`
for a self-hosted connection.

The existing Worker observes authenticated daemon peers for enrolled Environments,
using durable generation/revision fencing under its execution lease. On restart it
reconciles old connection observations before admitting new ones. A connection or
heartbeat does not prove native readiness or process quiescence. Failed or stale
observations cannot establish a current connection.

Preparation resolves the immutable local binding and holds the existing native
resource without creating model work. Start transfers that resource once; failed
start and abandoned preparation retain the established cleanup rules. Strict resume
uses the bound native history and never falls back to a new Session.

Every executable preparation must implement `PreparedCancellation`; read-only
preparations may implement only `Prepared`. The Router rejects and closes an
executable preparation before `ready` when that contract is missing. Codex fences
future Start under the transfer lock; unused resources use preparation teardown,
while transferred resources use the existing Session cancellation path inside the
adapter. `Close` remains inert after transfer.
`CancellationOutcome` exposes observed content, Usage and verified native identity;
an unstarted resource has no measured Usage or observed resume identity. This is
best-effort cancellation and an observed snapshot, not immutable final output,
notification drain, caller-deadline compliance or remote process quiescence.
From Start admission onward, the exact `PreparedCancellation` object is the sole
release target: a late Session never receives fallback `Cancel`, and the Router
never falls back to preparation `Close` or owner-context cancellation. Successful
`Cancel` means local cleanup is complete and no more output writes can occur. A
failed or timed-out call returns without waiting for output, retains execution
ownership, and permits only a later explicit serialized retry on that same object.
Before publication, failed cleanup also retains the preparation slot. Successful
publication permanently transfers resource tracking to the Run; subsequent release
does not restore preparation ownership or make its old handle cancel that Run.
Forwarded permission and user-choice observations from that cancelled handoff do
not register actionable interactions. Codex prepared cancellation also waits for
the transferred Session's local cleanup, which can finish after output closes.
Codex cancellation has one continuing native owner: caller deadlines bound only
their wait, leaving child observation and cleanup alive for an explicit retry.
Only observed child terminal facts can settle the child; actual observation
failures remain failures. Native process-exit confirmation remains retryable.
One prepared-output consumer starts before `Prepared.Start`, so native output beyond
the 64-frame channel capacity cannot deadlock Start. It retains the first terminal
frame, drains later output, and forwards accepted frames before the observed cancellation
outcome receipt. Missing capability, failed cancellation or failed forwarding cannot
produce an applied receipt. An unused resource may supply an empty observed outcome;
the Router never fabricates one.

The returned Session remains private until the `started` status send succeeds. The
handoff has one permanent release claim, one current native attempt and one
success-only settlement. Function results, permission decisions, user-choice
decisions and steering admitted before the claim hold the same operation barrier
through native submission, replay bookkeeping and receipt delivery. Natural
completion waits for Start publication before cancellation; an abort wakes that
same attempt and may fence a concurrent Start. The initial started-status send is
bounded by the preparation deadline, which is rechecked at publication commit.
Delayed expiry callbacks cannot cancel a successfully published handoff. The successful attempt waits for Start and the sole output
consumer, then forwards the retained terminal frame and removes the Run. Internal
paths join the current attempt; only an explicit cancel, Release, expiry, device
shutdown or later Router Shutdown may retry a failed attempt. Workspace reads require
a published, open Session but keep their independent bounded lifecycle.

Receipt settlement waits at most ten seconds, with a separate five-second send
budget and the existing gateway settlement deadline. A timeout does not free the
resource or stop tracking late cleanup. Shutdown cancels receipt waits while owned
Start/cleanup work remains tracked. This ordering covers cancellation received while
Start is pending; ordinary post-transfer cancellation, complete native output and
remote process exit retain their separate limitations.

The private daemon preparation controls reuse execution configuration but reject
input, RunID, Conversation, attachments and product authoring. The local profile
requires an exact Environment binding, stable state key, strict resume and completion
release. Its separate capability is registered through an execution-only factory
and preserved through the product registry wrapper and heartbeat mapping. Native
details remain inside the adapter; this private profile does not narrow upstream.

Preparation request IDs correlate only control responses. The daemon returns a
fresh opaque handle before slow work; Start supplies that handle and the actual
RunID/prompt. Handles belong to one daemon connection. Gateway preparation
subscriptions do not register Runs. Per-handle revisions order asynchronous status
snapshots; reject responses describe control errors without inventing run events.
At most four native preparations may be preparing, ready, starting or closing.
Start admission expires five minutes after acceptance; retries do not extend it.
Failed cleanup retains the native resource and its capacity until Close succeeds;
terminal handles with retained resources cannot be pruned or started again.
At most 64 request records are retained; retired request IDs may allocate a fresh
handle, while old handles cannot consume replacements. This is not durable
exactly-once preparation or cross-connection recovery.

Preparation and Start execute outside the receive loop and router lock, with
tracked lifetime work. Start reserves the real RunID and fixed cancellation target
before native work; cancellation in that phase uses the adapter contract across
the transfer. A late result cannot resurrect released ownership. Shutdown claims
or retries every prepared release under the lock before closing its cancellation
signal. A failed native attempt returns Shutdown without waiting on an output
consumer that may still be blocked; a later Shutdown retries the same target.
Successful publication stops the preparation deadline and uses the same prepared
output consumer and completion release;
later preparation Release cannot cancel that Run. Released/expired status makes
the handle unusable; asynchronous native cleanup still counts toward capacity and
does not promise immediate OS quiescence. Release retries retained cleanup.
Concurrent Shutdown calls join one tracked attempt within their caller deadlines;
a later call retries failed preparation cleanup and reports any remaining error.
A caller timeout does not discard ownership or repeat in-flight cleanup. These
records remain connection-local, not a persistent remote retirement fence.
The public idle-text path uses this
admission/start wiring; complete Environment lifecycle remains required work.

The Dispatcher prepares pending Environment input only on its exact enrolled or
managed device. Preserve the same physical peer and preparation handle through
readiness, atomic promotion/claim and the first non-replay Start. Derive workspace
and Environment identity from Store ownership, never caller-selected private paths.
Initial prompt/cursor come from the reserved batch; later messages use ordinary
steering. Never hold a database lock during native preparation.

Observe the original pending deadline, cancellation, deletion and peer loss while
waiting for readiness. Preparation failure leaves pending input and its deadline
intact unless storage has settled it; it creates no failed Turn or input history.
During Start, consume preparation controls alongside the ordinary Run stream so a
control-only rejection or pending-start cancellation can settle promptly. Reuse
ordinary journal, receipt and completion/native-history persistence. Once cancellation
is sent, preparation errors/closure cannot replace its receipt or timeout path.
Pending-start cancellation uses the adapter's observed outcome after daemon handoff
and output forwarding. Missing or unconfirmed outcomes still fail conservatively;
preparation control errors cannot substitute for the cancellation receipt.
Do not fabricate an empty cancellation outcome or infer native quiescence.
The connection owner spans preparation and the transferred Run without a reservation-derived Run
deadline; every exit releases it.

The existing Worker scans pending inputs using the same bounded scheduling slots,
Session locks, durable deadlines and engine capability checks. A self-hosted Session
waits for its dedicated enrolled device; it cannot select an arbitrary same-tenant
device or migrate an existing binding. Preparation failure can retry while still
pending without extending the deadline. Unknown promotion results or errors after
admission retain the existing no-replay settlement rules.

`AGENTS_API_DAEMON_WS_URL` enables the private gateway and supplies the unchanged
public `remote_url`. `AGENTS_API_HARNESSES` explicitly adds deployment-supported
engines to the default engine and configured managed profiles; advertising a
heartbeat alone does not enable an engine. The three native profiles share enrollment
at `/workspace`. Their new user-managed public chain requires fixed-client/raw HTTP,
real-model, recovery, cancellation and credential-lifecycle acceptance separately
from prior Docker or retired remote-executor evidence.

#### Independent build artifacts

`make build-agents-api` produces `agents-api`, `agents-api-migrate`,
`agents-api-device` and `agents-api-environment-key` under `${PARSAR_HOME:-$HOME/.parsar}/build/agents-api`.
`AGENTS_API_BUILD_DIR` may select another absolute output directory. The build
uses only the explicit source set in `scripts/build-agents-api.sh`: the execution
service, its Go contracts and required shared daemon/logging packages, plus the
root Go module manifests. Product server/frontend, other applications and their
migrations/assets are absent from the temporary build context. Keep this boundary
explicit when introducing shared dependencies; do not copy the whole repository
to make an accidental product dependency compile.

The build uses Go directly with workspace discovery and CGO disabled, read-only
module manifests and trimmed paths. It requires no Node, Docker or product setup.
`make check-agents-api` runs this build before its tests, so the full `make check`
and the dedicated CI workflow enforce the same boundary. CI exercises the built
migration command and uses the built server for official-client HTTP checks.
`make docker-build-agents-api` reuses that build for Linux amd64 and sends only
its four executables and `services/agents-api/Dockerfile` to Docker. The pinned
Distroless runtime runs without root, a shell, product assets or an embedded
harness. Keep runtime credentials outside the image and migrations explicit.
`make check-agents-api-container` runs the existing official-client suite against
the image with a read-only root filesystem; it requires Linux Docker, a non-root
host user, the pinned SDK and a dedicated execution test database. Dedicated CI
runs this after binary validation. Changes to the image/build path require this
check in addition to `make check`; do not make ordinary Go builds require Docker.
Registry publication, additional runtime architectures, daemon packaging and
product cutover remain separate work.

`make build-agents-api-release` reuses the isolated build for a Linux amd64 archive
under `~/.parsar/`, with its four commands, license, operator guide, source/tree and
protocol manifest, and file/archive checksums. It requires clean committed source
and Python 3.9+, stages output privately, and packages fixed artifacts deterministically.
Keep runtime configuration, credentials, product sources and separately installed
daemons/harnesses out of the archive. Archive changes require content/hash and
fresh-extraction operator checks plus `make check`; execution acceptance uses the
packaged operators and public protocol, not private Store provisioning. Preserve
the database and native history when replacing the API package. This target does
not publish a release or provide an installer/supervisor.

`AGENTS_API_RELEASE_RUNTIME_IMAGE=sha256:<image ID>` adds an explicitly qualified
Linux amd64 Runtime to the same release builder. The Docker archive contains that
immutable image export, the committed seccomp policy and hosted operator guide,
with hashes in its manifest and checksums. The default Core-only archive remains
Docker-free. Image selection and packaging do not qualify an arbitrary image or
prove compatibility between unrelated Core/Runtime versions: accept the exact
package with a fresh database, extracted binaries, loaded image and real public
workflow. Keep model/operator credentials external and Provider ownership stable
across upgrades. This is the same managed Runtime, not user-managed enrollment.

#### Current implementation

The constraints below describe existing code, not requirements to preserve legacy
design. The [protocol assessment](contracts/agents-api/README.md#implementation-direction)
identifies replacements and gaps. Update these rules when their implementation is
replaced; do not carry obsolete compatibility code forward to satisfy this section.

- `internal/agentdaemon/gateway` is the shared daemon connection implementation.
  Its persistence interfaces use `internal/agentdaemon/device`, never product Store
  types. Product adapters live in `server/internal/agentdaemon`. Keep protocol
  frames in `internal/agentdaemon/proto` until the contracts directory migration.
  Store aliases preserve existing callers during this transition.
- Session deletion uses a durable `sessions.deleted_at` marker, committed with an
  existing Turn cancellation request under the tenant Session lock. Public reads,
  metadata changes, event streams and input admission exclude deleted Sessions;
  admission checks visibility under that lock before retry lookup. Creation keys
  remain reserved and cannot resurrect deleted Sessions. Missing/repeated deletion
  locally returns 404 and reuse of a deleted creation identity returns 409; exact
  hosted errors and overlapping stream timing remain unverified. Existing streams
  close when removal is observed without a fabricated deletion event.
  Internal Turn/receipt/finalization and restart reconciliation retain access so
  hidden work can settle under the existing execution lease. Queued deletion
  prevents claim; an already claimed execution may complete or receive cancellation.
  Confirmation does not guarantee native quiescence. Never revoke a shared device,
  remove a saved Agent or touch product data as part of Session deletion. Physical
  SQL/native history cleanup remains a separate required implementation gap; these
  records are retained, not claimed purged. Do not deploy a pre-deletion service
  against a database with deletion markers; migration rollback refuses to remove
  the column while deleted records exist, preventing public resurrection.
- `services/agents-api` owns its SQL schema, sqlc queries and embedded goose
  migrations. `AGENTS_API_DATABASE_URL` is required; never fall back to the product
  database URL. Its first persistence slice stores tenant-scoped Sessions with a
  stable engine and idempotent creation. It does not switch production execution.
- Reusable Agents have their own tenant-scoped `agents` records, independent of
  Session snapshots, engine bindings and product Agent definitions. The Store
  persists caller-validated non-secret configuration and metadata without applying
  harness capability restrictions or model defaults. Resource identity and equal
  initial creation/update timestamps come from the persistence boundary. The
  create primitive creates a fresh resource; public retry conformance remains
  unverified. Internal storage admission is 512 KiB for configuration and 64 KiB
  for metadata, not a claim about upstream limits.
- Vaults have their own tenant-scoped records in the execution database. Initial
  `POST /v1/vaults` and `GET /v1/vaults/{vault_id}` operations persist and read the
  resource without creating Sessions or contacting an engine. Omitted name is
  null; a supplied non-null string is trimmed and limited to 1–256 UTF-8 bytes.
  Omitted/null metadata becomes an empty object. Reuse the 64 KiB encoded metadata
  storage bound, without applying Session-specific pair/character limits. This is
  a local bound, not hosted parity. `GET /v1/vaults` lists the authenticated project's
  records using creation-time/ID keysets, default descending order and a default
  limit of 20 clamped to 1–100. Other resource limit policies are unchanged.
  Status accepts `active`/`archived` as a scalar or SDK `status[]` array, with both
  included by default. Private stored classification defaults existing/new rows
  to active; it is never exposed in the Vault response. Listing reads no Credentials
  and needs no encryption key or execution service connection. Mixed status encodings
  and repeated scalar parameters are rejected locally. Exact hosted errors, equal-time
  ordering and changes between pages remain unverified. Private archived fixtures
  prove filtering only: there is no public archive writer, archive timestamp or
  inferred delete-to-archive behavior. Retrieval, Session binding and dispatch retain
  their existing rules. Archive/revocation lifecycle remains a separate gap;
  do not introduce product roles or speculative lifecycle fields. Migration rollback
  refuses to discard classification while archived rows exist.
- Vault `DELETE /v1/vaults/{vault_id}` removes the project-owned parent and all
  Credentials through the existing foreign-key cascade in one SQL mutation. Do not
  loop through child deletions, decrypt secrets, require the storage key or call
  providers. Deletion applies to both stored classifications. Local missing/repeated
  deletion returns not-found; subsequent parent/child reads and new attachments
  cannot use the removed resources. Preserve Session snapshots, frozen choices,
  history and recorded retries. Later secret lookups fail without selecting another
  attached Vault or anonymous MCP. Already-resolved tokens and running Sessions
  are not revoked. Exact hosted archive, visibility, overlapping-mutation and error
  semantics remain unverified; row removal does not prove physical storage erasure.
- Static-bearer Credentials are children of tenant-owned Vaults in the execution
  database. Creation admits the owner in the same SQL statement as the insert;
  retrieval joins the owning Vault and selects public metadata only. No public
  operation decrypts or returns a token. Encrypt before passing secret values to
  SQL, using the execution service's separately configured random 32-byte key and
  the standard library's random-nonce AES-GCM. The versioned authenticated binding
  includes tenant, Vault, Credential, authentication purpose and exact destination.
  Never reuse product master-key conventions or daemon transport encryption for
  this storage boundary. Missing key configuration disables credential writes;
  malformed explicit configuration fails startup. See
  [`services/agents-api/credentials.md`](services/agents-api/credentials.md) for
  key persistence and current limits. OAuth and storage-key rotation remain
  separate gaps; resource creation never contacts the destination.
- `GET /v1/vaults/{vault_id}/credentials` lists safe metadata only, with both
  project and Vault ownership enforced on the parent, cursor and row query. An
  inaccessible parent returns not-found, even when the collection would be empty.
  Reuse the Vault status/limit parser and Credential metadata mapping. SQL must
  never select ciphertext for listing; no encryption key or execution is needed.
  Credential status is a separate private active/archived classification, defaulting
  historical/new records to active and never derived from the parent Vault's status.
  Synthetic archived fixtures prove filtering only. There is no public archive writer,
  timestamp or delete-to-archive inference; existing create/retrieve/token replacement,
  Session bindings and dispatch keep their rules. Migration rollback refuses to lose
  archived classification. Archive/revocation lifecycle, OAuth and hosted
  query/concurrency semantics remain gaps.
- Credential `POST /v1/vaults/{vault_id}/credentials/{credential_id}` replaces only
  the static-bearer token and update time. Require `auth.type=static_bearer` and a
  string `auth.token`, preserving opaque bytes; reject extra mutation fields before
  writing. Reuse safe metadata for the immutable encryption binding, then scope the
  atomic SQL mutation independently by tenant, Vault, Credential, static auth type
  and exact destination. Never decrypt the previous token or send plaintext to SQL.
  Missing encryption configuration or a failed write preserves the old row. Return
  the existing safe metadata projection; identity, name, destination, creation time
  and Session snapshots stay unchanged. Subsequent dispatch reads use the committed
  replacement through existing scoped lookup; already-resolved requests may retain
  the old token. This is not storage-key rotation, in-flight revocation or hot reload.
  OAuth and exact hosted concurrent-update/retry/timestamp semantics remain gaps.
- Credential `DELETE /v1/vaults/{vault_id}/credentials/{credential_id}` removes one
  owned row, including ciphertext, with tenant/Vault/ID checked in the same SQL
  mutation. It needs no encryption key, secret read or network call. Local reads,
  updates, listings and subsequent dispatch lookups cannot use that ID; missing
  and repeated deletion return not-found. Preserve frozen Session choices, retry
  identities and history without fallback to another credential or anonymous MCP.
  A token already read before deletion may remain in a dispatched request. Deletion
  does not revoke provider tokens, cancel Sessions or prove physical erasure from
  native history, WAL or backups. Do not infer a delete-to-archive mapping; exact
  hosted archive, post-delete visibility and repeat/error semantics remain unverified.
- Public reusable Agent create/retrieve uses `/v1/agents` and the same authenticated
  tenant/Beta-header boundary as Sessions. The resource envelope owns identity,
  timestamps and metadata, separately from saved configuration and Session state.
  Resolve known defaults and validate supported schema before writing. Preserve
  model/name/instructions verbatim, nullable fields and structured JSON numbers.
  Stored reasoning/service tiers, enabled multi-agent settings, JSON Schema output
  and deferred/tool-search/programmatic tools do not imply execution support.
  Reuse function wire validation, keeping Session execution restrictions separate.
  Model-derived reasoning effort is unresolved when omitted; do not infer it from
  the selected harness. Omitted/null service tier currently uses `auto`; complete
  upstream default/error/retry conformance and remaining MCP/web-search variants
  remain gaps. Unknown/unsupported variants fail explicitly. No product lookup is permitted.
- Service-origin public HTTP MCP uses the native harness client and tool loop on
  trusted service-owned `environment:none` compute, with Codex or Claude SDK.
  The V1 colocated `self_hosted` profile rejects it: user-owned compute cannot be
  relabeled service-origin or receive its attached Vault credentials. Hosted
  service-origin MCP also remains unsupported. Environment-origin Plugin MCP uses
  its separate qualified local Runtime transport and isolation contract; do not
  disable that path or infer optional-feature equality across engines.
  Admission, device selection and preclaim still require the exact supported MCP
  capabilities. Native declarations do not widen public placement authorization.

- Keep accepted public MCP credential profiles separate from private adapter
  capabilities. The execution service declares the verified public bearer profiles
  centrally; a daemon capability alone cannot open a public profile. Reuse frozen
  binding validation at admission and later input, then the same MCP capability and
  placement checks at device selection, final preclaim and request construction,
  before scoped decryption. Native configuration and token injection stay in the
  adapters. Add bounded shared checks while changing the related execution path;
  do not defer known duplication to a general engine or plugin framework.
- The shared MCP resolver preserves omitted/null `allowed_tools` as unrestricted
  and an explicit empty list as deny-all. Saved HTTP transport output includes
  `headers:{}`; the effective Session transport omits headers, matching the two
  pinned resource types. Saved-Agent updates never change existing Session
  snapshots; per-Session tools replace the whole field. The initial profile admits
  HTTP(S), boolean `required` (default false), empty/null metadata and empty/null headers.
  Static bearer authentication requires HTTPS and the attached-Vault rules below.
  Inline authorization, URL userinfo/query/fragment,
  other origins and stdio remain explicitly unsupported.
- Codex required MCP initialization additionally needs `mcp_http_required`, advertised
  only for the verified native pin and checked during selection, final preclaim
  and daemon dispatch/preparation. Preserve the boolean through typed messages,
  native rendering and exact configuration preflight. Reuse native required-server
  initialization during root thread creation and cold resume; send no native Turn
  until it succeeds, and never replace a failed strict resume with a new thread.
  Public work may already be accepted/queued/in progress while native initialization
  waits. This is not a continuing health monitor or a new public readiness state;
  exact hosted Session creation timing and initialization errors remain unverified.
- Send MCP declarations through typed daemon fields, independently of function
  callbacks. In Codex, a non-nil declaration replaces operator MCP options; use the existing
  native renderer and original tool names for `enabled_tools`, including `[]`.
  Before thread creation/resume, query native `config/read` with the exact cwd and
  reject additional servers or effective configuration differences. Disable native
  plugins/apps and select file-only MCP credentials; reject existing credentials
  in the private native home without deleting them or native history. Native
  reserved labels are an adapter restriction, not a saved-resource schema rule.
  Requests without this typed field keep the existing product behavior. The check
  is a snapshot on trusted service compute, not an atomic barrier against concurrent
  operator configuration changes. Discovery of a declared deny-all server can still
  contact it; deny-all governs tool exposure. Reuse neutral tool observations and
  the existing public `mcp_call` projection, never add a second MCP/model loop.
- The private Codex adapter's HTTPS MCP bearer authentication requires
  `mcp_http_bearer_auth` and the existing MCP/environment capabilities, checked
  before the factory. It is restricted to trusted service-side Codex with
  `environment:none`. A transient
  per-server `bearer_token` becomes a fresh daemon-owned `bearer_token_env_var`
  reference for each native process. Put the exact secret only in that app-server
  child's environment, after auxiliary launch probes; never in global environment,
  arguments, configuration/history, public snapshots or logs. Preflight accepts
  only the expected server/reference pairing and retains the existing rejection
  of ambient credential sources. Service-side bearer variables must not enter
  generated commands, files or public history/snapshots. Use the native HTTP client
  with TLS verification.
  This execution profile rejects empty values and bytes outside RFC 6750 b64token
  syntax with generic errors; it never trims tokens or narrows opaque Credential
  storage. OAuth and hosted redirect/error equivalence
  remain separate work.
- Session `vault_ids` omission/null/empty means `[]`; nonempty attachments must all
  belong to the authenticated tenant. Preserve caller order and public MCP
  `credential_id`. Saved Agents may store a nullable/nonempty credential reference
  without authorizing its use. Session admission resolves an explicit credential
  only inside attached Vaults for the exact declared URL, or selects the unique
  matching static credential when the ID is omitted/null. No match remains
  anonymous; ambiguity is a local 400 and unavailable references use the same 404.
  Resolve before any Session, initial input or event write. Freeze safe bindings,
  including anonymous decisions, in private Session configuration; never populate
  the public credential field from implicit resolution. At actual dispatch, recheck
  tenant, attached Vault, selected ID, static auth type and exact URL before scoped
  decryption. Metadata queries select no ciphertext; tokens enter only the existing
  transient daemon request. Selected authentication requires `mcp_http_bearer_auth`
  during device selection and the final preclaim check. Missing/wrong keys or
  binding failures never fall back to anonymous execution. Exact URL equality,
  immutable selection timing, implicit response population and hosted error/redirect
  semantics remain local decisions or unverified gaps. No new MCP loop is permitted.
- Public Agent updates use `POST /v1/agents/{agent_id}` with the same tenant/Beta
  boundary and shared saved-field validation. Preserve omission separately from
  null; only supplied fields replace saved values. Metadata is a separate whole-map
  replacement, with null/empty clearing it. Lock the tenant-owned Agent row while
  merging validated fields and enforcing the complete configuration bound, then
  commit configuration, metadata and update timestamp together. Never write a stale
  full snapshot over another update. No-field updates read without changing timestamps.
  Supplied nested fields currently replace the whole field and explicit null uses
  existing saved defaults; exact hosted nested/null and no-op timestamp semantics
  remain unverified. Model-derived reasoning defaults remain a separate gap.
  Neither updates nor retries modify existing Session snapshots or execution state.
- Public Agent deletion uses `DELETE /v1/agents/{agent_id}` and one tenant-scoped
  `DELETE RETURNING id` statement. Return the stored canonical ID with
  `object=agent.deleted` and `deleted=true`; missing/repeated deletion locally
  returns not found. It never deletes Sessions, history or runtime state and does
  not cancel accepted execution. Recorded creation identities still recover the
  accepted Session; new references cannot resolve an absent source. Historical
  identities retain their documented limitation. Exact hosted errors and ordering
  of overlapping source creation/deletion remain unverified; no tombstone or
  successful result is fabricated for an absent resource. Reject query/body data.
- Reusable Agent listing uses the same tenant/Beta-header and response mapping as
  create/retrieve. Page by `(created_at, id)` with a same-tenant saved-Agent cursor;
  listing never resolves Sessions, product objects or execution capabilities.
  Reuse shared list-query parsing. Agent requests accept positive int64 limits and
  return at most 100 records per page with accurate continuation; other resources
  retain their current 1..100 request rule. The local default is 20. Return the
  list envelope with data/has_more and first/last IDs (null for empty pages).
  Exact pinned upstream default/cap, empty-envelope and error semantics remain
  unverified; do not present local limits or generic SDK parsing as full conformance.
- Session `agent_id` lookup uses the authenticated tenant. Copy the saved resource
  ID and effective configuration into the immutable Session snapshot; saved metadata
  does not become Session metadata. Never look up the source Agent when reading or
  executing an existing Session. Omitted override fields inherit; supplied objects
  and arrays replace whole fields before defaults and execution admission apply.
  Reuse saved configuration validation and keep native capability restrictions at
  Session admission. Reject unsupported effective options instead of dropping them;
  an explicit supported replacement may make a saved configuration executable.
  Inline Sessions use the same admission path. New saved-reference Sessions and
  inline requests with Vault attachments or credential references record a separate
  caller-intent hash: source ID (empty for inline),
  supplied overrides (including field presence), environment/vaults, original metadata
  and normalized initial input. Exclude response streaming and resolved source values.
  Compare that same-tenant retry identity before looking up the source. A matching
  retry returns the existing Session without input admission or source revalidation;
  ordinary reads include current activity. Stream retries use the row's committed
  event cursor and emit no created event. Recheck after source resolution failure
  for a concurrently committed creator; do not hold a lock across resolution.
  The unique creation upsert remains authoritative when concurrent resolutions differ.
  Unrelated inline requests keep their existing resolved/default equivalences.
  Recorded credential-bound retries recover before reading mutable Vault contents,
  so another same-URL Credential cannot change an accepted selection. Rows with a
  known creator but without recorded request intent retain resolved-hash behavior;
  original overrides cannot be reconstructed, so no backfill is permitted. Source
  mutation-independent retries apply only to recorded identities. Exact hosted
  retry/error semantics remain separate work.
- Tenant scope must come from authenticated service identity before calling the
  execution Store. Product workspace/user references in metadata grant no access.
  Keep credentials and effective execution options out of Session metadata.
  Store resolved, non-secret Agent/environment configuration in the Session's
  immutable configuration snapshot. Inline and historical creation identities include
  the resolved configuration; new saved references use the separate caller intent.
  Session metadata updates replace only metadata under the authenticated tenant:
  omission is a read, null/empty clears, and a nonempty object replaces all pairs.
  Keep execution state, timestamps and the original creation request hash unchanged;
  creation retries return the current resource without restoring its old metadata.
  Public schema validation belongs to the API; the Store validates JSON structure.
- Session listing optionally filters by the immutable root `configuration.agent.id`
  within the authenticated tenant. IDs are opaque and include inline Agents; never
  require a surviving saved Agent or resolve product ownership. Apply filtering
  before pagination and activity projection, using the tenant/Agent/creation index.
  Omission retains unfiltered listing; a supplied empty string remains a filter.
  Preserve the existing tenant-owned cursor and creation-time/ID ordering rules.
  Hosted empty-filter, mismatched-filter cursor and exact error semantics remain
  unverified. Other resource lists do not accept this parameter.
- `make sqlc-generate` and the drift gate cover both services. Run
  `make check-agents-api` with `PARSAR_AGENTS_API_TEST_DATABASE_URL` pointing to a
  dedicated `parsar_agents_api_*_tests` database for Session integration tests.
  CI provides a separate PostgreSQL service. Migration immutability and ordering
  apply independently to each service directory.
- Turn writes serialize on the tenant-scoped Session row. An idle message starts
  a Turn; messages during queued/running/waiting work belong to that same Turn.
  Store input retry identities and order durably. Cancellation retains its first
  target, including an idle no-op, so retries cannot stop later work. Queued work
  can cancel before dispatch; active work needs an executor outcome. Terminal
  states and outcomes cannot be overwritten. Public event admission uses these primitives; live output streams remain separate.
- Input requests are ordered batches committed under the same Session lock. A
  retry key identifies the complete ordered batch; changed length/order/content
  conflicts and a failed transaction leaves no partial inputs or cancellation.
  Existing single-event requests retain their identities at batch position zero.
  Internal admission limits are 64 events and 512 KiB of payload per request;
  the public API must still validate the upstream event schema.
- The external protocol reference is `openai/openai-python`'s `beta/agents`, pinned
  in `contracts/agents-api/upstream.json`. Follow its Session/Turn/event semantics
  and verify supported behavior using the official client. Track current coverage
  in `contracts/agents-api/README.md`; SDK workflow objects are not this contract.
- Shared supported wire types live in `contracts/agents-api/v1`. `make openapi`
  separately generates the product spec and `contracts/agents-api/openapi.yaml`;
  never mix their routes or authentication schemes. CI checks both for drift.
- The standalone service uses `AGENTS_API_DATABASE_URL` and operator-provisioned
  SHA-256 API key bindings from `AGENTS_API_KEYS_FILE`. Each key resolves one
  organization/project and typed user/service-account principal. The internal
  tenant UUID is its project resource partition. Before starting the listener or
  Worker, atomically insert or verify the configured project-to-tenant bijection
  in `execution_project_scopes`; never remap or delete existing associations when
  keys change. Configuration requires explicit identities, with no legacy default.
  Optional `OpenAI-Organization` and `OpenAI-Project` headers must match the key;
  repeated/conflicting values fail authentication. Metadata, forwarded identities
  and product session cookies grant no access. Keys can rotate under the same
  principal; changing or removing caller bindings requires a service restart.
  Every new Session requires an explicit typed creator at the Store boundary,
  including internal callers. Public creation derives it only from the authenticated
  principal. Persist creator kind/ID in the creation transaction and never rewrite
  them on retry, update or source mutation. The tenant remains the project partition;
  do not duplicate project identifiers or create a product identity dependency.
  Both early saved-reference recovery and the authoritative creation upsert require
  matching creator kind/ID before returning a Session or event cursor. Different
  credentials for the same principal can retry; another principal using the same
  project/key receives the local idempotency conflict. This does not introduce
  creator-only resource reads or mutations, or claim verified hosted retry parity.
  Pre-migration Sessions retain null creator columns and remain project-readable;
  creation retries cannot claim them. Missing creator and missing request intent
  are distinct. Never infer historical ownership from keys, metadata or product
  records. Retire older API writers before serving the creator-enforced deployment;
  mixed-version writers are not supported. Tests must supply explicit synthetic
  creators; only controlled historical fixtures may seed unknown ownership.
  The operator-selected
  `AGENTS_API_ENGINE` is separate from the requested model.
  Public execution supports the enabled `none` profiles, the three colocated
  self-hosted profiles and qualified three-harness Docker hosted profiles; reject unsupported
  input/environment/agent options explicitly.
- `packages/agents-client/v1` configures the pinned official `openai-go` Session
  service. Use SDK request/response types, pagination and errors directly rather
  than reimplementing transport or copying wire types. Supply an explicit service
  base URL/key and creation retry key; SDK retries are disabled by default. Product
  integration is a later cutover, not a side effect of constructing this client.
- `services/agents-api/tests/official_client.py` verifies the actual server with
  the pinned SDK and strict response validation. It requires a dedicated test DB
  prepared by the Store tests and `AGENTS_API_SERVER_BIN`; it never starts Docker.
  The same harness runs the official Go client with fresh execution tenants and
  checks its created Sessions through the Python SDK.
- Execution devices are operator-provisioned in the Agents API database with
  tenant ownership and a credential digest. Their internal daemon gateway uses
  `/api/v1/agent-daemon/*`, separately from the official `/v1/agents/*` surface;
  device credentials grant no Session API or product permissions. The optional
  `AGENTS_API_DAEMON_WS_URL` enables that gateway. It is a single-process registry,
  not a claim of multi-pod execution or stock `exec-server` interoperability.
  Self-hosted enrollment uses this gateway with an exact Environment binding.
  Session/device bindings are tenant-scoped and immutable. Revocation denies new
  connections and binding reads; an existing connection closes on its next
  heartbeat. Connectivity comes from the live registry, not a persisted online
  flag. `last_seen_at` is diagnostic only. Product gateway behavior is unchanged.
- `services/agents-api/internal/execution` dispatches internally resolved Turns
  through that gateway. Claim `queued` to `in_progress` before subscribing/sending;
  never automatically replay a claimed or interrupted Turn. Ordered extra inputs
  require native steering receipts. Commit terminal outcome and native Session ID
  together under the admission lock; unapplied messages prevent successful completion.
  Resolve credentials separately from the immutable non-secret snapshot.
- Internal execution requires the advertised `durable_turns` engine capability,
  strict resume, and optional cancellation receipts
  and `release_on_completion` support. Reject unadvertised peers before claiming;
  failed strict resumes must not fall back to a new native thread. Release the native writer before forwarding
  completion, so the next Turn can resume its durable native ID. For
  release_on_completion Runs, close new steering admission and finish all
  existing steering receipt sends before releasing the executor and forwarding
  Done. Cached input identities and conflicts remain readable while completing;
  queue/write success is not consumption. The receipt worker stays busy through
  its send, and router shutdown cancels its native and transport waits.
  `durable_input_receipts` is required before execution binding/claiming. The
  per-input `durable_receipt` opt-in requires release-on-completion and a phased
  adapter. Its ten-second transport timer stops only after a complete native write;
  a separate `written` acknowledgement stops the API's thirty-second delivery timer.
  Neither that phase nor legacy `in_flight` advances the input cursor. Await final
  native acceptance/consumption under the Run lifetime without automatic redelivery.
  Receipt sends retain a separate five-second shutdown-aware context, and Done
  retains a fifteen-second final settlement bound. Once cancellation is sent, its
  receipt owns the terminal outcome even if an input becomes unknown first.
  Calls without the opt-in retain their existing response deadlines. Existing product
  requests retain their default idle-process policy. Native history still requires
  the device's persisted engine files; IDs alone cannot restore deleted history.
  Cancellation receipts carry the stopped engine's continuity snapshot when no
  Done is emitted. Preserve separately reported usage on failure; do not add the
  same counters again when Done also includes them.
- The dispatcher is an internal entry point used by the standalone service worker.
  Legacy internal `daemon` configuration is not a public Environment type.
  Self-hosted enrollment uses exact local binding; further pending interactions
  remain separate slices. Unexpected interaction requests fail explicitly until supported.
- `environment_none` advertises an adapter's explicit environment-disable
  path. Execution snapshots with public `environment.type=none` require that
  capability and set `disable_execution_environment` on the internal prompt.
  For Codex, the daemon forces `CODEX_EXEC_SERVER_URL=none` after caller environment options
  and confirms native `local` and `remote` environments are unknown before starting
  or resuming a thread. Unsupported binaries fail closed. The bound device hosts
  the engine process; it is not a user execution environment. This is not an OS
  isolation guarantee, and engine state still lives on that host. Ordinary product
  requests retain their existing environment. The public worker selects an authenticated same-tenant engine host for this mode.
- `execution_controls` advertises the typed search/verbosity block on the daemon
  prompt. Agents API requires it in addition to the selected engine's required capabilities
  before binding/claiming work. Older peers with only option-based capabilities
  must not receive controls they would ignore. The API sends resolved search and
  text verbosity values; native option names belong to adapters. Codex translates
  them using its existing validation/catalog path after cloning operator options,
  so explicit controls take precedence without mutating those options. Omitting
  the entire block preserves ordinary product behavior; a supplied block requires
  both valid fields. This internal contract does not add public configuration or
  engine support. Future native adapters must verify the same semantics before
  advertising the capability.
- Explicit public `programmatic_tool_calling.enabled=false` uses the common
  `ExecutionControls.DisableProgrammaticToolCalling` field and the operation-specific
  `programmatic_tool_calling_disable` capability. Both public qualification and
  Runtime support are required for that request; omission creates no prerequisite.
  New and resumed executions retain the frozen setting. Codex disables native
  code-mode features and checks managed requirements before starting/resuming a
  thread, rejecting a conflicting requirement. Claude and MiniMax retain their
  restricted native inventories, which exclude programmatic execution. This does
  not remove unrelated native utilities or claim enabled programmatic support.
  Explicit `web_search.mode=disabled` reuses the existing disabled search control.
  Search remains off when omitted. Optional search settings are resource data and
  do not cause execution while disabled. Enabled search remains unqualified here.
- `web_search_control` advertises the Codex adapter's explicit `web_search` option
  (`disabled`, `cached`, or `live`). Agents API requires this capability before Codex dispatch;
  the typed execution controls force search off on new and resumed Turns. Native configuration translation stays in the
  adapter. Product requests that omit the option inherit their existing defaults.
  This is tool selection, not a network isolation guarantee.
- Inline Agent `text.verbosity` accepts `low`, `medium` and `high`; omitted or
  null values resolve to `medium` in the immutable configuration snapshot. The
  Codex dispatcher requires `text_verbosity` support and sends the effective value in
  typed execution controls through the Codex adapter for both new and resumed Turns. The adapter queries
  the native active catalog with `codex debug models`, checks model support and
  pins that catalog snapshot for execution. The probe requires Unix process-group
  cancellation; other daemon hosts do not advertise this capability. For models
  without declared verbosity support, including the native unknown-model fallback,
  `medium` selects native default text generation by omitting the override. The
  pinned protocol defines `medium` as the default text amount. Supported models
  still receive explicit `medium`, even when their catalog default differs.
  Unsupported `low`/`high` and unreadable catalogs fail before model execution;
  unsupported non-default levels remain an explicit implementation gap.
  Product requests that omit the native option retain their existing defaults.
  Structured output has a separately qualified profile described in
  [Structured output execution](#structured-output-execution).
- `subagent_control` advertises native subagent tool control. Agents API requires
  it when resolved `multi_agent.enabled` is false and sends the typed internal
  `disable_subagents` policy on both new and resumed Turns. Native translation
  stays in the adapter: Codex disables both multi-agent feature generations,
  overriding operator feature preferences. Product prompts that omit the policy
  retain their defaults. Enabled multi-agent observations require separate operation qualification; the Agent tools list is not
  proven to enumerate every harness-internal utility.
- Subagent resources use the common observations in
  `internal/agentdaemon/proto/subagents.go`: verified identity, successful lifecycle
  effects, native-owned Turns/Items and neutral coordination operations. Core
  assigns public IDs and projects them under the existing Session lock and leased
  execution journal. Native names, history parsing and outcome proof stay in
  adapters. Public GETs read persisted resources without starting native work.
  Child Turns have a native writer and a separate table from the Core queue;
  `public_execution_turns` provides the shared Session read/pagination view.
  Session Items stay root-owned; copied parent transcripts never become child work.
  Repeated effects are idempotent. Active includes idle; task completion, process
  release and cancellation cannot fabricate public closure. Native timestamps
  retain their actual precision and unknown Usage stays null.
  Reuse the existing native owner for child settlement and cancellation, freeze
  root output first, and deliver child Items before their terminal Turn snapshot.
  Do not add another scheduler or a broad recovery framework. Capability
  advertisements do not qualify unsupported native facts. The exact read contract,
  admission limits and remaining evidence are in [Subagents](contracts/agents-api/subagents.md).
- `function_tools` advertises the optional native function-call bridge. Explicit
  prompt definitions become Codex dynamic tools; unchanged prompts carry none.
  Requests and ordered text/image results are scoped by Run and native call ID.
  Normalize string results into one text part at the public execution boundary;
  the internal result carries a typed content array, and adapters translate it
  to native content without fetching images or dropping parts. Validate content
  before consuming a pending call. Retry identity includes the complete ordered
  content and success flag. Reuse
  application receipts and conflict detection; a receipt confirms the native
  reply was written, not that an external side effect succeeded. Pending calls
  end with their Run; the execution service owns persistence and recovery, while
  Parsar retains business approval and credential-owner authorization. Do not
  map native approval requests to invented official protocol resources.
- Function-call storage is scoped by authenticated tenant, Session and Turn, with
  immutable public/executor call identities and arguments. Result admission and
  application receipts serialize on the same Session lock as cancellation and
  terminal transitions. Store the complete caller-validated result object; wire
  validation and native translation belong to their API and execution boundaries.
  Identical retries return the saved decision; changed results conflict. Pending
  reads exclude applied calls and cancelling/terminal Turns while history remains
  readable. Persistence does not imply transparent native-process recovery.
  Recording a call moves the Turn to `waiting`; the last application receipt
  resumes it. Session reads use one database snapshot for Turn, actions and usage.
  Session state events retain their action snapshot, without private executor IDs
  or results. Cancelling/terminal Turns expose no actionable calls. A waiting Turn
  can fail or cancel before a result arrives; successful execution requires resume.
  Actions remain visible until native application is acknowledged. This timing is
  an implementation choice, not verified upstream event sequencing.
- Function results can join internal message/cancel input batches. Their explicit
  Turn/call identity selects an existing call; admission never creates a Turn for
  a result. Save the complete result and its input retry record in the same Session
  transaction. Any invalid target, conflicting result or later batch error rolls
  back the whole request. Identical saved results remain retryable after termination
  without applying them again. The execution input cursor skips function results;
  their separate native receipts still determine application. Public result events
  validate variant-specific fields and required values before admission; retain
  omitted versus null error/output and ordered text/image parts. Inline function
  tools resolve into the immutable configuration with explicit
  `defer_loading=false`. Validate required strings and parameter objects before
  persistence; reject unsupported deferred discovery. Omitted/null/empty tool
  lists resolve to no tools. The public worker selects or waits for a same-tenant
  device advertising `function_tools` when the Session has functions.
  Function results are Session input Items: emit `item.added` without an output
  index, and never emit `item.done`, whose upstream union only allows agent output.
  Project their public output/error from the saved submission, including missing
  versus null fields; native content normalization must not change public history.
- Internal function execution requires an advertised `function_tools` capability
  before claiming a Turn. Translate resolved definitions in the execution adapter,
  persist declared callbacks before exposing actions, and deliver each saved result
  once per live dispatch. Keep its success flag and ordered text/image output;
  append a non-null error as a final text part because the native result has no
  separate error field. Retain the original complete result in storage. Do not
  treat transport delivery as application or automatically replay an uncertain
  result. The adapter waits for outstanding application receipts even when Done
  arrives first. Waiting Turns still accept execution observations and cancellation.
  The public function workflow is verified with the pinned SDK and a real daemon
  and Codex process against a synthetic model endpoint. This does not verify
  other tool types, deferred discovery or upstream service timing.
- `message_items` advertises native assistant-message observations. Agents API
  opts in with `observe_messages` only for advertised peers; ordinary product
  requests retain their existing frame sequence. Opted-in text deltas carry their
  native item ID, and `output_message` records start/completion, phase and the
  completion text snapshot. A snapshot is not another delta; uncompleted messages
  remain partial when their Turn ends. Keep these observations in the journal
  before projecting public Items. This does not promise daemon event replay.
- Legacy `tool_items` / `observe_tools` raw snapshots remain available to old
  daemon callers. New Agents API execution does not request or decode them.
- `tool_observations` advertises engine-neutral tool snapshots. The opt-in
  `observe_tool_observations` takes precedence over legacy `observe_tools`:
  attach the typed `observation` to existing tool-call frames without `native_item`.
  Native adapters own discriminator/status/action translation and preserve raw
  structured values; reuse the shared function-result content type. Kinds are
  `command`, `mcp`, `function` and `web_search`; observation status is
  `in_progress`, `completed`, `failed` or `incomplete`. A present empty function
  content array remains distinct from missing content. These are
  execution facts, not public Items: the API owns public IDs, schema projection,
  lifecycle events and persistence. Product requests that omit the opt-in keep
  their frame sequence and fields. Agents API requires this capability before
  claiming work and always requests neutral observations. Its Item projector
  validates this shared contract and never decodes engine-native tool snapshots.
- Codex callers opting into neutral tool observations also receive `command_output`
  fragments with the existing native command identity. The adapter filters the root
  Thread/Turn; the service requires an already indexed command in the same Turn.
  Journal and Item updates commit with `agent.output.command_execution_output.delta`
  events, retaining original fragments and the command's stable output index.
  Completion output replaces accumulated drafts; absent completion output retains
  observed text. Terminal Items ignore late fragments, and cancellation preserves
  partial output without inventing successful command completion. Native text
  conversion and output quotas still apply; this is not a byte-complete stdout/stderr
  guarantee. Pinned native 0.153.4 also has an early-output subscription window;
  missing native notifications/aggregate bytes remain a separate execution gap,
  not output to reconstruct from model tool-result prose. Older peers may supply
  only completion snapshots. Product requests
  without the observation opt-in retain their existing frames.
- Execution observations are written to tenant-scoped `turn_events` in ordered,
  idempotent batches before they can back recovery or publication. Keep daemon
  payloads intact; this internal journal is not the public SSE protocol. Flush at
  least every 100 ms while consuming events and before terminal persistence;
  uncommitted observations can be lost on a hard process crash. Terminal outcome,
  journal entry and native continuity commit together. Preserve partial text on
  cancellation, including frames queued before a separate cancellation receipt.
  Do not infer successful completion after a persistence error or stream overflow.
- Agents API uses the gateway's durable subscription; overflow or disconnection
  closes it with an explicit error. Product subscriptions retain their existing
  best-effort behavior. Journal limits are 512 KiB per payload, 1 MiB per batch,
  65,536 observations and 32 MiB per Turn; terminal persistence reserves one
  additional outcome entry. These are internal admission limits, not promises
  about upstream API limits or durable daemon-to-service replay.

- Live Session SSE reads execution-owned `session_events`, committed with the
  corresponding input, Item or lifecycle transition under the Session lock.
  Store immutable transition snapshots; never render an old event from a later
  Turn state. Reuse the API's response mapping and keep internal snapshots out of
  wire payloads. Historical index rebuilding emits no live events.
- The notification buffer retains at most 256 events and 64 MiB per Session
  after each transaction, retaining a single oversized event if necessary.
  Read batches are bounded to 32 events / 1 MiB, with the same single-event
  exception. This buffer is not a public replay log: GET begins at the committed
  high-water mark, ignores Last-Event-ID, and polls committed events every 100 ms.
  Missing sequence positions produce a safe stream error and close; recover via
  Session/Turn/Items queries. Socket writes have a five-second deadline and hold
  no database connection. Client disconnect releases the handler; comments keep
  idle connections alive. SSE does not close merely because one Turn finishes.

- Session creation with `stream=true` reuses atomic input admission and the live
  event loop. The upsert returns its cursor under the Session lock, before initial
  inputs; never replace it with a post-commit cursor lookup. A new response emits
  one request-local `agent.session.created` with the pre-input resource snapshot,
  then committed changes from that cursor. The local creation retry key excludes
  response mode: retries observe only later events and admit no work again. Retry
  the same request/key with `stream=false` to recover a lost Session ID. GET event
  streams retain their current live-only start. Disconnect never cancels admitted
  work. Exact upstream created-snapshot timing, POST stream lifetime and creation
  retry response semantics remain unverified; the separate SDK one-Turn helper
  does not define this endpoint. Do not present local retry behavior as replay.

- Public Turn retrieve/list project persisted execution state and the immutable
  Session Agent identity. Scope both resources and pagination cursors to the
  authenticated tenant and Session, ordering by creation time then ID. Do not
  expose adapter outcomes, native IDs or raw errors. Failure uses a customer-safe
  category; usage is nullable when a complete upstream breakdown is unavailable. Submission uses the separate Session events endpoint.


- Public Items list reads a persisted execution-owned projection, updated in the
  same Session transaction as admitted messages and journal batches. IDs derive
  from the Turn and source identity; the first-observation timestamp and stable
  tie breakers never change when content or status changes. Allocate each new
  Item's Session position under the Session lock, preserving observation order
  for equal timestamps. Allocate a separate zero-based `output_index` per Turn;
  inputs do not consume output indexes. Updates and retries retain both values.
  Existing indexed history keeps its pre-upgrade deterministic order; missing
  original ordering cannot be reconstructed. Cursors are scoped to the authenticated Session.
  Terminal Turns expose unfinished Items as `incomplete`, preserving completed
  message/tool states independently of the Turn outcome.
- Public history reads use the persisted index; the private pre-Items journal
  backfill is retired. Migration 15 rejects unprepared historical Turns before
  removing the obsolete indexing marker. Prepare them with release `906069e`
  before upgrading, following `services/agents-api/README.md`. The migration
  preserves indexed Item payloads, positions, output indexes and source journals;
  never mark unprepared history indexed by hand or replay engine execution.
- Project only the declared public Item variants; native adapter metadata is not
  a response schema. Preserve structured tool JSON without float conversion.
  Completion text replaces accumulated deltas. Item merging must not mutate the
  incoming observation or the previous snapshot: public text delta events read the
  original fragment after merging, while Items retain the accumulated text.
  Copy the content slice before replacing its text pointer. Keep partial output on termination;
  do not turn an unfinished call into a successful result. Thinking fragments are
  internal observations, not a claim of upstream reasoning-item support.

- Public `POST /v1/agents/sessions/{session_id}/events` accepts ordered text-message
  and cancellation batches through the pinned official client. Preserve individual
  input messages in the Item index while deriving text for native dispatch. Batch
  idempotency and cancellation targets remain durable; unsupported variants fail
  before admission. Session creation accepts initial text as a string
  or user-message array through the same parser and admission path. Commit the
  Session, initial input, first Turn and Item/event projections in one transaction.
  A creation retry returns the existing Session without re-admitting initial work,
  including after terminal or later Turns. Omitted/null input retains idle creation.
  Creation streaming uses the shared live path above; non-text messages remain a gap.
- Enabling `AGENTS_API_DAEMON_WS_URL` also starts a bounded execution worker. Select
  only connected, capable devices owned by the authenticated tenant; bind once and
  preserve native continuity. Metadata cannot select a device. Offline work stays
  queued and can be cancelled. An engine host is not a self-hosted environment.
- One worker service owns an execution database through a dedicated PostgreSQL
  advisory-lock connection. Its execution Store view uses that same connection for
  every Session transaction: binding, claim/reconciliation, journal/Items/Usage,
  function callbacks/application receipts and terminal/native continuity. Serialize
  these short transactions and lease pings; execution transactions have a five-second
  deadline including gate and Session-lock waits. Never hold a transaction across
  daemon/model work, reconnect the writer or fall back to the pool after lease loss.
  The original Store handles public admission and device/auth maintenance on pooled
  connections. Execution reads may also use the pool; a read grants no write authority.
  At startup, reconcile previously claimed work as failed, preserve queued inputs
  and never replay uncertain execution. Shutdown cancels active dispatch and attempts
  terminal persistence before releasing the lease; a lost owner cannot commit it.
  Lease Close invalidates its writer and waits for pgx connection cleanup within
  the caller deadline. A later Close can resume that wait after a timeout. This
  drains client resources; it does not acknowledge remote advisory-lock release.
  Tests that immediately transfer ownership must observe the previous owner's
  exact database advisory lock disappearing before starting its successor. Bound
  that wait and fail on query errors; do not retry Worker startup to mask competing
  owners or change production lease behavior for a test's timing assumption.
  Worker shutdown retains its existing bounded best-effort close policy.
  This fences database writes, not already queued daemon commands or native effects.
  Native quiescence/reconnect and recovery of unreported outcomes remain separate
  gaps; this is not distributed exactly-once side-effect execution.
- Session state and last activity derive from its latest persisted Turn. Queued or
  active work is `in_progress`, successful/cancelled work is `idle`, and failures
  use a safe public error. The worker does not replace product dispatch, business
  authorization, or the separate approval/environment lifecycle work.

### SDK subprocess ownership

The shared daemon `clirunner` offers opt-in Unix process-group ownership for
adapters whose SDK launches a native child. Existing callers keep their current
process policy. Explicit cancellation and parent-context cancellation share a
TERM grace period and bounded KILL escalation. An internal reaper also cleans
remaining group members when the direct process exits, even if a descendant
still holds stdout open. During cancellation, surviving descendants keep the
remaining TERM grace after the leader exits. Unsupported hosts reject this mode before launch.

Owned output pipes remain readable after the leader exits. Consumers must drain
stdout and stderr before calling `Wait`, which joins the cached process result
and closes the readers. `Done` reports leader reaping and group cleanup signals;
it is not a native execution receipt or proof of persisted history. SDK adapters
must close their query, await their native child and drain observations before
publishing completion. Process groups are lifecycle supervision, not OS isolation
or containment of descendants that deliberately leave the group.

### Harness qualification and onboarding

Codex, Claude and future harnesses have equal architectural status. The common
Runtime wire protocol and Factory/Session/Prepared interfaces own lifecycle,
input receipts, cancellation, recovery and resource access; each native adapter
retains its implementation and model/tool loop. A new engine supplies an adapter,
a qualified profile in `services/agents-api/internal/engine`, registration and
an independently verified deployment. It does not add engine-name branches to
API handlers, persistence, dispatch or scheduling.

Agents Core is pre-release. Replace superseded internal interfaces and execution
paths cleanly; do not retain version fallbacks or compatibility shims. Preserve
the pinned official public protocol, valid data and still-used infrastructure.

The small static profile catalog owns engine-specific public admission and value
limits. Profile callbacks are pure and use existing public/protocol types; they
cannot query business data, decrypt credentials or control native processes.
Public schema validation, qualified engine support and actual Runtime capabilities
remain separate. Runtime advertisements alone never enable public operations.
Shared dispatch checks capability combinations, not a whitelist of engine names.
Harness onboarding does not require feature equality. Verify common lifecycle
obligations and use the same public assertions for each declared operation,
retaining native isolation tests where appropriate. Optional native differences
remain independently prioritized capability/protocol work, not onboarding blockers.
Keep the complete pinned public protocol target and accepted functionality intact.
Never equate accepted parameters with applied native behavior.

The base daemon `Session` owns cancellation. Permission and user-choice response
methods are optional `PermissionResponder` and `UserChoiceResponder` interfaces;
an adapter only implements them when it emits those interactions. Unsupported
responses receive a negative receipt, never fabricated application. The router
retains its existing interaction routing and retry ownership.

`execution.Policy` supplies immutable service qualification to HTTP admission,
Worker device selection and final dispatch. Custom service composition must give
the same Policy to `api.WithExecutionPolicy` and `Dispatcher.Policy`. Zero values
use built-in profiles; an explicitly empty catalog authorizes none. Runtime
advertisements cannot add service profiles. No mutable global registration or
compatibility fallback is permitted. The synthetic third-harness acceptance under
`services/agents-api/internal/store` exercises the actual gateway and daemon router;
its fixture under `apps/parsar-daemon/testdata` is never a production engine.
See [the integration guide](contracts/agents-api/harnesses.md) for contract and
operation-specific acceptance and the [onboarding reference](contracts/agents-api/harness-onboarding.md)
for implementation and registration steps.

MiniMax Code's opt-in Agents API profile qualifies native ACP 0.4.12 for
`environment:none` text execution. It reuses the shared lifecycle without public
workspace, functions or MCP. Enabled Subagents use the separately qualified
common observation path below. Native configuration disables
file/shell authority and external
capability discovery; the child receives a private Session home and a restricted
environment. Active-input application requires a native ACP receipt, cancellation
settles the process and output, and continuation requires the exact owned native
history. Do not infer history IDs or qualify hosted execution from this text
profile. See [deployment and acceptance](services/agents-api/deploy/mcode/README.md).

The MiniMax workspace profile builds one CLI from the fixed upstream source and
lockfile through the existing companion packaging path, and isolates native
workspace tools behind its standard MCP client. The process and native Session
share one private control directory; public workspace files cannot configure that
process or become privileged project instructions. A trusted adapter-owned bridge
runs the original six tool implementations in the upstream Linux sandbox, with no
unsandboxed fallback. Keep native history bound to the control directory and Files/
Artifacts bound to the public workspace. Core and shared file helpers remain engine
neutral. This internal MCP transport does not admit public MCP configuration.
Record the upstream revision, native admission patch hashes and worker-source
provenance. The bounded patch checks the shared descendant-task limit inside the
existing native SQLite admission transaction before start, without another
scheduler. ACP initialization must acknowledge the applied limit before input.
The native tool catalog applies the protected workspace policy to every child,
not only the root's configured profile. Only the Session's authorized internal
workspace MCP entry crosses the native child selector; this does not grant
external MCP access or bypass the native profile's read/write restrictions.
Initialization must acknowledge that protected tool policy before input as well.
Subagent reads use the Session-private protected native database. Multi-agent
workspace execution installs only the existing authorized workspace MCP entry in
that private native configuration so children inherit the same tools; public MCP
and Environment-origin MCP combinations remain separately qualified. Complete
[workspace acceptance](contracts/agents-api/mcode-workspace-v1.md) before enabling
hosted execution. The standalone companion uses its own npm lock; `make check`
runs its lifecycle tests and script checks, while its exact-source Linux build and
Docker qualification (including `native.test.mjs` under both network policies)
are required when the companion changes.

Claude hosted functions compose the existing SDK function bridge with the native
workspace sandbox. Only declared function tools and the verified native tool
inventory are available. The bundle advertises this combination separately from
basic workspace execution; function preparations require that verified combination.
Service-origin hosted MCP remains unqualified; Environment Plugin declarations
use the separately qualified initialization path above. Function callbacks do not change file,
credential, history, subagent or network authority.

### Claude dedicated Docker Runtime

Build the pinned SDK bundle with `scripts/build-claude-sdk-runtime.sh`, then use
`scripts/build-claude-runtime.sh` with the existing shared workspace helper build.
See [deployment and engine onboarding](services/agents-api/deploy/claude/README.md).
The helper executables retain their historical Codex names; their local directory,
write and export operations are shared and do not launch an engine.

`PARSAR_CLAUDE_SDK_WORKSPACE=managed` requires the shared dedicated local binding,
canonical workspace and its same-inode `/workspace` mount, and explicit immutable
network policy. Native history, home and scratch live separately under
`PARSAR_HOME/runtime/claude-sdk`; daemon authentication stays under
`PARSAR_HOME/parsar-daemon`. The trusted image and protected staging directory
remain outside writable workspace roots. No product state or native user profile
is imported. The separate unbound `environment:none` profile keeps its behavior.

The Docker operator option `nested_sandbox` is false by default. The qualified
Claude image requires it: Docker supplies an init process and permits nested procfs
mounting by removing its outer `/proc` masks/read-only submounts. `/sys/firmware`
and powercap remain masked; the root and sysfs mounts remain read-only, capabilities
remain dropped, and the existing seccomp/no-new-privileges policy remains enabled.
Do not enable privileged mode, weaken the native sandbox, mount host process state,
or apply host-global policy changes. This is an image deployment prerequisite,
not a public API option or an engine-name branch in the Provider.

The SDK adapter advertises `local_runtime_v1` only for its Linux bridge contract.
Registration combines that contract with the verified operator binding. Core uses
an explicit accepted engine profile independently of advertisements. This profile
supports native Bash/Read/Edit, preparation, shared Files/Artifacts, cancellation
and same-history continuation. The separately advertised `workspace_functions`
combination supports declared public functions with text results. Service-origin
HTTP MCP remains unqualified here; its existing `none` support is retained.
Environment Plugin MCP follows the separately qualified transport path above.
Native Bash network access uses the harness's HTTP proxy; no alternate networking
or tool loop is implemented by Core.

Recovery uses the SDK's history APIs. An explicitly supplied native identity must
exist. If Core requires existing history without having recorded an identity, the
adapter accepts only one nonempty native history for the exact bound cwd. Missing,
foreign, ambiguous or metadata-only history rejects before model input. The Runtime
volume and shared Environment/Session binding establish ownership; this lookup
cannot select another Session's home or infer ownership from a model response.

### Deferred function discovery

Public `tool_search` and function `defer_loading` are shared Runtime intent.
Core preserves complete immutable definitions, sends `PromptRequestPayload.ToolSearch`
and each `FunctionTool.DeferLoading`, and requires the operation's existing profile
qualification plus the Runtime `tool_search` capability. It never performs native
search, selects native names, interprets provider policies or implements another
model/tool loop. Additional harnesses implement the same intent in their adapters.
The pinned Session AgentTool response union excludes the tool_search input member;
project it out of Session/SSE resources while preserving saved and frozen input.

The bounded implementation targets Claude's single-agent `environment:none`
function profile, including qualified inline message images. The adapter explicitly enables native ToolSearch and sets
per-function MCP `anthropic/alwaysLoad` from the requested deferral flag. Ordinary
functions stay eager. Existing function callbacks, application receipts, cancellation
and cold continuation remain the only execution/result lifecycle. Runtime discovery
advertises this operation only with an installed bridge supporting `tool_search`.

Known conflicting native provider modes and search/beta settings reject in the
adapter. The maintained native harness owns dynamic model/provider eligibility;
its SDK exposes no reliable pre-input receipt proving effective deferral after a
policy change. Do not represent tool inventory or an operator allowlist as that
proof. Record exact real model/provider evidence and this detection gap separately.
Search-only, missing-search, duplicate-search, workspace, MCP and Subagent combinations
remain unqualified. See [the operation coverage](contracts/agents-api/tool-search.md).

### Structured output execution

The public `text.format={type:"json_schema",schema:{...}}` is resolved with saved
Agent overrides and frozen in the existing Session configuration. Core transports
it in `ExecutionControls.OutputFormat`; it does not append prompt instructions,
validate/retry model answers, repair JSON or select native tool names. Public
admission requires the selected profile's structured-output qualification, and
only requests using this option require the Runtime's `structured_output` and
message-observation capabilities. A capability advertisement does not qualify a
new public combination. Claude advertises this operation only when the installed
SDK bridge reports its `structured_output` feature. A workspace Runtime also
requires the complete local Runtime contract and `workspace_structured_output`;
preparation checks that bundle before native launch. These remain adapter readiness
features, not new Core lifecycle or public protocol variants.

The current qualified path is Claude SDK, `environment:none` or Core-managed
Docker `openai_hosted`, medium verbosity, single Agent, with optional ordinary
function tools and text results. The workspace uses its existing preparation and
native sandbox with only the SDK's configured `StructuredOutput` tool added to
inventory and permission checks. Frozen schemas reach preparation before the
input handoff; Start cannot replace them. Skills, Plugins, capability directories,
HTTP MCP, Subagent/tool-discovery combinations and non-object root schemas remain
unqualified. Check resolved template contents as well as inline configuration;
ordinary text requests retain their existing qualifications.
The SDK uses binary64 JSON numbers: reject execution schemas whose numeric values
would change during that conversion, without narrowing saved Agent storage.
Codex and MiniMax structured output remain explicit execution gaps.

The Claude adapter passes `outputFormat` to the maintained native SDK and allows
its native `StructuredOutput` terminal tool. A matching live root tool result and
an attributed successful SDK result confirm the final output. Publish the native
`result.result` string unchanged as a completed `final_answer` Message using the
native tool-use ID; the parent assistant ID can already own a prose Item. Do not
publish unvalidated retry candidates or serialize `structured_output` back to
JSON. Native retries remain harness-owned. Existing input receipts, usage,
cancellation, release and recovery rules apply unchanged. The implementation and
qualification limits are recorded in [the coverage note](contracts/agents-api/structured-output.md).

### Claude SDK adapter foundation

`packages/claude-sdk-adapter` privately owns the pinned official TypeScript SDK
and native message translation. The Go `claudesdk.NewFactory` uses the shared
owned process runner and emits the existing daemon delta/error/Done frames.
The SDK owns the model loop. Its narrow stdio protocol carries a start request,
text deltas, function calls/results/receipts, active text input/receipts, usage
snapshots and one terminal result/error; native translation stays inside the adapter.
With `observe_messages`, it also emits the existing neutral `output_message`
start/completion snapshots and tags deltas with the native Messages API message
ID, not the SDK event UUID. Text blocks in one native message share that identity.
The SDK's per-block assistant snapshots replace draft block text; only native
`message_stop` completes the message, without replaying its text as another delta.
Thinking/tool-only messages produce no text Items; interrupted messages retain
their streamed partial text. No phase is inferred from the final result.
SDK/native child release and output draining precede daemon completion.

`claudesdk.Config.Workspace` is a private, trusted operator binding for one
qualified placement. It enables native Bash/Read/Edit and declared host functions
in the existing SDK loop. The entire factory must already run inside an outer mount/process boundary
that excludes application, daemon and other-tenant credentials and host policy.
The factory does not create that boundary. The dedicated Docker profile below
selects this binding at startup; cwd and request options cannot select its policy.
The workspace, managed history, runtime home, scratch and protected secret roots
must be pre-existing canonical, separate directories. Runtime code and dependency
search paths must remain outside those roots and be read-only in the placement.
The complete packaged runtime directory, including `node_modules`, must not
overlap any bound root. Its bridge uses the packaged `dist/main.js` layout.
Node, the bridge entrypoint and its readiness companion must use canonical file
paths. Dependency aliases outside mutable roots are resolved before use in PATH;
aliases within mutable roots are rejected even if their current target is safe.
The operator owns allocation, exclusive use and retention; a binding is not
per-Session authorization, a tenant boundary or an idle Files owner.

For this profile, `Config.Env` replaces inheritance for both readiness and
execution, selecting only supported provider/proxy variables. The adapter fixes
HOME/history/scratch, native enforcement flags and tool inventory; the bridge
request contains variable names, never credential values. Native Bash uses the
strict sandbox with no fallback or weaker isolation. Separate native file-tool
permissions and a session tool hook restrict Read/Edit to the bound workspace
and deny protected roots; background/unsandboxed Bash requests are rejected.
The deployment must retain these controls, including the SDK-owned hook.
External MCP and remote-environment combinations remain rejected in this private
profile until separately qualified. The existing `none` profile retains its
behavior. Packaged `workspace_tools` establishes bridge support only, not host
isolation or a public capability. The dedicated Runtime integration composes
public preparation, shared placement quotas, command Items and Files ownership.
`TestLiveClaudeWorkspaceFactory` is explicit real-provider acceptance inside a
qualified placement, including effects, cancellation and same-history continuation.

The private workspace bridge also accepts `prepare` without a prompt. It freezes
validated configuration and resume identity, checks required history, and retains
one native process through the pinned SDK's `startup`/`query` API. Preparation
requires initialization and acknowledgement of the required hooks while the input
iterator remains empty. Its `prepared` receipt permits one later `start` containing
only the initial prompt; configuration replacement, premature or duplicate start
is rejected. Native Session identity and actual tool inventory are still checked
at execution initialization before `input_ready`. Existing direct execution uses
the same observation and completion path.

Unused EOF, owner signals, invalid control input and native exit release owned
resources before a terminal event. The private `workspace_prepare` runtime feature
identifies this bridge contract only. It does not register daemon preparation,
enable public admission, or project public Environment readiness. Preparation may
write native runtime metadata outside the workspace and perform startup traffic;
it does not prove provider authentication, complete sandbox health or tenant
placement authorization.

`claudesdk.NewPreparationFactory` privately binds that workspace bridge to
`agent.Prepared`; the existing workspace factory uses the same Prepare/Start path.
Preparation receives configuration and required resume identity without a RunID or
prompt, checks the installed `workspace_prepare` feature, and owns the process until
one successful Start transfers it. The selected configuration and environment are
fixed before returning; a later Start supplies only its actual RunID, prompt and
output channel. Early preparation failure returns without an executing Session or
fabricated completion. The non-workspace direct factory keeps its existing path.

The preparation owner context spans the eventual Session. Start's context bounds
that operation only, and Close is inert after successful transfer. Abandoned or
failed preparation, owner cancellation and native exit release owned work. The
same output consumer and cancellation settlement follow the resource across Start;
an unstarted preparation has no measured Usage or observed native Session identity.
A cancellation deadline cannot establish cleanup completion while cleanup remains
pending. This adapter ownership seam does not register a daemon capability or
supply per-Session placement authorization, public admission or an idle Files owner.

The optional private `agent.WorkspaceReader` on this preparation and transferred
Session requires the packaged `workspace_read` feature. It sends bounded relative
paths to that same SDK Query's native `readFile` control. Only the adapter combines
the path with the frozen workspace root; callers cannot replace the placement.
The pinned native read handler awaits file-handle close before its successful
base64 response. The adapter validates bytes and truncation, bounds each result
to 1 MiB and each request to 8 KiB, and admits one read at a time. Its continuous
bridge output consumer retains read receipts during preparation and across Start.
Caller cancellation detaches observation without cancelling the Run or discarding
an admitted waiter; its original deadline still applies. Owner closure stops
admission. Native null, malformed receipts, timeout and interrupted delivery remain
uncertain and stop the owner; local reap is not a successful read settlement.
The SDK's nullable result catches all native/control errors, so it cannot distinguish
missing files from denial or transport failure. This does not provide a public
Files endpoint, snapshot consistency, placement registration or idle owner policy.
The qualified live workspace fixture also checks binary, empty and bounded reads
before input and during real execution, plus effects before cancellation and reads
on fresh-process history continuation.

The optional private `agent.WorkspaceDirectoryLister` requires the Linux-only
`workspace_directory` bridge feature on the same preparation or transferred Session.
The pinned SDK has no directory-enumeration control; its fuzzy file suggestions are
not an inventory. This narrow adapter operation therefore reads metadata inside the
already qualified co-located mount/process boundary. It pins the configured workspace
root and opens each relative directory component with `O_DIRECTORY | O_NOFOLLOW`,
using `/proc/self/fd` paths anchored to held descriptors. It rejects directory symlink
traversal; `lstat` reports a symlink entry itself without following its target.
The fixed workspace policy requires canonical, disjoint protected roots and excludes
dynamic permission replacement and remote-workspace fallback. It does not implement
an additional permission engine or authorize an unqualified placement.

Directory requests are bounded to 8 KiB and 1,000 immediate entries, with explicit
truncation, literal names, kinds, and sizes only for regular files. They do not promise
ordering, snapshots, recursion, or public pagination. Missing and permission errors
are returned only from distinguishable filesystem outcomes; unknown results stop the
owner. Each operation closes its directory and intermediate descriptors before a
successful receipt; the root descriptor remains owned until bridge release.
Caller cancellation, Start transfer and owner shutdown retain the existing workspace
read settlement rules. This adapter gap fill alone does not enable public Claude Files; the dedicated
Runtime integration supplies public placement and ownership.

With `ObserveToolObservations`, private workspace execution requires the packaged
`workspace_command_observations` feature and emits the existing neutral command
snapshots. Match root, current-query native Bash call/result identities after input;
ignore historical replay, synthetic and child work. Preserve exact command text and
the native per-call textual result, including native rendering or truncation. This
is final native output, not incremental stdout/stderr or reconstructed interleaving.
Native error results are failed; unambiguous structured interruption is incomplete.
Missing results close as incomplete after the observation drain; query cancellation
does not overwrite an already observed native failure. Do not infer an
exit code from rendered text or supply cwd/duration without qualified native fields.
Preparation alone emits no command. Cold continuation must not reissue historical
observations. This private translation does not enable public workspace admission,
Read/Edit Items or Files ownership.

The private adapter also accepts typed anonymous HTTP and static-bearer HTTPS MCP
declarations on the trusted `environment:none` harness host. The packaged readiness
report must include `mcp_http_tools`; discovery advertises that feature only when
present, and execution
rechecks the installed bundle before dispatching an MCP request. An unchanged SDK
version alone cannot qualify an older bridge. Authenticated private requests also
require the packaged `mcp_http_bearer_auth` feature at discovery and dispatch.
The daemon generates a separate environment reference for each server and launch;
only those references enter the bridge request and native SDK configuration.
The native HTTP client expands them from its owned process environment. Literal
bearers must never enter SDK MCP headers because that configuration enters argv.
Readiness probes receive no per-request bearer environment. Token validation is
shared with the Codex adapter; credential storage remains an opaque-string contract.
Public Claude MCP admission reuses the shared resolver, immutable Session snapshots
and neutral Item/event projection.
The API checks the supported profile before persistence, during device selection
and again before claiming execution; a missing runtime capability leaves work queued.

MCP queries use the SDK's main-thread Agent definition to restrict model-visible
tools, in addition to empty built-ins, strict MCP configuration, empty setting
sources and default-deny permissions. Permission allowlists alone do not restrict
the native model inventory. Null selects all tools from a declared server; an empty
list selects none. Host functions compose with those selections. Native server
status supplies original tool identities; map their normalized native aliases while
preserving the original names in observations. Native status deduplicates aliases,
so it does not prove a complete original server inventory. A native PreToolUse hook
waits for inventory verification before admitting root calls and denies unverified,
mismatched or cancelled calls. The native Agent restriction controls model-visible
tools; inventory verification is not a barrier before the model request.
Anonymous HTTP declarations explicitly set an empty Authorization header to disable
native OAuth and automatic credential injection. Preserve that header; do not erase
native history or credentials to enforce this boundary. Servers that reject a blank
Authorization header, normalized name collisions and inventory changes during a
query require separate validation; this profile covers static inventories.
Private SDK status/control objects can contain expanded authentication headers.
Read only connection and tool identity fields; never retain, log or publish raw
status/configuration or control responses. Diagnostic projections must whitelist
safe fields. This does not permit filtering actual model/tool output to hide a leak.
The bounded adapter profile currently requires connected servers, reserves the
`functions` label, accepts alphanumeric/underscore/hyphen server labels and
alphanumeric/underscore/hyphen/dot selected tool names, and excludes remote
environments. Required startup is separately qualified by `mcp_http_required`.
All HTTP MCP queries use native SDK startup and an empty input iterator to
confirm initialization hooks. Required declarations additionally check connected
server status before the initial prompt is released exactly once. Pending, failed,
missing or ambiguous required status rejects before input; native startup timeouts are retained without
an adapter retry loop. Normal system/init still verifies Session identity and the
complete inventory before input readiness/tool authority. Optional servers retain
their existing inventory checks without a new pre-input connection requirement.
A Runtime must advertise the concrete required-initialization capability; there
is no fallback to an older execution path.

Public Claude static-bearer HTTPS MCP reuses the shared
Vault attachment, frozen selection and scoped decryption path. Selection and final
preclaim require the existing bearer capability; shared authentication dispatch
uses capability/placement checks rather than a Codex-name restriction. Missing keys
or failed lookup/decryption never fall back to anonymous execution; an attached
Vault with no matching credential may remain anonymous. These are execution limits,
not saved-Agent schema restrictions or changes to the official protocol.

Root assistant tool calls and live root user results produce the existing neutral
MCP observations. Correlate actual Session/call identities; exclude replay,
synthetic and subagent work and keep host function receipts separate. Preserve
the exact native `tool_use_result` when one result is unambiguous, otherwise the
per-call result content. Native errors remain observed native errors. The SDK can
replace annotated MCP content with rendered structuredContent and flatten MCP
errors; these observations do not claim original MCP envelope fidelity or hosted
output parity. Do not reconstruct lost fields or infer output from model prose.
Unfinished observed calls become incomplete on shutdown, without claiming that
remote tool effects were cancelled. Rich content, native truncation and asynchronous
MCP task results remain unverified.

Daemon `connect` optionally registers this factory as `claude_sdk` when the
operator sets `PARSAR_CLAUDE_SDK_ENTRYPOINT` to the absolute packaged `dist/main.js`.
`PARSAR_CLAUDE_SDK_NODE` selects Node (default: `node` on PATH). Discovery resolves
Node once and checks that exact configuration before pairing; the SDK's bounded
runtime check is independent of legacy CLI version probes. A ready SDK alone is
sufficient to start the daemon. No configuration means no SDK probe or descriptor;
failed readiness reports an unavailable descriptor with a rejecting factory.
Runtime checks establish local readiness, not provider authentication.

SDK state lives under `paths.ProfileDir(profile)/runtime/claude-sdk`, independently
of the replaceable runtime bundle. Both the entrypoint and managed state root must
be absolute. Background re-execution inherits operator configuration; it does not
persist provider credentials in pairing profiles. Product `claude_code` remains
unchanged. Product registration explicitly opts existing engines into
`WorkspaceAuthoring`; the authoring registry wraps only that opt-in. SDK registration
bypasses product capability-download, skill-upload and workspace-authoring wrappers.
It does not accept caller-supplied environment variables or business write authority.

The SDK descriptor advertises the validated daemon subset, including durable
Turns/input receipts, text observations, function tools, raw usage and restrictive
execution controls. It does not advertise permissions, product authoring, legacy
raw tool Items, general web-search control or text-verbosity levels. Router admission
for `environment:none` uses the available engine capability, not an engine name.
The independent API selects new Session engines through `AGENTS_API_ENGINE`
(`codex` by default, `claude_sdk` or `mcode`); existing Sessions keep their stored engine.
This remains the deployment default; the optional Core harness extension selects
an enabled engine for one saved or inline Agent configuration. API admission,
device selection and the final preclaim check share the execution service's narrow
engine policy without importing native adapters. Selected engines require the common
durable execution capabilities. Codex retains its general search/verbosity checks;
Claude uses its restrictive profile without claiming those general capabilities.
Idle and initial-input Session creation qualify the resolved configuration before
persistence; saved Agent resources remain independent of engine restrictions.
Claude additionally requires medium verbosity and explicit object-root function
schemas. Function-result batches normalize through the existing shared parser. Claude accepts
text results and, on `none` and Core-managed Docker `openai_hosted`, successful
ordered inline PNG/JPEG results. Unqualified placements, failed image results and
invalid/remote references reject before any batch write, preserving pending calls
and retry identity. Public qualification receives the full neutral result so
success-dependent limitations remain in the profile. Image-bearing delivery alone
requires Runtime function-result image support; text results and function
declarations do not acquire that requirement. These are implementation limits, not changes to the upstream contract.
Do not bypass them by dropping fields, changing model identity or fabricating usage.
Operators may configure the daemon provider environment or the existing transient
`AGENTS_API_EXECUTION_OPTIONS_FILE` with adapter-owned `claude_provider`
(`base_url` HTTPS and `bearer_token`). Core forwards these opaque options without
persisting them in Session configuration. The adapter exclusively selects the
provider environment and removes credentials from native tool environments. Product `claude_code` and product execution are unchanged.
The `none` public profile accepts only
text, explicit model/system instructions, managed state, exact native resume and
declared functions with ordered text or successful inline PNG/JPEG results, and the HTTP MCP subset
described above. It rejects unsupported request
options and disables built-in tools and undeclared MCP discovery.
`DisableExecutionEnvironment` and `DisableSubagents` are accepted assertions about
the single-Agent restrictive profile. Omission does not enable built-in tools.
Explicit Subagent observation enables only its qualified native delegation tools,
with admission before start and verified child identity before workspace authority.
Public function/MCP combinations remain unqualified with Subagents. Single-Agent
new and resumed queries use the SDK's empty built-in tool set, explicit function MCP
configuration and allowlist, strict MCP configuration and empty user/project/local
setting sources. Without HTTP MCP declarations, native initialization and real
provider request inventories must contain only the declared host functions. Managed operator policy may further
restrict execution; it must not widen the profile. This limits model tool access,
not native state files or filesystem access by an explicitly supplied host function;
it is not sandbox/file isolation. The private factory accepts typed execution
controls only for disabled search and medium text verbosity. Search remains excluded
by the native tool inventory; medium retains the SDK's default text generation,
without adding instructions or changing caller input. The pinned SDK has no native
verbosity-level option: low/high and enabled search remain explicit implementation
gaps. Missing/invalid fields in a supplied control block fail before native setup;
omitting the block keeps the same restrictive profile. Public engine admission is
qualified separately by the API policy described above.
Use the SDK's history lookup before explicit resume; never fall back to a new
Session. Native files remain device-affine under a caller-selected managed
runtime directory. The launch configuration supplies trusted provider environment;
request options cannot supply environment variables or business write authority.
Omitted, null and empty `system_prompt` map to empty SDK instructions only at this
adapter boundary; null model values and unsupported options remain rejected.

The internal SDK function-server helper uses the maintained MCP server's public
request handlers and standard Tool/CallToolResult types. It snapshots definitions
and forwards JSON Schema without a JSON Schema-to-Zod conversion; supplied tools
are always loaded. Native call identity comes from the pinned harness's
`claudecode/toolUseId` MCP metadata, independently of request IDs, names or arrival
order. Missing identities and undeclared tools fail before invoking the host.
Return content/error fields unchanged over MCP and forward its per-request abort
signal. The private Go factory connects declared functions through this helper and
reuses the daemon function-call/result interface and opt-in neutral observations.
The native function-server registry must contain exactly those functions. SDK allowlisting admits
only these host callbacks; the host still owns result decisions and any business
permission checks. It grants no runtime-token business authority.

Function results remain pending after stdin/MCP delivery. A matching live, root
native user tool_result confirms application only when its Session/call identity,
error flag and ordered content match the submission. Text matches exactly; each
submitted image position must remain a valid native base64 image. Native resizing
or re-encoding may change image bytes. This acknowledges incorporation into native
history, not byte/pixel fidelity or completed provider consumption. Public Items
retain the original caller content; real image-dependent model responses separately
qualify usability. Ignore replayed, synthetic and
subagent messages. Native error text joins the submitted text parts with newlines;
neutral observations retain their original order and separate failure status.
Missing/mismatched receipts fail the execution; do not replay unknown delivery.
Result submission waits at most ten seconds for a receipt and cancels uncertain
execution on timeout. Invalid or unsupported image results fail before consuming
a pending call. Function state belongs to one live Run and ends with it; the
existing router owns receipt retry/conflict handling. This does not establish
crash recovery or exactly-once effects. Public schemas outside MCP's object-root
contract, failed image results and remote image references remain admission/execution gaps.

Each SDK result supplies one native usage snapshot, including reported failures.
`Usage.Raw.claude_sdk_result` holds the latest; queries with multiple native results
also retain all snapshots in order under `claude_sdk_results`. Main-loop `usage`
is per native turn, while query-pipeline `modelUsage` and estimated `total_cost_usd`
are cumulative within the query. Retain subtype/error provenance and earlier
snapshots even when a later failure reports zero counters. Reuse the latest full
snapshot set in Usage and Done; never sum cumulative measurements. Each factory
invocation owns one SDK query, including cold resume, so no prior query counters
are carried forward. Missing native results do not imply zero consumption. SDK estimates stay
in raw evidence, outside the billed cost field; do not select an arbitrary model
or invent missing public token breakdowns. The API does not parse native counters.
Precise public usage projection, unreported costs and crash/partial accounting
remain gaps; the native snapshot alone is not complete protocol Usage compatibility.

Active text uses the SDK's `AsyncIterable<SDKUserMessage>` input, with a fresh
native UUID mapped to each daemon input ID. A native query may fold text into its
current native turn or queue another; one daemon Run can therefore contain several
native turns. Never promise Codex's same-native-turn semantics. Writes, queued
notifications and user-message echoes do not confirm consumption. Only matching
root assistant/partial/result `user_message_uuids` (or the singular fallback)
confirm applied input. Typed mid-turn folds may appear only on the native result.
Preserve that receipt even when the result reports failure. Check pending functions
after the query drains: the SDK may dispatch later-turn callbacks before the
earlier result handler finishes.
Keep the iterator open until every submitted input has a consuming result, even
when an earlier result reports an empty native queue. Close admission before
releasing final receipt waiters; drain and release the SDK/native processes before
one daemon Done. Cancellation resolves unconfirmed pending receipts as unknown and ends the
owned execution. Successful private SDK Cancel waits for owned-process exit and
stdout/stderr drain, then exposes the same settled CancellationOutcome as Done.
It retains native identity verified at input readiness, partial text and observed
Usage even on cancellation/failure; requested resume identity alone is not evidence.
A caller deadline before settlement reports failure/unknown, while cleanup continues.
Settlement precedes terminal publication so router completion cleanup cannot wait
on its own Done consumer. Cancellation releases intermediate event backpressure;
the settled outcome remains readable even when connection loss prevents publication.
A receipt timeout after a full write preserves the process and pending identity
without redelivery; a blocked write is cancelled and released.
The private adapter permits one input awaiting consumption and at most 63 extra
inputs per Run, preserving the native 64-UUID receipt bound. Durable receipt opt-in
separates bounded writes from native consumption waits; calls without it retain
the router's ten-second deadline. Larger input capacity and interrupted-input
recovery remain separate work. Daemon registration alone does not establish public acceptance.

Public text/function execution, active input, pending-call cancellation and cold
continuation are accepted for the registered restrictive profile. Environment
provisioning, broader tools/verbosity, complete public Usage, failed image results and
process-loss recovery remain gaps. Managed installation and release publication
remain separate tasks. `make check-cli` also builds
and tests the SDK package, including native output draining; CI selects that check
for changes to the package. Live adapter
acceptance is opt-in and must use a real provider with private credentials.

### Private Claude SDK runtime artifact

`make build-claude-sdk-runtime` exports the compiled bridge and pinned production
SDK/MCP dependencies, including the native package for the build host, into a
platform/architecture/libc-specific `.tar.gz` and SHA256 file under
`${PARSAR_HOME:-$HOME/.parsar}/build/claude-sdk-runtime`. `CLAUDE_SDK_BUILD_DIR`
may select another absolute output directory. The production dependency closure
requires Node20 or newer; Node22 is the tested version. Node is operator-supplied
and is not bundled; the bundle is independent of product sources, services and databases.
It does not add Node or SDK assets to the Agents API binaries/image.

The build validates source manifests with the repository-pinned pnpm frozen
install and compiles into fresh managed staging, never exporting incremental
checkout output. It then uses modern `pnpm deploy` with command-scoped workspace injection
and its dedicated frozen lock. The adapter has no workspace dependencies; keep
that boundary explicit. Do not enable injection globally or replace this with a
custom dependency copier. Export only compiled `dist` and production dependencies;
retain their package metadata, lockfile and licenses. Check dependency links stay
inside the export, pinned SDK/MCP/native versions, native `--version`, and bridge
startup before publishing the archive. Startup with stdin EOF is an import check,
not model execution acceptance. `make check-cli` includes this artifact check.

Extract the archive into a fresh managed runtime directory on a matching host
and use its absolute `dist/main.js` as the private factory entrypoint. Validate
relocation and real provider cancellation/continuation before accepting an
artifact. Linux x64/glibc with Node22 is the currently exercised platform;
other hosts require their own native acceptance. Do not reuse a bundle across
platforms or libc variants. Automatic Node installation, managed activation and
release publication remain separate work. Operator-configured daemon discovery/registration is supported as
specified above.

The exported `dist/runtime_check.js` companion is the local readiness contract.
It checks Node20+, installed SDK/MCP/native versions against the package manifest,
contained dependency resolution, native startup, and the exact `dist/main.js` bridge
with stdin EOF. It emits one versioned JSON report without calling a model or
creating Session state. The artifact check reuses this companion and separately
checks all exported links, the lockfile and source pins. `claudesdk.CheckRuntime`
uses the same operator-supplied Node, entrypoint and environment as execution,
with shared process-group ownership, bounded output and a 15-second deadline
plus bounded cleanup. Both native and bridge probes have five-second limits.
Return unavailable on failed or malformed probes; never forward native diagnostics
or treat local readiness as provider authentication, public capability acceptance
or filesystem isolation. Automatic installation remains separate.


### Harness selection and Agent defaults

Core accepts the optional `agent.x_agents_core.harness` extension through the
saved Agent and inline Session configuration paths. Define the extension once in
`contracts/agents-api/v1`; never use metadata or a competing top-level selector.
Resolve saved overrides before selecting the existing Session engine, and apply
that engine's execution policy before persistence. Omitted selection preserves
the deployment default; explicit unavailable selection fails without fallback.
Effective extension reads use the persisted engine; Sessions without the extension
retain the official Agent response shape. See the [extension contract](contracts/agents-api/harness-selection.md)
for null/retry behavior and operator configuration.

Hosted engine-to-provider selection belongs to Core composition. Admission and
initial allocation share the mapping; retained allocations use their persisted
provider identity. Runtime images must satisfy their existing qualification rules.
Transient model options are partitioned by engine and must not expose another
engine's credentials. Do not infer an engine from a model name or template.

Parsar Agents save default model, harness extension and a typed, non-confidential
`config.environment` selector. The Agent form requires an explicit harness before
saving; existing records without one remain unset in read-only views. The Agent
list shows the configured environment type and harness in both table and compact
layouts. The environment selector is product configuration;
it becomes the official Session `environment`, never part of the protocol Agent.
Only a template reference or supported environment selector is stored, not live
container identity or initialization secrets. An explicitly chosen conversation
environment retains its existing override behavior. Otherwise the first message
uses the Agent defaults. Creating an Agent or opening an empty chat performs no
Core Session or container creation. One conversation/Agent binding freezes the
request on first execution; later messages and observer retries reuse it. Separate
conversations get independent Sessions and environments. Edits affect future
Sessions only. Keep the existing product navigation and direct empty-chat composer.
