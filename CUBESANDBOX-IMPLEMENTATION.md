# CubeSandbox managed Runtime backend — implementation specification

Status: **approved for implementation**. Decisions are closed. Vendor details we
could not verify offline are converted into an explicit contract-pinning step
(M0) instead of an open question.

Branch: `feat/cubesandbox-runtime`, based on `origin/main` @ `c300ef4`.

This document is self-contained: an executor should be able to implement the
change from this file plus the referenced repository files, without re-deriving
the design.

## 0. How to read this document

| Section | Purpose |
| --- | --- |
| §1 | Goal and non-goals |
| §2 | Verified local and upstream anchors |
| §3 | Closed decisions and their rationale |
| §4 | Architecture and request flows |
| §5 | The pinned CubeSandbox contract this adapter depends on |
| §6 | The Go package: files, types, per-method behaviour, error mapping |
| §7 | Operator configuration and registration changes |
| §8 | Runtime image, template, storage and network |
| §9 | How Core's lifecycle drives the adapter |
| §10 | Contract and documentation updates |
| §11 | Test and acceptance plan with exact commands |
| §12 | Rollout, operations and capacity |
| §13 | Risks and bounded mitigations |
| §14 | Milestones with acceptance criteria |
| §15 | Deferred work: reversible expiry |
| §16 | Executor checklist |

## 1. Goal

Add a second managed Runtime backend behind the existing `openai_hosted`
discriminator, so Core can provision one colocated Runtime per Session as a
microVM on a CubeSandbox cluster instead of a local Docker container.

The public API, Web and the pinned protocol do not change. The provider choice
stays operator-owned and invisible to callers, as already required by
`apps/web/src/features/sessions/environment/environment-templates.ts:13-16` and
`docs/web/protocol-coverage.md:103-104`.

### Non-goals

- No change to the public `/v1/agents/**` contract, OpenAPI or Web behaviour.
- The default backend stays Docker; Cube is opt-in through operator config.
- No reversible expiry / pause contract change (see §15).
- No user-managed (`self_hosted`) Cube packaging.
- No new engine or harness support.

## 2. Verified anchors

### 2.1 Local (this repository, `c300ef4`)

| Concern | Location |
| --- | --- |
| Provider contract (5 methods) | `services/agents-api/internal/sandbox/provider.go:53-59` |
| Types to reuse | `provider.go`: `Reference`, `Bootstrap`, `Info`, `Command`, `CommandResult`, `MaxCommandInputBytes`, and the five sentinel errors |
| Reference implementation | `internal/sandbox/docker/{provider,bootstrap,command}.go` |
| Registration and validation | `cmd/server/managed_runtimes.go:18-111` |
| Lifecycle, reconcile, keepalive | `internal/execution/runtime_lifecycle.go` |
| Initialization operations | `internal/execution/runtime_initialization.go`, `runtime_setup.go` |
| Allocation state and lease | `internal/store/runtime_allocation{,_state}.go`, `internal/db/queries/runtime_allocations.sql` |
| Runtime image build | `scripts/build-agents-runtime.sh`, `deploy/{codex,claude,mcode}/`, `deploy/runtime/` |
| Release packaging | `scripts/build-agents-api-release.sh`, `HOSTED-RELEASE.md`, `RELEASE.md` |
| Contract text | `contracts/agents-api/{environments,environment-templates,environment-files,workspace-placement,harness-selection}.md` |

### 2.2 Upstream (CubeSandbox `master` at time of writing)

| Fact | Source |
| --- | --- |
| CubeAPI is an "E2B-compatible sandbox API server"; server base path `/cubeapi/v1` | `openapi.yml` |
| Sandbox create/list/get/delete, refresh, resume (deprecated), rollback, volumes | `openapi.yml` |
| Template build from an OCI image; the image must be reachable from CubeMaster nodes | `docs/guide/tutorials/template-from-image.md` |
| **A local, unpushed image works when CubeMaster runs on that machine** | `docs/guide/tutorials/template-build-practice.md` |
| A template build requires an HTTP 2xx probe; the probed port is also the readiness contract | `docs/guide/templates.md` |
| `envd` is optional; without it command/file APIs and create-time env init are unavailable | `docs/guide/tutorials/bring-your-own-image.md` |
| `envd` is compiled from `e2b-dev/infra` | `docs/guide/tutorials/bring-your-own-image.md` §7 |
| Host mount is node-local, outside snapshots, and pins pause/resume to the origin node | `docs/guide/persistent-storage.md` |
| Auth: no auth by default; `Authorization: Bearer` or `X-API-Key`, forwarded to a callback when configured | `docs/guide/authentication.md` |
| Sandbox states, per the pinned `SandboxState` enum: `running`, `paused`, `pausing`, `unknown`. `resuming`/`terminated` exist in the narrative docs but not in the pinned API enum, and unrecognised CubeMaster statuses are reported as `running` | `openapi.yml`, `CubeAPI/src/services/sandboxes.rs` |
| Non-zero delete statuses: `404`, `408` (request timeout), `409` (paused sandbox cannot be admitted for internal resume) | `openapi.yml` |
| CLI: `cubemastercli tpl create-from-image` / `tpl watch` / `tpl info` | `docs/guide/tutorials/template-from-image.md` |

## 3. Closed decisions

