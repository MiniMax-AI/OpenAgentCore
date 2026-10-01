# Process protocol

The process protocol is how the agent host starts and controls processes in a sandbox. The Sandbox I/O service in the sandbox serves it, and the agent host's broker is its client. It launches a process from an explicit spec, streams its output as ordered events, accepts stdin at exact offsets, and reports the leader's exit, the end of output and the end of the process scope as separate facts.

[`internal/sandboxprocess/protocol.go`](../internal/sandboxprocess/protocol.go) is the authored definition: message tags, payload layouts, validators and the `Service` interface. The same package has the generic client and server. [`apps/sandboxio/internal/processservice`](../apps/sandboxio/internal/processservice) is the Linux service. Frames use the shared [framing](sandbox-link-protocol.md#framing), and the Link layer supplies the authenticated attachment of each stream.

## Streams and operations

- A stream belongs to one attachment. No request names an attachment, an OS user or a credential; the stream's attachment scopes every operation ID it uses.
- A request's RequestID follows the [request ID rule](sandbox-link-protocol.md#framing), and its response carries the same RequestID. Requests on one stream run concurrently, so responses can arrive in any order. A service may refuse a request beyond its concurrency limit with `Busy` and `EffectNone`.
- An operation is one launch, named by an `OperationID` the client chooses. Its record lives in one service incarnation, named by `ServerInstanceID`.
- Events are frames with RequestID 0. Each carries its `OperationID` and a `Sequence` that starts at 1 and grows by one per event of that operation.
- An operation has one observer: the stream that started it, or the stream of its latest `Attach`. A later `Attach` moves the observer. The response to the `Start` or `Attach` that subscribes a stream arrives before any event it subscribes.

## Implement a client

The Go client is `sandboxprocess.NewClient(stream)`. `Start` and `Attach` return an `Operation` handle whose `Events()` channel delivers that operation's events once each, in sequence order; when `Start` finds an existing operation, the client attaches from sequence 0. The handle offers `WriteStdin`, `CloseStdin`, `CloseOutput`, `Resize`, `Signal`, `Cancel`, `Ack`, `Inspect` and `Release`.

1. Call `Describe`. Keep `ServerInstanceID`, and check each spec and signal against the `Capabilities` before sending it. The Go client keeps the capabilities and splits stdin writes into chunks of `MaxDataBytes`.
2. Choose a new `OperationID` for each launch. After a `Start` failure with `EffectPossible`, repeat `Start` with the same ID and spec: `Existing` means the launch already happened. Never choose a new ID for work whose launch is uncertain.
3. Process events, then `Ack` the last processed sequence so the service can reclaim its replay buffer. After a stream loss, `Attach` with the last processed sequence on a new stream.
4. Write stdin at the offset the service has accepted. After a stdin failure with `EffectPossible`, `Inspect` to learn the accepted offset before deciding what to resend. The client never retries a stdin write or a signal on its own.
5. On `InstanceChanged` the service restarted. The operations of the old incarnation are unknown, and their IDs cannot be reused to find out.

## Implement a service

Implement `sandboxprocess.Service` and serve each stream with `sandboxprocess.Serve(ctx, stream, attachment, service)`. `Serve` decodes and validates requests, answers a malformed payload with `InvalidArgument`, ends the stream on a framing violation, and returns a method's `*Failure` as the typed failure. It runs up to 64 requests of a stream at once and answers each request beyond that with `Busy` without running it; it never stops reading, and it ends the stream when another 64 refusals are waiting to be written. A method sends events with `Conn.Send`, which blocks while the peer is not reading.

A service must:

- generate a new `ServerInstanceID` whenever its operation records are lost, and answer requests for any other incarnation with `InstanceChanged`;
- advertise only what it enforces, and reject anything else in a spec or signal request with `Unsupported`;
- keep every operation record for the whole incarnation, as described in [Deduplication and tombstones](#deduplication-and-tombstones);
- never drop an event it has not been told was delivered, as described in [Output, replay and flow control](#output-replay-and-flow-control);
- treat the loss of a stream as nothing more than the loss of an observer, as described in [Ownership](#ownership).

The Linux service calls `processservice.Init()` first in the binary's `main`. Go cannot set a child's umask, so each launch re-executes the service binary as a trampoline that reads the launch from an inherited descriptor, marks every inherited descriptor above 2 close-on-exec, applies the umask and working directory, and execs the target. `Init` runs that trampoline and returns at once in a normal start. The Linux service launches every operation in a new session with `setsid`, observes the session through `/proc`, and advertises `ScopePOSIXSession` only. It requires `pidfd_open` and `pidfd_send_signal` (Linux 5.3 or later): without them `processservice.New` fails with `ErrPidfdUnsupported`.

Before serving, `main` makes the process a child subreaper (`prctl(PR_SET_CHILD_SUBREAPER)`) and runs `processservice.Reap(ctx)` for the life of the process. `Reap` is the process's only `wait`: it reaps every child, delivers each leader's exit to its operation, and reaps the orphaned descendants the subreaper inherits. Nothing else in the binary may wait for children, and no operation observes an exit while `Reap` is not running. When the binary stops, it calls `Shutdown(ctx)` after its streams have ended: `Shutdown` cancels every live operation as [ownership cleanup](#ownership) does and returns once each scope has closed or `ctx` ends.

## Reference

### Requests

Every request except `Describe` begins with `ServerInstanceID` and `OperationID`. A response begins with a success or failure discriminator; a failure carries a [`Failure`](#errors).

| Tag | Request | Fields | Response | Meaning |
| --- | --- | --- | --- | --- |
| 1 | `Describe` | – | `ServerInstanceID`, `Capabilities` | The incarnation, capabilities, limits and cleanup policy |
| 2 | `Start` | `Spec` | `Disposition` (`Created` or `Existing`) | Reserve the ID, validate the spec, then launch once. `Created` subscribes this stream. `Existing` changes nothing; `Attach` observes the operation |
| 3 | `Attach` | `AfterSequence` | `Status` | Observe from the event after `AfterSequence`. Never launches |
| 4 | `Inspect` | – | `Status` | The current record |
| 5 | `WriteStdin` | `Offset`, `Data` | `Accepted` | Write at the current stdin offset; return the bytes accepted |
| 6 | `CloseStdin` | `Offset` | – | Close pipe stdin after `Offset` accepted bytes. Idempotent at the same offset. `Unsupported` for a PTY |
| 7 | `CloseOutput` | `Stream` | – | Close the read side of one stream. The writer sees the native pipe or PTY behavior, and the stream ends `Abandoned` |
| 8 | `ResizePTY` | `Rows`, `Cols`, `XPixels`, `YPixels` | – | Set the terminal size; the foreground job gets `SIGWINCH` |
| 9 | `Signal` | `Signal`, `Target` | – | Deliver a declared signal to a declared target |
| 10 | `Cancel` | `GraceMillis` | – | Send TERM to the scope, then KILL after the grace, capped at `CancelGraceLimitMillis`. Completion arrives as events. During `Starting` the cancel is kept and applied as soon as the process launches |
| 11 | `AckEvents` | `Sequence` | – | Acknowledge events through `Sequence`, releasing them from the replay buffer |
| 12 | `Release` | – | – | Release a settled operation's resources and keep its tombstone. `Busy` for an unsettled operation |

`Status` holds `State`, `Exit` (present in state `Exited`), `StartFailure` (present in state `StartFailed`), `StdinOffset`, `StdinClosed`, `Output` (the disposition, present once every stream has closed), `Scope`, `Released`, and the retained event range `FirstRetained` through `LastSequence`, empty when `FirstRetained` is greater.

| Enum | Values |
| --- | --- |
| `OperationState` | `Starting`, `Running`, `Exited`, `StartFailed`, `Unknown` (the exit could not be observed) |
| `ScopeState` | `Active`, `Closed`, `Unknown` (the scope could not be observed; the service keeps trying, and `Closed` can still follow) |
| `OutputDisposition` | `Drained` (end of file), `Abandoned` (after `CloseOutput`), `Lost` (a read failed) |

An operation is settled when it failed to start, or when its exit was observed or lost, all its output has closed, and its scope is `Closed`. While the scope is `Unknown` the operation is not settled, so `Release` returns `Busy` until the service confirms the scope closed. `Release` never turns an `Unknown` state into a confirmed result.

### Events

| Tag | Event | Fields | Meaning |
| --- | --- | --- | --- |
| `0x4001` | `Started` | – | The launch succeeded |
| `0x4002` | `StartFailed` | `Failure` | The launch failed. No other event follows, and the record is kept. The effect is `EffectNone` only when the target was provably never executed |
| `0x4003` | `Output` | `Stream`, `Offset`, `Data` | Output bytes. `Stream` is `Stdout`, `Stderr` or `Terminal` |
| `0x4004` | `StreamClosed` | `Stream`, `Offset`, `Disposition` | One stream ended at its final offset |
| `0x4005` | `Exited` | `ExitCode`, or `ExitSignal` and `CoreDumped` | The leader's wait result |
| `0x4006` | `OutputClosed` | `Disposition` | Every captured stream closed. The disposition is the worst of the streams': `Lost`, then `Abandoned`, then `Drained` |
| `0x4007` | `ScopeClosed` | – | The operation's scope is empty |
| `0x4008` | `ObservationLost` | `Observation` (`Exit` or `Scope`), `Failure` | A required observation became unavailable. The matching state becomes `Unknown`, and the effect is `EffectPossible`: processes may still run. A lost scope observation can recover, and `ScopeClosed` then follows |

Ordering:

- `Started` or `StartFailed` comes first.
- `Exited` and `OutputClosed` are independent. A background process holding a stream open keeps `OutputClosed` pending after `Exited`, and output can close before the leader exits.
- `Exited` follows every output byte buffered in a captured stream when the service reaps the leader, so the client sees everything the leader wrote before it exited, as a native parent does once `waitpid` returns. Output written later, by processes that still hold the stream, can follow `Exited`, as it does natively.
- `Exited` never waits for an acknowledgement: when the [replay limit](#output-replay-and-flow-control) stops the service reading, `Exited` follows the output read until then, and the rest follows `Exited`. With a PTY, Linux passes terminal output to the master through an asynchronous kernel queue, so output still in that queue when the service reaps the leader can follow `Exited`.
- `OutputClosed` follows every output byte the service will deliver, and each `StreamClosed`.
- Output of different streams has no order relative to each other or to changes in the file system.

### Process spec

A launch uses only the spec; the service's own environment, directory and umask never reach the process.

| Field | Rule |
| --- | --- |
| `Executable` | NUL-free bytes. A name without `/` is looked up in the spec's `PATH` entry like `execvpe`, and a spec without `PATH` cannot use one. A relative path with `/` resolves against `Cwd`. A missing file fails the launch with `NotFound` |
| `Argv` | The complete argv, including argv[0], unchanged. At least one entry |
| `Env` | The complete environment: unique names without `=` or NUL, values without NUL |
| `Cwd` | An absolute path |
| `Umask` | At most `0o777` |
| `IOMode` | `IOPipes` or `IOPTY`. Only descriptors 0, 1 and 2 are connected |
| `PTY` | Present exactly for `IOPTY`: the size, `Term` and the terminal modes |
| `Scope` | `ScopePOSIXSession` or `ScopeCgroupV2` |

Launch failures map the OS error: a missing file or directory is `NotFound`, a permission error is `Unauthorized`, an unexecutable file is `InvalidArgument`, a resource limit is `ResourceExhausted`, and anything else is `IO`.

### Deduplication and tombstones

- Operation IDs are scoped to `(AttachmentID, ServerInstanceID, OperationID)`.
- `Start` reserves the ID before launching and keeps the SHA-256 digest of the encoded spec. The same ID with the same spec returns `Existing`, including for concurrent requests; a different spec returns `OperationConflict`.
- Records last for the whole incarnation. `Release` keeps a tombstone with the digest, the state and the results; a `Start` for a released ID returns `Released`.
- Records are never evicted. When `MaxOperationRecords` or `MaxActiveOperations` is reached, `Start` fails with `ResourceExhausted`.

### Stdin offsets

The stdin offset counts the bytes the service has accepted, from 0. `WriteStdin` and `CloseStdin` succeed only at the current offset; any other offset returns `InputOffsetConflict`, so an old or overlapping write never reinjects bytes. `Accepted` can be less than the data sent. After the process closes its stdin, writes return `StdinClosed`.

### Output, replay and flow control

- The service retains each operation's events until they are acknowledged, and delivers them to the observer in order.
- Unacknowledged `Output` data of all the operation's streams together is limited to `MaxReplayBytesPerOperation`. At the limit the service stops reading the process's output, so the process blocks on its own writes. Nothing is discarded.
- A slow observer holds back its stream: the service writes events only as fast as the peer reads them.
- `Attach` resumes after any sequence in the retained range. A request for acknowledged events returns `ReplayGap`, so missing output is never skipped silently. The events an accepted `Attach` promised stay retained until they are sent, even when another stream acknowledges them first.

### PTY

`PTY` sets the size in rows, columns and pixels, the `TERM` value (the environment must not set `TERM` itself) and terminal modes as RFC 4254 §8 opcode and value pairs, plus `IUTF8` (42) from RFC 8160. A control-character mode takes the character, and 255 disables it; a flag mode takes 0 or 1. Requested modes must appear in `Capabilities.PTYModes`. Only the terminal semantics come from SSH, not its transport.

With a PTY, output arrives as the `Terminal` stream, stdin writes are terminal input, and there is no stdin half-close: to signal end of input, write the terminal's EOF character, `^D` by default. The terminal output ends `Drained` when every process has closed the terminal. `CloseOutput` on the `Terminal` stream closes the terminal, which hangs up its session.

### Scope and signals

Both scopes start the process in a new POSIX session. `ScopeCgroupV2` also places it in a new cgroup and is advertised only when the service enforces that. A descendant can leave a `ScopePOSIXSession` scope by calling `setsid`; that is the scope's limit. The Linux service observes a session scope by polling `/proc` for live members, so `ScopeClosed` is best-effort within that limit.

A request never names a process ID. `Signal` takes a signal number from `Capabilities.Signals` and one of these targets:

| Target | Receives the signal |
| --- | --- |
| `TargetLeader` | The launched process |
| `TargetInitialProcessGroup` | The leader's process group, which the session created |
| `TargetPTYForegroundGroup` | The terminal's foreground process group. `InvalidArgument` without a PTY |
| `TargetScope` | Every process in the scope: the session's members, or the cgroup |

A target with no process returns `NotRunning`, and so does every target after `ScopeClosed` and a `TargetPTYForegroundGroup` whose terminal has no foreground group.

A signal reaches only a process the service can prove is in the operation's session, never one that reused a PID or a session ID. While the leader is unreaped, its PID pins the session and initial process group IDs, and the Linux service signals the leader or that group by ID. Otherwise it signals through the pidfds it holds for the session's processes, and only those a refresh in the same request proved still in the session; when that refresh fails, it signals nothing more and the request fails with `IO`. It takes a pidfd for a process showing the session ID only when a process it already holds stayed in the session across that read, and it updates the held set before reaping any held process. When it holds no process but a live process still shows the session ID, it cannot tell the session from a new one with the same ID: it signals nothing, the target returns `NotRunning`, and the scope becomes `Unknown` with `ObservationLost`.

### Ownership

Losing a stream only loses the observer; the operation continues and any stream of the same attachment can `Attach` to it. When the Link layer reports that the attachment's ownership lapsed, the service waits `OwnerLossGraceMillis`. If ownership returns within the grace, nothing happens. When the grace expires or ownership is revoked, the service cancels each of the attachment's live operations with TERM, then KILL after `CancelGraceLimitMillis`; an operation still starting is cancelled as soon as it launches. From then on a `Start` of a new operation on that attachment returns `StaleAttachment` until ownership returns.

### Capabilities

| Field | Meaning |
| --- | --- |
| `Platform` | `PlatformLinux`: signal numbers and terminal semantics are Linux's |
| `Scopes`, `IOModes`, `Signals`, `SignalTargets`, `PTYModes` | What a spec or signal request may use |
| `MaxStartBytes` | The largest encoded `Start` payload |
| `MaxDataBytes` | The largest stdin write and output chunk, at most 64 KiB |
| `MaxActiveOperations` | Operations not yet settled |
| `MaxOperationRecords` | All records of the incarnation, tombstones included |
| `MaxReplayBytesPerOperation` | Unacknowledged output retained per operation |
| `OwnerLossGraceMillis` | How long operations survive lapsed ownership |
| `CancelGraceLimitMillis` | The longest `Cancel` grace, and the grace of ownership cleanup |

Deduplication, replay, pipe half-close and separate exit and output completion are protocol semantics that every service implements, not capabilities.

### Errors

A failure carries a `Code`, an `Effect` and a message. `EffectNone` means the request had no effect; `EffectPossible` means it may have. The client reports a transport loss after sending a request as `IO` with `EffectPossible`. When the caller's context ends before the Go client writes a request, the failure is `Cancelled` or `DeadlineExceeded` with `EffectNone`; when it ends during the write, the client closes the stream and the failure has `EffectPossible`; when it ends after the write, the stream stays open and the failure has `EffectPossible`. A cancellation that races the end of a write may still close the stream, and the requests in flight on it then fail with `EffectPossible`.

| Code | Meaning |
| --- | --- |
| `InvalidArgument` | The request breaks a rule, such as an event sequence beyond the last one |
| `Unsupported` | The request uses something the capabilities do not declare |
| `Unauthorized` | The OS denied the launch |
| `StaleAttachment` | The attachment is no longer valid, or its operations were cleaned up and ownership has not returned |
| `InstanceChanged` | The request names another incarnation |
| `NotFound` | No such operation, or the executable or directory is missing |
| `OperationConflict` | The ID is in use with a different spec |
| `Released` | The operation was released |
| `ReplayGap` | The requested events were acknowledged and are gone |
| `InputOffsetConflict` | The stdin offset is not the current one |
| `StdinClosed` | Stdin is closed |
| `OutputClosed` | The stream or terminal is already closed |
| `NotRunning` | The target has no process, or the operation is still starting |
| `Busy` | The operation has not settled, or the stream already runs as many requests as the service allows |
| `ResourceExhausted` | A declared limit or an OS resource is exhausted |
| `DeadlineExceeded`, `Cancelled` | The caller's deadline passed or it gave up |
| `IO` | The service or transport failed |
| `Unknown` | Any other failure |

## Verification

`go test ./internal/sandboxprocess` checks the golden frames in `internal/sandboxprocess/testdata`, and `go test -fuzz FuzzDecode ./internal/sandboxprocess` fuzzes the decoder. `go test ./apps/sandboxio/internal/processservice` runs the Linux service over in-memory streams with real processes.
