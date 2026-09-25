# Environment contract and implementation path

This assessment covers the fixed [Python SDK contract](upstream.json), with partial
coverage. Core-managed Docker runs the three qualified native harnesses. V1
user-managed `self_hosted` enrollment uses the same colocated Runtime for Codex,
Claude SDK and MiniMax at `/workspace`; see its [real deployment qualification](user-managed-runtime-v1.md).
For hosted compute, the deployment selects E2B, Docker or microsandbox through
[Hosted Sandbox Manager](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md).
For the separate caller-managed E2B path, the user owns allocation, renewal and
cleanup through the official SDK and [Runtime packaging](../../services/agents-api/deploy/e2b/README.md).
[Templates](environment-templates.md) remain a hosted-only resource path;
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
Runtime lifecycle and workspace interfaces with its own native adapter/isolation.
See the [engine profile guides](README.md#public-engine-profiles) for setup and limits.
The standalone [operator configuration](../../services/agents-api/deploy/codex/README.md#standalone-operator-configuration)
selects the qualified immutable Runtime image; advertised capabilities alone do
not enable admission. An idle or initial-text creation commits Session, Environment
and retry identity before the existing leased Worker provisions its allocation.
A committed creation interrupted before bootstrap is recovered without replaying
an existing allocation's Create.

Omitted/null network defaults to enabled. Enabled, disabled and exact-host restricted
policies use the same qualified image with adapter-selected immutable native policy.
Templates and inline configuration share initial files, env, packages, ordered setup
and inline or tenant-owned referenced Skills through the hosted initializer.
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
`workspace_directory: "/workspace"` and omitted/null/empty `capability_directories`. Creation
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
[canonical wait rules](../../CONTRIBUTING.md#environment-ownership-and-placement).

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
Mixed events, non-text input and nonempty capability directories remain unsupported.
The public field types are unchanged; `/workspace` is the V1 deployment limit, not
an upstream schema change. `remote_url` is the configured daemon WebSocket URL,
returned unchanged. This is our private connection contract and does not claim
stock `exec-server` compatibility. Service-origin HTTP MCP is explicitly rejected
on `self_hosted`; `none` MCP and hosted Template Plugin MCP retain their own scope.

Mechanism tests cover enrollment, credential checks and exact-device dispatch. They
do not establish real public qualification. Before claiming that qualification,
exercise fixed SDK/raw HTTP creation/input, actual native tools, Files/Artifacts,
second-Turn history, Core/Runtime restart, cancellation and credential rotation/
revocation/deletion on each declared deployment.

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

The canonical [architecture rules](../../CONTRIBUTING.md#environment-ownership-and-placement)
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
