---
title: "Root Session and Turn diagnostics"
---

These read-only routes require the Core key. The Project ID selects the target Project and does not authenticate. Both return `Cache-Control: no-store`:

- `GET /core/v1/projects/{project_id}/sessions/{session_id}/diagnostics`
- `GET /core/v1/projects/{project_id}/sessions/{session_id}/turns/{turn_id}/diagnostics`

Missing, malformed, foreign and deleted resources follow the Session and Turn not-found rules. A Subagent Turn is not a root Turn and returns 404. Reads never contact an executor, provision an Environment, repair history or change execution.

## Session snapshot

The object is `core.session_diagnostics`, with `session_id`, the official Session `status`, and nullable `failure`. Each Session or Turn read uses one repeatable-read database snapshot and the public status projection, within a five-second budget that an earlier caller deadline shortens. The transaction is closed after the read, including on cancellation. `failure` is null unless the projection says `failed`. Its fields are:

| Field | Value |
| --- | --- |
| `source` | `turn`, `environment` or `environment_input` |
| `turn_id` | Present only for `source: turn` |
| `code` | Fixed category from [the diagnostics catalog](./core-errors.md#diagnostic-failure-categories) |
| `params` | An object of fixed safe values; `{}` for categories without parameters |
| `failed_at` | RFC 3339 timestamp, or null when the time is unknown |

A hosted provisioning failure takes precedence over input activity, and input activity over the latest root Turn; the public Session response uses the same precedence. Private outcome text, native messages, provider bodies, command text, paths and credentials are never included. Unknown outcome codes become `internal_error`; no prefix matching or reason-text parsing is used.

Provisioning parameters come from the confirmed structured receipt, persisted in the failure transaction that settles the Environment and its input and records the events. `step` is `setup`, `python`, `npm`, `system`, `file`, `skill` or null. `index` is a nonnegative JSON-safe integer for setup only, otherwise null. `exit_code` is 1 to 255 for script steps, otherwise null. Details that were not recorded remain null. These private fields do not change the public failure reason or SSE.

## Turn snapshot and Item receipt timing

The object is `core.turn_diagnostics`, with `session_id`, `turn_id`, the official root Turn `status`, nullable `failure`, `items` and `items_truncated`. A Turn failure has `code`, `params` and nullable `failed_at`, without a source or Turn ID. Only failed Turns have failure details; cancelled and completed Turns never inherit a classification from a private outcome.

Native categories apply only to a failed Turn whose outcome is `engine_failed`, and come from the finite outcome metadata described in [native failure classification](../../docs/runtime-protocol.md#native-failure-classification). `connection_failed` params contain `http_status`, 100 to 599 or null; other native categories have empty params.

`items` contains at most 1000 root Items, ordered by `(created_at, position, id)` ascending like the public Items list. Storage reads at most 1001 rows to detect truncation. Each entry has `item_id`, `started_at`, nullable `completed_at`, and nullable integer `observed_duration_ms`.

- `started_at` is Core's first persisted input or event receipt of the Item.
- `completed_at` is the first terminal input or event receipt. An Item first observed terminal settles at that same receipt, with zero observed duration.
- An Item still in progress when its root Turn terminates settles with one database `clock_timestamp()` sampled after the Session lock and the terminal projection, shared by all such Items of that transaction. It uses neither the transaction-start `now()` nor the native Turn completion time.
- Repeated terminal projections keep the first settlement. Terminal Items stored without a settlement keep null; Core does not backfill or estimate them.
- `observed_duration_ms` is the integer millisecond difference when both receipts are known, otherwise null. It is not native execution time: journal batching (the event journal flushes about every 100 ms), transport, persistence and database clock behavior all affect it. Values are not clamped.

The public completion time of a successful Turn may come from the native executor and is unchanged by these reads.

## Client

`AdminClient.retrieveSessionDiagnostics(projectId, sessionId, options)` and `AdminClient.retrieveTurnDiagnostics(projectId, sessionId, turnId, options)` in `packages/agents-client` support request cancellation and validate scope, categories, nullability and safe parameter values.
