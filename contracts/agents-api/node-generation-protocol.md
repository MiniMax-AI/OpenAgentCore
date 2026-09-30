# Sandbox node protocol

A sandbox node runs the Docker or microsandbox Provider on its host and connects to Core over one WebSocket. Core sends Provider operations over that connection; the node runs them against its local provider and reports readiness, host measurements and the deployment generations it holds. Core stays the only lifecycle owner: the node never retries a mutation or schedules work. The frames and validators live in [`services/core/internal/sandbox/node`](../../services/core/internal/sandbox/node) (`wire.go`, `generation_wire.go`); the HTTP routes a node uses to enroll and read its configuration are in the [machine connection API](machine-api.md#node-routes).

## Frames and version

Every frame is one JSON text message whose `version` equals `node.ProtocolVersion`; both peers reject any other version, and there is no fallback decoder. Member names are exact and unique: unknown members, case aliases, duplicates and unexpected nulls are rejected. Control frames (`hello`, `welcome`, `heartbeat`, `heartbeat_ack`, `retention`, `retention_ack`) are at most 32 KiB; `request` and `response` frames at most 72 MiB. An invalid frame closes the connection.

## Connection

1. The node dials `/api/v1/sandbox-node/connect?node_id=<uuid>` on its stored Core origin (`wss` for `https`) with its node credential as a Bearer header. Core answers 401 to a rejected credential, which the node treats as permanent; any other failure, including a 403 from a proxy, is retried with bounded backoff. Core refuses a second connection for a node identity while one is opening, live or closing, with 409.
2. Within 15 seconds the node sends `hello` with its identity (`node_id`, `installation_id`, `provider`, `backend_fingerprint`, the enrolled `deployment_generation` and `specification_digest`, `max_active`, `max_retained`), its first health report and, when it can prepare and retain several deployment generations, `generation_management: true`. Core closes the connection unless the identity matches the authenticated node.
3. Core records the node's presence, then replies `welcome` with a new `connection_id`, the current `owner_epoch` and, for a generation-managing node, `deployment`: the target `generation`, its `specification_digest` and a nullable `serving_generation`. The node stores a higher owner epoch and refuses a lower one.
4. Every 10 seconds the node sends `heartbeat` with `connection_id`, `owner_epoch` and health. On each heartbeat Core authenticates the node credential again and checks the owner epoch, records the health and replies `heartbeat_ack`, with `deployment` for a generation-managing node. Either peer closes the connection after 35 seconds without a frame.

Core counts a node as online while it is connected under the current owner epoch and its last heartbeat is less than 45 seconds old. A heartbeat establishes provider readiness and the last host measurements, never Session activity.

Health carries `provider_ready`, an optional fixed `diagnostic`, `observed_at`, `active_operations` (at most 32) and the host measurements that the [Runtime telemetry API](runtime-observability-api.md#node-host-observations-and-history) reports. A node without generation management probes its provider for every report; an unready provider reports one fixed diagnostic code, classified from typed probe errors, and the probe text and host paths stay on the node. Core stores an unknown code as `provider_unavailable`. The [nodes guide](../../docs/getting-started/nodes.md#readiness-codes) lists the codes and their causes. A generation-managing node reports readiness per generation instead, as described below.

## Provider requests

Core sends `request` frames with:

| Field | Meaning |
| --- | --- |
| `id` | New UUID per request |
| `sequence` | Increases by exactly one per request on this connection |
| `connection_id`, `owner_epoch` | The values from `welcome` |
| `deployment_generation` | The generation the allocation belongs to, separate from its compute generation |
| `operation` | One of the operations below |
| `timeout_ms` | Remaining budget, 1 to 120000 |
| `reference` | The exact `(tenant_id, environment_id, allocation_id)` |

Each operation carries its own arguments and returns the following result on success:

| `operation` | Provider method | Arguments | Successful result |
| --- | --- | --- | --- |
| `create` | `Create` | `bootstrap` | `info` |
| `info` | `GetInfo` | None | `info` |
| `renew` | `Renew` | None | `info` |
| `kill` | `Kill` | None | None |
| `command` | `RunCommand` | `command` | `command` |
| `observe` | `Observe` | `observation` | `sample` |
| `initial` | `Initial` | None | `compute` |
| `new_compute` | `NewCompute` | Positive compute `generation` and optional `snapshot` | `compute` |
| `compute` | `GetCompute` | `compute` | `state` |
| `kill_compute` | `KillCompute` | `compute` | None |
| `resume_compute` | `ResumeCompute` | `compute` | `state` |
| `command_compute` | `RunCommandCompute` | `compute` and `command` | `command` |
| `suspend` | `Suspend` | `suspend` | `state` |
| `resume` | `Resume` | `resume` | `state` |
| `delete_snapshot` | `DeleteSnapshot` | `snapshot` | None |

A request whose `connection_id`, `owner_epoch` or `sequence` does not match closes the connection. A malformed request gets an `invalid` response. A node without generation management accepts only its enrolled `deployment_generation`; a generation-managing node runs the request on that generation's provider and answers `unconfirmed` when it cannot. Core sends `create` and a `resume` that is not observe-only only to a generation that is ready on that node, and keeps at most 32 requests pending per connection.

The budget is relative: the node anchors `timeout_ms` to its own clock on receipt and consumes it while the request waits in its queue, so the hosts' clocks need not agree. Core still bounds its own wait. A full node queue closes the connection.

The `response` frame carries `id` and `connection_id`. A successful response carries the result named in the operation table, with no result field for `kill`, `kill_compute` or `delete_snapshot`. A failed response carries an `error_code`:

| `error_code` | Meaning |
| --- | --- |
| `invalid`, `ownership`, `exists`, `not_found` | `ErrInvalid`, `ErrOwnership`, `ErrExists`, `ErrNotFound` |
| `command_unconfirmed` | `ErrCommandUnconfirmed` |
| `observation_unavailable`, `runtime_not_running` | The observation outcomes |
| `unsupported` | The operation is declared unsupported; see below |
| `unconfirmed`, or any other value | The outcome is unknown |

A failed response carries no result, except an `info` that is an exact-reference `CreateSettled` receipt: a confirmed native Create that failed a later check can still prove that the attempt settled. A timeout, a lost response or a disconnect is unavailable or uncertain, never evidence of absence, and Core never replays a mutation after one; it observes the original operation instead. The [Sandbox Provider guide](../../docs/sandbox-provider.md#operation-outcomes-and-retries) defines each outcome.

Node startup and generation loading validate complete Provider operation declarations before accepting work, and the Core proxy uses the same registered declaration, so an unsupported operation rejects before node resolution or native I/O. The [operation contract](../../docs/sandbox-provider.md#explicit-operation-contracts) owns the inventory. An `unsupported` response carries an `unsupported` object with the exact method `operation` and an authored safe `reason`; the proxy checks both against the request. Missing, malformed or mismatched evidence is an unconfirmed result, never proof that a mutation was rejected. Unsupported stays distinct from observation unavailability and unknown compute or command results, and it neither settles resource ownership nor authorizes a replay.

## Generation control

A node without generation management serves only its enrolled generation, with fixed configuration, and receives no preparation or retention frames. A generation-managing node prepares the target generation Core announces in `welcome` and `heartbeat_ack` and keeps serving its durable serving generation while it does; target preparation is independent of the serving provider's readiness.

Its `hello` and heartbeats carry at most eight generation observations. Each names a positive signed-64-bit generation, its lowercase SHA-256 specification digest, a `ready`, `preparing` or `failed` state and an optional fixed diagnostic. The target and serving generations come first; other records rotate fairly. Eight bounds one message, not the number of generations a node may keep. An omitted observation never authorizes deletion or implies absence.

Retention uses its own bounded exchange. A `retention` request names at most eight local `(generation, specification_digest)` references, a UUID, a sequence that increases by one, the current `connection_id` and the owner epoch. The `retention_ack` must match the complete pending request, entry order and identity included, and give an explicit boolean `keep` for every entry. One exchange is pending per connection, and a disconnect discards it. An unsolicited, replayed, stale, partial or mixed acknowledgement deletes nothing. Retention traffic never uses the Provider request queue.

## Local retention and helper lifetime

A Core drop grant is necessary but not sufficient for collection. Queued and running provider calls, preparation, the local target and the serving pin all keep references; collection rechecks them and refuses on an already canceled connection. A canceled provider caller does not prove that its native helper stopped: the node counts a helper until its actual `Wait` returns.

Every generation owns a permanent private lease file, `state/node/generations/<generation>.lease`. Before starting a native helper, the node takes a shared flock on it, validates the durable lease identity under that lock, and requires the published final provider configuration and no preparing, collecting or dropped journal. The helper inherits the descriptor; the node closes its own copy only after `Wait` and never unlocks the shared open-file description, so a canceled caller or a node exit does not release a live helper's reference. Native helpers set the descriptor close-on-exec before calling the SDK, so VM and daemon descendants do not inherit it.

Collection takes the exclusive nonblocking flock before it inspects references, removes shared images or release files, or publishes the dropped marker, and holds it through those changes. Lease files belong to stable node state and are never removed or replaced during collection; symlinks, multiply linked files, foreign ownership, unsafe permissions and replaced lock paths are refused. Before the first helper can start, the installer creates the lease exclusively and fsyncs it, then atomically persists and fsyncs a private `.lease-identity` record and its directory. The record binds installation, generation, specification digest, device and inode. The Python collector and the Go helper opener validate the same record on every open, including after a restart; neither adopts a missing identity, replaces its inode or erases it after collection. An installation interrupted before the identity is durable refuses re-adoption and is kept for inspection. A removed identity or a replaced lease refuses even when its current metadata agree.

A dropped generation can never be prepared or used again. A helper's exit is evidence about local files only, not proof that a remote mutation or an uncertain provider receipt has been released; Core's durable allocation and placement retention stays independent.

## Matched fresh installation

The host program release and Core's selected Runtime release are independent. A fresh node gets its executable and private preparer from the console's current release, and reads the exact Runtime source, image identities and native runtime and firmware digests from its authenticated configuration. When that Runtime is older, the console still serves its immutable `releases/<source>/` manifest, checksums and allowlisted artifacts: the Runtime helper, firmware, seccomp profile and image bytes come from the selected release, and artifact URLs stay pinned to their verified manifest even if the console's current release changes during a download.

A missing retained release refuses the installation rather than substituting the current Runtime, and so does a local bundle that holds only a different Runtime. These refusals happen before the installer writes the node identity, imports the Runtime, registers the node or starts its service.

A published console release keeps its metadata and artifact bytes. Publishing it again first validates all metadata and every existing declared artifact, then may atomically add only missing, checksum-matched declared artifacts; any conflict prevents every addition, and nothing is overwritten.

## Restart recovery

Missing Runtime bytes never move a pinned placement to the current Runtime. The node keeps the original generation and specification digest as unready and asks Core's authenticated configuration route for that exact generation before recovery. Missing seccomp bytes may leave an unready provider placeholder; missing image or native artifacts found by a provider probe queue a repair without advertising readiness.

Preparation and repair are serialized. Target and serving generations come first, with bounded progress on the others. Each attempt has a 30-minute deadline, and failures back off for 1, 2, 5 and 10 minutes, then at most 30. Nothing is prepared before the connection has delivered deployment facts. Repair keeps existing configurations and paths, verifies the selected release and every existing sibling checksum, and downloads only missing immutable files. Conflicting bytes or a different retained specification refuse repair, and a missing provider configuration without an exact preparation plan is refused rather than reconstructed.

Repair takes the same exclusive generation lease and installation lock as collection, so a live helper or a running collector keeps ownership and repair retries later. After the bytes are restored, the node still runs the provider's readiness probe; file presence and executable capability never establish readiness.

## Preparation and collection records

New preparation writes two distinct records. Before downloads, `.preparing` holds the installation, generation and specification identity, the private provider paths and `import_started: false`; it is a recovery and collection plan, not a provider. Before the importer runs, the plan records `import_started: true`. Python retention discovery and Go restart recovery both recognize pending-only plans but never build, probe or acquire a provider from them, and recovery or collection still needs authorization on the current connection.

Only a successful preparation publishes the final `.json` provider configuration, which is write-once. Docker records the immutable local image ID its resolver returns; either the builder's config identity or the manifest identity can be valid for the same specification. Publication is durable before the preparation journal is cleared. An interruption between the two revalidates the same plan and final identity; plan, specification or path drift refuses. A canceled or failed import stays visible to Core's retention exchange without becoming a serving generation.

Before any native or release deletion, the node persists a private collection journal bound to its installation, generation and specification digest. After a restart an unfinished journal is only a retention-exchange candidate: it cannot prepare, probe, acquire or advertise that generation, and a fresh correlated Core drop grant is needed to resume. Native completion is persisted before release files are removed, so a retry can finish a partly removed release without running an already removed helper. The digest-bound dropped marker follows durable file cleanup and prevents re-adoption; the small configuration and ownership journals stay as local identity records.

For the installation-private microsandbox store, a successful, complete native image inventory distinguishes absence from a CLI failure; a failed query, malformed inventory, native in-use refusal or unknown ownership keeps the bytes. Shared microsandbox images and private releases stay until their last local reference. Docker images belong to the host's shared daemon: automatic collection never removes or prunes them, and only the host administrator can remove them after confirming that no installation on the host needs them.

A fresh installation also records the verified checksums of its Runtime files separately from the host program. Collecting the original generation removes only the exact private Runtime helper, executable, firmware, seccomp and import-cache files that no retained configuration references. Every remaining file is checked before the first deletion; an unknown hash, changed bytes, links or missing ownership metadata refuse cleanup. The node executable, preparer, identity, base provider configuration and manifests stay, so a restarted node can still read its enrolled identity and construct a newer retained provider. Shared native paths are compared across all retained configurations before removal.

An interrupted download repairs only missing bytes at the original paths. When collection comes before any import attempt, the preparation journal proves that the generation has no imported native image. A generation whose native executable is missing and whose import may have started stays retained: missing files never prove native absence, and an empty native inventory never erases receipt or store history.

The diagnostic codes are authored in `services/core/internal/sandbox/node_diagnostic.go`. The shared `services/core/internal/sandbox/testdata/node-diagnostics.json` fixture checks the Go mapping, OpenAPI source annotations and generated enums, and the TypeScript client declaration. Web uses the client normalizer and checks localized messages for every declared code. Update these projections with a code change; unknown codes normalize to `provider_unavailable`.

Preparation diagnostics keep fixed typed causes. Only artifact transfer, checksum or release-provenance failures report `runtime_download_failed`; the private preparer signals that class through its exit category, without Core or the node parsing stderr. Provider, ownership, cancellation and unclassified failures keep their typed code or `provider_unavailable`. No raw provider text crosses the protocol.
