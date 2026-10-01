---
title: "Core wire behavior"
---

The pinned OpenAI Python SDK ([upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json)) defines the `/v1` routes, fields and types. This page states what Core does where those types are silent, such as status codes, error fields, defaults and list bounds, and where Core behaves differently from the official service. The [coverage ledger](./index.md) lists the differences and open gaps; [Sessions, events and history](./sessions-events.md), [message content](./message-content.md), [Vaults](./vaults.md), [source Files and Skills](./source-files.md) and [Environment files and Artifacts](./environment-files.md) own the rules of their resources.

"Beta routes" below are the routes under `/v1/agents` and `/v1/vaults`. "Files and Skills" are the routes under `/v1/files` and `/v1/skills`, which ignore `OpenAI-Beta`.

## Requests

### Paths and methods

| Case | Core behavior |
| --- | --- |
| Empty, `.` or `..` path segments | Served on the canonical path, never redirected. Segments resolve with Go ServeMux semantics and a trailing slash is kept, so `/v1/agents/x/../` reaches the trailing-slash 404. |
| Percent-encoded unreserved characters (`A–Z`, `a–z`, `0–9`, `-`, `.`, `_`, `~`) | Decoded before routing, including `%2E` dot segments. Other escapes, such as `%2F`, `%5C` and double encodings, stay encoded and never separate segments. Every spelling reaches the route and authentication of its canonical path. |
| `HEAD` on a `GET` route | Runs the `GET` route after the same Beta and authentication checks and returns its headers without a body. |
| `HEAD` on the event stream, on File, Skill, Skill version and Artifact content, and on the Environment files list | 405, so `HEAD` never holds a stream open or reads content. |
| Unsupported method, including unknown methods such as `FOO` | 405, code `unsupported_operation`, message "This API method is not supported.", and an `Allow` header listing the route's methods in the order `GET,HEAD,POST,DELETE`. |
| Unknown sub-route under `/v1`, including a trailing slash | 404, code `unsupported_operation`, after the Beta and authentication checks. |
| `OPTIONS` and CORS | No CORS handling. |

Creating an Agent, Vault, Credential, Environment Template, Environment file or Session returns 201, also for a streamed Session creation. An empty update body on an Agent or Environment Template advances `updated_at` and changes nothing else; timestamps have one-second precision.

### Headers

Beta routes require exactly one `OpenAI-Beta` header value, equal to `agents=v1`. A missing, different or repeated value returns 400 with type and code `invalid_beta` and the message "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'." This check runs before authentication. `agents=v0` is rejected.

Every Agents API response, including errors and event streams, carries:

| Header | Value |
| --- | --- |
| `X-Request-Id` | A fresh `req_` followed by 32 lowercase hex characters. Core also logs it as `request_id`. A caller-supplied value is not echoed. |
| `OpenAI-Version` | `2020-10-01` |
| `OpenAI-Processing-Ms` | Handling time when the headers are written |
| `X-Content-Type-Options` | `nosniff` |
| `Cache-Control` | `no-store` on JSON responses |

### Authentication

