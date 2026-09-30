# Managed E2B SDK helper

Core's Go adapter invokes this one-shot helper for Create, GetInfo, Renew, Kill
and initialization RunCommand. Daily execution and Files remain on the existing
Runtime connection. The helper uses the official E2B Python SDK 2.51.0; it does
not implement provider HTTP, envd RPC, a scheduler or a network service.

Managed deployment validation uses a separate read-only helper request, bounded
to 30 seconds. The pinned SDK reads the selected template's build inventory and
requires the exact build UUID to be ready with the configured CPU and memory; a
selection without resources adopts the ready build's CPU and memory. It returns
the build's status, CPU, memory and reported disk size for Core to record with
the selection, and creates neither compute nor allocation receipts.

The console's Core-key-only setup flow uses two more read-only helper requests.
`list_templates` pages the credential's visible templates through the official
`GET /v2/templates` SDK operation; `list_builds`
pages one selected template and returns ready exact build IDs and resources.
Both operations use the same explicit endpoint selectors and a transient API
key. They cap results at 200, never write a receipt, and cannot replace the
deployment write's exact-build validation.
Compatible endpoints must return the E2B SDK 2.51.0 template-list and
template-build response models. The helper does not adapt provider-specific
catalog shapes.

Runtime observation uses a third read-only request, `observe`, for at most 100
Runtime observation uses a separate read-only request, `observe`, for at most 100
allocations. It reads each allocation's sandbox ID from its receipt without the
allocation lock, then runs one `GET /sandboxes/metrics` request and one labelled
listing of this installation's running sandboxes concurrently, within the
caller's deadline; the listing stops once every requested sandbox has appeared.
Only a sandbox that the listing confirms for exactly that allocation is
reported, with the listing's start time. A malformed metrics point makes only
its row unavailable. It never connects to,
renews or changes a sandbox and never writes receipts. See
[Runtime observability](../../../../contracts/agents-api/runtime-observability.md). Actual sandbox information
is checked before writing bootstrap credentials and on subsequent inspection;
resource drift still permits ownership-based cleanup. E2B disk capacity is not
an independently configurable limit. Sandbox inspection does not expose a build
UUID: build provenance comes from the validated immutable create selector.

Credential replacement uses `verify_credential`, a read-only request with at most
32 Core allocation references. It verifies the fixed build, the SDK's paginated
team-owned template listing, and each settled live receipt against the labelled
sandbox listing. Template and sandbox scans each stop at 100 pages or the caller's
30-second deadline. Missing/unsettled receipts, repeated template cursors and
unconfirmed reads never authorize replacement. No receipt is changed. Core scans
retained generations and allocation pages under one bounded verification context,
then repeats verification while provider calls are fenced before credential commit.
Authentication rejection, ownership mismatch and uncertainty remain distinct fixed
codes. A readable public template is not proof that the key owns it.

The private JSON boundary has version 1. Requests and credentials enter stdin;
stdout contains one bounded response with sanitized error codes. API keys never
enter arguments, inherited environment or receipts. The process retains its
allocation lock when the Core caller times out, until the bounded SDK operation
returns. Core must serialize lifecycle requests and never replay Create.
The request carries the deployment's explicit API origin and sandbox domain.
Every SDK call uses these selectors after ambient `E2B_*` variables are removed.
Receipts bind an allocation to those selectors; receipts written before this
feature belong to official E2B. Endpoint changes retain earlier generations on their original API and data-plane domain. The candidate credential must verify all retained ownership before an online switch.
Core tracks actual child exit even after caller timeout. Credential fencing
waits for those children without killing them; child exit itself never proves remote
Create settled. Core must serialize lifecycle requests and never replay Create.

`StateDir` must already exist, be owned by the service user and have mode 0700.
Keep it on durable private storage through Core upgrades/restarts. Its receipts
contain provider connection credentials, ownership identities and one-shot
creation claims, not Core execution state. Losing this directory cannot authorize
recreation or successful cleanup. Do not delete receipts after an uncertain call.

GetInfo uses SDK metadata/ID reads. SDK `connect` is never used because it can
resume paused compute. The SDK's version-pinned constructor restores clients
from private connection material; this deprecated constructor is intentionally
contained in `sdk.py` and covered by a no-connect/no-create test. An unknown
Create without connection material can be discovered and reclaimed, but cannot
resume bootstrap. Empty lookup cannot settle an unknown Create. Cleanup retains
every matching candidate and confirms exact-ID absence before writing a tombstone.

`CreateSettled` proves the original Create/bootstrap can no longer mutate. It is
independent of `BootstrapComplete`, which acknowledges the protected initializer's
last step, not enrollment or native readiness. Explicitly settled absence returns
successful Info with `State=absent`; ordinary missing compute has no such proof.
An explicitly rejected Create with a settled receipt and no provider IDs proves
absence without another cloud request. GetInfo and Kill retain that rejection
receipt, so repeated recovery remains possible even when the API key is invalid.
Other receipts still require cloud discovery and ownership checks.
Unconfirmed initialization commands require reclaiming the whole allocation.

## Build

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers)
builds the helper. The artifact contains only regular files and directories, with
executable permissions preserved, including the native `pyqwest` and
`protobuf-py-ext` wheels. `--check` needs no account credential. The installation
owns the durable receipt path independently of this immutable helper payload.

`deploy/e2b/build-template.py` packages `init.py` and `managed_init.py` with the
qualified Runtime image. Existing templates without these files must be rebuilt.
The shared protected image preparation is used by both managed and self-hosted
startup; their credential formats and one-shot receipts remain separate.

## Verification

```sh
python -m unittest discover -s services/core/tools/e2b-provider -p '*_test.py' -v
python -m unittest discover -s services/core/deploy/e2b -p '*_test.py' -v
```

The build runs the first suite and validates the relocated helper's `--check`
report. These checks do not establish cloud authentication, native isolation or
real execution. Live acceptance must use owned E2B compute and the same Runtime,
with actual lease renewal, restart/unknown outcome reconciliation and confirmed
cleanup. Initializer and three-harness qualification remain deployment checks.

## Adapter rules

Core-managed E2B is a separate hosted deployment choice, using the official pinned
Python SDK through a packaged private helper. Do not restore the retired custom
HTTP/Connect or envd implementation. The adapter implements the same five operations;
cloud allocations use direct placement with no synthetic node, while Runtime execution
and file access keep the shared daemon contract. Its immutable Runtime template build
is deployment configuration, not a public Environment Template. Keep the account API
key encrypted in the database, write-only through admin input and absent from helper
arguments, logs, metadata and receipts. SDK connection materials and attempted-create
receipts belong in the private durable provider state directory; never replace missing
state to make cleanup appear successful. Create runs once. Unknown control-plane
outcomes remain blockers even if a listing is empty. Explicit matching-reference
CreateSettled evidence proves that the original initialization cannot mutate further;
confirmed absent compute may then be released. Ordinary 404 responses do not prove it.
The helper's pinned SDK, dependencies and licenses ship with Core; users do not install
Python packages after selecting E2B in Web. Application-managed self_hosted tooling
remains independent and uses the same Runtime. Qualify each changed path using actual
provider and model execution before claiming acceptance. Run `make check-e2b-provider`
with `OAC_TEST_E2B_SDK_PYTHON` pointing to the pinned SDK environment; the packaged
helper build runs the provider tests as well. The SDK gate also covers the
application-managed launch tests. `make check` covers shared initialization and
managed initialization using only the Python standard library.
