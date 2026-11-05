---
title: "Automations"
---

An Automation saves a schedule and a target. When the schedule fires, Core delivers the target: it creates a new Session, or sends instructions to an existing one. All ten operations of the pinned SDK are implemented under `/v1/agents/automations` with the same Project authentication and `OpenAI-Beta: agents=v1` as other Beta routes. An Automation belongs to the Project of its key; a missing ID and another Project's ID both return 404.

| Operation | Result |
| --- | --- |
| `POST /agents/automations` | 201, an active Automation; nothing is delivered |
| `GET /agents/automations` | A page; `session_id` keeps Automations whose target is that Session. Lists clamp `limit` to 1–100, and an unresolved `after` returns 404 |
| `GET /agents/automations/{automation_id}` | The Automation |
| `POST /agents/automations/{automation_id}` | Replaces the fields in the body; `target` and `metadata` replace the whole field |
| `DELETE /agents/automations/{automation_id}` | `agent.automation.deleted`; the target Session and the Sessions it created stay |
| `POST …/pause`, `POST …/resume` | The Automation |
| `POST …/invocations` | 200, a queued manual invocation |
| `GET …/invocations`, `GET …/invocations/{invocation_id}` | A page, or one invocation |

## Resource

`type` is `periodic` or `once`.

- `periodic` has `expression` and `timezone`. Status is `active` or `paused`.
- `once` has `run_at`, a Unix timestamp in seconds, at most 253402300799. Status is `active`, `paused` or `completed`; completed means the schedule has finished, not that the agent's work has.
- `name` holds 1–256 characters, `expression` 1–256, `timezone` 1–128. `metadata` follows the [validation errors](./wire-semantics.md#validation-errors).
- `created_by` is always `{"type": "developer"}`: the `automations` agent tool is not supported, so an agent cannot create one ([Session admission](./wire-semantics.md#session-admission)).

### Schedules

`expression` is a five-field crontab: minute, hour, day of month, month and day of week. A field is `*` or a comma list of `n`, `a-b`, `*/s` and `a-b/s`; months and weekdays also take three-letter names, and weekday 7 is Sunday. When neither day field starts with `*`, a day matches either. Descriptors such as `@daily`, a seconds field, `Local` and an empty zone are rejected, and so is an expression with no occurrence in the next five years.

`timezone` is an IANA name, resolved from the system zoneinfo (or the directory in `ZONEINFO`) first and from the tzdata Core embeds otherwise. A job whose minute and hour fields both start with something other than `*` is fixed-time. On a daylight saving change:

- Spring forward: a fixed-time job whose time falls in the gap runs at the first instant after the gap. A job with a `*` minute or hour runs only at wall times that exist.
- Fall back: a fixed-time job in the repeated hour runs once, at its first instant. A job with a `*` minute or hour runs on both passes.

If Core was down when occurrences were due, it records only the latest one.

## Targets

`target.type` is `new_session` or `existing_session`. An update keeps the type; another type returns 400 with param `target.type`.

`new_session` takes the fields of the Session create body, plus `type`: `environment` and `input` (an array of user messages) are required, exactly one of `agent` and `agent_id` must be given, and `metadata` and `vault_ids` are optional. Create and update run the checks Session create runs before it creates anything: the request shape, the Environment Template, the saved Agent, the model and its provider, the Vault bindings, and the Harness's support for the configuration, so an unsupported tool such as `automations` returns 400 at create. The rejections are Session create's, with their params prefixed `target.`: `environment.environment_id`, `container_size`, `desktop.enabled`, `packages.system` and `blocked_domains` are rejected. Deployment readiness, admission and leases are checked at each delivery. Beyond that:

- An inline `agent` follows Session create: `model` is required unless a deployment default model applies.
- `x_agents_core` is rejected anywhere in the target.
- The saved resource shows the inline agent without `id` and `name`, MCP headers removed. `environment` shows the members the request supplied and `null` for the rest, which means inherit; a `self_hosted` environment always lists `capability_directories`. `env` and `setup_commands` are never shown.

`existing_session` takes `session_id` and `instructions` (1 to 1,048,576 characters). The Session must exist when the Automation is created or updated, and Core saves its canonical ID whatever form the request used; a Session deleted later fails each delivery with the input path's 404. Core sends `instructions` as one user message with one `input_text` part, exactly as `agent.session.input.message` would.

Create and update save the target; references inside it, such as a Skill, a File or an Agent, resolve at each invocation. A queued invocation delivers the target as it was when it was recorded or invoked, so an update applies to later invocations.

The target request may hold values that reads never return, so Core stores it sealed with the credential key, bound to the Project and the Automation.

## Invocations

A scheduled occurrence and a manual invocation each produce one invocation record. `source` is `manual`, or `automation` with the Automation as it was at the occurrence.

| Status | Meaning |
| --- | --- |
| `queued` | Recorded, not yet delivered |
| `completed` | Delivered; `session_id` is the created Session, or the target |
| `failed` | Delivery was rejected for good; `error` holds the `code` and `message` the same request would return |
| `skipped` | `skip_reason` is `pending_delivery`: the Automation already had a queued invocation |

Core records due occurrences and delivers queued invocations once a second, and runs at most 16 deliveries at the same time, each for up to 5 s. A delivery uses the code of `POST /v1/agents/sessions` or `POST /v1/agents/sessions/{id}/events`, with the invocation ID as its `Idempotency-Key`, so a retry after a crash finds the Session or the input it already made. Core retries a delivery that gets a status of 500 or above, finds a Turn that cannot take input, or times out; a 4xx status fails the invocation. One Automation delivers one invocation at a time, oldest first, and the Automation attempted least recently goes first, so a stuck invocation does not hold every slot. An invocation whose saved target cannot be opened fails with `internal_error`. Invocations are kept until the Automation is deleted.

A `once` Automation completes when its scheduled invocation reaches `completed`, `failed` or `skipped`. A manual invocation never changes the schedule.

## State changes

| Request | Rule |
| --- | --- |
| Pause | Stops scheduled delivery, holds a queued scheduled invocation until resume, and stops no Session. Manual invocations still deliver. Pausing a paused Automation returns it unchanged |
| Resume | A periodic Automation takes its next occurrence from now. A `once` Automation resumes if its scheduled invocation is pending or `run_at` is in the future; otherwise 409 `conflict_error`. Resuming an active Automation returns it unchanged |
| Pause or resume a completed Automation | 409 `conflict_error` |
| Update | `expression` and `timezone` apply only to periodic Automations and `run_at` only to `once`; another type's field returns 400 naming it. A past `run_at` returns 400. Changing `run_at` after the scheduled invocation exists returns 409 |
| Manual invoke | Allowed in every status |

Core's choices where the official sources are silent are listed in the [ledger](./index.md#unverified-against-the-official-service). Invocation creation writes the audit entry `create` on `automation_invocation`; scheduled recording writes none ([write provenance](./admin-api.md#write-provenance)).
