# Agents API

Independent execution service implementing part of the pinned OpenAI Agents API.
It owns reusable Agents, durable Sessions/Turns/Items, live events, function actions
and a daemon execution worker. Public execution supports qualified Codex, Claude Code
(`claude_sdk`) and MiniMax Code (`mcode`) profiles through the shared Runtime contract.
The three-harness Linux amd64 Docker V1 MVP has accepted evidence. V1 user-managed
Runtime enrollment has [recorded real acceptance](../../contracts/agents-api/user-managed-runtime-v1.md)
with explicit deployment coverage. [E2B deployment](deploy/e2b/README.md) is user-managed;
its earlier Core-managed qualification remains historical evidence.
It builds and runs with its own PostgreSQL database and credentials;
Parsar's product service, frontend and database are not required.

Use this guide to build, configure and connect a client. The
[protocol coverage](../../contracts/agents-api/README.md) lists supported operations,
engine limits, acceptance evidence and missing resources. The target remains the
complete pinned protocol; current workflows do not establish full compatibility.
Parsar product execution and its eventual public-client cutover are separate.

## Reusable Agents

Static-bearer and OAuth Vault Credentials support creation, replacement, deletion
and safe metadata retrieval/listing. Vault deletion atomically removes its
Credentials. Configure their independent encryption key and authenticated Session
use through the [credential guide](credentials.md); see [OAuth credentials](oauth-credentials.md)
for application authorization, dispatch-time refresh and revocation boundaries.

The pinned Python client saves an Agent independently, then starts a Session with initial input:

```python
agent = client.beta.agents.create(model="your-model", name="Example")
session = client.beta.agents.sessions.create(
    agent_id=agent.id, environment={"type": "none"}, input="Hello",
)
```

These resources belong to the authenticated execution tenant. Saving configuration
does not launch an engine. Optional Session `agent` fields override the saved
configuration: omitted fields inherit, supplied objects/arrays replace whole fields.
Source and Session metadata stay separate; execution never looks up the source again.

- Retrieve with `client.beta.agents.retrieve(agent.id)`; list saved resources with
  `client.beta.agents.list(limit=20, order="desc")` and SDK auto-pagination.
- Update with `client.beta.agents.update(agent.id, instructions="New instructions")`.
  Omitted fields remain unchanged. Metadata replaces all pairs; null/empty clears it.
  Existing Sessions retain their configuration; new Sessions resolve the update.
- Delete with `client.beta.agents.delete(agent.id)`. Existing Sessions and history
  remain available. New references fail; recorded creation retries recover their
  accepted snapshot without consulting the deleted source.
- List Sessions with `client.beta.agents.sessions.list(agent_id=agent.id)`. Filtering
  uses the immutable root ID, including inline Agents and history after source changes.

