# Environments and Templates

An Environment is the execution resource of a Session: the machine, workspace and prepared capabilities that a Harness runs in. A Session creates its Environment through its `environment` configuration; there is no standalone create call. An Environment Template is reusable preparation configuration that a Session resolves when it is created. This contract covers both resources, the two placements, input admission, capability preparation, Skills, Plugins and MCP connection origins.

Related owners:

- [Environment files](environment-files.md): the Files API on a live workspace.
- [Executor credentials](environment-executor-credentials.md): enrollment, the installation grant and connection status of a `self_hosted` machine.
- [Sandbox deployment](sandbox-deployment.md): which Sandbox Provider (E2B, Docker or microsandbox) hosts `openai_hosted` Environments.
- [Core–Runtime protocol](../../docs/runtime-protocol.md): the `runtime_prepare` transfer and every other wire message.
- [Runtime and outer isolation](../../docs/design-principles.md#runtime-and-outer-isolation): the daemon runs tools with its launching user's permissions; isolation comes from the outer Environment.

## Resources and states

Paths follow the SDK resource methods, before the service's `/v1` prefix. The linked pinned source owns the fields and unions.

| Resource | Operations | Rules |
| --- | --- | --- |
| [Environment](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/environments.py) | `GET /agents/environments/{id}` | Created through Session configuration. Returns `id`, `type`, `status` and the `files`, `plugins` and `skills` recorded at Session creation. |
| [Template](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py) | `POST`, `GET /agents/environments/templates`; `GET`, `POST`, `DELETE /agents/environments/templates/{id}` | See [Templates](#templates). |
| [Files](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py) | `POST`, `GET /agents/environments/{id}/files` | See [Environment files](environment-files.md). |

An Environment read joins its live owning Session within the caller's Project and returns the durable connection status. It needs no live Runtime, starts no native work and changes no connection state. The installation arrays list API-managed files, Plugins and Skills for both placements: files as `{id, type, path, file_id, size_bytes}` without content, Skills as `{type, name, description, skill_id, version}` and Plugins as `{type, name, description}`. Capabilities discovered in local capability directories are not listed.

[Session environment input](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment_param.py) and [output](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/environment.py) have different shapes:

- `none` selects no Environment.
- `self_hosted` input requires `workspace_directory`; the nullable `capability_directories` defaults to an empty list. Output adds the Environment ID and the output-only `remote_url`. The output's `/workspace` default does not make the input field optional.
- `openai_hosted` can reference a Template and supply `capability_directories`, `network`, `packages`, `files`, `plugins`, `skills`, `env` and `setup_commands`.

| Projection | States |
| --- | --- |
| [Environment resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environment_info.py) | `pending`, `connected`, `disconnected`, `expired`, `failed` |
| [Session environment event](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_session_environment_state.py) | `pending`, `ready`, `connected`, `disconnected`, `failed`, with a nullable error |

The two vocabularies are separate; never cast one to the other. Session environment events carry Session and Environment identity and an optional Turn identity, and never configuration, credentials, registration IDs or revisions. A Session's `required_actions` can contain `{type: "environment_connection", environment_id}`, separate from function calls. Environment connection, Session status and Turn status are independent.

Core creates the Environment record in the Session creation transaction; the Session upsert picks the retry winner, and retries never create or repair an Environment. The Environment derives its Project and immutable configuration from the owning Session. Its first state is `pending`. After the Session is deleted, reads hide the Environment while Core keeps the record for settlement and cleanup. A Session with `none` has no Environment.

A managed outer Environment must exclude broader application credentials and other tenants’ secrets.

## Placements

Both placements run the same Runtime: the daemon, the selected Harness, native tools and the workspace run together on one machine. They differ only in who owns that machine.

| Object | Responsibility |
| --- | --- |
| Session and Environment | Durable ownership, configuration, pending interaction and connection observations (Core) |
| Provider allocation | Compute and filesystem lifetime: Core's Sandbox Provider for `openai_hosted`, the application for `self_hosted` |
| Device and daemon connection | Authenticated Runtime identity and the replaceable dispatch transport |
| Harness process and native session | The native model and tool loop, its execution state and native history |
| Enrollment | The exact Environment, device and executor-key binding of a `self_hosted` machine |

### Hosted (`openai_hosted`)

The deployment's configured Sandbox Provider (E2B, Docker or microsandbox, see [sandbox deployment](sandbox-deployment.md)) hosts the Environment. [Harness capabilities](harness-capabilities.md) lists which Harnesses run there.

- Session creation, with or without initial input, commits the Session, Environment and retry identity before the Worker provisions compute. A creation interrupted before bootstrap is recovered without repeating the Provider's Create.
- Provisioning needs no caller action; the Session stays idle until a Turn starts.
- Omitted or null `network` means enabled; `disabled` and `restricted` are rejected before the Session is created ([restricted network](#restricted-network)).
- A Core restart keeps the allocation, workspace and native identity and never replays uncertain work.
- Terminal cleanup revokes authority and settles pending input in one transaction before the Provider reclaims compute. New input on a terminal Environment is rejected.
- Deleting the Session reclaims its Environment; deleting a Template does not.

### Self-hosted (`self_hosted`)

The application owns the machine. It creates the Session with a clean absolute `workspace_directory` and optional absolute local `capability_directories`. Core returns the Environment ID, the `remote_url` and an install command in `x_agents_core.installation`; running that command on the machine installs the daemon and enrolls it ([self-hosted guide](../../docs/getting-started/self-hosted.md), [executor credentials](environment-executor-credentials.md)).

- `remote_url` is the daemon WebSocket URL derived from Core's public URL, never from request headers or a daemon address. It names Core's private daemon transport.
- Enrollment binds the exact Session, Environment, device and executor key. It creates no allocation and cannot move a Session to another device.
- The Session's workspace must equal the `/workspace` alias or the exact canonical directory the Runtime is bound to. Naming a path grants no access to it.
- The Session carries its own model provider; deployment defaults never apply ([model execution](model-execution.md#saved-defaults-and-precedence)).
- Session reads, lists and events return the `self_hosted` output with the Environment ID, workspace and capability directories, never private configuration. `capability_directories` lists the caller's selections; the Runtime's installation locations stay private.
- Compute, workspace and files stay the application's. Deleting the Session or revoking the credential denies further access but does not stop native processes; the machine owner stops and cleans up.
- The workspace and native history must survive a daemon restart. Losing them never authorizes silent replacement or replay.

**Application-managed E2B.** An application can run the Runtime in an E2B sandbox it creates, renews and destroys with the E2B SDK, then enroll that Runtime as a `self_hosted` Environment ([E2B Runtime guide](../../services/core/deploy/e2b/README.md)). Core keeps no E2B allocation for it and never renews or kills it.

### Ownership rules

- Keep Environment identity, ownership, configuration and lifecycle in Core, separate from Provider compute, device identity, daemon sockets and native sessions. Keep mutable connection state out of immutable configuration; a replacement owner fences stale observations.
- Callers, devices and Environment connections use distinct credentials. The daemon gateway authenticates the enrolled executor key and the exact device. Connection observations keep generation and revision fencing. Registration and connection do not establish readiness.
- Rotation, revocation, Session deletion and loss of ownership deny further access; they do not promise that native effects stop at once.
- Native history stays on the bound Runtime. Preserve it, or demonstrably restore it, across compute replacement; never silently move a bound Session or replay unknown work.
- Durable metadata reads need no live Runtime. Live file reads need an authorized view of the exact workspace and bounded operation ownership. Operations that write, replace or retire a workspace owner also apply mutation fencing.
- When a Session has a started Turn but no recorded native session ID, the next Turn requires existing-history recovery through a verified Runtime capability. A supplied native ID stays authoritative. The Codex adapter recovers only a unique, non-archived root in the Session's private native home and expected working directory, through native listing and exact-ID resume. Missing, incomplete or ambiguous history fails without starting a new root, as does a recorded start that may precede native work. Recovery never replays interrupted input. Device identity and Environment scope belong to `ExecutionDevice`; native session identity and prior Turn state belong to `SessionExecutionBinding`.
- Runtime discovery gives each installed Harness a 15-second version probe; a missing binary fails at once. A version result is availability, not Environment readiness.

## Input admission

Session input goes through `POST /agents/sessions/{id}/events` in ordered batches of 1–64 events. On an Environment-bearing Session, Core reserves input that cannot start yet until the Environment is connected and prepared.

### Initial input

Creation accepts initial text as a string or an ordered array of user messages. Omitted or null input creates no Turn and no connection action.

- The creation transaction stores the initial batch as a reservation; for `self_hosted` it also records a connection action. Only the creation winner inserts them; retries never insert them again.
- A `self_hosted` creation returns the Session and the Environment connection target at once, while the machine is still offline, without waiting for admission. A streamed `self_hosted` creation sends the committed JSON projection as its `created` snapshot, already showing `requires_action` and the connection action, then the committed `requires_action` event.
- A fresh creation stream ends right after the idle state recorded when the admitted Turn ends or the reservation stops being pending, or after a failure. The connection alone clearing the action does not end it. A creation without input ends right after its created snapshot; a same-key stream retry ends at once without events. Later subscribers see only future events and recover history through queries.
- Closing the stream leaves committed work intact; the Worker prepares and admits the input.
- If the initial reservation expires, the Session becomes `failed` with a safe error and no actions, and no Turn is created. The Environment itself is not failed.
- Creation retries keep the original identity, deadline and input. Saved-Agent retries with recorded intent recover before execution admission or source resolution.

### Later input

| Batch | Behavior |
| --- | --- |
| Messages, Turn active | Appended to the current Turn through ordered admission and native delivery. No new Turn, preparation or reservation. |
| Messages, Session idle | Reserved; the request waits for the Worker to prepare, admit and claim. |
| Cancellation only | Admitted through the ordinary durable cancellation path. An idle cancellation creates no Turn. A new cancellation conflicts while a reservation is pending; it never cancels pre-Turn input. |
| Function results only | Admitted to the named pending calls with the ordinary result receipts, without a Turn or preparation. Cannot bypass a pending reservation. Function results never become Environment installation metadata. |
| Mixed kinds | Rejected. |

Core returns 202 only after the batch is durably admitted. For a cancellation, 202 confirms admission, not native completion or process exit. Under the Session lock, a retry recovers its original reservation or receipt before Core chooses between the active Turn and a new reservation, and keeps that target during later work. An unlocked read or a retry after a conflict never picks a different path.

The waiting request uses bounded pooled operations, outside transactions and execution leases; it never prepares or starts native work. Only this route extends its response write deadline, to six minutes, covering the five-minute database deadline plus response time. Disconnecting stops the wait, not the reservation. Errors:

| Condition | Response |
| --- | --- |
| Reservation expired | 409 `environment_input_expired` |
| Reservation cancelled | 409 `environment_input_cancelled` |
| New input on a failed `openai_hosted` Environment | 409 `conflict_error`, "the hosted environment failed to provision" |
| New input on a failed `self_hosted` or an expired Environment, or input that was waiting when the Environment failed | 409 `environment_unavailable` |
| Execution ownership lost | 503 `execution_unavailable` |
| Session deleted | 404 |

### Reservations

A reservation stores one canonical message batch before Turn admission, with a five-minute deadline from the database clock.

- It requires an Environment-bearing Session without active work. Reservations and direct input share the Session lock and retry identity; a pending or settled key cannot bypass its reservation through direct admission.
- A pending reservation blocks new direct batches, including cancellation, while successful earlier retries stay readable.
- Promotion commits the original inputs, history, reservation settlement and the Turn claim (`queued` to `in_progress`) together. It requires the current leased execution writer and the retained native preparation; no database lock is held during preparation. Only the first successful, non-replay receipts authorize Start on that preparation. An admitted retry returns the original receipts without reclaiming execution; a read or an uncertain commit never authorizes another Start.
- A crash after promotion but before Start goes through claimed-Turn reconciliation (`execution_interrupted`), including for unbound or deleted Sessions.
- Session deletion is rejected while input is pending and changes nothing. Deletion after the claim is rejected like any active Turn.
- Deadlines are checked after taking the Session lock. Expiration and targeted cancellation keep their terminal identity, and a retry of a terminal reservation cannot affect a later reservation or Turn.
- Initial reservations fail the Session when they expire; later ones return the Session to idle. Failure events capture the settled activity and Usage atomically. A late connection or a creation retry cannot reset or replay expired input.

A Runtime `failed` result with `preparation_failed` and no Run settles the pending input at once with `runtime_preparation_failed`. So does a rejection of the current `execution_prepare` with no Run and code `invalid_configuration`, `unsupported_configuration` or `unsupported_preparation`. Core records a safe Session failure before any Turn exists and releases the input gate; after the local cause is fixed, new input is accepted. Transport loss, capacity rejection and unconfirmed cleanup stay retryable within the original deadline. Core uses these common control states, never Harness-specific error text.

### Activity and required actions

Before a Turn exists, Session activity comes from the latest relevant reservation and the connection state. Pending input on an offline `self_hosted` Environment requests `environment_connection`; the connection arriving clears it to `idle`, while the Worker still owns native readiness and admission. An offline Environment without pending input requests nothing, and `openai_hosted` provisioning never requests a connection. A reservation or connection change commits immutable Session activity and usage snapshots in the same transaction; a newer or active Turn owns later activity. Settled non-initial reservations return the action to `idle` until newer work exists.

### Expiry and scheduling

The Worker expires at most 32 due reservations per tick, after checking its lease and before checking devices or execution slots. The sweep uses the leased Store connection with its transaction timeout, never a pooled writer. A partial deadline index and `SKIP LOCKED` Session locks let unrelated work proceed. The cutoff is statement time, and settlement rechecks the database clock after taking the Session lock. A restart resumes expiry on normal ticks; there is no separate scheduler. A failed Turn never stands in for a pre-Turn connection failure.

When next-Turn input is pending on a completed managed allocation that is suspended or recovering, Core hints the managed Runtime maintenance loop. Initial input, cold creation, running or disabled compute, terminal receipts, cancellation and function results, history and file operations send no hint. Hints are best effort and coalesced without blocking; they add at most one scan per normal five-second cycle and never bypass capacity, a busy lifecycle gate or ownership checks. Persisted work and the normal ticker stay authoritative.

## Runtime capability preparation

Preparation installs what a Session selected: initial files, tool configuration, Skills, Plugins, Environment MCP, npm and Python packages and setup commands. Both placements use one Runtime path.

### Ownership and lifetimes

Resource management keeps Provider placement, capacity, allocation and Environment create, renew and reclaim. The authenticated Runtime connection carries initialization, capability preparation and executor operations; it never allocates or destroys compute. Providers never run Core initialization commands.

Closing an Executor, cancelling a Turn or losing the transport keeps the installed snapshot, workspace and allocation. Reclamation is an explicit operation coordinated with active work; a disconnected socket does not prove that native effects stopped. [Harness onboarding](harness-onboarding.md#executor-and-turn-lifetimes) owns the Executor and Turn lifetimes.

### Preparation order

Core freezes resource versions, metadata and source selections at Session creation. Initialization then runs in this order, each step over `runtime_prepare` with the same daemon:

1. initial files and tool configuration;
2. Skill and Plugin bundle import;
3. npm and Python packages, then setup commands in order;
4. snapshot of the capability directories.

The runner uses only neutral Environment and Session identity and a Runtime peer, with no Provider, deployment or operating-system branch. Harness differences stay in the adapters.

**Portable preparation input.** `self_hosted` input carries only `workspace_directory` and `capability_directories`. A Session with either workspace placement can also send `x_agents_core.environment`, a Core extension with `environment_template_id`, `files`, `env`, `packages`, `setup_commands`, `skills`, `plugins` and `capability_directories`. It uses the same parsers and inheritance rules as `openai_hosted` input. A field supplied both there and in `environment` is rejected, including an explicit null. A `self_hosted` Session names its Template only through this extension and cannot use a Template whose network is not `enabled` (400, param `x_agents_core.environment.environment_template_id`). Machine location, sizing and network policy are not extension fields.

```json
{
  "agent_id": "agent_example",
  "environment": {"type": "self_hosted", "workspace_directory": "/home/user/project"},
  "x_agents_core": {"environment": {"environment_template_id": "env_template_example"}}
}
```

Changing `environment` to `{"type":"openai_hosted"}` reuses the same preparation input. Resource resolution, Project authorization, concrete Skill versions, encrypted file contents and confidential tool variables freeze at Session creation. Retries and reconnects reuse those snapshots; new Sessions resolve new versions. A `self_hosted` Environment never needs an allocation record.

**Readiness.** Transport `connected` is a connection observation, not readiness. Execution and live file access wait for initialization; then the native preparation owner validates the installed snapshot and Harness before admitting a Turn. File reads keep their own readiness and authorization and do not require capability or native readiness. Deployment model credentials are never sent to application-owned machines.

**Transfer.** Initial files, configure, npm, Python and setup operations, inert Skill and Plugin archives and finalization selections travel as typed `runtime_prepare` operations with canonical Session and Environment identities; the [protocol](../../docs/runtime-protocol.md#preparation-and-execution-order) owns chunking and receipts. Files and setup working directories use logical `/workspace` addresses; the Runtime chooses executables and physical destinations, and Core supplies no executable or host-platform field. Source selections accept portable absolute Unix, Windows drive and UNC paths; Core never resolves them on its own host, and the daemon applies its local path and access checks.

### Installed snapshot

Both origins use the common Runtime parser and an `installed.json` manifest on Linux, macOS and Windows. The Runtime operator chooses the capability root ([installer options](../../docs/getting-started/self-hosted.md#options-for-automation)); Core and transfer requests cannot.

- The manifest binds the Session and Environment to the ordered source-selection digest. The preparation owner verifies or creates it before native execution, also for an empty selection.
- After the setup commands, the initializer snapshots the declared workspace-contained capability directories into Runtime storage. Directory bytes are read after setup, not at Session creation.
- A filesystem lock prevents concurrent installation. A private completion record keeps only the operator's installation root, so a deleted snapshot is never mistaken for a first preparation or captured again.
- Missing, partial, conflicting or foreign snapshots fail without deleting data, repairing or replaying.
- Reconnecting and replacement Executors load the installed contents without rereading sources. Source edits reach only a new Session.
- Recursive references to the snapshot and directory entries that escape it are rejected.
- Read-only snapshot modes are integrity hints, not protection from the launching user.

Executor admission validates only the frozen descriptor. The preparation owner makes the capabilities ready before calling the native factory; adapters receive only the resolved Runtime-owned Skill paths and MCP declarations. A reused Executor keeps its original configuration.

### System dependencies and Runtime directories

The daemon runs as its launching account and never uses sudo or raises its permissions. Only user-directory dependencies install during preparation.

- System dependencies must be preinstalled in the managed image or by the owner of a self-hosted machine. A missing executable or library fails the operation that needs it.
- `packages.system` is rejected in Templates and inline configuration, including a null or empty list (400, param `packages.system`). Package responses still carry the official required `system: []`.
- npm installs into a local prefix and Python/pip into a local target under the Runtime package directory; Node/npm and Python/pip must already be installed. Their dependencies are visible to native tools in every working directory.
- Setup commands run with Bash; on Windows, Git Bash is required and no other shell substitutes. The default working directory is `/workspace`.
- On Windows, npm installation and stdio MCP commands named `npm` or `npx` (including their `.cmd` shims) run through npm's JavaScript entry point with Node, without an extra shell.

Initialization and package directories default to `initialization` and `packages` under the Runtime home (`OAC_RUNTIME_HOME`) and can be set with `OAC_RUNTIME_INITIALIZATION_DIRECTORY` and `OAC_RUNTIME_PACKAGE_DIRECTORY`; packaged Linux images use `/environment/initialization` and `/environment/packages`. These are resource paths, never Environment-source or operating-system switches in Core.

Every command uses the launching user's permissions and the host network. Process ownership waits for exit and I/O settlement. Command output is discarded; a confirmed failure keeps only a bounded integer exit status.

### Explicit local tool environment

The installer's `--tool-env-file` (`OAC_RUNTIME_TOOL_ENV_FILE`) supplies the Runtime operator's base tool variables. Preparation copies these values into its private initialization snapshot, and the Session's `env` keys override them. The Runtime never rewrites the source file or inherits unrelated ambient credentials. Setup, capability resolution and Harness execution read the same prepared snapshot. Reconnecting keeps that snapshot even if the operator edits the file; a new Session reads the current file. A Harness profile may reference the Runtime-owned file but must not persist copies of its values. A missing or invalid configured file fails preparation.

### Initialization state and failure

An Environment's initialization is `pending`, `running`, `complete` or `failed`, independent of any allocation and of authentication and connection publication.

- The Worker's initialization scheduler scans 32 Environments at a time, wraps at the end and bounds concurrent preparations by execution concurrency, independently of Provider maintenance.
- A missing socket does not consume a pending attempt. An unavailable Harness fails before installation. Each operation rechecks current authority and the original socket; completion rechecks the exact binding.
- Each file transfer, configure, Skill, Plugin, package and setup step has a two-minute budget; the whole initialization has 30 minutes. Initial input keeps its five-minute admission deadline, so large installations should start from an idle Session.
- A running initialization whose owner is lost, including across a Core restart, fails as unconfirmed; nothing is replayed. A completed Environment never reinstalls on reconnect or native recovery, so later user changes survive.
- Failure is terminal for the Session but destroys neither compute nor files.

On failure, one transaction marks the Environment failed and records `agent.session.environment.failed`, an `error` event and one `agent.session.failed`. Session reads return `failed`, the reason as `error` and the failure time as `last_active_at`; live streams end after the failed event. Pending input settles as failed. The reason names only the step and its exit status:

| Failed step | Reason |
| --- | --- |
| Setup command `i`, confirmed exit 1–255 | `Failed to provision environment: script "setup_commands[i]" failed with exit code N` |
| Python packages, confirmed exit 1–255 | `Failed to provision environment: script "Python package installation" failed with exit code N` |
| npm packages, confirmed exit 1–255 | `Failed to provision environment: script "npm package installation" failed with exit code N` |
| Initial file installation, confirmed failure | `Failed to provision environment: initial file installation failed` |
| Skill preparation, confirmed failure | `Failed to provision environment: Skill installation failed` |
| Harness not installed on the Runtime | `Failed to prepare environment: the selected Harness is unavailable. Install the supported Harness version on the Runtime and create a new Session.` |
| Anything else: timeouts, unknown effects, missing or malformed receipts, Plugin installation, snapshot finalization, bootstrap rejection, Core restart | `Failed to provision environment: initialization did not complete` |

Every initialization operation returns a typed `rejected`, `failed` or `unknown` outcome, and the daemon confirms process exit and I/O settlement first. Core composes the reason from a fixed label and integers, so commands, env values, package names, paths and process output never reach the reason, events, logs or responses. The failed step is not retried and later steps do not run. Confidential env and setup snapshots are encrypted separately from ordinary metadata. Initial files use the atomic replacing writer and anchored workspace paths on every platform; Files API creation keeps its own no-overwrite rule.

## Templates

A Template is Project-owned configuration for `openai_hosted` Sessions and for `x_agents_core.environment`. It holds no running workspace and is unrelated to Provider images such as E2B templates. Every Session that references it gets its own Environment through the same preparation as inline configuration. Template parsing, storage and resolution never select a Harness or Provider or depend on native tool names or private Harness paths, so a new Harness or Provider needs no Template change.

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
template = client.beta.agents.environments.templates.create(
    name="Python workspace", network={"access": "enabled"},
    env={"APP_MODE": "analysis"}, packages={"python": ["packaging==26.0"]},
    setup_commands=[{"command": "mkdir -p /workspace/outputs"}],
)
session = client.beta.agents.sessions.create(
    agent={"model": "your-configured-model"},
    environment={"type": "openai_hosted", "environment_template_id": template.id},
    input="Create /workspace/outputs/report.txt containing the result of 6 * 7.",
)
```

### Operations

- Every operation needs a Project API key and `OpenAI-Beta: agents=v1`. Template operations work without an execution deployment and allocate no compute.
- `name` is optional and nullable, kept verbatim, 1–256 Unicode characters.
- `network.access` is `enabled`, `disabled` or `restricted`; omitted or null means enabled. See [Restricted network](#restricted-network).
- Responses carry safe metadata and never `env`, `setup_commands` bodies or inline file data.
- List uses `after`, `limit` (default 20; 0 is treated as 1 and values above 100 as 100) and `order` (default `desc`), ordered by creation time and ID. Missing and foreign Template IDs and cursors return the same 404.
- Update: an omitted field keeps its value and a supplied field replaces it. Null clears `name` and every list and resets `network` to enabled.
- Writes and Session resolution that seal or open confidential content (files, env, setup commands, Skills, Plugins) need Core's [credential key](../../docs/configuration.md#installation-directory); metadata reads do not.
- A Session resolves `environment_template_id` within its Project once, at creation, freezes the effective configuration and never passes the Template ID to the Provider or Runtime. Updating or deleting a Template never changes an existing Session. Creation retries recover the recorded caller intent before reading the Template, even after it is deleted; a changed intent conflicts.

### Inheritance

The Session applies the Template first, then its own fields. Composition happens once, before the encrypted Session snapshot is written, and the result is revalidated with the ordinary validators. Caller intent (omitted, null or explicit) is kept separately for the retry policy.

| Field | Omitted or null in the Session | Supplied in the Session |
| --- | --- | --- |
| `network` | Inherit the whole Template policy | Must narrow it: enabled can become restricted or disabled; restricted can become a subset of its hosts or disabled; disabled cannot widen |
| `env` | Inherit the Template keys | Overlay by key; the Session value wins; `{}` keeps all Template keys |
| `setup_commands` | Inherit the sequence | Replace it; `[]` clears |
| `files` | Inherit the file set | Replace the whole set; `[]` clears |
| `packages` | Inherit both managers | Resolve `python` and `npm` separately: an omitted or null manager inherits, a list replaces, `[]` clears; `system` is rejected |
| `skills`, `plugins`, `capability_directories` | Inherit the list | Replace it; `[]` clears |

For comparison, an inline `openai_hosted` Session without a Template treats omitted and null `network` as enabled.

### Restricted network

`restricted` requires 1–100 exact ASCII hostnames in `allowed_domains`; subdomains and redirect targets need their own entries. Wildcards, URL or port syntax, IP literals, Unicode and trailing dots are rejected. Reads return the supplied spelling, order and duplicates; comparison uses a separate lowercase, deduplicated copy.

The Runtime does not enforce `disabled` or `restricted`, and no Provider enforces them for it. Core therefore stores these policies in Templates but rejects any Session whose effective network is not `enabled`, before allocating compute. Initialization and tools use the host's existing network.

### Env and setup commands

Env values are readable by Agent code but never appear in public metadata or initialization diagnostics. Names must match `^[A-Za-z_][A-Za-z0-9_]*$` and values cannot contain NUL. Core reserves `PATH`, `OPENAI_API_KEY` and every name starting with `OAC_` or `CODEX_`. Files and packages are installed before setup commands; a nonzero setup command fails initialization; no command is retried after unknown effects; completed setup never runs again on reconnect.

### Initial files

`files` entries place a file at an absolute destination inside `/workspace`, from inline standard-base64 `data` or from a Project-owned uploaded `file_id`.

| Limit | Value |
| --- | --- |
| Files per configuration | 50 |
| Inline file | 5 MiB |
| All inline content | 10 MiB |
| Referenced file | 50 MiB |
| Session or Template request body | 16 MiB |

Paths must be canonical, distinct and inside the logical workspace; the Runtime anchors each write to its bound workspace. This is API path scope, not a restriction on native tools running as the same user. Template metadata shows inline files as type, path and size and references as type, path and `file_id`; each Session gets fresh file IDs and sizes for both. File data stays out of ordinary configuration, responses, events and command arguments. A Template keeps references; each Session authorizes and freezes its own encrypted source bytes, so later source deletion cannot change them.

### Skills

Templates and inline configuration accept Project-owned Skill references and inline Skill ZIPs. Upload a directory through the pinned SDK, then reference its default version:

```python
skill = client.skills.create(files=[
    ("report/SKILL.md", b"---\nname: report\ndescription: Create the report.\n---\nFollow the report procedure.", "text/markdown"),
])
template = client.beta.agents.environments.templates.create(
    skills=[{"type": "skill_reference", "skill_id": skill.id}],
)
```

The pinned `/v1/skills` resource, version and content routes use the Project API key without the Agents beta header. ZIP uploads use `files` and directory uploads repeated `files[]`. The pinned SDK 3.13.0 drops a single file tuple during multipart extraction, so upload a single ZIP with raw HTTP. An upload holds at most 500 regular files and exactly one `SKILL.md`, 5 MiB compressed and 20 MiB expanded. [File resource semantics](file-resource-semantics.md) owns default-version and deletion rules.

A reference with an omitted or null version selects the default at Session creation, `"latest"` the latest version, and a positive version string that version. Template responses keep the unresolved selector (`version: null` for the default); Session metadata shows `{type, skill_id, version, name, description}` with a concrete version. A Session freezes the selected version's bytes and metadata in its creation transaction; later default changes, source deletion or Template updates cannot change it.

An inline Skill carries `name`, `description` and a base64 ZIP `source` (`media_type` `application/zip`). The archive has one top-level folder with `SKILL.md` and optional supporting files; the manifest name and description must match the request. Frontmatter may contain `name`, `description`, `license`, `compatibility` and string `metadata`; native hooks, permission controls and subagent directives are rejected. Only regular files are allowed: path traversal, links, duplicate destinations, special files and invalid manifests are rejected. Content is inert during installation, and executable bits are kept. Inline metadata shows only type, name and description.

### Plugins and capability directories

A Plugin is an inline ZIP with type, name and description whose single archive root contains `.codex-plugin/plugin.json`. The manifest's `skills` names Skill directories; the whole package layout is kept. Public Plugin metadata shows only type, name and description.

`openai_hosted` and Template `capability_directories` accept clean absolute paths inside `/workspace`, which initial files and setup can populate. `self_hosted` capability directories are absolute local paths on the machine. Directory-discovered Skills never appear as `skills` or `plugins` entries. Missing directories, duplicate Skill names, unsupported manifests and non-regular files fail initialization.

| Archive and installation limit | Value |
| --- | --- |
| Per archive | 5 MiB compressed, 20 MiB expanded, 1,000 entries |
| Inline Skills | 50, 10 MiB compressed and 50 MiB expanded in total |
| Plugins | 50, 10 MiB compressed and 50 MiB expanded in total |
| Installed snapshot | 50 Skills, 50 Plugins, 50 MiB |

## Skills, Plugins and Environment MCP

Skills and their immutable versions are Project resources, independent of Sessions and native installations.

- Version allocation and pointer changes serialize on the owning Skill row. The top-level name and description follow the default version in the same transaction; non-default uploads keep them. Version identities stay unique across concurrent uploads and deletion.
- Bundles are encrypted with the service cipher, bound to Project, Skill and version. Metadata reads never load or decrypt bundles. Deleting a Skill reclaims its versions without affecting frozen Sessions.
- References resolve inside the Session creation transaction, after the upsert establishes ownership. Referenced resources are locked in a stable order, and the version, metadata and bytes freeze together. Public reference metadata, unresolved Template intent and the resolved Runtime bundle stay distinct; a reference is never reported as inline.

Inline and referenced Skills use the same confidential snapshot and installer. The Runtime installs them under `skills/<name>` in its capability root before setup and native execution. Adapters register only the selected Skill roots: Codex as explicit extra roots, MiniMax Code through its native catalog, and Claude as one controlled envelope per package with real directories and immutable hard links. Automatic native MCP discovery stays disabled. Codex nested `SKILL.md` discovery, `agents/openai.yaml` dependency configuration and Claude inline shell preprocessing fail adapter preparation. Native Harness configuration is never passed through wholesale.

### Plugin MCP

A Plugin declares MCP servers with `mcpServers: "./.mcp.json"` in `.codex-plugin/plugin.json`, or through a root `.mcp.json` when the path is omitted. The file holds `mcpServers` keyed by server name. Selecting a Plugin root as a capability directory activates its MCP declarations; selecting a parent directory discovers Skills without activating nested MCP servers.

The shared parser accepts HTTP `url`, `bearer_token_env_var` and literal `http_headers`, and stdio `command`, `args`, selected `env_vars` and a package-relative `cwd`. Public `env_http_headers` is unsupported. The Runtime re-parses the frozen installed packages and resolves selected values only from the initialized env; a missing value fails instead of falling back to a model or daemon variable.

A stdio server starts through the daemon's stdio helper, which resolves the installed declaration and launches the command with the Harness's permissions. On Unix the helper replaces itself with the server; on Windows it forwards stdio inside the owned process tree. Initialized values override the declaration's variables. Process groups and Windows Jobs own cancellation and descendant cleanup, not isolation.

[Harness capabilities](harness-capabilities.md#environment-preparation) owns the supported Plugin transports and per-Harness limits.

Environment MCP needs enabled network. Duplicate server identities are rejected. Claude rejects literal headers because the pinned client expands them again and forwards custom headers across origins. MiniMax ACP HTTP declarations stay in session-local native memory; tokens never enter native configuration files or process arguments. Required initialization and tool allowlists cannot be set through the Plugin manifest.

### Effective bindings

The Runtime resolves public HTTP declarations and installed Plugin MCP through `agent.ResolveMCPBindings` before adapter projection. Each transient binding keeps its connection origin, transport, nullable tool allowlist, required flag, credential authority and installed stdio identity. Bindings are never persisted or logged; duplicate identities and unavailable selected credentials are rejected. MiniMax reads its session-private native runtime-name registry for exact first-frame identities and cross-checks completed native results for both transports; adapters never fabricate a delayed start event or guess identities.

### Public MCP connection origin

An Agent's HTTP MCP tool ([declaration](execution-tools.md#http-mcp)) has a `connection_origin`. Omitted or null means `service`, including on `self_hosted` Sessions. The origin is kept from saved configuration through the Session snapshot to the Runtime request, which requires the exact wire version; a `service` request never becomes an `environment` connection.

| Origin | Connects from | Allowed placements |
| --- | --- | --- |
| `service` | Core's service-side execution host | `none` only |
| `environment` | The Environment's workspace | `openai_hosted` and `self_hosted` with enabled network |

Core's Harness profile declares `MCPOrigins`; admission and dispatch check the origin against the placement and the Runtime's advertised HTTP, bearer and required-initialization capabilities, and the Runtime validates the origin again before invoking an adapter. No Harness-name or Provider branch selects a different path.

[Harness capabilities](harness-capabilities.md#tools) owns per-Harness origin support and policy limits.

Both origins support anonymous HTTP and HTTPS bearer credentials. The attached-Vault selection freezes the credential identity, including a unique implicit URL match or an anonymous selection. Only that Project-authorized credential enters the transient Runtime request; Core defaults and unrelated Vaults are never searched. A decryption failure or missing credential fails execution without an anonymous fallback. Public Environment MCP keeps `project_vault` authority and Plugin credentials keep `environment_configuration` authority; neither overrides a duplicate server label. Bearers never enter persisted native configuration or process arguments.

Tool allowlists and required initialization follow the [HTTP MCP contract](execution-tools.md#http-mcp).
