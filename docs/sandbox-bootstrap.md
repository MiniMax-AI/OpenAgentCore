---
title: "Sandbox bootstrap"
---

A Sandbox Provider starts the Sandbox I/O service by handing it one bootstrap file. This document owns that Provider-to-service startup input. The type and validator live in [`internal/sandboxbootstrap`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/sandboxbootstrap/bootstrap.go). With it, the service connects to the relay as the serve peer of the [Sandbox link protocol](./sandbox-link-protocol.md).

## Launch input

Deliver one JSON object in a regular file that only the service's account and trusted provisioning processes can read (mode 0600 on Linux), and pass its absolute path:

```sh
oac-sandbox-io --bootstrap-file /home/runtime/sandbox-io-bootstrap.json
```

The command takes no other argument and reads no environment variable or configuration file.

| Field | Meaning |
| --- | --- |
| `version` | The exact bootstrap version, `sandboxbootstrap.Version` |
| `link_url` | The relay's URL under the Link protocol's [URL rule](./sandbox-link-protocol.md#handshake): `wss://`, or `ws://` only to a loopback host, without user, query or fragment |
| `credential` | The serve credential Core issued for this resource: nonempty, at most 4 KiB, without whitespace or NUL |
| `resource` | The resource the credential serves, an object with exactly the fields below |
| `resource.tenant_id`, `resource.environment_id`, `resource.id` | Canonical nonzero UUIDs |
| `resource.kind` | `allocation` or `enrollment`; it records provenance only |
| `resource.generation` | The resource's generation, from 1; a recreated resource has a higher one |

The decoder rejects unknown, duplicate, missing and case-aliased fields at every level, other versions and documents larger than `sandboxbootstrap.MaxBytes` (16 KiB). Errors never include submitted values. A missing or malformed file fails before the service connects.

The file is the service's only authentication input. Credentials never go in command arguments, environment variables or the URL; the service sends the credential only in its `ServeHello`.

## Identity

The service runs as the account the Provider starts it with. The input names no user or group, and the service never changes identity. The Provider already creates the sandbox's accounts and launches its processes, so it chooses this account, and the service needs no privilege-dropping code.

## Responsibilities and readiness

The Provider creates the account and the sandbox, delivers this file and starts `oac-sandbox-io` as that account. `make build-sandbox-io` builds the static Linux binary. The Provider keeps the file for process restarts and removes it only during explicit cleanup of the resources it owns.

The service validates the input and owns the link: it connects as the serve peer, serves bound streams and reconnects while the credential stays valid. `resource`, including its generation, must be the resource the credential serves, or the relay refuses the link.

The File service serves the single [world export](./file-access-protocol.md#attach), and the Provider's sandbox setup owns its isolation. The [Process service](./process-protocol.md#implement-a-service) runs processes as the service's account, and the service, a child subreaper, reaps their orphaned descendants. The service also serves the [Network protocol](./sandbox-network-protocol.md): it resolves names and dials TCP from the sandbox's network namespace, within the egress each stream's `Bind` carries.

The service exits nonzero with a message naming the failed step when it cannot start, and with the relay's failure code when `Serve` returns a [refusal](./sandbox-link-protocol.md#implement-a-serve-peer). Neither message includes the credential. On SIGTERM it stops accepting streams, cancels its live operations as [ownership cleanup](./process-protocol.md#ownership) does, waits for them to end, at most the cancel grace limit plus five seconds, and exits 0.

A successful launch proves only the handoff. The service is ready when the relay holds it as the resource's current serve peer, so that an `Open` of the resource reaches it instead of failing with `ServiceUnavailable`.

## Verification

`go test ./internal/sandboxbootstrap` covers the input contract, and `go test ./apps/sandboxio/internal/sandboxio` runs the service against a test relay on Linux.
