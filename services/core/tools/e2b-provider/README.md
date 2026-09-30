# E2B Sandbox Provider helper

Core's E2B Sandbox Provider ([`sandbox/e2b`](../../internal/sandbox/e2b)) is a pure-Go adapter that runs this one-shot Python helper for each lifecycle and read operation. The helper uses the official E2B Python SDK 2.51.0 ([`requirements.lock`](requirements.lock)); it implements no provider HTTP, envd RPC, scheduler or network service. E2B uses direct placement: there is no node, and each sandbox's daemon connects to Core over the public URL. Runtime execution and Files use that daemon connection. [Add a Sandbox Provider](../../../../docs/sandbox-provider.md) owns the provider contract this adapter implements.

## Deployment

The deployment selects E2B with an account key and an immutable `templateID:build_UUID`; [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the selection, key replacement and reset rules. Build the template with [`build-template.py`](../../deploy/e2b/README.md#build-a-template); it carries the `managed_init.py` startup script that Create runs, and a template without it is rejected as `template_invalid`. The template is deployment configuration, not a public Environment Template.

The account key is stored encrypted in Core's database and is write-only. It reaches the helper only on standard input, never in a template, command argument, inherited environment, receipt or response. The helper, its dependency closure and licenses ship in the Core image and native Core; the [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds it. The server needs no Python.

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
| `observe` | Runtime observations | Up to 100 allocations; see [Observations](#observations) |
| `verify_credential` | E2B key replacement | Up to 32 allocation references; see [Credential verification](#credential-verification) |

Compatible endpoints must return the SDK 2.51.0 template-list and template-build response models; the helper does not adapt other catalog shapes. E2B has no independently configurable disk limit, and sandbox inspection does not expose a build ID: build provenance comes from the validated create selector.

## Private JSON boundary

The boundary has version 1. The request and credentials arrive on standard input; standard output carries one bounded response with a sanitized error code. The helper removes ambient `E2B_*` and `PYTHON*` variables and calls the SDK only with the request's explicit API origin and sandbox domain. Each receipt is bound to those selectors, and a receipt without them belongs to the official endpoints (`https://api.e2b.app`, `e2b.app`). An endpoint change keeps earlier generations on their original API and sandbox domain; the candidate key must verify all retained ownership before an online switch.

## Receipts and state directory

`OAC_E2B_STATE_DIR` ([configuration](../../../../docs/configuration.md#appendix-core-environment-without-the-installer)) must already exist, be owned by the helper's user and grant no group or other access. Keep it on durable private storage across Core upgrades and restarts. Its receipts hold SDK connection material, ownership identities and one-shot creation claims; they are not Core execution state. Losing the directory cannot authorize recreation or successful cleanup; never delete receipts after an uncertain call.

A helper holds its allocation's lock until the SDK operation returns, even after Core's caller times out. Core tracks the helper's actual exit, and a credential change waits for running helpers without killing them. Helper exit never proves that a remote Create settled. Core serializes lifecycle requests per allocation and never replays Create.

## Create

1. Record `create_pending` with the bootstrap identity, then call `Sandbox.create` with the template, the configured timeout, the ownership metadata, `on_timeout=kill` and auto-resume disabled. A definite rejection records a settled `rejected` receipt with no sandbox IDs.
2. Record the sandbox ID and connection material, check the sandbox domain, then read the sandbox by ID and check its ownership metadata, template and resources before writing any credential. A mismatch records a settled rejection and returns `CreateSettled` with the error.
3. Check that `/opt/oac-e2b/managed_init.py` is readable, write the managed bootstrap input to `/root/.oac/e2b/managed-bootstrap.json` and run `managed_init.py` as root.

`managed_init.py` prepares the image as the [application-managed startup](../../deploy/e2b/README.md#startup-and-security-boundary) does, writes the [Runtime bootstrap](../../../../docs/runtime-bootstrap.md) file to `/home/runtime/runtime-bootstrap.json` (mode 0600, owned by UID 1000), sets the Environment, Session and network variables and starts `oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json` as UID/GID 1000. It records process handoff in `/root/.oac/e2b/managed-ready.json` and refuses to run again once any launch record exists. `BootstrapComplete` becomes true when a later inspection reads that record with the expected identity; it does not prove enrollment or native readiness.

An unknown Create is never repeated. A Create whose connection material was lost can be discovered and destroyed but cannot resume bootstrap, and an unconfirmed startup requires reclaiming the whole allocation.

## Inspection and cleanup

Inspection uses SDK metadata and ID reads only. SDK `connect` is never used because it can resume paused compute. The version-pinned constructor that restores a client from saved connection material is confined to [`sdk.py`](sdk.py) and covered by a no-connect, no-create test. Resource drift fails inspection but still permits ownership-based cleanup.

`CreateSettled` proves that the original Create and bootstrap can no longer mutate; it is independent of `BootstrapComplete`. An empty lookup never settles an unknown Create. Kill destroys every matching sandbox, confirms that none remains and only then records a settled tombstone; it returns `State=absent` with `CreateSettled`. A settled rejected Create with no sandbox IDs proves absence without a cloud request, so GetInfo and Kill still succeed when the key is invalid. Ordinary missing compute has no such proof.

## Observations

`observe` reads each allocation's sandbox ID from its receipt without taking the allocation lock. It then runs one `GET /sandboxes/metrics` request and one labelled listing of the installation's running sandboxes concurrently, within the caller's deadline; the listing stops once every requested sandbox has appeared. Only a sandbox that the listing confirms for exactly that allocation is reported, with the listing's start time. A malformed metrics point makes only its row unavailable. Observation never connects to, renews or changes a sandbox and writes no receipt. [Runtime observability](../../../../contracts/agents-api/runtime-observability.md) owns the field mapping.

## Credential verification

`verify_credential` checks the fixed build, the key's paginated team-owned template listing and each settled live receipt against the labelled sandbox listing. Template and sandbox scans each stop at 100 pages or the 30-second deadline. A missing or unsettled receipt, a repeated template cursor or an unconfirmed read never authorizes replacement, and no receipt changes. A readable public template does not prove that the key owns it. Core scans retained generations and allocation pages under one bounded verification context, then repeats the verification while provider calls are fenced, before it commits the new key. Authentication rejection, ownership mismatch and uncertainty return distinct fixed codes.

## Build and tests

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers) builds the helper. The artifact contains only regular files and directories with executable modes preserved, including the native `pyqwest` and `protobuf-py-ext` wheels. `--check` needs no account credential.

```sh
make check-e2b-provider
```

With `OAC_TEST_E2B_SDK_PYTHON` pointing at the pinned SDK environment, this runs this directory's tests and the [template scripts' tests](../../deploy/e2b/README.md#tests). The helper build runs this directory's suite and checks the relocated helper's `--check` report. These tests create no cloud resources.
