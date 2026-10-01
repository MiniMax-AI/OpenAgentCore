# Sandbox network protocol

The Network protocol is how the agent host opens a TCP connection that originates in a sandbox. The agent host opens a Network stream through the Link and sends one `Connect`. The Sandbox I/O service resolves the name with the sandbox's resolver, checks the destination against the egress the Link bound to the stream, dials from the sandbox's network and answers. After `Connected` the stream carries the connection's raw bytes in both directions. Credentials and TLS stay with the agent host: the service carries the bytes the client writes and reads nothing in them.

[`internal/sandboxnet/protocol.go`](../internal/sandboxnet/protocol.go) is the authored definition: message tags, payload layouts, validators, the egress rule and the `Service` interface. The same package has the client and the generic server. [`apps/sandboxio/internal/netservice`](../apps/sandboxio/internal/netservice) is the Linux service. Frames use the shared [framing](sandbox-link-protocol.md#framing). A stream's authority, including its `Egress`, comes from the Link when the stream [opens](sandbox-link-protocol.md#opening-a-stream), never from `Connect`.

## How a connection works

1. The client opens a Link stream for `ServiceNetwork` version 1.
2. The client sends one `Connect` with a request ID that follows the [request ID rule](sandbox-link-protocol.md#framing), and writes nothing else until the answer.
3. The service checks the request against the stream's egress, resolves the host, and dials the first permitted address within `TimeoutMillis`, as described in [Egress check](#egress-check).
4. The service answers `Connected` or `Failed` with the request's ID. After `Failed` it ends the stream in order. After `Connected` both directions carry raw bytes without framing.

Each direction ends on its own. The client's orderly end (`CloseWrite`, a FIN) reaches the destination as a TCP half-close after every byte before it, and the destination's half-close reaches the client as EOF after every byte before it. A half-close starts no timeout, and the other direction carries on.

Anything else aborts both directions: a reset from the destination, a failed read or write on either side, the end of the attachment or the loss of a link. The client then reads an error that is never EOF, and the service resets the socket. After `Connected` a failure is never reported as a frame.

A stream carries one connection. There is no replay or resumption: a lost answer leaves the attempt uncertain, and nothing retries it automatically.

## Implement a client

The Go client is `sandboxnet.Connect(ctx, stream, host, port, timeout)`. It takes a newly opened Network stream, owns it from then on, and returns a `*sandboxnet.Conn`, which is a `net.Conn`.

- A failure is a `*sandboxnet.Error` with a `Code` and an `Effect`, and `errors.Is(err, sandboxnet.CodeDenied)` matches its code. Arguments that fail validation fail with `InvalidArgument`, and a context that ended before the request was sent fails with `Cancelled` or `TimedOut`, both with `EffectNone`. Once the request may have been sent, a lost answer (`IO`), a malformed answer (`Unknown`) or the end of the context (`Cancelled` or `TimedOut`) fails with `EffectPossible`: the sandbox may have connected. `Connect` never retries.
- `timeout` is rounded up to whole milliseconds and must be from 1 ms to 60 s. The context bounds the wait for the answer.
- `Conn.CloseWrite` ends the write direction in order. `Conn.Close` ends in order only after `Read` returned `io.EOF` with no failed read or write; otherwise it aborts, so unread input never holds the Link's flow-control window. `Conn.Reset` aborts explicitly.
- Deadlines apply to the stream, and a passed deadline returns `os.ErrDeadlineExceeded`. `RemoteAddr` is the requested host and port. `LocalAddr` is empty, because the protocol does not report the sandbox's local endpoint.

A client in another language follows the same rules: one `Connect` per stream, no bytes before the answer, and no automatic retry of a failure with `EffectPossible`.

## Implement a service

Implement `sandboxnet.Service` and serve each Network stream with `sandboxnet.Serve(ctx, stream, bind.Egress, service)`, where `ctx` is the attachment's. `Serve` owns the protocol:

- It reads one frame. A frame that is not a `Connect`, or whose request ID is zero, resets the stream. A `Connect` that fails validation is answered `InvalidArgument`.
- It keeps reading the stream while it resolves and dials. Any byte before `Connected`, including a second `Connect`, is a protocol violation: it cancels the dial and resets the stream.
- It runs the [egress check](#egress-check) and calls `Resolve` and `Dial`. `Dial` connects to the address it is given and never resolves.
- It answers an `*Error` from `Resolve` or `Dial` as is. Any other error becomes `TimedOut` when the timeout passed, `NameResolutionFailed` with `EffectNone` from `Resolve`, and `IO` with `EffectPossible` from `Dial`.
- After `Connected` it splices the stream and the socket. The end of `ctx` aborts both at any point.

`netservice.New()` returns the Linux service. It resolves with the sandbox's system resolver, which reads the sandbox's `/etc/hosts` and `/etc/resolv.conf`, and dials from the sandbox's network namespace. `Service.Handle` is the `Serve` function of the `sandboxlink.ServiceNetwork` handler. It types a failed dial by its errno as the [failure table](#failures) lists.

## Reference

### Messages

```text
Version = 1                    // the Link service version of ServiceNetwork
OpConnect = 1
ConnectResponseTag = 0x8001

ConnectRequest payload, in order:
  Network        uint16        // NetworkTCP = 1
  HostLength     uint32
  Host           byte[HostLength]
  Port           uint16
  TimeoutMillis  uint32

ConnectResponse payload, in order:
  Result         uint16        // ResultConnected = 1, ResultFailed = 2
  if ResultFailed:
    Code         uint16
    Effect       uint16        // EffectNone = 1, EffectPossible = 2
```

A payload is at most 265 bytes. Unknown enum values, a failure field after `Connected` and trailing bytes are malformed.

### Arguments

- `Host` is 1 to 253 bytes: an unbracketed IPv4 or IPv6 literal without a zone, or a DNS name. A name is labels separated by dots, with an optional final dot. Each label is 1 to 63 ASCII letters, digits, hyphens and underscores, and does not begin or end with a hyphen. The last label is not all digits. A name with non-ASCII characters is sent in IDNA A-label form (`xn--`). NUL, whitespace, URLs, brackets and embedded ports are rejected.
- `Port` is from 1 to 65535.
- `TimeoutMillis` is from 1 to 60,000 and covers resolution plus dialing.

### Egress check

The service checks a `Connect` against the stream's [`Egress`](sandbox-link-protocol.md#opening-a-stream) in this order:

1. When no rule admits the port, the answer is `Denied` and nothing is resolved.
2. An IP literal is the only candidate address. Otherwise the service resolves the name, and each address it returns is a candidate.
3. Each candidate is checked with the port. An IPv4-mapped IPv6 address is checked and dialed as IPv4, and an address with a zone is never permitted.
4. The service dials the first permitted candidate once, at that address, without resolving again. When no candidate is permitted, the answer is `Denied`.

### Failures

| Code | Name | Effect | Returned when |
| --- | --- | --- | --- |
| 1 | `InvalidArgument` | None | The `Connect` is malformed, or its host, port or timeout is out of range |
| 2 | `UnsupportedNetwork` | None | The service does not serve the requested network |
| 3 | `Denied` | None | The egress admits neither the port nor any candidate address, or the sandbox refused the connect (`EACCES`, `EPERM`) |
| 4 | `NameNotResolved` | None | The name does not exist or has no address |
| 5 | `NameResolutionFailed` | None | Resolution failed for another reason |
| 6 | `ConnectionRefused` | None | The destination refused the connection (`ECONNREFUSED`) |
| 7 | `Unreachable` | None | The sandbox has no route to the destination (`ENETUNREACH`, `EHOSTUNREACH`, `ENETDOWN`, `EHOSTDOWN`, `EAFNOSUPPORT`) |
| 8 | `TimedOut` | None during resolution or before the client sent the request, Possible after | `TimeoutMillis` passed or the kernel gave up the connect (`ETIMEDOUT`); for the client, its context's deadline passed |
| 9 | `ResourceExhausted` | None | The sandbox ran out of ports, descriptors or memory (`EADDRNOTAVAIL`, `EMFILE`, `ENFILE`, `ENOBUFS`, `ENOMEM`, `EAGAIN`) |
| 10 | `Cancelled` | None before the request is sent, Possible after | The client's context was cancelled, or the attachment ended |
| 11 | `IO` | Possible | The dial failed for another reason; for the client, the stream failed before the answer arrived |
| 12 | `Unknown` | Possible | For the client, the answer broke the protocol |

## Verification

`go test ./internal/sandboxnet/ ./apps/sandboxio/internal/netservice/` covers the golden frames, decode rejection, the host grammar and the egress rule, and, over a test relay, bytes in both directions, a half-close from each side, a destination reset, `Denied` without a dial, `NameNotResolved`, `ConnectionRefused`, `TimedOut`, a second `Connect` and a lost answer. `go test -run '^$' -fuzz FuzzDecode ./internal/sandboxnet` fuzzes the decoder.
