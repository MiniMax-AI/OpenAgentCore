---
title: "Runtime bootstrap"
---

A Sandbox Provider starts a managed Runtime by handing it one bootstrap file. This document owns that Provider-to-Runtime startup input. The type and validator live in [`internal/runtimebootstrap`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/runtimebootstrap/bootstrap.go); Go providers build it with `sandbox.Bootstrap.RuntimeConnection()` in [`runtime_bootstrap.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/internal/sandbox/runtime_bootstrap.go), and SDK helpers forward the serialized object unchanged. A provider never reads or writes the Runtime's private authentication store.

## Launch input

Deliver one JSON object in a regular file that only the Runtime account and trusted provisioning processes can read (mode 0600 on managed Linux), and pass its absolute path:

```sh
oac-daemon connect --bootstrap-file /home/runtime/runtime-bootstrap.json
```

| Field | Meaning |
| --- | --- |
| `version` | The exact bootstrap version, `runtimebootstrap.Version` |
| `core_url` | HTTP(S) machine API base ending in `/api/v1`, without credentials, query or fragment |
| `device_id` | Canonical nonzero UUID of the daemon identity Core issued |
| `credential` | Nonempty daemon credential Core issued, without whitespace or NUL |

The decoder rejects unknown, duplicate, missing and case-aliased fields, other versions and documents larger than `runtimebootstrap.MaxBytes` (16 KiB). Errors never include submitted values. A missing or malformed file fails before the daemon connects.

The file is the only authentication input for this launch: the daemon refuses to combine it with pairing or self-hosted enrollment options, and reads the credential into memory without saving it to a stored profile. Credentials never go in command arguments, environment variables or receipts. The provider keeps the file for process restarts and removes it only during explicit cleanup of the resources it owns.

## Responsibilities and readiness

The provider creates the account, mounts and workspace, delivers this file, sets the Runtime's resource and Environment binding settings, and starts the daemon as the unprivileged Runtime account. Docker writes the file into the Runtime's owned home volume; microsandbox and E2B deliver it before launching the same command.

The Runtime validates the input and owns authentication and connection. A successful launch proves only the handoff: an authenticated connection, prepared capabilities and execution readiness are separate observations under the [Core–Runtime protocol](./runtime-protocol.md), and the [Sandbox Provider guide](./sandbox-provider.md#four-distinct-readiness-facts) lists what each one proves.

Self-hosted executors and operator-provisioned devices get their daemon identity in other ways; the [machine connection API](../contracts/agents-api/machine-api.md#credentials) lists every credential source. All of them enter the same Runtime execution loop.

## Verification

`go test ./internal/runtimebootstrap ./apps/daemon/internal/cli` covers the input contract, the exclusivity of credential sources and restart behavior. Provider tests verify delivery and file permissions without relying on the Runtime's private storage.
