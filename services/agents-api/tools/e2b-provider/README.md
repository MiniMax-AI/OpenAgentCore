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

Runtime observation uses a third read-only request, `observe`, for at most 100
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

The private JSON boundary has version 1. Requests and credentials enter stdin;
stdout contains one bounded response with sanitized error codes. API keys never
enter arguments, inherited environment or receipts. The process retains its
allocation lock when the Core caller times out, until the bounded SDK operation
returns. Core must serialize lifecycle requests and never replay Create.

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

From the repository root:

```sh
E2B_PROVIDER_BUILD_DIR="$HOME/.oac/build/e2b-provider" scripts/build-e2b-provider.sh
```

Docker builds Linux amd64 output with the pinned CPython 3.12.12/Debian 12 image.
The full Python dependency closure, including PyInstaller, has hashes in
`requirements.lock`. Native `pyqwest` and `protobuf-py-ext` wheels are included.
No account credential is needed for builds or `--check`. When building from an
archived source tree, supply `E2B_SOURCE_REVISION` with its actual commit.

The output is `oac-e2b-provider-linux-amd64.tar.gz` and its `.sha256` file.
Extraction yields `oac-e2b-provider/oac-e2b-provider`, `_internal/`,
`licenses/`, `requirements.lock` and `manifest.json`. The artifact contains only
regular files/directories, with executable permissions preserved. Core's image
and native installer use the same tree; the target needs compatible Linux/glibc
and CA certificates, but no separately installed Python. The installation owns
the durable receipt path independently of this immutable helper payload.

`deploy/e2b/build-template.py` packages `init.py` and `managed_init.py` with the
qualified Runtime image. Existing templates without these files must be rebuilt.
The shared protected image preparation is used by both managed and self-hosted
startup; their credential formats and one-shot receipts remain separate.

## Verification

```sh
python -m unittest discover -s services/agents-api/tools/e2b-provider -p '*_test.py' -v
python -m unittest discover -s services/agents-api/deploy/e2b -p '*_test.py' -v
```

The build runs the first suite and validates the relocated helper's `--check`
report. These checks do not establish cloud authentication, native isolation or
real execution. Live acceptance must use owned E2B compute and the same Runtime,
with actual lease renewal, restart/unknown outcome reconciliation and confirmed
cleanup. Initializer and three-harness qualification remain deployment checks.