| Id | Decision | Rationale |
| --- | --- | --- |
| D1 | Implement the adapter with the standard library `net/http`, not the Cube Go SDK | Matches the existing adapter style (`internal/sandbox/docker` uses the official Moby client, while the retired E2B adapter was pure `net/http` and added no modules). We need five operations. Vendoring a sandbox SDK into Core's module graph for an opt-in backend is not justified. The SDK remains an accepted fallback if data-plane routing proves harder than expected. |
| D2 | Reach the sandbox data plane through CubeProxy, implementing the IP-override transport ourselves | Outside the node, `*.cube.app` does not resolve, so the client dials the proxy node IP while preserving the virtual Host header (`<port>-<sandboxID>.<domain>`). This is a small `http.Transport.DialContext` plus a Host override, testable offline. |
| D3 | Use our **own** HTTP readiness endpoint as the template probe, not `envd` | The probe defines when Cube freezes the template snapshot **and** is the sandbox readiness contract. Probing `envd` would only certify that `envd` is up. Our endpoint returns 2xx only once the daemon is connected and initialized, and doubles as `Info.BootstrapComplete` evidence. |
| D4 | Ownership and reconciliation through Cube `metadata` | Cube returns `metadata` on list and detail, and `GET /sandboxes?metadata=` filters server-side. This mirrors the retired E2B adapter's `inspect()` contract: exactly one match, otherwise `ErrOwnership`. |
| D5 | Persistence: one per-allocation host mount at `/environment`, plus a second mount of its `workspace` subdirectory at `/workspace` | The atomic rename contract operates **inside `/environment`** (`staging` → `workspace`), so no rename ever crosses a mount point. `/workspace` is a second view for native tools only. See §8.5. |
| D6 | Egress mapping: `enabled` allows ordinary outbound; `disabled` allows only Core and platform addresses | The Docker profile confines native tools while the trusted harness keeps Core/model connectivity. A blanket deny at VM level would break the daemon's WebSocket to Core, so `disabled` must be an allow-Core-then-deny policy, not a full air gap. |
| D7 | Keep Core's one-hour `kept_at` lease as the authority; `Renew` extends the Cube TTL | Core's lease, keepalive and expiry logic already exist and are provider-independent. The Cube TTL is a second, longer safety net and must exceed Core's window. |
| D8 | Single node: local image, no registry. Multi-node: private registry | CubeMaster reads a local image directly when it runs on the same machine, so the existing registry-free distribution (`image.tar` + `docker image load`) carries over for the first deployment. A registry is needed only once the cluster spans machines. |
| D9 | Include `envd` from the digest-pinned `cubesandbox-base` tag | Gives us the process and filesystem APIs plus create-time environment initialization. Cube builds this `envd` from `e2b-dev/infra`, the same upstream the retired E2B adapter vendored its process protocol from, so the surface is known. |
| D10 | Core may own remote compute; the default backend stays Docker; reversible expiry is deferred | Authorized 2026-09-22. Scope is opt-in and operator-configured. See §15. |

## 4. Architecture

### 4.1 Components and placement

```
+-- control-plane host ------------+        +-- CubeSandbox cluster --------------+
| Core (agents-api) + PostgreSQL   |  HTTP  | CubeMaster / CubeAPI                |
| managed-runtimes.json            | -----> |   |- Cubelet (per node)             |
|   |- cubesandbox provider key    |        |   |- CubeProxy (data plane)         |
|                                  |  WSS   |   '- KVM microVM x N                |
| agent-daemon gateway  <----------+--------+         '- parsar-daemon --> Core    |
+----------------------------------+        +-------------------------------------+
```

Core does **not** need Docker, KVM or cluster membership. Cube nodes do not need
access to Core's database. Only two network paths matter:

1. Core → CubeAPI (control plane) and Core → CubeProxy (data plane).
2. microVM → Core (daemon WebSocket, and the model endpoints the harness uses).

### 4.2 Create flow

1. Public Session creation commits Session, Environment and allocation identity.
   Existing behaviour, unchanged.
2. The worker calls `Provider.Create` with a `Bootstrap` carrying Core URL, device
   id, executor credential, network access and allowed domains.
3. The adapter lists sandboxes by ownership metadata. A match means the
   allocation already exists -> `ErrExists`. Never overwrite.
4. The adapter creates the microVM from the operator-pinned template, with
   ownership metadata, the host mounts from D5, the egress policy from D6 and the
   Cube idle TTL from D7.
5. The adapter bootstraps: it writes the daemon auth profile into the Runtime's
   private daemon directory, preserving the existing file contract
   (`server_url`, `runtime_id`, `runner_credential`) and its `0600` mode.
6. Core's lifecycle waits for `GetInfo` to report `running` with
   `BootstrapComplete: true`, derived from the readiness endpoint rather than
   from the microVM being alive.

### 4.3 Execution and reclamation

- Steady state: Core's worker keeps the one-hour lease renewed while the daemon
  connection is authenticated and `Renew` succeeds. `Renew` extends the Cube TTL
  of the **original** sandbox; it never resumes or replaces it.
- Session deletion or expiry: `RequestRuntimeCleanup` revokes authority, then
  `Kill` deletes every owned match and confirms absence.
- Lost `Create` response: the owner reference is already durable, so the next
  reconcile resolves the real state through `GetInfo`. `Create` is never
  replayed.
- A stopped, paused or missing sandbox never authorizes destroying retained
  workspace or history; only explicit cleanup or actual expiry does.

## 5. The pinned CubeSandbox contract

### 5.1 M0 — pin the contract first

Before writing adapter code, commit the vendor contract this adapter depends on,
exactly as the retired E2B adapter vendored `process.proto` from
`e2b-dev/infra` with a recorded SHA-256:

1. Copy CubeSandbox `openapi.yml` into the adapter package or a `docs/`-adjacent
   pinned location, recording the source revision (commit SHA) and the file's
   SHA-256.
2. Copy the `envd` process proto from the CubeSandbox-pinned `e2b-dev/infra` tag
   (`2026.16` in current docs) and record its SHA-256, if the implementation
   needs generated types for `RunCommand`.
3. Record the exact template id, image digest and base tag in the deployment
   record produced in §8.4.

Everything below is derived from that contract. If the pinned file disagrees
with this document, the pinned file wins and this document is corrected in the
same PR. The pin itself is recorded in
`services/agents-api/internal/sandbox/cubesandbox/contract/README.md`, which
lists the revisions, hashes, the client-side wire facts taken from the vendor's
own SDK at that revision, and the reconciliation items against this document.
The pinned base image for §8.2 is `ghcr.io/tencentcloud/cubesandbox-base:2026.16`
at manifest index digest
`sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472`.

### 5.2 Transport and authentication

- Control plane: CubeAPI, base path `/cubeapi/v1`. Configured as `api_url`.
- Data plane: CubeProxy, reached as `<port>-<sandboxID>.<domain>` with the Host
  header preserved while dialing the proxy node IP. Configured as `proxy_url`
  plus `sandbox_domain` and `proxy_node_ip`.
- Auth: `Authorization: Bearer <key>` (preferred) or `X-API-Key: <key>`.
  CubeAPI allows unauthenticated access by default, so an operator **must** turn
  on the auth callback; Core sends credentials regardless.
- The key is read from a private file at startup, trimmed, held in memory only,
  and never logged, echoed, put in argv, labels or metadata.

### 5.3 Endpoints this adapter uses

