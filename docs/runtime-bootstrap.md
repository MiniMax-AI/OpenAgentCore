# Runtime bootstrap

This document owns the Provider-to-Runtime startup boundary. The input type and
validator live in [runtimebootstrap](../internal/runtimebootstrap/bootstrap.go).
Providers must not read or write Runtime's private authentication store.

## Launch input

Deliver one JSON object in a regular file accessible only to the Runtime account
and trusted provisioning processes (0600 on managed Linux). Pass its absolute path:

```sh
oac-daemon connect --bootstrap-file /home/runtime/runtime-bootstrap.json
```

| Field | Meaning |
| --- | --- |
| `version` | Exact bootstrap version declared by `runtimebootstrap.Version` |
| `core_url` | HTTP(S) machine API base ending in `/api/v1`, without credentials, query or fragment |
| `device_id` | Canonical nonzero UUID for the daemon identity issued by Core |
| `credential` | Nonempty daemon credential issued by Core, without whitespace or NUL |

The decoder rejects unknown, duplicate, missing and case-aliased fields, other
versions and documents exceeding `runtimebootstrap.MaxBytes`. Errors exclude
submitted values. Go providers use `Bootstrap.RuntimeConnection()`; SDK helpers
forward the serialized object without defining their own authentication format.

The file is the sole authentication input for this launch. It cannot be combined
with pairing or self-hosted enrollment flags. Credentials never go in command
arguments, environment variables or receipts. The provider retains the protected
file for process restarts and removes it only with explicit owned-resource cleanup.
Runtime reads it into memory and neither overwrites nor falls back to a private
auth profile. A missing or malformed file fails before connecting.

## Responsibilities and readiness

The Provider provisions the account, mounts and workspace, delivers this input,
sets the existing Runtime resource and Environment binding settings, and starts
the daemon as the unprivileged Runtime account. Docker supplies a file in its
owned home volume; microsandbox and E2B deliver it before launching the same
command. These are delivery mechanisms, not different bootstrap protocols.

Runtime validates the input and owns authentication and connection establishment.
A successful process launch proves only handoff; authenticated connection,
capability preparation and execution readiness remain separate observations under
the [Core–Runtime protocol](runtime-protocol.md).

Self-hosted enrollment exchanges its executor credential for a daemon identity
through the machine API; it is a different source of authority, not a managed
bootstrap-file fallback. Both paths enter the same Runtime execution loop.

## Verification

`go test ./internal/runtimebootstrap ./apps/parsar-daemon/internal/cli` covers the
input contract, credential-source exclusivity and restart behavior. Provider tests
verify delivery and permissions without relying on private Runtime storage.
