# E2B Sandbox Provider helper

Core's E2B Sandbox Provider ([`sandbox/e2b`](../../internal/sandbox/e2b)) is a pure-Go adapter that runs this one-shot Python helper for each lifecycle and read operation. The helper uses the official E2B Python SDK 2.51.0 ([`requirements.lock`](requirements.lock)); it implements no provider HTTP, envd RPC, scheduler or network service. E2B uses direct placement: there is no node, and each sandbox's daemon connects to Core over the public URL. Runtime execution and Files use that daemon connection. [Add a Sandbox Provider](../../../../docs/sandbox-provider.md) owns the provider contract this adapter implements.

## Deployment

The deployment selects E2B with an account key and an immutable `templateID:build_UUID`; [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the selection, key replacement and reset rules. Build the template with [`build-template.py`](../../deploy/e2b/README.md#build-a-template); it carries the `managed_init.py` startup script that Create runs, and a template without it is rejected as `template_invalid`. The template is deployment configuration, not a public Environment Template.

The account key is stored encrypted in Core's database and is write-only. It reaches the helper only on standard input, never in a template, command argument, inherited environment, receipt or response. The helper, its dependency closure and licenses ship in the Core image; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds it. The server needs no Python.

## Operations

| Helper operation | Core use | Behavior |
| --- | --- | --- |
| `create` | `Create` | Creates the sandbox once and runs `managed_init.py`; see [Create](#create) |
| `inspect` | `GetInfo` | Reads the sandbox by recorded ID, or by ownership metadata when no ID is recorded, and checks ownership, domain, template and resources |
| `renew` | `Renew` | Extends the lease of the running sandbox to the configured timeout, then rereads it |
| `kill` | `Kill` | Destroys every matching sandbox and confirms that none remains |
| `command` | `RunCommand` | Runs one bounded command as the Runtime user on a running sandbox whose bootstrap completed; output is limited to 1 MiB per stream |
| `validate_deployment` | Deployment setup | Reads the template's builds and requires the exact build to be ready with the configured CPU and memory. Without configured resources the selection adopts the build's CPU and memory. Returns the build's status, CPU, memory and reported disk for Core to record; bounded to 30 seconds |
| `list_templates`, `list_builds` | [Configuration discovery](../../../../contracts/agents-api/sandbox-deployment.md#configuration-discovery) | Pages the key's visible templates (`GET /v2/templates`) or one template's ready builds, with a transient key. Results are capped at 200 and write no receipt |
| `observe` | Runtime observations | One allocation; see [Observations](#observations) |
| `verify_credential` | E2B key replacement | Up to 32 allocation references; see [Credential verification](#credential-verification) |

Compatible endpoints must return the SDK 2.51.0 template-list and template-build response models; the helper does not adapt other catalog shapes. E2B has no independently configurable disk limit, and sandbox inspection does not expose a build ID: build provenance comes from the validated create selector.

## Private JSON boundary

[`helper_contract.go`](../../internal/sandbox/e2b/helper_contract.go) owns the adapter-private wire types, version, operation and error vocabulary, and bounds. Its generator projects Python declarations into the helper and template sources, deriving managed-bootstrap fields from the Sandbox Provider types, network access values from `agentnetwork.Policy.Validate`, and the SDK version from the hashed dependency lock. The command-input limit comes from its shared Go contract. The generated modules have no SDK or repository dependency and ship with the frozen helper and protected template startup scripts.

Run `go generate ./services/core/internal/sandbox/e2b` from the repository root after changing these declarations. `make check-e2b-provider` and the Go adapter tests reject stale projections; both languages consume generated valid and invalid exchanges covering wire types, extra fields, operation/reference bounds and managed-bootstrap fields. The helper build copies those fixtures with its source before running the pinned-SDK suite.

The boundary has version 4. Managed bootstrap and Resume accept an omitted or null `Workspace`; E2B rejects non-null external workspace bindings before native calls. The request and credentials arrive on standard input; standard output carries one bounded response with a sanitized error code. The helper removes ambient `E2B_*` and `PYTHON*` variables and calls the SDK only with the request's explicit API origin and sandbox domain. Each receipt is bound to those selectors, and a receipt without them belongs to the official endpoints (`https://api.e2b.app`, `e2b.app`). An endpoint change keeps earlier generations on their original API and sandbox domain; the candidate key must verify all retained ownership before an online switch.

## Receipts and state directory

`OAC_E2B_STATE_DIR` ([configuration](../../../../docs/configuration.md#appendix-core-environment-without-the-installer)) must already exist, be owned by the helper's user and grant no group or other access. Keep it on durable private storage across Core upgrades and restarts. Its receipts hold SDK connection material, ownership identities and one-shot creation claims; they are not Core execution state. Losing the directory cannot authorize recreation or successful cleanup; never delete receipts after an uncertain call.

A helper holds its allocation's lock until the SDK operation returns, even after Core's caller times out. Core tracks the helper's actual exit, and a credential change waits for running helpers without killing them. Helper exit never proves that a remote Create settled. Core serializes lifecycle requests per allocation and never replays Create.

## Create

1. Record `create_pending` with the bootstrap identity, then call `Sandbox.create` with the template, the configured timeout, the ownership metadata, `on_timeout=kill` and auto-resume disabled. A definite rejection records a settled `rejected` receipt with no sandbox IDs.
2. Record the sandbox ID and connection material, check the sandbox domain, then read the sandbox by ID and check its ownership metadata, template and resources before writing any credential. A mismatch records a settled rejection and returns `CreateSettled` with the error.
3. Check that `/opt/oac-e2b/managed_init.py` has regular-file type through SDK file information, then open it through the SDK streaming file API as root and close the response without downloading its contents. This verifies readability without a remote probe process. Then write the managed bootstrap input to `/root/.oac/e2b/managed-bootstrap.json` and run `managed_init.py` as root.

`managed_init.py` prepares the image as the [application-managed startup](../../deploy/e2b/README.md#startup-and-security-boundary) does, writes the [Runtime bootstrap](../../../../docs/runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600, owned by UID 1000), sets the Environment, Session and network variables and starts `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json` as UID/GID 1000. It records process handoff in `/root/.oac/e2b/managed-ready.json` and refuses to run again once any launch record exists. `BootstrapComplete` becomes true when Core validates that record with the expected identity. The initialization command returns the bounded durable receipt on its existing output stream, avoiding a separate file read on successful Create; Core still rereads cloud ownership and configuration before returning. If the command response is lost or its receipt is unreadable, inspection can recover the record without replaying startup; it does not prove enrollment or native readiness.

An unknown Create is never repeated. A Create whose connection material was lost can be discovered and destroyed but cannot resume bootstrap, and an unconfirmed startup requires reclaiming the whole allocation.

## Inspection and cleanup

Inspection uses SDK metadata and ID reads only. Inspection never uses SDK `connect`, which is reserved for the explicit managed Resume operation. The version-pinned constructor that restores a client from saved connection material is confined to [`sdk.py`](sdk.py) and covered by a no-connect, no-create test. Resource drift fails inspection but still permits ownership-based cleanup.

`CreateSettled` proves that the original Create and bootstrap can no longer mutate; it is independent of `BootstrapComplete`. An empty lookup never settles an unknown Create. Kill destroys every matching sandbox, confirms that none remains and only then records a settled tombstone; it returns `State=absent` with `CreateSettled`. A settled rejected Create with no sandbox IDs proves absence without a cloud request, so GetInfo and Kill still succeed when the key is invalid. Ordinary missing compute has no such proof.

## Observations

Create emits six sequential timing records on helper stderr: `sandbox_create`, `ownership_check`, `template_check`, `bootstrap_write`, `bootstrap_run` and `ready_inspect`. Each record contains only `event=e2b_create_stage`, `stage`, integer `duration_us` and boolean `completed`. The Core adapter accepts only the stages listed here, durations between zero and 1800 seconds, and one record per stage, then logs `e2b create stage` with the allocation and Environment IDs from its validated request. Unknown fields, oversized records and arbitrary SDK diagnostics are discarded. Timing uses a monotonic clock; `completed` describes the local step, not Runtime readiness. Records are exported when the helper exits before caller cancellation, including failed operations; the Core log timestamp is export time, not the time the step ran. Missing records are unobserved, not zero. Helper startup, lock waits and receipt work outside these steps remain part of the outer Provider duration. No request payload, credential, SDK error text or command output is logged.

`bootstrap_run` also contains helper-side `bootstrap_stream_open` (the SDK background start call) and `bootstrap_stream_completion` (the existing handle wait, including command output draining). Guest timings on the same command stderr are filtered to `bootstrap_claim`, `bootstrap_protection`, `bootstrap_layout`, `bootstrap_credentials`, `bootstrap_spawn` and `bootstrap_receipt`, then exported through the same bounded Core log path. These cover the one-shot claim, image protection/account locking, workspace/environment layout, private credential persistence, daemon handoff and durable ready receipt. The helper accepts at most 8192 characters of guest diagnostics and one fixed four-field record per stage, at most 256 characters each; arbitrary stderr is discarded. Receipt JSON and command stdout are unchanged. Guest timings require a template containing the instrumented initializer; they remain unobserved on other templates. A stream failure can prevent guest timings from returning, even when initialization ran. Guest and SDK intervals overlap within `bootstrap_run`; do not sum them, or add daemon startup to provider completion. `bootstrap_spawn` confirms handoff only, and the daemon can start before the provider returns. All existing filesystem protection, fsync, no-replay and recovery behavior remains in force.

`observe` reads the allocation's sandbox ID from its receipt without taking the allocation lock. It then runs one `GET /sandboxes/metrics` request for that sandbox and one listing of running sandboxes with the allocation's labels concurrently, within the caller's deadline; the listing stops once the sandbox has appeared. Only a sandbox that the listing confirms for exactly that allocation is reported, with the listing's start time. A malformed metrics point is unavailable. Observation never connects to, renews or changes a sandbox and writes no receipt. [Runtime observability](../../../../contracts/agents-api/runtime-observability.md) owns the field mapping.

## Credential verification

`verify_credential` checks the fixed build, the key's paginated team-owned template listing and each settled live receipt against the labelled sandbox listing. Template and sandbox scans each stop at 100 pages or the 30-second deadline. A missing or unsettled receipt, a repeated template cursor or an unconfirmed read never authorizes replacement, and no receipt changes. A readable public template does not prove that the key owns it. Core scans retained generations and allocation pages under one bounded verification context, then repeats the verification while provider calls are fenced, before it commits the new key. Authentication rejection, ownership mismatch and uncertainty return distinct fixed codes.

## Build and tests

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds the helper. The artifact contains only regular files and directories with executable modes preserved, including the native `pyqwest` and `protobuf-py-ext` wheels. `--check` needs no account credential.

```sh
make check-e2b-provider
```

With `OAC_TEST_E2B_SDK_PYTHON` pointing at the pinned SDK environment, this runs this directory's tests and the [template scripts' tests](../../deploy/e2b/README.md#tests). The helper build runs this directory's suite and checks the relocated helper's `--check` report. These tests create no cloud resources.

## Managed suspension

The adapter implements the complete shared suspension group using opaque retained state. A native pause retains the same sandbox; it is not a portable filesystem snapshot. Core's common activity rule quiesces the Runtime, then the adapter calls the pinned SDK's memory-preserving `Sandbox.pause`. The private allocation receipt commits the operation before native I/O and reports resource release only after observing the exact sandbox paused.

Resume calls `Sandbox.connect` once with `on_resume=restore` and the existing native timeout. It preserves the native sandbox ID while advancing the shared logical generation. Fresh connection material is persisted before returning running. Unknown pause and connect outcomes are observed without replay; a missing connect receipt keeps execution unavailable. The adapter fences stale generations before commands and deletion. Consumed pause receipt cleanup preserves running compute.

The shared registration owns the 300-second idle duration and 86400-second retention default. Native timeout remains 3600 seconds and automatic native resume remains disabled. SDK calls use the configured official-compatible endpoint. Generated declarations and fixtures own the private helper shape.
