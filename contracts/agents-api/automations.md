---
title: "Automations"
---

An Automation saves a schedule and a target. When the schedule fires, Core delivers the target: it creates a new Session, or sends instructions to an existing one. All ten operations of the pinned SDK are implemented under `/v1/agents/automations` with the same Project authentication and `OpenAI-Beta: agents=v1` as other Beta routes. An Automation belongs to the Project of its key; a missing ID and another Project's ID both return 404.

| Operation | Result |
| --- | --- |
| `POST /agents/automations` | 201, an active Automation; nothing is delivered |
| `GET /agents/automations` | A page; `session_id` keeps Automations whose target is that Session |
| `GET /agents/automations/{automation_id}` | The Automation |
| `POST /agents/automations/{automation_id}` | Replaces the fields in the body; `target` and `metadata` replace the whole field |
| `DELETE /agents/automations/{automation_id}` | `agent.automation.deleted`; the target Session and the Sessions it created stay |
| `POST …/pause`, `POST …/resume` | The Automation |
| `POST …/invocations` | 200, a queued manual invocation |
| `GET …/invocations`, `GET …/invocations/{invocation_id}` | A page, or one invocation |

Lists take `after`, `limit` (default 20; 0 becomes 1 and above 100 becomes 100) and `order` (default `desc`). A cursor that names no record returns 404 `not_found_error`.

## Resource

`type` is `periodic` or `once`.

- `periodic` has `expression` and `timezone`. Status is `active` or `paused`.
- `once` has `run_at`, a Unix timestamp in seconds. Status is `active`, `paused` or `completed`; completed means the schedule has finished, not that the agent's work has.
- `name` holds 1–256 characters, `expression` 1–256, `timezone` 1–128. `metadata` follows the [validation errors](./wire-semantics.md#validation-errors).
- `created_by` is always `{"type": "developer"}`: the `automations` agent tool is not supported, so an agent cannot create one ([Session admission](./wire-semantics.md#session-admission)).

### Schedules

`expression` is a five-field crontab: minute, hour, day of month, month and day of week. A field is `*` or a comma list of `n`, `a-b`, `*/s` and `a-b/s`; months and weekdays also take three-letter names, and weekday 7 is Sunday. When both day fields are restricted, a day matches either. Descriptors such as `@daily`, a seconds field, `Local` and an empty zone are rejected, and so is an expression with no occurrence in the next five years.

`timezone` is an IANA name, resolved from the tzdata Core embeds. A wall-clock minute that does not exist on a daylight saving change is skipped; a minute that occurs twice fires once, at its first instant.

If Core was down when occurrences were due, it records only the latest one.

## Targets

`target.type` is `new_session` or `existing_session`. An update keeps the type; another type returns 400 with param `target.type`.

`new_session` takes the body of Session create without `type`: `environment` and `input` (an array of user messages) are required; `agent` or `agent_id` is optional, and exactly one of them must be given; `metadata` and `vault_ids` are optional. The target is validated by the code that validates Session create, so its rejections are the same, with their params prefixed `target.`: `environment.environment_id`, `container_size`, `desktop.enabled`, `packages.system` and `blocked_domains` are rejected. Beyond that:

- An inline `agent` needs `model` (param `target.agent.model`) and rejects `x_agents_core`.
- `x_agents_core` is rejected anywhere in the target.
- The saved resource shows the inline agent without `id` and `name`, MCP headers removed. `environment` shows the members the request supplied and `null` for the rest, which means inherit. `env` and `setup_commands` are never shown.

`existing_session` takes `session_id` and `instructions` (1 byte to 1 MiB). The Session must exist when the Automation is created or updated; a Session deleted later fails each delivery with the input path's 404. Core sends `instructions` as one user message with one `input_text` part, exactly as `agent.session.input.message` would.

Create and update save the target; references inside it, such as a Skill, a File or an Agent, resolve at each invocation.

The target request may hold values that reads never return, so Core stores it sealed with the credential key, bound to the Project and the Automation.

## Invocations

A scheduled occurrence and a manual invocation each produce one invocation record. `source` is `manual`, or `automation` with the Automation as it was at the occurrence.

| Status | Meaning |
| --- | --- |
| `queued` | Recorded, not yet delivered |
| `completed` | Delivered; `session_id` is the created Session, or the target |
| `failed` | Delivery was rejected for good; `error` holds the `code` and `message` the same request would return |
| `skipped` | `skip_reason` is `pending_delivery`: the Automation already had a queued invocation |

Core records due occurrences and delivers queued invocations once a second, and runs at most 16 deliveries at the same time, each for up to 5 s. A delivery uses the code of `POST /v1/agents/sessions` or `POST /v1/agents/sessions/{id}/events`, with the invocation ID as its `Idempotency-Key`, so a retry after a crash finds the Session or the input it already made. Core retries a delivery while the cause is temporary: execution unavailable, a sandbox reset, a pending earlier input, or a Turn that cannot take input. Any other rejection fails the invocation. One Automation delivers one invocation at a time, oldest first, and invocations are kept until the Automation is deleted.

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