`/v1` accepts only a Project API key as `Authorization: Bearer <key>`. All keys of one Project act as the same caller: they share its resources and its Session creation retries. Core resolves the key and its Project in the database on every request, with no credential cache and a five-second timeout. Revoking a key or archiving its Project takes effect on the next request. All keys of a Project act as subject `service_account/project:<Project ID>`. [Projects and keys](./admin-api.md#projects-and-keys) describes key management.

The optional `OpenAI-Organization` and `OpenAI-Project` headers must, when sent, appear once and equal `core` and `proj_<Project ID>`; any other value rejects the key.

| Failure | Response |
| --- | --- |
| No key, another scheme, an empty or repeated `Authorization` header, an unknown or revoked key, a key of an archived Project, the Core key, or mismatched scope headers | 401, type `invalid_request_error`, message "A valid Agents API bearer key is required.", `WWW-Authenticate: Bearer`. The code is null on Beta routes. On Files and Skills it is `invalid_api_key` when exactly one Bearer credential was sent and rejected, and null otherwise. |
| The key lookup fails, for example because the database is unavailable | 503, type `server_error`, code `authentication_unavailable` |

### Request bodies

Every `/v1` JSON route passes one body gate before route decoding, validation or lookup: Agent create and update, Vault create, Credential create and update, Environment Template create and update, Environment file create, Session create and update, and Session events. DELETE routes, multipart Files and Skills uploads and Skill update keep their own readers. Except for the 413 size error below, gate errors are 400 with type and code `invalid_request_error` and a null param.

| Order | Case | Response |
| --- | --- | --- |
| 1 | `Content-Type` missing, not JSON or malformed, including a bodyless `POST`. `application/json` and `application/*+json` are accepted case-insensitively, with parameters | "expected request with Content-Type: application/json" |
| 2 | Body over the route limit: 16 MiB for Session create and Environment Templates, the [Environment file](./environment-files.md) bound for file create, 1 MiB elsewhere | 413, code `request_too_large` |
| 3 | Invalid UTF-8 | "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode." |
| 4 | Malformed JSON, trailing data, two values, a byte order mark, a whitespace-only body, or an escape that forms a lone UTF-16 surrogate | "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)" |
| 5 | A key repeated in one object, at any depth | `"Invalid body: duplicate JSON key '<key>' at '<path>'. Duplicate JSON keys are not supported."` The path joins object keys with `.` and omits array indices, such as `metadata.k` or `tools.type`. Keys compare after unescaping and case-sensitively. The first repeat in document order is reported. |
| 6 | A root that is not an object, including an array | `"Invalid type: expected an object, but got <kind> instead."` |
| — | An empty body or `null` | Treated as `{}` |

Member names match exactly. A case variant such as `Metadata` or a nested `Role` is an unknown member and gets the route's unknown-member error before any write.

Errors guarded by `echotext.Allowed`, such as unknown-member, enum, schema-root and cursor errors, repeat caller values only when they are at most 256 bytes of printable UTF-8. An unknown member that cannot be repeated gets a generic message and a null param. Metadata errors use their own validation and may repeat longer keys.

### Resource identifiers

A malformed path identifier gets exactly the response of a well-formed missing one on that route, including when the body or query is also invalid. Missing, malformed and foreign resources are indistinguishable. Identifiers that are UUIDs also resolve when written in another spelling that Go's UUID parser accepts, such as uppercase, braces or `urn:uuid:`.

## Errors

### Error types

| Status | `type` | `code` |
| --- | --- | --- |
| 400 for a missing or invalid `OpenAI-Beta` | `invalid_beta` | `invalid_beta` |
| 404 for a missing, malformed or foreign resource on Beta routes | `not_found_error` | `not_found_error`, message "Resource not found." |
| 404 for a missing File or Skill | `invalid_request_error` | null |
| 401 | `invalid_request_error` | See [Authentication](#authentication) |
| 409 | `conflict_error` | `conflict_error` for conflicts the official service reports; Core-only conflicts keep their own code, such as `idempotency_conflict` |
| 5xx | `server_error` | Core's code |

Other 400 responses use type `invalid_request_error`. Validation failures with an official equivalent use code `invalid_request_error` and the observed param and message; other request errors keep Core's local codes, such as `invalid_request` or `unsupported_or_invalid_configuration`.

### Validation errors

| Case | Response |
| --- | --- |
| More than 16 metadata pairs, a key over 64 characters or a value over 512 characters on Agent or Session create or update | Param `metadata` or `metadata.<key>` and the official message with the actual count or length. Pairs are checked before keys and values, keys in sorted order. |
| A non-string metadata value on Agent or Session create or update, or Vault create | Param `metadata.<key>`, message `"Invalid type for 'metadata.<key>': expected a string, but got <kind> instead."` The first such value in document order is reported first. |
| Agent `name` over 128 characters | Param `name`. Empty and untrimmed names are accepted. |
| U+0000 in a metadata key or value | Param `metadata.<key>` |
| U+0000 or invalid UTF-8 in any other stored string or query filter | Null param, message "Request text contains characters this service cannot store or compare, such as U+0000 or invalid UTF-8." Nothing is written. PostgreSQL cannot store U+0000, which the official service accepts. |
| Environment Template or inline Session network rejections: wildcards, ports, schemes, IPv6, empty hosts, a `restricted` policy without domains, more than 100 domains, domains with another access mode | Null param, Core's message |
| Empty Session update body | "At least one update field is required" |

## Lists

Lists return `object: "list"`, `data`, `has_more`, `first_id` and `last_id`; an empty page has null first and last IDs. `order` defaults to `desc`. [Environment files](./environment-files.md) page with their own `page` token and are not covered here.

### Query parameters

| Case | Beta lists | Files | Skills and Skill versions |
| --- | --- | --- | --- |
| Unknown key, including `tenant_id` | Ignored, on lists and on single-resource routes | Ignored | Ignored |
| Repeated supported key, including a scalar `status` | 400 `invalid_request_error`, null param, "Failed to deserialize query string: duplicate field `<key>`" | 400 `unsupported_parameter` | 400 `duplicate_parameter`, param `<key>`, the official message |
| `order` other than `asc` or `desc`, including an explicit empty `order=` | 400 `invalid_request_error`, null param, "Failed to deserialize query string: order: unknown variant `<value>`, expected `asc` or `desc`" | 400, null code, "order must be asc or desc." | 400 `invalid_value`, param `order`, `"Invalid value: '<value>'. Supported values are: 'asc' and 'desc'."` |

The pinned Python SDK drops empty query values, so `list(order="")` sends no `order` and uses the default. `after` is trimmed of surrounding whitespace. Checks run in this order: repeated keys, then `limit`, then `order`; Vault and Credential `status` is checked first. These checks run before any resource lookup.

Vault and Credential lists accept `status` as a scalar, as `status[]` entries, or both, and filter by their union. Both statuses are listed by default. Another value returns 400 `invalid_request_error` with a null param and "Failed to deserialize query string: status: data did not match any variant of untagged enum VaultStatusFilterParam".

### Page size

| Lists | Default | Accepted | Other values |
| --- | --- | --- | --- |
| Agents, Sessions, Items, Environment Templates, Subagent Items, Subagent Turn Items | 20 | 1–100 | 0 becomes 1; above 100 becomes 100 |
| Vaults, Credentials | 20 | 1–100 | 0, negative and larger integers, including overflowing ones, are clamped into 1–100 |
| Turns, Subagents, Subagent Turns, Artifacts | 20 | 1–100 | 400 `invalid_request_error`, "limit must be between 1 and 100" |
| Skills, Skill versions | 20 | 0–100 | 0 returns an empty page whose `has_more` reports whether a resource follows the cursor. Negative: 400 `integer_below_min_value`, param `limit`. Above 100: 400 `integer_above_max_value`, param `limit` |
| Files | 10000 | 1–10000 | 400 with a null code, "limit must be between 1 and 10000." |

A `limit` that is not a decimal integer, including an empty value, returns 400 `invalid_request_error`, "Failed to deserialize query string: limit: invalid digit found in string" on Beta lists; outside Vault and Credential lists, a value above the signed 64-bit range returns "Failed to deserialize query string: limit: number too large to fit in target type". A leading `+` is accepted when encoded as `%2B`; a leading `-` returns the invalid-digit error on Beta lists except Vaults and Credentials. Skills return `invalid_request`, "limit must be an integer between 0 and 100."; Files return `invalid_request` with the Files range message.

### Cursors

`after` names a resource of the same list, inside its already resolved parent and tenant. The parent is resolved first: a missing or foreign parent returns its 404 before the cursor is read. A cursor that does not resolve, whether random, malformed, of another type, of another parent, deleted or of another tenant, returns:

| Lists | Response |
| --- | --- |
| Agents, Sessions, Turns, Environment Templates, Vaults, Credentials | 404, type and code `not_found_error`, "Resource not found." |
| Session Items, Subagent Items, Subagent Turn Items | 400 `invalid_request_error`, null param, "Invalid session item ID in `after`" |
| Subagents, Subagent Turns | 400 `invalid_request_error`, null param, "Invalid resource ID in `after`" |
| Session Artifacts | 400 `invalid_request_error`, null param, "after is not a valid artifact ID" |
| Skill versions | A value that does not begin with `skillver`: 400 `invalid_value`, param `after`, `"Invalid 'after': '<value>'. Expected an ID that begins with 'skillver'."` A version of another Skill: the same fields, "Skill version cursor does not match this skill." A malformed `skillver` suffix or a missing, deleted or foreign version: 404 with a null code and param |
| Skills | 404 with a null code and param |
| Files | 404, param `after` |

## Agents

### Saved configuration

Agent create requires `model`. Core saves and returns these values for omitted fields:

| Field | Saved value |
| --- | --- |
| `name`, `instructions` | null |
| `metadata` | `{}` |
| `tools` | `[]` |
| `text` | `{"format": {"type": "text"}, "verbosity": "medium"}` |
| `reasoning` | Saved as sent; an omitted effort stays unset rather than taking a model default. Agent and Session responses always carry `reasoning.effort` and `reasoning.summary`, null when unset |
| `service_tier` | `auto` |
| `multi_agent` | Disabled. When enabled without `max_concurrent_subagents`, 6 |
| Function `defer_loading` | `false` |
| `programmatic_tool_calling.enabled` | `true` |
| `web_search` | Every pinned mode is saved; see [tool policy](./execution-tools.md#web-search-and-programmatic-tool-calling) |
| HTTP MCP transport | Saved with `headers: {}`; nonempty headers are rejected. Origin and allowlist defaults are in [public MCP connection origin](./environments.md#public-mcp-connection-origin) |

Saving a value does not make it executable. Session creation admits a smaller set; see [Session admission](#session-admission).

### Configuration validation

Agent create and update bodies and the inline `agent` of Session create are checked against the pinned shapes of `tools`, `text`, `reasoning`, `service_tier`, `multi_agent`, `model`, `name` and `instructions`, before their parsers and before Harness admission. Failures return 400 with type and code `invalid_request_error`:

| Case | Param | Message |
| --- | --- | --- |
| Missing required member | JSON path, such as `tools[0].parameters`; on Session create `agent.tools[0].parameters` | `Missing required parameter: '<path>'.` |
| Unknown member, including a case variant and the unpinned `tool_choice` | JSON path | `Unknown parameter: '<path>'.` |
| Wrong JSON type | JSON path | `Invalid type for '<path>': expected <kind>, but got <kind> instead.` |
| Unsupported enum value | JSON path | `Invalid value: '<value>'. Supported values are: ...` with the pinned values |
| Integer below minimum | JSON path | `Invalid '<path>': integer below minimum value. Expected a value >= 1, but got <n> instead.` |
| Repeated function name, more than one `web_search`, more than one `tool_search` | null | `duplicate function tool name: <name>`, `duplicate web_search tool`, `duplicate tool_search tool` |
| Function `parameters` with a string root `type` other than `object` | null | `Invalid schema for function '<name>': schema must be a JSON Schema of 'type: "object"', got 'type: "<type>"'.` |
| `text.format` JSON schema with a string root `type` other than `object` | null | `agent.text.format.schema must have top-level type "object"; got "<type>"`, also on Agent requests |

Within one object Core reports a union's `type` first, then unknown members, then member values in document order, then missing members; tools before `text`, and the whole object before the duplicate and schema-root checks. Schemas without a string root `type` are not checked. Function and output schemas, MCP `transport`, `request_metadata`, `metadata` and `x_agents_core` keep their own parsers. Update bodies and the inline Session agent are validated before the Agent lookup, so owned, foreign, missing and malformed Agent IDs give the same response.

Core saves values the pinned shapes allow even when it cannot run them: function names of any length, enabled programmatic tool calling, reasoning effort `max` and service tier `flex`.

### Session admission

A Session's effective configuration must also pass execution admission, which applies to saved and inline configuration alike. Admission reports protocol errors from the table above first, including duplicate tools and schema roots in saved Agents, then these, all 400 `unsupported_or_invalid_configuration` before any write:

| Configuration | Message |
| --- | --- |
| Explicit `reasoning.effort` or `reasoning.summary` | "Explicit reasoning execution options are not supported by this service yet." |
| `service_tier` other than `auto` | "Execution currently supports service_tier=auto only." |
| Enabled or omitted-mode `web_search`, enabled `programmatic_tool_calling` | See [tool policy](./execution-tools.md#web-search-and-programmatic-tool-calling) |
| More than 64 functions, or a function name that is blank or longer than 512 bytes | "This service supports at most 64 function tools." or "Function names must be nonempty, unique and at most 512 bytes." |
| Two `programmatic_tool_calling` declarations, two MCP servers with one label | "Execution requires distinct tool controls.", "Execution requires distinct MCP server labels." |

A per-Session `tools` replacement admits a Session whose saved tools would be rejected. Support for each tool and Harness is in [execution and tools](./execution-tools.md).

Omitted, null and explicit `medium` text verbosity give the same Session configuration. For a model whose native catalog declares no verbosity support, the Codex adapter drops a `medium` setting and uses the model's default, and rejects `low` or `high`.

### Update, delete and list

| Operation | Core behavior |
| --- | --- |
| `POST /agents/{agent_id}` | Replaces only the supplied fields. Nested objects replace the whole field; null `name` or `instructions` clears it; null or `{}` metadata clears all pairs, and an object replaces them. Existing Sessions keep their snapshots. |
| `DELETE /agents/{agent_id}` | Returns `{id, object: "agent.deleted", deleted: true}`. Sessions created from the Agent, their history and their creation retries are unaffected. A repeated or missing deletion returns 404, and new Sessions that name the Agent return 404. |
| `GET /agents` | Pages by creation time, then ID. |

## Sessions

### Configuration snapshot

Session creation copies the effective Agent configuration into an immutable snapshot. With `agent_id`, the saved Agent is read once; fields in the inline `agent` replace the saved field whole, including arrays, and null `tools` clears the list. Omitted fields inherit; an inline `x_agents_core` that omits `harness` keeps the saved harness. Saved Agent metadata never becomes Session metadata. Later Agent updates or deletion affect only new Sessions.

`stream` defaults to false. `stream` and `agent_id` cannot be null. Omitted or null `metadata` is `{}`.

Creation validates the body and metadata types, the request fields and initial input, and the placement and streaming input requirements before looking up a creation retry. For new work, Core resolves the Template, saved Agent and model configuration, binds Vault Credentials, then validates the selected Harness and execution configuration before writing. A failed dependency lookup rechecks the retry identity so an already committed creation remains recoverable.

### Creation retries

Send an `Idempotency-Key` of 1–128 bytes that is not only whitespace; a longer or whitespace-only key returns 400 `invalid_request`. An empty header counts as no key. Without a key, every request creates a new Session. The official service creates a new Session for each request even with the same key; Core returns the original one.

| Case | Response |
| --- | --- |
| Same key, same request, same Project | 201 with the Session's current state. No input is admitted again. A `stream=true` retry returns 201 with no events and closes. |
| Same key, different request | 409 `idempotency_conflict` |
| Same key after the Session was deleted | 409 `idempotency_conflict` |

Keys are scoped to the Project; any key of the Project, including one issued after a rotation, can retry. A request that has an inline Agent without `model` or names a saved Agent, a template, initial files or preparation, `vault_ids` or credential references, `x_agents_core`, or an `openai_hosted` environment is compared as sent, before any of those sources is read: a matching retry returns the original Session even after the Agent, template, Credential or deployment default changes or is deleted. Other requests are compared by their resolved configuration. Model provider keys enter the comparison only as fingerprints.

### Update and list

`POST /agents/sessions/{session_id}` accepts only `metadata`, which is required: null or `{}` clears it and an object replaces all pairs. Execution state and the creation retry identity are unchanged.

`GET /agents/sessions` accepts `agent_id`, which matches the Session's immutable root Agent ID, including inline Agent IDs and Agents that were since updated or deleted. The filter applies before pagination; an empty `agent_id` is a filter, not an omission.

### Delete

`DELETE /agents/sessions/{session_id}` deletes a Session that is idle or failed, has no queued, running or waiting root Turn and no pending input reservation.

| Case | Response |
| --- | --- |
| Deletable | 200 `{id, object: "agent.session.deleted", deleted: true}`. Reads, updates, input, Turns and Items of the Session then return 404, and its open event streams end. Core releases the Session's Core-managed sandbox; a `self_hosted` machine and its files are left alone. |
| A root Turn is queued, in progress or waiting for required actions, or input is waiting for admission, a self-hosted connection or hosted provisioning | 409, type and code `conflict_error`, null param, "session must be durably idle or failed without required actions before deletion". Nothing changes. |
| The caller's own Session, already deleted | 200 with the same confirmation |
| Missing, malformed or foreign | 404 |

Subagent child Turns and pending Environment file writes do not block deletion. To delete running work, send `agent.session.input.cancel`, wait until the Session is idle, then delete. Input waiting for its Environment cannot be cancelled; the Session becomes deletable when the input starts, its five-minute deadline passes or the Environment fails. Core admits a Turn in the same transaction that returns 202 for its input, so a deletion right after that 202 returns 409.

### Response fields

A Session's `agent.tools` omits `tool_search` declarations, which the pinned Session tool union does not include; the frozen configuration keeps them. On `self_hosted` Sessions, `environment.remote_url` is Core's daemon WebSocket URL, `/api/v1/agent-daemon/ws` under the public URL, which only OpenAgentCore's Runtime daemon speaks; requests cannot set it. Other Session fields follow the pinned types; `x_agents_core` is described in the [Agents API guide](../../docs/api/public-agent-api.md#core-extensions-x_agents_core).