| Operation | Method and path | Notes |
| --- | --- | --- |
| Health | `GET /health` | Startup/config check only |
| Create | `POST /sandboxes` | Body `NewSandbox`; success `201` with `Sandbox` |
| List by owner | `GET /sandboxes?metadata=<urlencoded>` | Returns `ListedSandbox[]`. The pinned spec documents only the `metadata` parameter for v1 — no `state` filter and no paging — so the single response is treated as complete and exact metadata filtering does the work. `GET /v2/sandboxes` adds `state`, `nextToken` and `limit`, but the pinned document describes no next-token response field or header, so v2 paging cannot be implemented from the pin alone and is not used. |
| Inspect | `GET /sandboxes/{sandboxID}` | `SandboxDetail`; `404` means absent |
| Delete | `DELETE /sandboxes/{sandboxID}` | `204` on success; `404` absent; `408` request timeout; `409` paused sandbox cannot be admitted for an internal resume; `500` backend error; `503` sandbox is pausing, another lifecycle operation is in flight, or the internal resume failed, carrying `Retry-After`. Every non-`204`/`404` outcome is a real failure, never a success |
| Extend TTL | The pinned refresh endpoint (`POST /sandboxes/{sandboxID}/refreshes` with `{"duration": <seconds>}`) or the pinned timeout endpoint if that is what the pinned revision documents | This is `Renew`. Verify the exact path and body from the pinned spec; `POST /sandboxes/{id}/resume` is documented as deprecated and must **not** be used for renewal |
| Pause / resume | Not used in this slice | Reserved for §15 |

### 5.4 Create request fields we set

From `NewSandbox` in the pinned spec:

| Field | Value |
| --- | --- |
| `templateID` | Operator-pinned template id |
| `metadata` | Ownership keys (§6.3) plus the storage descriptor (§8.5) |
| `timeout` | Operator-pinned idle TTL in seconds, strictly greater than Core's one-hour window |
| `envVars` | Not used. Environment comes from Core's setup operations so it is never persisted in platform metadata |
| `network` | Egress policy from D6 |
| `secure` | The pinned spec accepts it but documents no semantics. **Not set**; recorded here as the chosen value rather than left ambiguous |
| `autoPause`, `autoResume` | Left unset in this slice so idle sandboxes terminate, matching current expiry semantics |
| `volumeMounts` / host mounts | Per D5 and §8.5 |

### 5.5 Response fields we consume

| Field | Use |
| --- | --- |
| `sandboxID` | `Info.ProviderID` and the reference for every later call |
| `state` | Mapped to our state vocabulary (§6.4) |
| `metadata` | Ownership verification; a mismatch is `ErrOwnership` |
| `templateID` | Sanity check against the configured template |
| `domain`, `envdAccessToken` | Data-plane addressing for bootstrap and `RunCommand`; `envdAccessToken` is optional and never logged. The pinned CubeAPI returns `null` for it on create, detail and connect, so the adapter must not require it |

### 5.6 State mapping

| Cube state | Our `Info.State` | `BootstrapComplete` |
| --- | --- | --- |
| `running` | `running` | From the readiness endpoint |
| `paused`, `pausing` | `paused`-class value, not `running` | `false` |
| `unknown` | `unknown`, not `running` | `false` |
| absent (`404`) | `ErrNotFound` | n/a |

The pinned enum has no `terminated` member: a killed sandbox disappears from the
list and returns `404` on detail, which is why absence — not a `terminated`
value — is the stopped-sandbox signal.

## 6. The adapter package

### 6.1 Files

New package `services/agents-api/internal/sandbox/cubesandbox/`:

| File | Contents |
| --- | --- |
| `provider.go` | `Config`, `Provider`, `New`, validation helpers, ownership metadata, reference/name derivation, `Create`, `GetInfo`, `Renew`, `Kill` |
| `control.go` | The HTTP client: request builder, auth headers, status and error mapping, list-with-metadata, inspect, delete, extend TTL, response size bounds, no redirects |
| `command.go` | `RunCommand`: argv, working directory, stdin, exit code, per-stream output cap |
| `bootstrap.go` | The initialization sequence: private daemon directory, auth profile write, readiness wait |
| `provider_test.go` | Offline contract tests against `httptest` |
| `command_test.go` | Command-input and output-bound behaviour |
| `recovery_test.go` (opt-in) | Real-cluster checks gated by environment variables |

The package must not import anything outside the standard library plus this
repository's own packages, except generated protobuf types if §5.1 step 2 is
needed.

### 6.2 Config and construction

```go
type Config struct {
	InstallationID string // provider key, a stable UUID
	APIURL         string // CubeAPI base, e.g. https://cubeapi.internal/cubeapi/v1
	ProxyNodeIP    string // data-plane proxy node address
	SandboxDomain  string // e.g. cube.app
	ProxyScheme    string // http or https
	Template       string // pinned template id
	APIKey         string // secret, from a private file
	LeaseSeconds   int    // Cube idle TTL, must exceed Core's expiry window
}
```

`New(config)` rejects, with `sandbox.ErrInvalid`:

- `InstallationID` not a canonical non-nil UUID.
- Empty `APIURL`, `Template`, `APIKey` or `SandboxDomain`.
- `APIURL` not absolute, or carrying userinfo, query or fragment.
- `ProxyScheme` not `http`/`https`.
- `LeaseSeconds` below the operator floor (see §7.3).

`Config` has no readiness port field: §8.1 fixes that port for the image's probe
and for this adapter, so the package declares it once as `ReadinessPort`
(`49984`) and the image's readiness endpoint and Dockerfile use the same value.
envd keeps its own fixed port (`49983`).

The constructor builds two `*http.Client` values: one for the control plane and
one for the data plane, both with:
`CheckRedirect` returning an error (no redirect following), a bounded response
reader (`1 MiB` for control responses), and a request timeout. The data-plane
client instead sets `Transport.DialContext` to dial `ProxyNodeIP` while keeping
the virtual Host header from the URL.

### 6.3 Names, ownership and references

- Reference type is the shared `sandbox.Reference{TenantID, EnvironmentID,
  AllocationID}`; all three must be valid UUIDs.
- Ownership metadata keys use the same prefix convention as the Docker adapter:

```
io.parsar.agents-api.installation = <InstallationID>
io.parsar.agents-api.tenant       = <TenantID>
io.parsar.agents-api.environment  = <EnvironmentID>
io.parsar.agents-api.allocation   = <AllocationID>
```

- `owns(metadata, reference)` requires every key to match exactly.
- The storage descriptor is the same array form (§8.5) and is derived from
  `InstallationID + TenantID + EnvironmentID + AllocationID`, so a sandbox can
  never mount another allocation's data.
- The pinned `NewSandbox` has **no** name field, so no name is derived or sent.
  Ownership is metadata-only, and nothing depends on a name: the Docker adapter
  needs one for its container and volume names, Cube does not. Recorded here as
  the M0 reconciliation for this bullet.

### 6.4 `Create(ctx, Bootstrap) (Info, error)`

Validation, all returning `sandbox.ErrInvalid`:

- `validReference(b.Reference)`, `validID(b.SessionID)`, `validID(b.DeviceID)`.
- `CoreURL` parses, scheme is `http`/`https`, has a host, no userinfo, no query,
  no fragment.
