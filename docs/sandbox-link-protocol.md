---
title: "Sandbox link protocol"
---

The Link protocol connects the two ends of sandbox I/O through a relay. The Sandbox I/O service runs inside a sandbox and serves it: it is the serve peer. The agent-host Runtime runs a Harness outside the sandbox and uses the sandbox through that service: it is the attach peer. Each peer authenticates its own link to the relay. The relay authorizes every service stream the attach peer opens, binds it to the current serve peer of the resource and then copies bytes between the two streams without reading them. Service frames never carry a credential or a grant.

The authored definition is [`internal/sandboxlink/protocol.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/protocol.go). The same package holds the serve and attach peer libraries; the relay core is [`internal/sandboxlink/relay`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/relay/relay.go). The service's startup input is the [Sandbox bootstrap](./sandbox-bootstrap.md). This document also owns the [frame layout](#framing) every sandbox I/O protocol uses.

## How a link works

A link is a WebSocket over TLS to the relay. Every WebSocket message is binary and carries the next bytes of one byte stream, on which yamux multiplexes streams. The peer is the yamux client. The first stream it opens is the control stream, which carries the Hello and then control requests and events. Every later stream carries exactly one service protocol:

1. The attach peer opens a stream and sends `Open`.
2. The relay has its Authority authorize the Open, opens a stream to the resource's current serve peer and sends `Bind`.
3. The serve peer answers `Bound` and the relay answers the attach peer `Opened`. From then on the two streams carry the service's own frames, which the relay copies unchanged.

A stream ends in one of two ways, and the relay keeps them apart end to end. An orderly end (`CloseWrite`, a yamux FIN) reaches the other peer as EOF after every byte written before it. An abort (`Reset`, or the relay closing the stream for lease expiry, revocation or link loss) reaches the other peer as an error that is never EOF.

## Implement a serve peer

The Sandbox I/O service runs `sandboxlink.Serve` with a `ServeConfig`:

- `URL`, `Credential` and `Resource` come from the [bootstrap input](./sandbox-bootstrap.md). The credential identifies the service, so the Hello carries no peer ID. `ServerInstanceID` is a new ID whenever the service starts without its operation and handle registries.
- `Services` holds one handler per offered service and version. A handler receives the `Bind`, which carries the authorized binding including a File stream's exports, a bind sequence and the stream. It owns the stream and returns when it is done with it. Its context ends when the attachment closes or `Serve` returns. Bind order is the order in which `Serve` assigns bind sequences, under the lock that tracks attachments and before it answers `Bound`; concurrent `Bound` and `Opened` replies can reach the opener in another order, and a handler that runs late keeps its stream's place. The sequence belongs to one `Serve` call and survives reconnects; it increases strictly but has gaps, since all attachments share it, and a service may rely on it to fence succession.
- `Serve` reconnects with jittered exponential backoff, sending the same `ServerInstanceID`, whenever the link drops. It returns when its context ends or when the relay refuses the Hello with a failure that is not [retryable](#failures), for example `AuthenticationFailed` after the credential is withdrawn or `StaleGeneration` after a newer sandbox took over the resource. Before returning it cancels every handler's context and waits for the handlers.
- `OnAttachmentLost` fires when an attachment's last open stream ends while the attachment is still open, such as when the link drops. `OnAttachmentRestored` fires when a stream binds a lost attachment again. `OnAttachmentClosed` fires with the reason when the relay reports `AttachmentClosed`. Losing a socket is not closing an attachment: the service keeps an attachment's state until it is closed.
- Closing is final. A `Bind` the relay sent before a close can arrive after the `AttachmentClosed`, so `Serve` refuses a `Bind` for an attachment closed within the last `sandboxlink.HandshakeTimeout` with `LeaseExpired`. A stream of a closed attachment that ends later never marks another attachment lost.

A serve peer opens no streams and sends nothing on its control stream after the Hello. The relay ends a link that does. The relay sends a serve peer only `AttachmentClosed` events; `Serve` ends a link that carries a request instead.

## Implement an attach peer

The agent-host Runtime calls `sandboxlink.DialAttach` with its Runtime ID and credential, then:

- `OpenService` sends `Open` on a new stream and returns the stream and `Opened`. A refusal returns a `*sandboxlink.Error`, and `errors.Is(err, sandboxlink.PermissionDenied)` matches its code. The context bounds the open, not the stream.
- `Renew` extends an attachment's lease with a current grant before `LeaseExpiresAt` passes. `CloseAttachment` ends an attachment and all its streams. Both wait for their turn to write and for the answer only as long as their context allows.
- A call whose context ends before its request is sent returns `ServiceUnavailable` with `EffectNone` and leaves the link up. A call whose request may have reached the relay but that ends without an answer, because its context ended or the link dropped, returns `ServiceUnavailable` with `EffectPossible` (`sandboxlink.Uncertain`). A context that ends while a control request is being written ends the link, because the control stream cannot carry a partial frame; one that ends after the request is written only stops the wait. A cancel that races the completion of a write may still close the link, and calls in flight then fail with `EffectPossible`.
- `OnAttachmentClosed` reports the relay closing an attachment for lease expiry, revocation or a newer resource generation.

An attachment outlives its link. After reconnecting, the Runtime opens a stream with the same binding identity. To resume state the service holds, it sets `ExpectedServerInstanceID` to the `ServerInstanceID` of the earlier `Opened`; `InstanceChanged` then means the service restarted and lost that state. After an attachment closes, the Runtime opens a new one under a new `AttachmentID`.

## Run a relay

`relay.New` takes a `relay.Config` with an `Authority` and returns a `*relay.Relay`, which is an `http.Handler`. The relay endpoint is served behind the installation's HTTPS ingress, which terminates TLS, so the handler accepts the upgrade on the ingress's plain HTTP hop; peers enforce TLS when they dial. Each link carries at most 256 concurrent service streams.

The owner of the relay implements `Authority` from its durable records, and the relay consults it for every Hello, Open and renewal. To revoke, withdraw the authority first, then call `RevokeAttachment` or `RevokeResource` so the relay closes what it holds.

Tests use `sandboxlinktest.NewAuthority`, which holds static credentials and grants, and `sandboxlinktest.StartRelay`, which runs a relay on an `httptest` TLS server and returns its URL and a TLS configuration that trusts it.

## Framing

Every sandbox I/O protocol, including Link, frames its messages the same way. [`internal/sandboxwire`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxwire/frame.go) implements the frame and the primitives. Each protocol's `protocol.go` owns its tags, payload layouts and validators.

A frame is a 16-byte header followed by the payload. Integers are big-endian.

| Offset | Field | Type | Rule |
| --- | --- | --- | --- |
| 0 | `PayloadLength` | uint32 | At most 1 MiB (`sandboxwire.MaxPayload`); checked before the payload is read |
| 4 | `MessageType` | uint16 | A tag the protocol defines |
| 6 | `Flags` | uint16 | Zero |
| 8 | `RequestID` | uint64 | For a request, nonzero and greater than the sender's previous request ID on the stream; the request's ID in its response; zero for an event |

Message tags:

- Requests use tags from 1 upward, in the order the protocol lists them.
- The response to request tag `t` uses `t | 0x8000`. Its payload begins with a uint16 result discriminator that the protocol defines. A failure carries a typed code and an `Effect`.
- Events use tags from `0x4001` upward, in the order the protocol lists them.
- Any other tag is malformed.

A payload is its fields in the order the protocol lists them, with no padding, names or reserved space:

| Primitive | Encoding |
| --- | --- |
| u8, u16, u32, u64 | Unsigned integer of that width |
| i64 | Two's complement, 8 bytes |
| Boolean | One byte, `0` or `1` |
| Enum | uint16; zero and unknown values are invalid |
| ID | 16 opaque bytes; the zero ID is invalid where an ID is required |
| Byte string | uint32 length, then the bytes |
| Array | uint32 element count, then the elements; each array has a maximum count |
| Optional field | A Boolean presence byte, then the value only when present |
| Effect | Enum: `EffectNone` = 1, `EffectPossible` = 2 |

Decoding rules:

- Check every length and count against the remaining payload and its maximum before allocating.
- Reject unknown tags, nonzero flags, unknown enum values, duplicate entries, overflow and trailing payload bytes. Every such error wraps `sandboxwire.ErrMalformed`.
- There are no maps and no implicit defaults.
- A file or process data chunk is at most 64 KiB (`sandboxwire.MaxChunk`).
- Each sender's request IDs on a stream strictly increase in wire order, so a receiver checks uniqueness in constant memory. A sender takes each ID from `sandboxwire.RequestSequence.Next` in the critical section that writes the frame. A receiver checks each ID with `Admit` and answers an ID that does not increase as a protocol violation, without dispatching the request. A service stream carries two sequences in turn: the Link exchange that opens it, `Open` and `Opened` on the attach side or `Bind` and `Bound` on the serve side, then the service protocol's own sequence, which starts after that exchange, so the first service request may use ID 1 again.
- A failed request states whether it may have taken effect: `EffectNone` or `EffectPossible`. Transport loss after dispatch is `EffectPossible` unless the server later establishes the result.

Each protocol keeps annotated golden frames under its package's `testdata` and a decoder fuzz target.

## Messages

| Tag | Request | Response | Stream |
| --- | --- | --- | --- |
| 1 `OpHello` | `ServeHello` or `AttachHello` | `HelloAccepted` | Control |
| 2 `OpOpen` | `Open` | `Opened` | Service stream from the attach peer |
| 3 `OpBind` | `Bind` | `Bound` | Service stream from the relay to the serve peer |
| 4 `OpRenewAttachment` | `RenewAttachment` | `AttachmentRenewed` | Control, attach peer |
| 5 `OpCloseAttachment` | `CloseAttachment` | `CloseAccepted` | Control, attach peer |
| `0x4001` `EventAttachmentClosed` | `AttachmentClosed` event | | Control, from the relay |

A response payload begins with a uint16 result: 1 for success, followed by the response's fields, or 2 for failure, followed by `Code` (enum) and `Effect`. Every Link payload is at most 16 KiB (`sandboxlink.MaxMessageBytes`).

Shared field types:

- `ResourceRef`: `TenantID` ID, `EnvironmentID` ID, `Kind` enum (`ResourceAllocation` = 1, `ResourceEnrollment` = 2), `ID` ID, `Generation` u64 of at least 1. `Kind` records provenance only; no service branches on it.
- `Service` enum: `ServiceFile` = 1, `ServiceProcess` = 2, `ServiceNetwork` = 3. A service version is a nonzero u16.
- A lease expiry is an i64 count of milliseconds since the Unix epoch, greater than zero.

```text
Hello
  Version           u16            // 1
  Role              enum           // RoleServe = 1, RoleAttach = 2
  if RoleServe:
    Credential        bytes        // 1..4096 bytes
    Resource          ResourceRef
    ServerInstanceID  ID
    Services          count 1..3 of { Service enum, Version u16 }, no service twice
  if RoleAttach:
    RuntimeID         ID
    Credential        bytes        // 1..4096 bytes

HelloAccepted
  LinkID            ID

Open
  Service                   enum
  Version                   u16
  Resource                  ResourceRef
  ExpectedServerInstanceID  optional ID
  AttachmentID              ID
  SessionID                 ID
  AssignmentID              ID
  AssignmentEpoch           u64    // at least 1
  AttachGrant               bytes  // 1..8192 bytes

Opened
  AttachmentID      ID
  ServerInstanceID  ID
  LeaseExpiresAt    i64 ms

Bind
  AttachmentID              ID
  Service                   enum
  Version                   u16
  SessionID                 ID
  AssignmentID              ID
  AssignmentEpoch           u64
  LeaseExpiresAt            i64 ms
  ExpectedServerInstanceID  ID     // the serve peer's ServerInstanceID as the relay knows it
  Exports                   optional, present exactly when Service is ServiceFile:
                              count 1..64 of ExportGrant, no ID twice
  Egress                    optional, present exactly when Service is ServiceNetwork:
                              count 0..256 of EgressRule

ExportGrant
  ID                bytes          // 1..64 bytes of a-z, 0-9, '_' and '-'
  ReadOnly          bool

EgressRule
  Family            enum           // FamilyIPv4 = 1, FamilyIPv6 = 2
  Address           4 or 16 bytes  // by Family
  PrefixLength      u8             // 0..32 or 0..128
  PortFirst         u16
  PortLast          u16

Bound               (no fields)

RenewAttachment
  AttachmentID      ID
  AttachGrant       bytes          // 1..8192 bytes

AttachmentRenewed
  AttachmentID      ID
  LeaseExpiresAt    i64 ms

CloseAttachment
  AttachmentID      ID

CloseAccepted       (no fields)

AttachmentClosed
  AttachmentID      ID
  Reason            enum           // CloseRequested = 1, CloseLeaseExpired = 2, CloseRevoked = 3, CloseStaleGeneration = 4
```

[`testdata/link_v1.hex`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxlink/testdata/link_v1.hex) holds annotated golden frames of these messages.

## Handshake

1. The peer dials the relay's URL: `wss://`, or `ws://` only when the host is `localhost` or a loopback address. The URL carries no user, query or fragment, and never a credential. `sandboxlink.CheckRelayURL` applies this rule for both peers and the [bootstrap input](./sandbox-bootstrap.md) and refuses any other URL with `sandboxlink.ErrRelayURL`. Over `wss://`, TLS authenticates the relay.
2. The peer starts yamux and opens the control stream.
3. It sends a Hello as the first request of the control stream, with request ID 1: `ServeHello` from the Sandbox I/O service, `AttachHello` from a Runtime. Credentials travel only in the Hello.
4. The relay authenticates the peer with its Authority and answers `HelloAccepted`, or a failure after which the link ends. A Hello of another version is answered `VersionMismatch` without reading past its version. When a revocation lands while the Authority decides a serve Hello, the relay asks again, so a withdrawn credential never installs a serve peer.

Later control requests continue the Hello's request IDs. The relay ends an attach link whose request ID does not increase with `ProtocolViolation`. `Open` and `Bind` are each the only request on their stream and use request ID 1.

For a serve peer, the Authority returns the peer's identity and the resource, including generation, that the credential serves. The resource must equal the Hello's, otherwise the answer is `PermissionDenied`. The relay then applies the [generation rule](#authority-and-staleness) and makes the link the resource's current serve peer.

The relay and the serve peer bound each handshake step, the WebSocket upgrade, the Hello, reading an `Open`, a `Bind` and its answer and each Authority call, by `sandboxlink.HandshakeTimeout` (10 seconds). The attach peer bounds an Open with its context.

## Opening a stream

The attach peer opens a stream and sends `Open`. The relay then:

1. Calls `Authority.AuthorizeOpen`. The Authority checks the grant, the Runtime, the current assignment and its epoch, the resource generation, the permitted service and access, and that the resource's serve authority is current. It returns the binding identity, the service, the lease, the exports for `ServiceFile` and the egress rules for `ServiceNetwork`. When a revocation lands while the Authority decides, the relay asks again.
2. Checks, in order: each link's stream limit (`LimitExceeded`); that the newest generation the relay has seen for the resource is not newer than the Open's (`StaleGeneration`); that a serve peer of the Open's generation is connected and offers the service (`ServiceUnavailable`) at the Open's version (`VersionMismatch`); that a nonzero `ExpectedServerInstanceID` equals the serve peer's (`InstanceChanged`); that the lease lies in the future (`LeaseExpired`); and that an attachment the relay already holds under this `AttachmentID` has the identical identity and Runtime (`AttachmentConflict`).
3. Opens a stream to the serve peer and sends `Bind`. The serve peer answers `Bound`, or a failure: `ServiceUnavailable` for a service it does not serve, `VersionMismatch`, `InstanceChanged` when `ExpectedServerInstanceID` is not its own, `LeaseExpired` for a recently closed attachment, or `ProtocolViolation`. The relay passes a failure on to the attach peer. When the `Bind` began to be sent but no answer arrives, the relay answers `ServiceUnavailable` with `EffectPossible`.
4. Answers `Opened` and splices the two streams.

An attachment's binding identity is its `AttachmentID`, `Resource`, `SessionID`, `AssignmentID` and `AssignmentEpoch`. Reopening an attachment, on the same link or a later one, requires the identical identity from the same Runtime and a current authorization.

`Bind` carries the authorized binding and never the grant or a credential:

- For a File stream, `Exports` lists 1 to 64 exports the stream may use, each by ID and read-write or `ReadOnly`. An export ID is 1 to 64 lowercase letters, digits, `_` and `-`, and IDs in a list are distinct. Process and Network binds carry no `Exports`. The [Sandbox bootstrap](./sandbox-bootstrap.md#responsibilities-and-readiness) states which exports the File service serves.
- For a Network stream, `Egress` lists the destinations the stream may reach: an address inside a rule's prefix on a port from `PortFirst` to `PortLast`. An empty list denies everything. Each prefix has its host bits zero, `1 ≤ PortFirst ≤ PortLast`, and no rule appears twice. File and Process binds carry no `Egress`. The [Network protocol](./sandbox-network-protocol.md#egress-check) states how the service applies it.

The exports and egress of a stream are fixed when it opens; a renewal changes only the lease.

## Authority and staleness

The relay keeps links, attachments and leases in memory. The Authority stays the durable judge:

- `AuthenticateServe(ctx, ServeHello) (ServePeer, error)` verifies a serve credential and the resource it serves.
- `AuthenticateAttach(ctx, AttachHello) (AttachPeer, error)` verifies a Runtime credential. The relay passes the `AttachPeer` to every later call, and its `Revision` lets the Authority refuse a link authenticated with a credential rotated since.
- `AuthorizeOpen(ctx, AttachPeer, Open) (Authorization, error)` decides an Open.
- `Renew(ctx, AttachPeer, RenewAttachment) (Authorization, error)` decides a renewal. Its `Authorization` names no service, exports or egress.

A method returns a `*sandboxlink.Error` for a typed refusal; any other error is answered `ServiceUnavailable`. Credential revision and allowed access come from the Authority, never from what a peer asserts.

A recreated resource has a higher generation. A serve peer of the same or a higher generation replaces the resource's current serve peer, and a higher generation also closes every attachment of an older generation with `CloseStaleGeneration`. A serve peer or an Open of a generation older than the newest the relay has seen is refused with `StaleGeneration`.

## Leases, closing and revocation

- Every attachment has a lease, reported as `LeaseExpiresAt` in `Opened` and `AttachmentRenewed`. When it passes without renewal, the relay closes the attachment with `CloseLeaseExpired`.
- Renewing an attachment the relay no longer holds returns `LeaseExpired`; renewing another Runtime's attachment returns `PermissionDenied`. An attach link has at most `sandboxlink.MaxControlRequests` (16) renewals being decided at once; the relay answers a further one `LimitExceeded` without consulting the Authority.
- `CloseAttachment` closes the caller's attachment with `CloseRequested`. Closing an unknown attachment succeeds, and closing another Runtime's returns `PermissionDenied`.
- `Relay.RevokeAttachment` closes one attachment. `Relay.RevokeResource` closes every attachment of a resource generation and older, writes the serve peer their `AttachmentClosed` events and then disconnects it. Both close with `CloseRevoked`.

Closing an attachment resets all its streams and sends `AttachmentClosed` to its attach peer, except after `CloseRequested`, and to the serve peer. The relay holds each serve peer's unwritten events in a set with no size limit and removes an event only once it is written, so a serve peer that is disconnected, or whose link drops before the event is written, receives it when it reconnects with the same generation. An Open that a close interrupts fails with `AttachmentConflict`, `LeaseExpired`, `PermissionDenied` or `StaleGeneration`, matching the reason, with `EffectPossible` when its `Bind` may have reached the serve peer.

Losing a link resets the streams it carries and keeps its attachments until their leases expire or they are closed.

## Stream ends

The streams handed to service handlers and returned by `OpenService` implement `sandboxlink.Stream`: `Read`, `Write`, `CloseWrite`, `Close`, which ends writing in order and discards further input, `Reset`, and `SetDeadline`, `SetReadDeadline` and `SetWriteDeadline`. The deadlines bound how long a `Read` or `Write` waits, as yamux's do: a call that would wait past its deadline fails with an error whose `Timeout` is true and leaves the stream usable, and a `Read` still returns input already buffered. One goroutine may read while another writes; concurrent reads, or concurrent writes, need the caller's own lock.

The relay copies each direction through a 32 KiB buffer and holds at most one 256 KiB yamux window per stream:

- When a peer ends its write side, the relay writes every byte before the end to the other peer and then ends that write side. The other direction carries on.
- When either stream fails, whether by `Reset`, a transport error, lease expiry, revocation or loss of either link, the relay resets both streams. An abort never becomes an orderly EOF.
- Losing either link resets both streams at once, even while the relay is waiting to write to the other peer.
- yamux reports a peer's `Reset` only to a reader or writer of that stream. When the relay is waiting to write to a peer that does not read, a `Reset` from the other peer reaches it when it next reads or writes the stream, or when the attachment closes or a link ends. The attachment's lease bounds that wait.

## Failures

| Code | Name | Returned when |
| --- | --- | --- |
| 1 | `VersionMismatch` | A Hello is not version 1, or the serve peer does not serve the Open's service version |
| 2 | `AuthenticationFailed` | The credential is not valid |
| 3 | `PermissionDenied` | The Authority refuses the Runtime, binding, service or peer, the attachment belongs to another Runtime, or it was revoked during the Open |
| 4 | `ResourceNotFound` | The Authority knows no such resource |
| 5 | `ServiceUnavailable` | No serve peer of the Open's generation offers the service, the Authority is unavailable, or a link dropped during the Open; with `EffectPossible` when the request may have taken effect |
| 6 | `StaleGeneration` | A newer generation of the resource exists |
| 7 | `StaleAssignment` | A newer assignment epoch exists |
| 8 | `InstanceChanged` | The serve peer's `ServerInstanceID` is not `ExpectedServerInstanceID` |
| 9 | `LeaseExpired` | The lease has passed, or the relay no longer holds the attachment |
| 10 | `AttachmentConflict` | The `AttachmentID` is held with another identity or Runtime, or was closed during the Open |
| 11 | `LimitExceeded` | A link's stream limit or its limit of renewals being decided is reached |
| 12 | `ProtocolViolation` | A message is malformed, not allowed where it arrived, or carries a request ID that does not increase |

`ServiceUnavailable` and `LimitExceeded` are transient: the same request may succeed later, and `Code.Retryable` reports them. Every other code is final: repeating the request with the same credential, attachment and generation fails again.

## Verification

`go test ./internal/sandboxlink/...` covers the golden frames, decode rejection, and the relay's authorization, generation, lease, revocation, renewal bound and reconnect behavior, including orderly end and abort propagation. `go test -run '^$' -fuzz FuzzDecode ./internal/sandboxlink` fuzzes the decoder.