See the [configuration and retry limits](../../contracts/agents-api/README.md#public-semantics)
before relying on optional settings or hosted error/default equivalence.

## Build standalone binaries

```bash
make build-agents-api
# Optional absolute output directory:
AGENTS_API_BUILD_DIR="$HOME/.parsar/build/agents-api-test" make build-agents-api
```

The default output is `${PARSAR_HOME:-$HOME/.parsar}/build/agents-api`:

- `agents-api`: HTTP service and execution worker.
- `agents-api-migrate`: this service's embedded database migrations.
- `agents-api-device`: operator device provisioning and revocation.
- `agents-api-environment-key`: principal executor key issuance, rotation and revocation.

Use these executables in place of the corresponding `go run` commands below.
The build needs Go and access to its pinned module dependencies; it does not need
Node, Docker, the product service or frontend. An isolated source context enforces
that boundary on every build. [Contributor rules](../../CONTRIBUTING.md#independent-build-artifacts)
define the allowed shared packages and required checks. Runtime database/key
configuration and a separately installed execution daemon are still required;
these binaries do not establish full protocol coverage. For a standalone Linux
container, see [Container deployment](CONTAINER.md).

`make build-agents-api-release` packages the same four commands in a versioned
Linux amd64 archive, with source/protocol identity, checksums, a license and
[operator instructions](RELEASE.md). Build from a clean Git worktree with Go and
Python 3.9+; output defaults to `~/.parsar/build/agents-api-release` (or
`AGENTS_API_RELEASE_DIR`). The extracted API needs no source checkout or compiler.
For a Docker-hosted package, first qualify an immutable Linux amd64 Runtime built
with [the existing Runtime builder](deploy/codex/README.md), then run:

```sh
AGENTS_API_RELEASE_RUNTIME_IMAGE=sha256:<qualified-image-ID> make build-agents-api-release
```

The resulting `agents-api-docker-<revision>-linux-amd64.tar.gz` also contains the
Runtime image export, committed seccomp policy and [hosted guide](HOSTED-RELEASE.md).
Consumers load the included image and start the extracted Core; no source checkout
or compiler is needed. The builder records the selected image ID and file hashes;
the exact Core/Runtime combination still needs deployment acceptance. The ordinary
archive remains Docker-free. Database/Docker setup and publication remain separate.

## Database ownership

Use a dedicated PostgreSQL database and account, separate from the Parsar product.
This service does not import `server/internal` or apply product migrations.
Migrations are embedded and tracked in `agents_api_schema_version`.

```bash
AGENTS_API_DATABASE_URL='postgres://.../agents_api' \
  go run ./services/agents-api/cmd/migrate
```

The public `Idempotency-Key` creation header is optional: omission creates a new
Session. A supplied key identifies the request within its authenticated tenant.
Inline retries use normalized effective configuration; new saved-Agent references
record caller intent independently of later source updates/deletion. Retries do
not admit initial input again. Every retry must match the original typed creator,
including across key rotation. Keys in the same Project share the creator principal,
so rotating a key preserves that retry identity. Records with a known creator but
no recorded request intent retain resolved-snapshot behavior; records without a creator cannot be retried.
These retry policies are not verified hosted semantics. See the
[retry boundary](../../contracts/agents-api/README.md#public-semantics).

The Store uses internal creation keys and preserves immutable engine/configuration,
native continuity and same-tenant device bindings. The public API applies schema
validation/defaults before storage. Internal bounds are 64 KiB for metadata and
512 KiB for configuration. Keep credentials out of both. Public metadata permits
at most 16 string pairs, 64-character keys and 512-character values; storage bounds
do not replace those rules. Violations and non-string values return
`invalid_request_error` with a `metadata` or `metadata.<key>` param. PostgreSQL
cannot store U+0000, so requests containing it in any stored string return 400
before anything is written; this is a local limit, not hosted parity. Tenant identity comes from authenticated credentials,
never metadata or a caller-supplied business identity.

## Internal Turn persistence

Validated message/cancel/function-result batches commit under a tenant-scoped
Session lock. Messages start a Turn when idle and steer active work. Retry keys
identify the whole ordered batch; an invalid event does not partially admit it.
Queued cancellation needs no live engine. Active cancellation awaits a native
outcome, and completion may win the race. Terminal states cannot be overwritten.

A Session keeps its effective configuration, engine and device across Turns;
product Conversations, native Sessions, connections, processes and sandboxes are
different objects. Strict native resume requires retained history on that device.
The API owns durable public history and pending function decisions; adapters own
native translation and their harness owns the model/tool loop.

The worker uses its database lease connection for execution writes. Lease loss
fences those writes; it does not prove native commands or side effects have stopped.
Restart conservatively fails previously claimed work and retains queued work.
Uncertain delivery is never blindly replayed. Function actions and live SSE are
available within the [current coverage](../../contracts/agents-api/README.md);
other pending interactions, process-loss recovery and environment lifecycle remain
incomplete. Durable acceptance is not an exactly-once side-effect guarantee.

## Standalone HTTP service

Run migrations first, then `go run ./services/agents-api/cmd/server`. The service
uses `AGENTS_API_DATABASE_URL` for its dedicated database; it does not read the
product database or accept product login cookies. Configure the separate deployment
administrator credential through `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` at startup to manage
Projects and keys. Deployment credentials cannot authenticate `/v1`, and application
API keys cannot authenticate administrator routes.

Projects and API keys live only in the database. Configuration files contain
infrastructure settings and deployment credentials, not business identities.
Installation creates no Project or application key. Using the
[administrator API](../../contracts/agents-api/admin-api.md), create a Project with
`POST /core/v1/admin/projects` and issue a named key with
`POST /core/v1/admin/projects/{project_id}/keys`. Both requests accept a JSON object containing `name`;
Core generates the identifiers. The Web management screens still need migration;
see [integration status](../../docs/web/README.md).

A Project owns one tenant and one execution principal. All keys in it have equal
access to its assets and share that principal; write provenance records the actual
key separately. Issuance returns plaintext once, and the database stores its digest.
Deliver the secret only to authorized applications. Rotate by issuing another key
in the same Project and revoking the old one. No secret-reset endpoint or service
restart is needed. Renaming a Project preserves its ID, principal and assets.
Archiving disables all its keys but retains assets and already accepted execution.
Administrators can read or delete retained resources and copy supported assets into
an active Project.

Optional `OpenAI-Organization` and `OpenAI-Project` headers must match the Project's
execution scope; repeated or conflicting values fail authentication. The catalog
Project UUID and its external execution-scope identifier are distinct; see the
administrator contract. These identities grant no product-user rights. New Sessions
persist the Project principal as creator atomically and never change it on retry.
Historical Sessions keep unknown creators and remain readable; no key, metadata or
product record can assign their ownership through a retry. Retire older API writers
before starting this deployment; mixed-version creation is unsupported. Executor
credentials separately match this recorded creator before authorizing an Environment
connection; they do not inherit general caller API permissions. Native Runtime and
node transport contracts are unchanged.

`AGENTS_API_ADDR` defaults to `127.0.0.1:8091`; use a TLS reverse proxy for remote
access. `AGENTS_API_ENGINE` defaults to `codex`; use `claude_sdk` for Claude Code
or `mcode` for MiniMax Code. Configure the corresponding qualified Runtime through
its [deployment guide](../../contracts/agents-api/README.md#public-engine-profiles).
It selects new Sessions independently of the requested
model. Existing Sessions retain their stored engine. Set
`AGENTS_API_HARNESSES=codex,claude_sdk,mcode` to explicitly enable installed profiles
for user-managed enrollment without a managed Provider. This list supplements the
default engine and any managed engine profiles; unknown names fail startup.
Enabling a profile does not install its harness or qualify its deployment.

The SDK base URL is `http://127.0.0.1:8091/v1`. Requests require a bearer key.
Agents and Vault routes also require `OpenAI-Beta: agents=v1` (set by their SDK
resources); general Files routes do not. Supported operations include:

- Safe read-only [Core startup configuration](../../contracts/agents-api/startup-configuration.md)
  for build support and process selections, without Runtime or Session observations.
- Saved Agent create/retrieve/update/list/delete.
- Session create/retrieve/list/delete and metadata-only update. Creation supports inline
  configuration or a saved `agent_id`, field replacements, initial text
  and ordinary or streaming responses. Initial input is required for `none` and
  streamed creation outside `self_hosted`.
- Session event submission and live streaming, Turn retrieve/list and Items list.
- Environment retrieve for three-harness colocated self-hosted and Docker
  profiles, bounded live file listing, and inline/source copies into qualified
  local workspaces. Shared Artifacts support capture, list/retrieve/content and
  deletion independently of the live Runtime after publication.
- Project-owned `user_data` source file upload/list, metadata/content retrieval and
  deletion; see [source Files](../../contracts/agents-api/source-files.md).
- Vault create/retrieve/list/delete, project-scoped pagination and stored status
  filtering; static-bearer and OAuth Credential create/retrieve/list/replacement/delete,
  plus [dispatch-time OAuth refresh](oauth-credentials.md). Public archive semantics remain gaps. Already-delivered credentials
  are not withdrawn by local deletion. Session attachments support
  [authenticated HTTPS MCP](credentials.md#use-a-credential-in-a-session).

Execution uses the selected
[engine profile](../../contracts/agents-api/README.md#public-engine-profiles),
including `none` and the colocated self-hosted profile described below.
Ordinary JSON requests have a 1 MiB body limit; file transfers use the separate
bounds in the Files contracts. Session lists support `after`, `limit` (0 is treated
as 1 and values above 100 as 100),
`order` (`asc`/`desc`) and optional immutable root `agent_id`. The local defaults
are 20 and descending order; exact hosted limits/error semantics remain unverified.
Session updates require the metadata field; null/empty clears it and an object
replaces supplied pairs. An empty update body rejects before resource lookup.

Delete with `client.beta.agents.sessions.delete(session.id)`. Only a durably idle
or failed Session without required actions or pending input can be deleted; a
queued, running or waiting root Turn or a pending input reservation returns 409
`conflict_error` and leaves the Session unchanged. Subagent child Turns and
pending Environment file writes do not block deletion. Cancel its work
with an `agent.session.input.cancel` event, wait until it is idle, then delete it.
Confirmation means public removal: Session/history reads and new input become
unavailable, and existing streams close on observing removal. Creation keys stay
reserved; deletion never affects other Sessions, saved Agents or their shared
device. Internal records are retained for execution settlement. Managed Docker
deletion separately revokes authority and reclaims owned compute/workspace/history;
caller-managed compute is not reclaimed by this service. Repeating the deletion of
your own deleted Session returns the same confirmation, missing and foreign
Sessions return 404, and creation-key reuse returns 409; overlapping stream timing
is unverified.

Codex command Items support live `agent.output.command_execution_output.delta`
events when emitted by the connected daemon. Queries retain accumulated drafts and
authoritative completion snapshots, including observed partial output after
cancellation. Native text conversion/output quotas apply; older peers may provide
only completion snapshots. Pinned native 0.153.4 may also omit early process
output from both notifications and its final aggregate; this remains an upstream
execution gap. Recover missed output with Items queries, not SSE replay.

Non-text message input, Subagents and installations beyond hosted initial files,
env, ordered setup and npm/Python packages remain unsupported. Saving optional Agent configuration does not make
it executable. Unsupported requests fail explicitly. `/healthz` reports liveness only.

## Managed hosted execution

The basic `openai_hosted` profiles for Codex, Claude Code and MiniMax Code require
explicit operator configuration. Select the qualified native image using the
[engine profile guides](../../contracts/agents-api/README.md#public-engine-profiles),
then follow the [Docker setup](deploy/codex/README.md#standalone-operator-configuration).
Core manages Docker only. For user-managed E2B, see
[E2B Runtime packaging](deploy/e2b/README.md).
Core remains independently deployed with its own database. Public idle and initial
text Sessions share the existing preparation, execution, Files and recovery paths.
Networking defaults to enabled; disabled and exact-domain restricted policy are
supported after setup. System/npm/Python packages use the shared initializer;
remaining unsupported combinations are explicit gaps. Initial inline/file_id files, confidential env, npm/Python packages,
ordered setup and [public Environment Templates](../../contracts/agents-api/environment-templates.md)
resolve to the same immutable hosted configuration, independently of provider templates. Additional harnesses
require separate integration and qualification.
Connected describes the authenticated Runtime connection, not native readiness.
A provisioning failure fails the Session with a safe step and exit-status reason
([initialization failure](../../contracts/agents-api/environment-templates.md#initialization-failure--september-23));
other exact hosted failure and expiry semantics remain unverified.

## Internal execution device connection

The standalone service can accept existing daemon connections without a Parsar
workspace or product database. Enable its internal gateway by setting
`AGENTS_API_DAEMON_WS_URL=wss://your-service/api/v1/agent-daemon/ws` (use `ws`
for local development). This configured URL is returned unchanged as
`self_hosted.remote_url`. It names
our private daemon transport, not stock OpenAI `exec-server` interoperability.

After migrations, an operator can provision a device for an execution tenant:

```bash
umask 077
mkdir -p ~/.parsar/parsar-daemon/agents-api
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --name 'local executor' \
  --url 'http://127.0.0.1:8091' \
  > ~/.parsar/parsar-daemon/agents-api/auth.json
parsar-daemon connect --profile agents-api
```

The command requires `AGENTS_API_DATABASE_URL` and emits a secret profile once.
Use a new profile rather than overwriting an existing device's credentials. When
provisioning remote compute, securely transfer this file to the same profile path
on the executor. The database stores only the credential digest. API keys and
device credentials are not interchangeable. To revoke a device:

```bash
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --revoke '<device-uuid>'
```

An existing connection is retired on its next heartbeat; new connections are
rejected immediately. The internal Store binds each Session to one same-tenant
device, preserves that assignment across retries/restarts, and refuses a silent
move to another device. Revoked bindings cannot be used for dispatch. Device
connections alone do not start a Turn. Submit text, cancellation or function results
through the official Session events endpoint; the worker assigns a same-tenant host and preserves that
binding. Managed Docker has three-harness evidence. This generic device provisioning path
is for `none`; self-hosted Sessions require the dedicated enrollment below.
User-managed enrollment has a separate [qualification record](../../contracts/agents-api/user-managed-runtime-v1.md); complete protocol semantics remain partial. See the [ownership rules](../../CONTRIBUTING.md#product-and-execution-service-separation).

### Enable Claude SDK execution

Build and extract the runtime archive into a fresh managed directory on a matching
executor host. The archive contains the compiled bridge and pinned production
SDK/MCP/native dependencies; Node is installed separately. Linux x64/glibc with
Node22 is the accepted platform. See the
[runtime artifact contract](../../CONTRIBUTING.md#private-claude-sdk-runtime-artifact)
for build outputs, version checks and platform restrictions.

```bash
make build-claude-sdk-runtime
# After extracting the matching archive into this operator-chosen directory:
export PARSAR_CLAUDE_SDK_ENTRYPOINT="$HOME/.parsar/runtimes/claude-sdk/dist/main.js"
export PARSAR_CLAUDE_SDK_NODE="/absolute/path/to/node"
parsar-daemon connect --profile agents-api
```

Set `AGENTS_API_ENGINE=claude_sdk` on the API service. Configure provider access in
the daemon's private native SDK environment. Runtime readiness checks versions and
startup before daemon registration; it does not validate provider credentials.
SDK state stays under the daemon profile, independently of the replaceable bundle.
A ready SDK can start the daemon without a legacy CLI. Product `claude_code` remains
separate. Managed Node installation, runtime activation and registry publication
are not supplied by these commands.

## Official client verification

After preparing a dedicated test database, build the server and verify it with
the official client installed from the commit in `contracts/agents-api/upstream.json`:

```bash
python -m pip install -r services/agents-api/tests/requirements.txt
make build-agents-api
AGENTS_API_SERVER_BIN="${PARSAR_HOME:-$HOME/.parsar}/build/agents-api/agents-api" \
  python services/agents-api/tests/official_client.py
```

The test uses `PARSAR_AGENTS_API_TEST_DATABASE_URL`, temporary service keys and
fresh tenant IDs. The suite checks upstream and generated response schemas, retries,
ordering, tenant isolation, unsupported options and reads after a process restart,
without a model provider. It also runs the
[official Go client integration](../../packages/agents-client/README.md), using two
fresh tenants, and validates its created Sessions through the Python SDK.

## Checks

```bash
PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_local_tests' \
  make check-agents-api
```

Set `PARSAR_OFFICIAL_SDK_PYTHON` to the fixed SDK interpreter for the Store client
fixtures, and run the separate official-client command above as well.
`TestEnvironmentRetrievalOfficialClient` verifies public creation, scoped safe
Environment reads and retrieval after reopening without execution configuration.
`TestEnvironmentInitialFailureOfficialClient` verifies failed Session reads and
matching SDK/raw live failure events after privately provisioned initial input
expires, without fabricating a Turn or changing the Environment status. It is a
persistence prerequisite test. `TestSelfHostedInitialCreationOfficialClient`
separately exercises ordinary/streamed public initial creation through the Worker,
retry identity, disconnect survival and explicitly controlled deadline failure.
The test database must be named `parsar_agents_api_*_tests` and contain no product
workspace tables. Tests apply only this service's migrations and use new tenant
IDs without truncating tables. Missing test configuration skips DB tests locally;
the `agents-api` CI workflow always supplies its own PostgreSQL service. Run the
full `make check` before review as well. Product OpenAPI generation excludes this
service; its supported HTTP contract is generated separately.

## Public text execution

With the daemon gateway enabled, the service owns one worker per execution database
and processes up to four Turns concurrently. Other workers are rejected by a
PostgreSQL advisory lock. A disconnected host leaves unsent work queued; clients
may cancel it. Session/Turn/Items queries expose durable results.

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8091/v1", api_key="<execution-key>")
session = client.beta.agents.sessions.create(
    agent={"model": "<model-available-on-the-engine-host>"},
    environment={"type": "none"},
    input="Hello",
    extra_headers={"Idempotency-Key": "first-message"},
)
```

Configure model access in the engine host's native configuration. API tenant keys
and daemon credentials authenticate this service, not a model provider. Never put
provider secrets in Session metadata. This path does not enable Parsar Skill/SP
callbacks or bypass the pending product authorization work.

Session creation admits initial text atomically; add `stream=True` for
created/live events. Open a GET event stream before submitting
later work, or use the official `sessions.stream` helper for one Turn. Function
handlers return results through the same public events endpoint. Recover missed
output with Session/Turn/Items queries; reconnecting SSE does not replay history.
See [creation streaming](../../contracts/agents-api/README.md#session-creation-streaming)
for retry behavior and unverified hosted timing.

The [accepted workflows](../../contracts/agents-api/README.md#acceptance-evidence-and-remaining-scope)
include real MiniMax execution through built API/daemon/Codex and Claude SDK,
function success/error, cancellation and native continuation. Controlled fixtures
remain useful but do not replace real-provider acceptance for execution changes.

## Upgrading archived Item history

Migration 15 retires private journal-to-Item backfilling. It preserves existing
public Items and source journals, and refuses to apply if any Turn still has
`items_indexed=false`. Do not set this marker manually or replay native execution.

For installations with pre-Items history:

1. Back up the execution database and stop new execution/submission. Drain active
   Turns before switching versions. Product storage is independent.
2. Run the previous service release `906069e` against the execution database with
   its worker disabled (omit `AGENTS_API_DAEMON_WS_URL`). Using each tenant's API
   credential, list every Session and request its Items once. The old service
   prepares the complete index under the Session lock, even with `limit=1`.
3. Verify `SELECT count(*) FROM turns WHERE NOT items_indexed` returns zero.
   A failed preparation must be resolved before upgrade; legacy journals cannot
   recover fields they never recorded. Stop the previous service.
4. Upgrade the daemon first so it advertises `tool_observations`, then apply the
   migrations and start the new service. No old/new service overlap is supported
   across this migration. Devices without this capability are not dispatched.

For step 2, use the pinned Python SDK and the usual private endpoint/key settings,
repeating with each existing Project and an authorized API key:

```python
from openai import OpenAI

client = OpenAI()  # OPENAI_BASE_URL and OPENAI_API_KEY
for session in client.beta.agents.sessions.list():
    client.beta.agents.sessions.items.list(session.id, limit=1)
```

Fresh installations and already indexed history need no backfill. Recovery reads
continue to use Session/Turn/Items; this procedure is an upgrade operation, not
an official SSE replay mechanism.

## User-managed Runtime enrollment

V1 uses our daemon as the user-side executor. Deploy daemon, selected harness,
local tools and protected `/workspace` together using the shared Runtime. Core
manages Docker-hosted compute only. The user owns local or E2B allocation, renewal
and destruction; use the official E2B SDK through the
[E2B guide](deploy/e2b/README.md), not a Core E2B Provider.

Create a Session with `environment={"type":"self_hosted",
"workspace_directory":"/workspace"}` and empty/default capability directories.
Retain the returned Environment ID and unchanged `remote_url`. Initial text may
wait for connection; an idle Session creates no Turn. Codex, Claude SDK and MiniMax
reuse the same exact binding and `LocalEnvironment` preparation path. Service-origin
HTTP MCP is rejected on this placement; `none` MCP and separately qualified hosted
Environment Plugin MCP remain available within their own limits.

An operator issues a connect-only principal executor key using the existing issuer:

```bash
umask 077
agents-api-environment-key --tenant "$TENANT_ID" \
  --organization "$ORGANIZATION_ID" --project "$PROJECT_ID" \
  --subject-kind service_account --subject-id "$SUBJECT_ID" --key-id "$KEY_ID" \
  > "$HOME/.parsar/executor-key.json"
```

The immutable principal must match a verified project mapping and the Session's
recorded creator. Add `--environment "$ENVIRONMENT_ID"` to restrict a key to one
live Environment. Keep the one-time `executor_token` output private. Only its digest
is stored; there is no secret read-back. `--rotate` and `--revoke` require the same
management ID and full principal; neither changes the key's restriction. Unknown
historical creators cannot enroll. API bearer keys and executor keys are separate.

Inside the qualified Linux Runtime, install that JSON as an owned mode-0600
`$PARSAR_HOME/parsar-daemon/executor-key.json`, outside the tool workspace. The
packaged Runtime supplies its native harness, filesystem helpers and isolation
profile. Start its daemon with the values returned by Session creation:

```bash
parsar-daemon connect --remote "$REMOTE_URL" \
  --environment-id "$ENVIRONMENT_ID" \
  --credential-file "$PARSAR_HOME/parsar-daemon/executor-key.json"
```

Use TLS outside loopback. Enrollment supplies the trusted Session identity; do
not inject an unrelated `PARSAR_RUNTIME_SESSION_ID`. The daemon persists its
immutable Environment binding beside the key and refuses to adopt another
Environment's existing native history. Rotation keeps the same key ID: update the
protected file, then restart the daemon to authenticate with the new token.
WebSocket authentication uses the `Authorization` header, never a URL token.

The private `POST /api/v1/agent-daemon/enroll` endpoint accepts that executor bearer
and `{"environment_id":"..."}`. It returns `device_id`, `session_id`,
`environment_id` and `workspace_directory`, never another credential. Enrollment
atomically binds one dedicated device/key to the Session; retries retain it and
conflicting bindings reject. No managed `runtime_allocation` is created. Runtime
onboarding connects to the returned `remote_url` using that exact binding and key.
The endpoint is outside the pinned public Agents API, which remains unchanged.
It is not stock `exec-server`, `/cloud/environment/*` or Noise interoperability;
there is no service-side harness or remote tool forwarding compatibility path.

The gateway and Worker recheck current credential authority. Rotation/revocation,
Session deletion and lost ownership deny further use; a replacement connection
cannot overwrite a successor's observations. Connection events report authenticated
connectivity, not native preparation readiness. Retained native history and workspace
must survive a Runtime restart for continuation; missing history fails closed.
Neither disconnect nor deletion promises immediate native process quiescence or
reclaims user-owned E2B/local compute. The user must stop and destroy it explicitly.

Pending input retains its durable identity/deadline through HTTP disconnects. Later
idle input returns 202 after preparation/admission, not after model completion;
use client/proxy timeouts above five minutes and recover progress through events
and reads. Exact upstream failure/error timing remains unverified.

The former registry/Noise relay, temporary harness credentials, separate native
executor launcher, private Codex harness package and old remote native probes are
retired. The Rust package retains only directory, write and workspace-export
helpers. Historical acceptance remains evidence for its original topology, not
proof of this new enrollment chain. The [current qualification record](../../contracts/agents-api/user-managed-runtime-v1.md)
identifies the separate fixed-SDK/raw HTTP, real-model, Files/Artifacts,
cancellation, restart/history and credential lifecycle evidence.


### HTTP MCP execution

This section covers `agent.tools` with `connection_origin: "service"`. An omitted
or null origin on HTTP transport is saved as `"service"`, exactly like the explicit
form. Environment-origin Plugin declarations use the separate
[initialization and transport contract](../../contracts/agents-api/environment-templates.md#environment-origin-mcp-plugins).

Service-origin MCP runs on trusted service-side compute. Codex supports `environment:{"type":"none"}`; Claude SDK supports HTTP MCP with
`environment:{"type":"none"}`. Inline or saved Agent tools may declare:

```json
{
  "type": "mcp",
  "server_label": "tickets",
  "transport": {"type": "http", "server_url": "https://mcp.example.com/mcp"},
  "connection_origin": "service",
  "allowed_tools": ["lookup_ticket"],
  "required": false
}
```

An omitted/null `allowed_tools` permits all tools from that server; `[]` permits
none. Native discovery may still connect to a declared server. The daemon must
advertise `mcp_http_tools`; selection waits for a capable device. The native
harness owns MCP discovery, calls and results. Public `mcp_call` Items use original
server/tool names; recover missed live events through Session, Turn and Items reads.

With Codex, set `required:true` to require initialization before the first native Turn. It
defaults to false and additionally requires the pinned daemon's `mcp_http_required`
capability. Native root thread creation and cold resume wait for required servers;
initialization failure stops execution without replacing retained history. Public
work can already be accepted or queued during this wait. Exact hosted creation
timing/errors and continuing MCP health monitoring remain unverified.

On `environment:none`, both Codex and Claude SDK support tenant-owned `vault_ids`
for static-bearer HTTPS MCP. `self_hosted` is explicitly unsupported for
service-origin MCP in V1, including anonymous requests.
An explicit
`credential_id` selects an attached credential for the exact HTTPS URL; omission/null
selects a unique matching credential, or stays anonymous if none matches. Ambiguity
fails before Session creation with 409 `conflict_error`. Selection is frozen
privately; Session reads and events show an implicitly selected credential ID in the
public tool, while the stored request keeps the caller's field. See
[credential setup and limits](credentials.md).
Authenticated execution additionally requires `mcp_http_bearer_auth`; missing keys
or failed authorization/decryption never fall back to anonymous execution.

The colocated V1 `self_hosted` profile does not admit service-origin MCP. The old
separate executor/service-side MCP combination and its remote capability gates are
retired. Do not forward Vault credentials to user-owned Runtime compute.

Claude SDK requires a packaged runtime that reports `mcp_http_tools`; the SDK
version alone does not qualify an older bundle. Its current profile
accepts either `required` value, requires connected servers and static inventories. Server labels
accept ASCII letters, digits, underscore and hyphen, except reserved `functions`;
selected tool names additionally accept dots. Declared HTTP MCP tools compose with
host functions; undeclared servers, built-ins and subagents remain disabled.
Authenticated requests also require `mcp_http_bearer_auth`. The existing Vault
selection rules apply, including implicit exact-URL matches and immutable private
bindings. Per-server/per-launch environment references keep bearer values out of
native argv and stored state. A Vault with no matching credential may stay anonymous.
Anonymous requests suppress native OAuth/credential injection with a blank
Authorization header, without deleting native state. Servers rejecting that header, normalized
name collisions, changing inventories and original MCP metadata fidelity remain
gaps. Items retain the observed native JSON, which may differ from the original
MCP envelope. See the [Claude SDK profile](../../CONTRIBUTING.md#claude-sdk-adapter-foundation).

The current subset rejects native OAuth login, inline authorization, nonempty headers or
request metadata, URL userinfo/query/fragment, the `environment` origin, stdio
and engines other than Codex/Claude SDK. The Codex adapter also
rejects reserved native labels and stored native MCP credentials. It verifies
exact effective MCP configuration before starting/resuming a native thread,
excludes undeclared servers and disables native apps/plugins. This runs on trusted
service compute; it does not provide filesystem isolation or guard against
concurrent operator configuration mutation. These limits are implementation gaps,
not changes to the pinned official protocol.

## Hosted sandbox nodes

The release includes `parsar-sandbox-node` for local and remote hosts. See the
[Hosted Sandbox Manager guide](HOSTED-SANDBOX-MANAGER.md) for provider selection,
registration, administrator credentials, fixed Session placement and maintenance.