- `Credential` is non-empty after trimming.
- `NetworkAccess` is `enabled`, `disabled` or empty.
- `AllowedDomains` must be empty. Domain-restricted policy stays unimplemented
  and explicitly rejected, matching the current hosted profile.

Then, in order:

1. `GetInfo` to detect an existing allocation: success → return the observed
   `Info` with `ErrExists`; any error other than `ErrNotFound` is returned.
2. Create the sandbox with the fields in §5.4.
3. **Return the reference even on failure.** From this point on the caller owns
   cleanup, exactly like the Docker adapter. Never erase uncertain owner state.
4. Bootstrap (§6.9).
5. Return `GetInfo`.

If the create response is lost, the caller reconciles with `GetInfo`; the
adapter never retries a create on the caller's behalf.

### 6.5 `GetInfo(ctx, Reference) (Info, error)`

1. List sandboxes filtered by the ownership metadata. Zero matches →
   `ErrNotFound`. More than one match → `ErrOwnership`; never pick one
   arbitrarily.
2. Inspect that single sandbox by id and re-verify `owns(...)`. A mismatch →
   `ErrOwnership`.
3. Fill `Info`:
   - `Reference` echoed unchanged.
   - `ProviderID` = `sandboxID`.
   - `State` per §5.6.
   - `BootstrapComplete` = `State == running` **and** the readiness endpoint
     answers 2xx **and** the daemon auth profile is present with the expected
     mode. Anything less reports `false`.

`BootstrapComplete` is provider evidence only. It must never be inferred from
"the microVM exists" or "the process started".

### 6.6 `Renew(ctx, Reference) (Info, error)`

1. Resolve as in `GetInfo`.
2. If the sandbox is not `running`, return an error. Never resume or revive it,
   matching the Docker adapter's "never synthesize an expiry or revive a stopped
   Runtime".
3. Extend the TTL using the pinned refresh call with `LeaseSeconds`.
4. Return `GetInfo`.

`Renew` changes only the expiry of the original sandbox. It must not create,
pause, resume, restart or replace anything.

### 6.7 `Kill(ctx, Reference) error`

Idempotent for absence only:

1. List owned matches.
2. Inspect every match and verify ownership **before deleting any**. Any
   ambiguity stops the operation.
3. Delete each match. `404` is acceptable; `408` and `409` are real failures and
   are reported.
4. List again and require zero remaining matches, otherwise return an
   unconfirmed-removal error.

Never prune broadly, never delete anything that fails the ownership check.

### 6.8 `RunCommand(ctx, Reference, Command) (CommandResult, error)`

Used only by trusted initialization (`installInitialFile`, `runRuntimeSetup`).

Validation, returning `sandbox.ErrInvalid`:

- `len(Args) > 0`.
- `len(Stdin) <= sandbox.MaxCommandInputBytes` (50 MiB + 32).
- `Directory` empty or absolute.
- A context deadline must be present, mirroring the Docker adapter.

Behaviour:

- Execute as the unprivileged Runtime user (uid 1000), not as root.
- Pass `Stdin` as command input. Secrets must never appear in argv.
- Preserve a non-zero exit code in `CommandResult.ExitCode`; do not treat it as
  a transport error.
- Cap stdout and stderr at 1 MiB each, as the Docker adapter does, failing the
  call rather than silently truncating.
- If the response is lost or the stream breaks, wrap the error with
  `sandbox.ErrCommandUnconfirmed`; the caller must reclaim the allocation rather
  than replay the command.

### 6.9 `bootstrap`

Preserve the existing daemon auth-profile contract (from `docker/bootstrap.go`),
because the daemon side is unchanged:

1. Build the auth JSON exactly as the Docker adapter does:
   `{"server_url": <CoreURL>, "runtime_id": <DeviceID>, "runner_credential":
   <Credential>}`.
2. Create the private directory chain under `/home/runtime/.parsar/parsar-daemon/
   default/` and write `auth.json` with mode `0600`, owner 1000:1000.
3. Ensure the `/environment` layout exists: `workspace`, `staging`,
   `initialization`, `packages`.
4. Wait, with a bounded deadline, until the readiness endpoint answers 2xx.

Implementation note: this needs a file-write path. The pinned contract provides
envd's `POST /files?path=&username=` with a raw `application/octet-stream` body
(multipart `file` as fallback), which is the chosen mechanism. The write is
followed by a trusted `RunCommand` that applies the directory modes and verifies
the resulting file mode, because envd's file API does not carry a mode. The
credential never appears in argv, environment variables, metadata or image
layers, and `stdin`-carried payloads use the pinned proto's `SendInput` and
`CloseStdin` operations.

### 6.10 Error mapping

| Cube outcome | Adapter result |
| --- | --- |
| `404` on inspect/delete | `sandbox.ErrNotFound` |
| Ownership metadata mismatch | `sandbox.ErrOwnership` |
| More than one owned match | `sandbox.ErrOwnership` |
| Create conflict for an existing allocation | `sandbox.ErrExists` |
| `400` invalid create request | `sandbox.ErrInvalid` with a status-only message |
| `408` request timeout on delete | error, never a success; caller retries cleanup |
| `409` paused sandbox cannot be admitted | error carrying the status; recorded as a known limitation (§13) |
| `503` on delete (pausing, lifecycle operation in flight, failed internal resume) | error carrying the status and `Retry-After`; cleanup stays pending and is retried |
| `5xx` or transport failure | error, joined with the context error; no state invention |
| Exec stream lost or unconfirmed | `sandbox.ErrCommandUnconfirmed` |

Error text must never include the API key, the executor credential, the access
token, a full URL with a secret, or a response body that may contain one. Return
status codes and safe identifiers only, as the retired E2B adapter did.

### 6.11 Client hardening

- No redirect following on either client.
- Bounded control-plane response bodies (1 MiB).
- Explicit per-call context deadlines; no unbounded waits.
- One request per operation; no automatic retry inside the adapter, because the
  caller owns reconciliation and replay policy.
- Log through `internal/obs/log` only, with identifiers (allocation,
  environment, sandbox id) and never with credential material.

## 7. Operator configuration and registration

### 7.1 `cmd/server/managed_runtimes.go`

The current file hard-codes a Docker-only backend in four places. All four
change, following the two-map precedent this repository already inherited once:

| Line | Now | After |
| --- | --- | --- |
| `:51` | `DisallowUnknownFields()` | Keep, but the struct gains the Cube field so the section parses |
| `:52` | `len(config.Docker) == 0` → error | Error only when **no** backend of any kind is configured |
| `:55-59` | Default provider must exist in `Docker` | Accept a match in any configured backend |
| `:71-77` | Engine provider key must exist in `Docker` | Accept a match in any configured backend |
| `:85-109` | Docker loop only | Add a Cube loop before or after it |

Additional rules:

- Provider keys are unique across **all** backend maps.
- The Cube loop reads `api_key_file`, rejects an empty file, constructs the
  provider, and registers it under the same key. `closeAll` must also release
  the Cube clients (HTTP transports), so the returned cleanup function covers
  both backends.
- Cube validation failures must not leave a partially built provider map; return
  the produced error with an empty cleanup, exactly as the Docker loop does.

### 7.2 Configuration schema

```go
type managedCubeConfig struct {
	APIURL        string   `json:"api_url"`
	ProxyNodeIP   string   `json:"proxy_node_ip"`
	SandboxDomain string   `json:"sandbox_domain"`
	ProxyScheme   string   `json:"proxy_scheme"`
	Template      string   `json:"template"`
	LeaseSeconds  int      `json:"lease_seconds"`
	APIKeyFile    string   `json:"api_key_file"`
	// Required by §8.5: the allowed host-mount prefix every per-allocation store
	// lives under. Not a Session-visible value.
	HostMountRoot string   `json:"host_mount_root"`
	// Required by §8.6 rule 1: the platform addresses a disabled Session must
	// still reach. Core's own host is added from CoreURL at create time.
	PlatformEgress []string `json:"platform_egress"`
}
```

Example operator file (a Cube-only deployment is valid after `:52` changes;
existing Docker entries keep working unchanged):

```json
{
  "core_url": "https://core.internal/api/v1",
  "default_provider": "<cube provider UUID>",
  "engine_providers": { "codex": "<cube provider UUID>" },
  "cubesandbox": {
    "<cube provider UUID>": {
      "api_url": "https://cubeapi.internal/cubeapi/v1",
      "proxy_node_ip": "10.0.0.20",
      "sandbox_domain": "cube.app",
      "proxy_scheme": "http",
      "template": "tpl-...",
      "lease_seconds": 43200,
      "api_key_file": "/etc/parsar/cube.key",
      "host_mount_root": "/data/shared/parsar",
      "platform_egress": ["models.internal"]
    }
  }
}
```

### 7.3 Validation rules for `lease_seconds`

- Minimum: strictly greater than Core's one-hour expiry window, with margin.
  Recommended floor `7200` (two hours), matching the constraint the retired E2B
  adapter enforced.
- Maximum: `86400` (24 hours), also matching that precedent.
- The value is the Cube idle TTL. Core still owns the effective lease through
  `kept_at`; the Cube TTL only prevents a platform-side reclaim from racing a
  live Core lease.

## 8. Runtime image, template, storage and network

### 8.1 Runtime image changes

Two additions to the existing Runtime image, nothing removed:

1. **A readiness endpoint.** Required by Cube (§2.2) and the basis of D3:
   - listens on a fixed port inside the sandbox;
   - returns 2xx **only** once the daemon has authenticated to Core and
     completed environment initialization;
   - returns a non-2xx status before that;
   - is reachable from CubeMaster during the template build and from the platform
     afterwards;
   - never discloses credentials or workspace content in its body.
   Suggested shape: a small process started alongside the daemon by the image
   entrypoint, exposing `GET /healthz` and reading the daemon's own state
   (connection established + initialization complete). Reuse an existing
   readiness signal if the daemon already publishes one; do not create a second
   source of truth.
2. **`envd`** copied from the digest-pinned `cubesandbox-base` tag, preserving the
   base entrypoint semantics so `envd` stays alive in the background while the
   Runtime's own entrypoint runs. The pinned documentation notes `envd` must run
   with `-isnotfc` (the base entrypoint adds it) for correct create-time
   environment handling.

Port layout: `envd` on its default `49983`, the readiness endpoint on a second
fixed port. Both are declared with `--expose-port`; **only the readiness port is
probed**, so the readiness contract matches our definition rather than `envd`'s.

### 8.2 Image construction

Create `services/agents-api/deploy/cubesandbox/Dockerfile`:

- `FROM ghcr.io/tencentcloud/cubesandbox-base:<pinned tag>`, digest-pinned.
- Install only what the existing Runtime image has and the base lacks. The
  existing `deploy/codex/Dockerfile` is the source of truth for packages, users
  and paths.
- Copy the Runtime bundle produced by `scripts/build-agents-runtime.sh`:
  `parsar-daemon`, the Rust helpers, `codex` plus resources,
  `requirements.toml`, `tool-env.py`, `runtime-initialize.py`, and the readiness
  binary.
- Recreate the `runtime` user at uid/gid 1000, the `/environment` layout and the
  `PARSAR_*` environment byte-compatibly with `deploy/codex/Dockerfile`, so the
  daemon and harness behave identically on both backends.
- Keep `ENTRYPOINT`/`CMD` semantics: the Runtime's own process starts after
  `envd`.

Sanity checks before any template build:

- Run the image locally and confirm both the `envd` health endpoint and the
  readiness endpoint respond as expected.
- Confirm `codex --version` matches the pinned version, as the current image
  build asserts.

### 8.3 Template creation

**Single node (recommended first deployment).** CubeMaster runs on the same host
that builds the image, so no registry is involved and the current registry-free
distribution is unchanged:

```sh
docker build -f services/agents-api/deploy/cubesandbox/Dockerfile \
  -t agents-runtime-cube:<release> "$AGENTS_RUNTIME_BUILD_DIR"
cubemastercli tpl create-from-image \
  --image agents-runtime-cube:<release> \
  --writable-layer-size 2G \
  --expose-port 49983 \
  --expose-port <readiness port> \
  --probe <readiness port> \
  --probe-path /healthz
cubemastercli tpl watch --job-id <job_id>
```

**Multi-node.** Push the same image to a private registry reachable by the nodes
and use the same command with `registry/ns/image@sha256:...`, adding
`--registry-username/--registry-password` if required. Cube's own artifact
distribution then places the template on nodes, so per-node pulls are not part
of steady-state operation.

**Fallback** if a registry is unacceptable on a multi-node cluster:
`cubemastercli tpl create-from-sandbox` (boot from a public base template,
install the Runtime, commit). It costs reproducibility and must be scripted and
re-verified per release.

### 8.4 Deployment record

Record in the release artifacts and the operator guide:

- CubeSandbox version and the source revision the contract was pinned from.
- Base image reference and digest.
- Built image digest, template id, and the template's `artifact_sha256` reported
  by `tpl watch`.
- The exact create command and the probe port/path.
- Verification output for §8.2 and §11 Tier B.

### 8.5 Storage: `/environment` and `/workspace`

Use the host-mount mechanism with a per-allocation path under an allowed prefix,
carried in the create request metadata as a JSON-encoded array:

```
[
  { "hostPath": "<prefix>/<installation>/<tenant>/<environment>/<allocation>",
    "mountPath": "/environment", "readOnly": false },
  { "hostPath": "<prefix>/<installation>/<tenant>/<environment>/<allocation>/workspace",
    "mountPath": "/workspace", "readOnly": false }
]
```

Why this satisfies the contract:

- `staging` and `workspace` both live under `/environment`, so the trusted
  atomic rename never crosses a mount point.
- `/workspace` is a second view of the same host directory for native tools,
  exactly what the Docker profile achieves with a volume subpath mount.
- The path derives from installation, tenant and allocation identity, never from
  user input, and the whole path sits under an operator-configured allowed
  prefix. A read-only mount does not prevent reading other tenants' data, so the
  prefix must be scoped per tenant exactly as the pinned documentation warns.

Operator prerequisites:

- Create the allowed prefix on every node and set `allowed_host_mount_prefixes`
  in CubeMaster. Keep the prefix narrow.
- Ensure Runtime uid 1000 owns each per-allocation directory.

Constraints to record:

- Host mounts are node-local and outside snapshots. **Pause/resume and
  cross-node snapshot restore with a raw host mount are pinned to the origin
  node.** Acceptable here because pause is deferred, but it is a hard input to
  §15: either accept node pinning, or move to a plugin-backed volume (COS/NFS)
  so any eligible node can attach the same store.
- Host mounts are not cleaned up automatically. Reclamation is Core's `Kill`
  path plus an operator retention policy for the directory.

### 8.6 Network policy

| `Bootstrap.NetworkAccess` | Cube egress policy |
| --- | --- |
| `enabled` (or empty) | Ordinary outbound allowed; platform addresses allowed |
| `disabled` | **Allow Core and platform addresses, deny everything else** |
| restricted domains | Rejected before create, matching the current hosted profile |

The exact request shape (`network` -> allow/deny lists, L3/L4 and L7 rules) is
taken from the pinned contract in M0. Two rules are fixed by this document:

1. `disabled` must never cut the daemon's WebSocket to Core or the platform's own
   addresses, or every `disabled` Session would be unable to execute.
2. The policy is applied per sandbox at create time; `RunCommand`-driven setup
   still needs network for package installation, so provisioning follows the
   existing pattern where the initializer uses its own network path and the
   public policy applies afterwards.

### 8.7 Docker-only knobs that disappear

These exist only in the Docker adapter and must be explicitly accounted for in
the deployment guide rather than silently dropped: `seccomp` JSON,
`apparmor=unconfined`, `ReadonlyRootfs`, `CapDrop`, `no-new-privileges`, masked
and read-only paths, `init: true`, tmpfs `/tmp`, pids/memory/CPU limits, and
volume subpath mounting.

Expected replacement: the virtualization boundary supplies the isolation that
Docker approximated with host-policy relaxation, and Cube resource options supply
the limits. The migration is only accepted once §11 Tier C proves the inner
harness sandbox (which needed extra seccomp allowances and
`apparmor=unconfined` under Docker) runs without relaxing host policy, and once
the native isolation probe passes.

## 9. How Core's lifecycle drives the adapter

These expectations come from `internal/execution/runtime_lifecycle.go` and
`runtime_initialization.go`. The adapter must satisfy them exactly.

| Core behaviour | Adapter obligation |
| --- | --- |
| `GetInfo` is polled every five seconds per allocation | Must be cheap and side-effect free. No create, no TTL change, no mutations |
| `running` + `BootstrapComplete` triggers `SettleRuntimeCreation` | `BootstrapComplete` must be true only from readiness evidence, or the creation is settled too early |
| `Renew` is called when the device peer exists and is open | Result must echo the same `Reference`, report `State == "running"` and `BootstrapComplete == true`, otherwise Core returns `ErrOwnership`. Any other outcome must be a real error, never a fabricated success |
| A stopped or missing sandbox must not destroy retained workspace or history | `GetInfo` returns `ErrNotFound` or a non-running state; it must never delete |
| Expiry or Session deletion | `RequestRuntimeCleanup` first, then `Kill`, then `ReleaseRuntimeAllocation` |
| Initialization runs one bounded operation per reconcile | `RunCommand` gets a 2-minute per-operation deadline inside a 30-minute total budget, and must therefore honour context cancellation |
| Initialization receipts | `ExitCode == 0`, `Stderr == ""`, stdout is a JSON receipt with `version: 1` and `outcome: "completed"`. Output above 1 MiB per stream must fail |
| Setup payloads are confidential | JSON payloads (including capability archives and skill files) arrive on stdin. They must never be written to argv, logs, metadata or the image |

The adapter does not implement scheduling, Turns, Files, Artifacts, cancellation
or expiry. Those stay in Core, unchanged.

## 10. Contract and documentation updates

| File | Required change |
| --- | --- |
| `contracts/agents-api/environments.md` | Record the remote managed-sandbox profile: what is accepted, the isolation model, the storage model, and the exact limits |
| `contracts/agents-api/workspace-placement.md` | Placement for the Cube deployment: node-local host mounts, node pinning for pause/resume (deferred), and which controls replace the Docker ones |
| `contracts/agents-api/environment-files.md` | Provider-neutral wording where Docker is currently implied |
| `contracts/agents-api/environment-templates.md` | Note the template is operator packaging configuration for both backends and never a provider selector |
| `contracts/agents-api/harness-selection.md` | Add the Cube provider example alongside the Docker one |
| `services/agents-api/HOSTED-RELEASE.md` | Add the Cube profile: image build, template creation (single node then multi-node), config example, network and storage prerequisites, and what is *not* yet supported |
| `services/agents-api/README.md` | Point to the Cube operator guide from the hosted section |
| `contracts/agents-api/README.md` | Coverage-ledger rows and remaining-gap wording for the new profile |
| `CONTRIBUTING.md` | Architecture/ownership note: Core now owns remote compute for an opt-in backend |
| `README.md` | Link the new operator guide if the entry point changes |

Rules for the wording:

- Do not claim capabilities that the acceptance run did not prove.
- State the gaps explicitly: restricted-domain policy is still rejected;
  reversible expiry is deferred; `409` on a paused sandbox is a known
  limitation; host mounts are node-local.
- Never describe the Cube template id, node names or egress internals as public
  API. The public surface remains `openai_hosted`.

## 11. Test and acceptance plan

### 11.1 Tier A — offline contract tests (always run)

`httptest` server impersonating CubeAPI. Required cases:

| Case | Expected result |
| --- | --- |
| Create happy path | `Info` with `ProviderID`, `State: running`, `BootstrapComplete` only from the readiness response |
| Create when an owned sandbox already exists | `ErrExists` and no second create request is sent |
| Create when the metadata matches another tenant | `ErrOwnership` |
| Invalid bootstrap input (empty credential, bad URL, bad network value, non-empty allowed domains) | `ErrInvalid`, no HTTP request |
| Create response lost after the server processed it | Caller reconciles via `GetInfo`; the adapter issues no second create |
| `GetInfo` with zero matches | `ErrNotFound` |
| `GetInfo` with two matches | `ErrOwnership` |
| `GetInfo` when the readiness endpoint answers non-2xx | `State: running`, `BootstrapComplete: false` |
| `Renew` when the sandbox is not running | error, and no TTL call is issued |
| `Renew` happy path | refresh call with `lease_seconds`, then `GetInfo` |
| `Kill` with a foreign sandbox present | aborts without deleting anything |
| `Kill` happy path then repeat | first succeeds and confirms absence, second is a no-op success |
| `Kill` when removal is not confirmed | unconfirmed-removal error |
| `RunCommand` non-zero exit | `CommandResult.ExitCode` preserved, no error |
| `RunCommand` stdin larger than the cap | `ErrInvalid` |
| `RunCommand` output above 1 MiB | error |
| `RunCommand` stream failure | error wrapping `ErrCommandUnconfirmed` |
| Any response with a redirect | rejected, not followed |
| Error text | contains no key, credential, access token or response body |

Run:

```sh
go test ./services/agents-api/internal/sandbox/... -count=1
go build ./...
```

### 11.2 Tier B — opt-in real-cluster checks

Environment-gated exactly like `AGENTS_RUNTIME_DOCKER_TEST_IMAGE`; when the
variables are absent the tests skip and are not counted as acceptance.

| Variable | Meaning |
| --- | --- |
| `AGENTS_RUNTIME_CUBE_API_URL` | CubeAPI base URL |
| `AGENTS_RUNTIME_CUBE_PROXY_NODE_IP` | Proxy node address |
| `AGENTS_RUNTIME_CUBE_TEMPLATE` | Pinned template id |
| `AGENTS_RUNTIME_CUBE_API_KEY_FILE` | Private key file |
| `AGENTS_RUNTIME_CUBE_DOMAIN` | Sandbox domain (for example `cube.app`) |

Cases: create → inspect → `RunCommand` → extend TTL → kill → kill again; a
second sandbox with foreign metadata must be refused; a created sandbox must
mount `/environment` and `/workspace` from the same host directory, and an
atomic rename from `staging` to `workspace` must succeed.

These prove provider observation only. They are not native or model acceptance.

### 11.3 Tier C — public end-to-end acceptance

Reuse the recorded acceptance shape from
`contracts/agents-api/user-managed-runtime-v1.md`, driven through the official
SDK plus raw HTTP against a Cube-backed `openai_hosted` Session:

1. Inline `Files.create` bytes, native command execution, two real output files,
   `Files.list` path/order/pagination, immutable Artifact download including
   foreign-tenant denial.
2. Core and daemon restart with unchanged committed Items, preserved history and
   outputs, and no repeated publication effects.
3. Cancellation of a foreground native process, stopped file effects, idempotent
   repeat cancellation, and continued execution without restarting cancelled
   work.
4. Native tools cannot read the executor credential or private staging
   witnesses; public responses contain no known private credential; private
   witnesses survive restart.
5. Native isolation probe in the style of
   `services/agents-api/tests/e2b_native_isolation.py`: history-directory
   witness, executor key, staging, PID-namespace separation, `/proc/1/root`
   aliases, denial of outer process secrets, and denial of `envd`, `sudo` and
   privileged accounts (extended for D9).
6. `make check` with the dedicated test database:

```sh
export PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_core_tests?sslmode=disable'
make check
```

Acceptance is complete only when all six pass on the recorded revision. Partial
runs are recorded as partial.

## 12. Rollout and operations

### 12.1 Deployment sequence

1. Stand up CubeSandbox on one x86_64 Linux host (bare metal with KVM, or a
   standard cloud VM using CubeSandbox's PVM kernel). Root, XFS at
   `/data/cubelet` (>= 50 GB) and the documented OS baseline are prerequisites.
2. Build the Runtime image (§8.2) and create the template (§8.3). Start with the
   single-node, registry-free path.
3. Configure Core with the Cube provider key, keeping the Docker entry if it
   exists. Point `default_provider` at Cube only when ready; a mixed
   configuration is valid either way.
4. Turn on CubeAPI authentication before exposing the API beyond the operator
   network. Core sends a key even when the server allows anonymous access.
5. Reserve the daemon WebSocket URL and firewall: microVMs must reach Core, and
   Core must reach CubeAPI and CubeProxy. Nothing else is opened.

### 12.2 Capacity

- Per-Session footprint: configure template CPU and memory to match the Docker
  baseline (2 vCPU, 2 GiB) until measured otherwise.
- Because idle sandboxes terminate in this slice (no pause), size capacity for
  concurrent live Sessions, not for total Sessions.
- Node count ≈ concurrent Sessions ÷ per-node density. Measure density on the
  target hardware with the documented benchmark rather than vendor headline
  numbers.
- Record the density measurement and the resulting node sizing in the deployment
  record.

### 12.3 Failure handling

| Situation | Expected behaviour |
| --- | --- |
| CubeAPI unreachable | `Create`/`GetInfo` fail; Core retries reconciliation and never replays a create |
| Capacity exhausted | Provider error surfaces; Core reports the failure and does not fabricate an Environment |
| Node loss | Affected sandboxes stop being observable; Core preserves the allocation record and never silently replaces it |
| Delete returns `408` | Treated as failure; the cleanup record remains and cleanup is retried |
| Delete returns `409` | Recorded as a limitation; the operator intervenes |
| Credential leak suspected | Rotate the Cube API key and the executor credential; never log them |

### 12.4 Backup and retention

- Back up PostgreSQL and the per-allocation host mount directories together; the
  Runtime's history lives in the mount.
- Deleting a Session reclaims the sandbox, but the host directory is outside the
  sandbox lifecycle. Define an operator retention window and a purge tool; do
  not rely on the platform to clean host mounts.

## 13. Risks

| Id | Risk | Status and mitigation |
| --- | --- | --- |
| R1 | `/environment` and `/workspace` cannot be two views of one store | **Resolved by design** (D5, §8.5). Must be proven in Tier B before Tier C |
| R2 | No stdin channel for in-sandbox exec | Bounded: the mechanism is fixed during M0 from the pinned contract and recorded in the package comment; the fallback is a documented file-write path |
| R3 | Cube's E2B-compat surface differs from the retired E2B adapter in paging, state names or error codes | Bounded by M0 pinning plus Tier A tests written against the pinned spec |
| R4 | Image distribution changes | **Resolved**: single node needs no registry (D8); multi-node adds a private registry |
| R5 | Core owning remote compute | **Resolved**: authorized 2026-09-22, opt-in, Docker stays default |
| R6 | Remote failures, `409`, network partitions | Bounded by the error mapping (§6.10) and §12.3. Never convert an uncertain outcome into a replay |
| R7 | Host mounts are node-local and outside snapshots | Recorded as a constraint; it becomes an explicit design input to §15 |
| R8 | Cube requires an HTTP 2xx probe and the Runtime has none | **Resolved by design**: the readiness endpoint in §8.1, which must not over-report readiness |
| R9 | `envd` widens the in-sandbox surface | Mitigated by the Tier C probe extension: `envd`, sudo and privileged accounts must all be denied to native tools |
| R10 | Idle sandboxes hold host resources because pause is deferred | Accepted in this slice; the platform still reclaims at TTL or on explicit cleanup |

## 14. Milestones

Each milestone is independently reviewable and leaves the default Docker
behaviour unchanged.

| Milestone | Content | Acceptance |
| --- | --- | --- |
| **M0** | Pin the vendor contract (§5.1); record revisions and hashes | Pinned files committed with hashes; this design corrected if the pin disagrees |
| **M1** | Registration generalization in `managed_runtimes.go` (§7.1), no Cube logic | Existing Docker configuration behaviour unchanged; new validation cases unit-tested |
| **M2** | Runtime readiness endpoint, Cube image under `deploy/cubesandbox/`, template build, deployment record | Template reaches `READY`; the readiness probe behaves correctly before and after daemon readiness |
| **M3** | Adapter read path: `GetInfo`, `Kill`, ownership, reconciliation | Tier A plus Tier B |
| **M4** | Adapter write path: `Create`, bootstrap, `Renew`, `RunCommand` | Tier A plus Tier B |
| **M5** | Public acceptance and documentation | Tier C complete; §10 documents updated; `make check` green |

## 15. Deferred: reversible expiry

Not in this slice. Picking it up later requires, at minimum:

1. A contract revision: expiry becomes recoverable for backed sessions, and the
   "expiry ends continuity" statement is narrowed accordingly.
2. An idle policy: short idle freezes, long idle checkpoints and releases.
   CubeSandbox already pauses with released CPU and memory, so no re-platforming
   is needed, only policy and Core state.
3. A uniqueness rule: a restored sandbox must be registered as the same
   Environment's continuation, the previous instance must not remain executable,
   and the resumed sandbox's dead host connections must be re-established
   through Core's existing connection gate.
4. A storage decision: host mounts are node-pinned for pause/resume, so either
   accept node pinning or move to a plugin-backed volume any eligible node can
   attach.
5. New acceptance: pause, resume, proof that workspace and native history
   survived intact, and the explicit rule that a failed resume is a recorded
   failure, never a silent replacement.

## 16. Executor checklist

Work in this worktree on `feat/cubesandbox-runtime`. Do not commit on `main`.
Run `make check` before reporting completion. Do not use `codex exec` for the
independent review.

### Execution status at handoff

Verified in this worktree: M0, M1, M3, M4 and the offline Tier A suite, plus the
M2 image assets. Blocked by the environment (no KVM host, no CubeSandbox cluster,
no Docker daemon and no dedicated test PostgreSQL in the executor environment):
template creation, Tier B and Tier C, and `make check`. Nothing below is claimed
as accepted while its box is unchecked; the deliverables for the blocked items are
the assets, gates and records a cluster run needs.

### M0 — pin the contract
- [x] CubeSandbox `openapi.yml` copied in with its source revision and SHA-256
- [x] `envd` process proto copied from the pinned upstream tag with its SHA-256
- [x] This specification corrected where the pin disagrees

### M1 — registration
- [x] `managedCubeConfig` added; unknown fields still rejected
- [x] Validation generalized: any-backend default, any-backend engine mapping, cross-map key uniqueness
- [x] Cube loop reads and validates `api_key_file`, registers the provider, and is covered by `closeAll`
- [x] Unit tests for every new validation branch
- [x] Docker configuration behaviour provably unchanged

### M2 — image and template
- [x] Readiness endpoint implemented and documented; returns 2xx only when genuinely ready
- [x] `deploy/cubesandbox/Dockerfile` stacks on the digest-pinned base, preserves uid 1000, `/environment`, and the `PARSAR_*` environment
- [ ] `envd` present and healthy; `codex --version` asserted — blocked: the image cannot be built without the Linux Runtime bundle
- [ ] Template created, watched to `READY`, and recorded with image digest, template id and `artifact_sha256` — blocked: needs a KVM host and a CubeSandbox cluster
- [x] `deploy/cubesandbox/README.md` documents prerequisites, commands and limits

### M3 — read path
- [x] `GetInfo` with metadata query, exactly-one-match, ownership re-verification
- [x] `Kill` verifies all owners before deleting any, then confirms absence
- [x] Tier A cases for `GetInfo` and `Kill` pass

### M4 — write path
- [x] `Create` with validation, existing-allocation detection, reference returned on failure
- [x] Bootstrap writes the auth profile with mode `0600` and never exposes it
- [x] `Renew` extends only the original sandbox and rejects non-running states
- [x] `RunCommand` honours argv, stdin, working directory, exit code and the 1 MiB cap
- [~] Tier A passes; Tier B (real cluster) is blocked and gated behind `AGENTS_RUNTIME_CUBE_*`

### M5 — acceptance and documentation
- [ ] Tier C items 1–6 pass on a recorded revision, with evidence retained privately — blocked: needs a Cube-backed cluster
- [x] §10 documents updated with accurate scope and explicit gaps
- [ ] `make check` green with the dedicated test database — blocked: the executor environment has no dedicated PostgreSQL and no Node/Rust toolchain, so the affected Go suites were run directly instead
- [x] Independent blind review requested with only requirements, acceptance criteria, boundaries, repository path and baseline
- [ ] In-scope blockers from that review fixed

### Guardrails for every milestone
- [x] Never log, echo or persist the Cube API key, executor credential or access token
- [x] Never replay a create or an uncertain command
- [x] Never delete a sandbox that fails the ownership check
- [x] Never make a stopped, paused or missing sandbox authorize destroying retained state
- [x] Never let a Cube concept (template id, node, egress internals) reach the public API or Web













