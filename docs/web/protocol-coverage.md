# Protocol coverage

This matrix is a delivery contract for Agents Core Web, not a claim of complete
OpenAI-hosted service compatibility.

## Compatibility baseline

- Current immutable in-repository Parsar Core capability baseline:
  [`0000a0b`](https://github.com/MiniMax-AI/parsar-core/commit/0000a0b32523deb0f7a4d907f31ca4503cff7e9e).
  This immutable revision retains the Web-used Agent admission, chat, Environment,
  and trace-observability boundaries, bounded live Workspace file-metadata listing,
  and the project-owned Source Files upload/retrieve/content/delete lifecycle.
  It also exposes the operator-gated basic Codex/Docker `openai_hosted` profile,
  both supported Environment resource projections, and Environment Files.create
  for that qualified managed placement. `OpenAI-Beta: agents=v1` plus the
  `/v1/agents/**` resources and Session events endpoint remain the versioned Web/Core
  contract. The Core startup extension exposes a safe build-support and process-
  configuration snapshot, but no execution-readiness, live Runtime capability, or
  build-version resource. It exposes Codex-only, Session-scoped `self_hosted`
  creation; basic Codex/Docker `openai_hosted` creation when Core is explicitly
  configured with a qualified managed provider; Environment retrieval; bounded
  Files.list; and Source Files. It still exposes no public Environment list or
  standalone CRUD, Source Files list, browser-facing key route, or
  managed-provider discovery/configuration route.
- Environment Template support was originally read at the immutable Parsar revision
  [`d8ed9d42`](https://github.com/MiniMax-AI-Dev/parsar/commit/d8ed9d4230dcda13293b262ad1dcb584842a6271),
  and is included in the current in-repository Core baseline. It adds the five
  `/v1/agents/environments/templates` operations and the
  Session `environment.environment_template_id` reference. That revision also
  records a separate real E2B qualification of the managed Runtime for the same
  three harnesses, alongside the earlier Docker qualification. E2B is a Core-side
  provider behind the unchanged `openai_hosted` discriminator: the public contract
  exposes no provider field, no image or sandbox template selector, and no
  provider configuration route. The startup extension can name the selected
  provider kind without exposing provider identity or proving Runtime readiness;
  Web still cannot select or configure it. A Core build without the Template resource rejects that
  collection path, and Web reports the absent capability instead of an empty
  configuration list.
- Upstream resource source: `openai-python` 3.13.0 beta Agents resources at
  [`d7c41efe`](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents)
- Required beta header for `/v1/agents/**`: `OpenAI-Beta: agents=v1`. Source
  `/v1/files**` routes deliberately omit it.
- Core base: same-origin `/v1` through the Web proxy for stock Parsar Core; a direct
  URL only for a compatible Core or proxy with explicit CORS support

Parsar implements a partial subset of the pinned upstream resource inventory, and
its handlers still implement partial request/event semantics. Importing an official
SDK or accepting extra fields is not compatibility proof. Unsupported capabilities
must fail explicitly.

Requests use `Authorization: Bearer <execution-principal key>`. Optional
`OpenAI-Organization` and `OpenAI-Project` headers, when present, must exactly match
the Core key binding. Agents Core Web's local proxy owns the bearer server-side.

## Web-used surface

| Resource / behavior | TypeScript client | Initial UI | Notes |
| --- | --- | --- | --- |
| Core startup configuration extension | Yes | Yes, System | Strictly projects build-supported harness/provider kinds and validated process selections. Operator endpoint reporting is boolean; URLs, credentials, paths, daemon/Runtime identity and Session/Environment observations are excluded. Unsupported older Core versions remain explicit |
| Saved Agents create/list | Yes | Yes | Dedicated setup covers model, name, instructions, bounded metadata, the Session-safe text/medium/implicit-reasoning/auto-tier profile, and the strictly bounded Function/service-origin HTTP MCP form profiles; the broader Saved Agent contract is not execution proof |
| Saved Agents retrieve/update/delete | Yes | Yes | Agent details support viewing, editing, and deleting saved Agents; unsupported or saved-only Tool values remain read-only and are omitted from unrelated updates |
| Function form profile | Yes | Yes | Web validates at most 64 non-deferred definitions, unique non-whitespace names of at most 512 UTF-8 bytes, descriptions, and object JSON-Schema parameters before writing |
| HTTP MCP form profile | Yes | Yes | Web writes only HTTP(S), `transport.type:http`, and `connection_origin:service`, with optional allow-list and boolean `required`. Authentication is either anonymous or an exact catalog-resolved Vault static-bearer Credential; arbitrary headers, request metadata, OAuth, stdio, and browser-origin remain unavailable |
| Sessions create/list/retrieve | Yes | Yes | Managed creation can reference one tenant-owned `environment_template_id`; Core freezes the resolved configuration and returns no template reference. With no meaningful initial input, UI sends a JSON `stream:false` create for `none`/`self_hosted` and receives an idle Session. With meaningful input, and for every `openai_hosted` create, it consumes the POST SSE while running the required durable refresh, then hands off to the normal events stream. List supports both an all-pages loaded result and a server-side root-Agent scope |
| Root-Agent Session filter | Yes | Yes | Core applies `agent_id` before pagination, and every page and opaque continuation carries that same scope; **All Agents** omits it. Web rejects a returned Session whose root Agent does not match the requested scope |
| Session-only Agent overrides | Yes | Yes, finite | Whole-field overrides can replace `model`, set/clear `instructions`, replace the supported plain-text configuration, reset saved-only `multi_agent`, `reasoning`, or `service_tier` values to Core defaults, and inherit, clear, or fully replace `tools` through the bounded Function/HTTP MCP editor. Untouched fields are omitted; there is no arbitrary Agent JSON or patch-style partial Tool update |
| Sessions update/delete | Yes | Yes | Title/string metadata editing and one-Session confirmed deletion; no bulk or Workspace deletion |
| Session live events | Yes | Yes | Authenticated `fetch` stream, not `EventSource`; reconciles durable state, projects exact text and command-output deltas, and treats an in-band `error` as a retryable interruption rather than conversation data |
| Input message / steering | Yes | Yes | Opens SSE before submitting `agent.session.input.message`; a Session `failed` projection from its latest failed input/Turn can accept a later message that starts a new Turn, while an exactly observed terminal `failed`/`expired` Environment keeps input disabled. Only HTTP 204 is durable admission; Core errors or an unexpected 2xx remain visible and uncertain failures retain the in-memory payload/key for an explicit unchanged manual retry only |
| Active Turn cancel | Yes | Yes | Submitted as a Session event, not a Turn-create endpoint |
| Turn list | Yes | Yes, read-only | Selected Sessions load every page in ascending creation order; no Turn mutation UI |
| Turn retrieve | Yes | No | Reusable client diagnostic method; timeline recovery uses the all-pages list |
| Item list/recovery | Yes | Yes | Authoritative recovery after stream loss |
| Parsar `apply_patch` Item presentation | Existing function Item fields | Yes, read-only | Parsar extension recognized only for the pinned `changes[].{path,kind,diff}` shape; not an OpenAI standard Item type |
| Function result/error | Yes | Yes | Supports text `agent.session.input.tool_result` success/error handoff for exact `function_call` actions |
| Initial-input creation stream | Yes | Yes, bounded text messages | Accepts an exact non-empty text string or an ordered array of user messages containing `input_text` parts only. Images, attachments, non-user roles, and other content parts are not supported |
| Environment Files.list | Yes | Operator-gated, explicit read-only | Hidden unless the complete list + managed-create profile is qualified and `AGENTS_CORE_WEB_ENVIRONMENT_FILES=1`, because Core has no capability-discovery route. For a complete supported projection, lists direct regular-file path and size metadata only; `openai_hosted` uses `/workspace`, while `self_hosted` uses its exact `workspace_directory`; no automatic load, content, download, recursion, or snapshot guarantee |
| Source Files upload/retrieve/content/delete | Yes | Yes, explicit by ID | Multipart purpose is fixed to `user_data`; metadata and binary responses are strictly projected; Core has no Source Files list; uncertain writes are never replayed |
| Environment Files.create | Yes | Hosted-gated | The client supports the exact `inline`/`file_id` union. UI offers bounded inline or upload → durable `file_id` copy only after an exact current basic `openai_hosted` Session projection and same-ID resource read satisfy the non-terminal, empty-installation write gate |
| Artifacts | No | Hidden | Source Files and Environment Files are not an Artifacts API |
| Environment connection action | Yes | Guided, not submitted | `environment_connection` is distinct from a function call; Web can show an operator-run launcher template but sends no result and never opens the native transport |
| Environment lifecycle events | Yes | Read-only | UI projects pinned pending, ready, connected, disconnected, and failed live snapshots; unknown/malformed status events clear prior live claims and render as unavailable |
| Environment retrieve | Yes | Yes, read-only | Strictly recognizes `self_hosted` and `openai_hosted` resource types. Session UI automatically reads the valid current Environment ID for either supported type; hosted write eligibility additionally requires the exact basic hosted projection and same-ID durable resource; there is no standalone Environment create/list/update/delete resource |
| Environment overview | No public list API | No top-level UI | Web does not turn loaded Session projections into a catalog; a selected Session may still show its exact Environment data |
| Environment Templates create/list | Yes | Managed-hosted-gated | The Templates navigation entry lists all pages and creates basic name/network configurations. It shares the catalog with Session creation. An unsupported or failed read is explicit, never an empty catalog |
| Environment Templates retrieve/update/delete | Yes | Basic management | Templates supports partial name/network updates and confirmed deletion, followed by a Core catalog refresh. It neither creates Runtime instances nor edits existing Session snapshots. Advanced profiles outside the current strict client projection remain unsupported; no hidden fields are erased |
| Environment keys | No public browser API | Hidden | Operator-issued executor credentials stay on executor compute and never enter browser state, request previews, navigation, or Create actions |
| Vaults and static-bearer Credentials | Yes | Yes, when discovered | Web traverses the Vault and per-Vault Credential page chains to their Core end markers before publishing one loaded metadata result, provides safe lifecycle controls and write-only token create/replace, and deterministically attaches each selected Credential's owning Vault to Session creation. The reads are non-atomic and not a current Core total. Tokens are never returned; catalog success is not runtime proof |
| Protocol Subagents / enabled multi-agent | Later | No | Distinct from storing multiple Agent configurations |
| Usage/observability | Response types | Yes, scoped | Session aggregate and per-Turn token Usage are labelled separately; unavailable measurements remain unknown, not zero |

## Runtime boundary

- The Web creates `environment: {"type":"none"}` Sessions by default. The
  non-secret `AGENTS_CORE_WEB_SELF_HOSTED_SESSIONS=1` build/dev-server flag exposes
  a second, Codex-only `self_hosted` choice for an operator-reviewed Core deployment.
  The independent `AGENTS_CORE_WEB_OPENAI_HOSTED_SESSIONS=1` flag exposes only the
  pinned basic Codex/Docker `openai_hosted` request after an operator has qualified
  Core's managed Runtime provider. Its network choice is omitted/default enabled,
  explicitly enabled, or explicitly disabled; restricted domains are not exposed.
  The same flag exposes reading and saving basic Environment Templates and
  referencing one from the created Session. An omitted network inherits the
  referenced Template policy, an enabled request is blocked before submission when
  the Template disables network access, and an explicit null network is never sent
  beside a reference. Saving a Template stores configuration only and allocates no
  Runtime. Whether Core provisions that managed Runtime as a local container or a
  remote sandbox is operator-owned and invisible to this Web.
  The flag is off by default because Core has no public capability-discovery route.
  Both flags change Web presentation only; neither is evidence that the connected Core has
  configured execution, an executor registry, a reachable executor origin, or a
  ready managed Runtime, native harness, model, or provider.
- Environment Files UI is independently hidden unless the operator has qualified the
  connected Core and sets `AGENTS_CORE_WEB_ENVIRONMENT_FILES=1`. A 404
  `unsupported_operation` is reported as an unsupported Core, never as an empty
  Workspace. Managed reads use `/workspace`; self-hosted reads are constrained to and
  send the exact frozen `workspace_directory` returned by the Session.
- Start Session exposes an optional title only. The title is written as
  `metadata.title`; additional creation metadata stays empty and is not exposed in
  this form. The existing-Session action surface may edit additional string metadata;
  the complete map follows the pinned 16-pair, 64-character key, and 512-character
  value limits and must never contain secrets.
  Initial input accepts an exact non-empty text string or an ordered array of user
  messages containing `input_text` parts only. Message order and grouping are
  preserved. Each message must contain non-whitespace joined text; images,
  attachments, non-user roles, and other content parts are not supported. Empty or
  whitespace-only simple text is omitted. Meaningful input selects streaming
  `POST /agents/sessions`; an omitted input selects JSON `stream:false` for `none`
  and `self_hosted`, while `openai_hosted` always uses creation SSE so provisioning
  can be reconciled before the ordinary events-stream handoff.
- Streaming creation must begin with a canonical `agent.session.created` snapshot.
  Web buffers subsequent creation events while reconciling the durable Session, all
  Item pages, and an eligible Environment projection and starts the all-pages Turn
  read independently. After the POST SSE settles with a known Session, Web performs
  the required durable refresh and hands live updates to
  `GET /agents/sessions/{session_id}/events`; Turn pages apply separately only if their
  fenced read remains current and do not block the handoff. A failure before a
  canonical created snapshot leaves the create outcome unconfirmed; a later stream
  failure reconciles and hands off instead of replaying the POST.
- The optional inline `agent` object is a finite, whole-field Session-only override.
  Web can replace `model`, set or clear `instructions`, replace the supported
  plain-text configuration, reset saved-only `multi_agent`, `reasoning`, or
  `service_tier` values to Core defaults, and inherit, clear, or fully replace
  `tools` through the bounded Function/HTTP MCP editor. Untouched fields are omitted.
  Admission and Vault derivation use the effective Agent snapshot after applying
  that override. There is no arbitrary Agent JSON editor or patch-style partial Tool
  update. Environment creation is limited to `none`,
  the separately enabled `self_hosted` profile, and the separately enabled basic
  `openai_hosted` profile. Managed creation is blocked before persistence when the
  effective Agent contains MCP because hosted MCP is not qualified.
- A failed create is never retried automatically. Its stable recursive fingerprint
  contains `agent_id`, the finite `agent` override when present, `environment`, exact
  optional `input`, normalized `metadata`, `stream`, and sorted derived `vault_ids`.
  Object keys are normalized recursively and other array order remains significant.
  Only an explicit retry with the same complete fingerprint reuses the in-memory
  idempotency key; any projected request change rotates it.
- The Sessions root-Agent filter is a Core query, not a client-side filter over one
  loaded page. Core applies `agent_id` before pagination; the first request and every
  continuation carry that same scope together with the preserved limit and order.
  **All Agents** omits `agent_id`. Filter changes abort and fence stale reads, and an
  accepted filtered page may not contain a Session whose Agent snapshot has another
  ID.
- Message, active-Turn steering, cancel, and function-result/error writes use the
  current `agents=v1` Session events contract. Web does not add a separate private
  runtime-readiness gate. The caller creates and retains a non-blank, at-most-128-byte
  idempotency key before the first event write; the client never retries automatically,
  and an uncertain explicit retry must reuse the same payload and key. Only exact HTTP
  204 denotes durable event admission; every other status, including another 2xx,
  fails closed. HTTP acceptance, health, Agent creation, and an open SSE stream still
  do not prove that execution will complete; subsequent durable Session, Turn, and
  Item state is authoritative.
- Product navigation and the global Create menu do not widen the protocol. Agent
  and Session creation call the existing client methods. The top-level
  Environments destination and Environment key entries are absent because
  Core exposes no corresponding list or management APIs. Environment Templates have
  a Core contract but no top-level destination: they appear only inside managed
  Session creation.
- The Agent setup request preview is derived entirely from editable Agent fields and
  the sanitized Core base URL. Its authorization header always contains the literal
  `${AGENTS_CORE_API_KEY}` placeholder; it never reads or renders the connection's
  server-managed or current-tab bearer.
- Saved Agent persistence and Session execution are separate contracts. Parsar
  `dadf64a7` can store explicit reasoning, non-`auto` service tiers, and JSON-schema
  text formats, but rejects each of them before creating a Session. Enabled
  multi-agent configuration, saved-only tool types, deferred/invalid/duplicate
  function or MCP identities, and MCP credentials without attached Vaults are
  rejected at the same boundary. Web resolves every MCP `credential_id` against the
  last successfully traversed Vault/Credential page-chain result, requires an exact
  destination URL match, and
  derives sorted unique owning `vault_ids`; missing, stale, URL-mismatched, or
  ambiguous bindings are deterministic blockers. The Web therefore
  omits reasoning for new Agents, uses `service_tier:auto`, ordinary text, and
  medium verbosity, and blocks every Session-start entry point for a loaded Agent
  with a deterministic admission conflict. Existing saved-only values remain
  inspectable; fields exposed for editing are never silently rewritten.
- The Agent form exposes exactly two Tool write profiles. Its Function profile is
  always non-deferred and enforces a unique non-whitespace name of at most 512
  UTF-8 bytes, a description, object JSON Schema parameters, and a maximum of 64
  Functions. Function execution remains outside the browser; the existing result
  handoff uses the exact durable Turn and call identity reported by Core.
- The MCP form profile is service-origin HTTP(S). It supports anonymous access or a
  Vault-backed static-bearer Credential selected from the last successfully
  traversed Vault/Credential page-chain result.
  Selecting a Credential locks the exact destination URL and writes only its
  `credential_id`; the token never enters the Agent form. Arbitrary headers, request
  metadata, OAuth, stdio, client-origin, unresolved Credentials, and malformed MCP
  variants remain unsupported/read-only. MCP execution and discovery remain
  Core-service-owned, including for a `self_hosted` Session whose Workspace commands
  are executor-owned.
- Other Tool definitions—including `tool_search`,
  `programmatic_tool_calling`, deferred Functions, unresolved or malformed MCP,
  and future variants—remain read-only. Web offers no Web Search or Code Mode
  switch. An unrelated Agent edit omits `tools`; an attempted Tool edit while a
  read-only definition remains fails closed instead of deleting, interpreting, or
  resending unknown JSON. The public contract exposes no Function, MCP, model,
  provider, or executor readiness resource.
- Saved Agent names are limited to 128 Unicode characters. Agent metadata is limited
  to 16 string pairs, 64 Unicode characters per key, and 512 per value; the Web
  enforces those limits before a write.
- A selected `environment:none` Session renders no Environment or Workspace panel;
  absence of that optional UI is not an error and does not block conversation use.
  Read-only Environment status is shown only for a complete supported `self_hosted`
  or basic `openai_hosted` projection, or as an explicit unavailable state for an
  unsupported Environment variant that must fail closed.
- Parsar Core at the audited revision supports creating a `self_hosted` Environment
  only as part of a Codex Session. When the operator flag is enabled, Web can create
  a Session with an absolute executor-host `workspace_directory` and exactly empty
  `capability_directories`. It may carry the same optional text input, title metadata,
  and finite Session-only Agent override as `environment:none`; it is idle only when
  input is omitted. Web cannot create an Environment independently, choose an
  executor, or claim the requested Workspace exists. A Core rejection leaves the
  setup visible and is never retried automatically. The exact failed projected
  request retains its in-memory idempotency key only for an explicit unchanged retry;
  the complete fingerprint rules above determine every new operation.
- The reusable client distinguishes the admitted `self_hosted` request fields
  (`workspace_directory` and optional `capability_directories`) from the safe Session
  response projection (`id`, `remote_url`, `workspace_directory`, and normalized
  `capability_directories`). Unknown Environment variants remain opaque, inspectable
  records and are not eligible creation inputs.
- A successful self-hosted Session response supplies the Environment ID and
  `remote_url` used by the connection guide. A runnable launcher template is shown
  only for a complete known projection with a canonical Environment UUID and a
  strictly valid HTTPS executor origin or loopback HTTP development origin. The
  template uses static `$REMOTE_URL` and
  `$ENVIRONMENT_ID` variables plus credential-file and Codex-binary path placeholders;
  it never embeds an executor token, caller bearer, provider credential, URL userinfo,
  query, or fragment. Copying the template does not start an executor.
- An optional, default-off local Docker guide is presentation policy layered on
  that same strict projection; it is not an Agents API resource or executor
  capability. It is rendered only for a loopback HTTP origin and a complete
  operator build profile containing non-secret image, API-container, numeric-user,
  credential-path, and runtime-root strings. The copied block uses an exact
  Environment UUID, a stable per-Environment container/state directory, and an
  explicit host-to-Environment Workspace bind. Web never reads the credential,
  opens Docker, executes the block, retries it, or treats copying/running it as
  connection or runtime readiness. Existing queued input may execute when the
  operator connects the Environment.
- `required_actions` is a discriminated union. `function_call` carries call, Turn,
  function-name, and argument fields; `environment_connection` carries only
  `environment_id`. The initial Web renders the latter as an operator-owned wait and
  does not expose a Function Result form or claim that the browser can connect it.
- Environment resource status and Session Environment event status are distinct
  contracts. The durable resource accepts `pending`, `connected`, `disconnected`,
  `expired`, and `failed`. Live events accept `pending`, `ready`, `connected`,
  `disconnected`, and `failed`, with a nullable structured error. `expired` is
  therefore durable-only and `ready` is live-only; neither vocabulary is widened by
  an unchecked cast. Unknown or malformed values clear any older live claim.
- Once a current Session read supplies a valid `self_hosted` or `openai_hosted`
  Environment ID, the
  client issues one authenticated, abortable
  `GET /agents/environments/{encoded_environment_id}`. It accepts only HTTP 200 and
  strictly projects exactly `id`, `object`, `type`, `status`, `files`, `plugins`, and
  `skills`, with the matching ID, `agent.environment` object, a known
  `self_hosted` or `openai_hosted` type, a supported durable status, and array-valued
  installation metadata. Session recovery consumes either supported branch; only
  the exact basic `openai_hosted` branch can proceed to hosted write eligibility.
  UUID comparison
  permits an uppercase request to match Core's lowercase canonical response, and the
  projected resource retains that canonical response ID. It never writes or retries
  this read.
- Empty `files`, `plugins`, and `skills` arrays mean only that Core reports no
  API-managed installations. They are not the host filesystem, Workspace contents,
  launcher capabilities, executor inventory, or the data source for Files.list.
- For a complete supported `self_hosted` or basic `openai_hosted` Session
  Environment projection, Web
  exposes a user-triggered, authenticated
  `GET /agents/environments/{encoded_environment_id}/files` read. It never loads
  files automatically. The UI supplies an absolute directory within the reported
  Workspace, `limit=20`, the selected `asc` or `desc` order, and Core's opaque
  `page` token for continuation while preserving the applied directory, order,
  and limit.
- The client accepts only HTTP 200 and the official page envelope
  `{object:"page",data,next,has_more}`, where `has_more` is true exactly when
  `next` is set. A directory typed with a trailing separator is sent in cleaned
  form, which Core requires. Every entry must have the matching Environment ID,
  `object:"agent.environment.file"`, a safe absolute path, and a non-negative
  safe-integer `size_bytes`. A malformed success rejects the complete page; no
  partial result is accepted or retried automatically.
- Files.list is a live, non-recursive metadata read. Paths remain plain text. It
  does not read file contents, follow browser links, upload, download, mutate, or
  create files, and it does not create a Turn. Core may invalidate an opaque
  continuation when directory paths or sizes change; Web then requires an explicit
  refresh. Requests are abortable and fenced when the Environment or Workspace
  changes.
- Source Files are a separate project-owned lifecycle under `/v1/files`. Upload is
  one multipart `file` plus fixed `purpose=user_data`, with a 512 MiB content bound;
  metadata retrieve, complete binary download, and delete operate only by the
  returned `file-…` ID. These routes use the caller bearer but never the Agents beta
  header. There is no list, expiration, resumable upload, alternate purpose, or
  browser-persisted inventory. The Web keeps the selected file and returned ID in
  component memory only and never writes content or IDs to URL, storage, Agent or
  Session metadata, fixtures, or logs.
- Source download accepts only HTTP 200 with `application/octet-stream`, an
  attachment disposition, valid exact `Content-Length`, `Cache-Control:no-store`,
  and `X-Content-Type-Options:nosniff`; the complete received byte count must match.
  JSON or truncated content is never saved as a file. Upload has no public
  idempotency or list-based reconciliation path, so an uncertain outcome is shown
  as unknown and is not replayed. An uncertain delete performs at most one metadata
  GET to distinguish currently present from absent, never a second DELETE.
- Environment Files.create is a single-attempt exact union: strict standard Base64
  `inline` bytes or a project-owned Source `file_id`, plus one canonical file path
  beneath `/workspace/`. The client accepts only HTTP 201 Created. Source upload is
  bounded at 512 MiB while destination copy is bounded at 50 MiB. The Web's primary flow is upload → returned Source ID →
  `file_id` copy; it never substitutes a local filename or path for that ID.
  A missing response can outlive caller cancellation. Web performs at most one
  read-only directory list as a clue; matching path and size cannot prove byte
  identity, so the outcome remains unknown and the POST is not replayed.
- A hosted write form appears only when the complete current Session projection is
  the pinned basic `openai_hosted` shape and
  `GET /agents/environments/{id}` itself returns the exact same Environment identity,
  type, a non-terminal `pending`/`connected`/`disconnected` status, and empty
  `files`, `plugins`, and `skills`. It is never inferred from a Session type,
  connection event, health, Files.list, Docker configuration, or an error response.
  Terminal `expired`/`failed`, wrong identity/type, populated installations,
  restricted networking, or future fields fail closed. A referenced Template is
  resolved and frozen by Core and never returned in the Session Environment, so it
  neither relaxes nor satisfies this write gate. `self_hosted`
  Workspace listing remains read-only.
- The durable `self_hosted` Session Environment projection has no connection-status
  or error field, so the separate Environment resource read is the only durable
  status source used by the UI. The UI renders the Session's ID, sanitized HTTP(S)
  remote URL,
  `workspace_directory`, and `capability_directories` as read-only data. It removes
  URL userinfo, query, and fragment, renders even safe HTTP(S) executor URLs as
  non-clickable text, never displays non-HTTP(S) or malformed values, and never
  turns directory strings into `file://` or browser/executor access.
  Missing fields fail closed as unavailable. A matching `environment_connection`
  action is labeled as durable Core-required work, never as proof of pending or
  available execution.
- Environment failures render generic copy only; raw error code, type, and message
  fields are hidden because they can contain arbitrary credentials, Vault IDs,
  paths, or private URLs. Unknown and incomplete required actions block
  the composer rather than selecting a guessed form. A valid
  `environment_connection` notice remains separate from any simultaneous
  `function_call` result form and has no result submission control.
- Workspace means the execution directory within this Environment. It is not a
  top-level workspaces API, file-content browser, editor, or artifact capability. The UI
  links to the immutable pinned Core and caller-started launcher setup documentation.
  The path must be absolute and already meaningful on the caller-managed Linux
  executor; it is not a browser, Web-server, or daemon-container path. The operator
  issues the executor credential outside Web, stores it in a private file on that
  compute, and starts `agents-api-codex-executor` there. Web never reads the file,
  starts a process or container, or connects to daemon/executor transports.
- Known Item and Session-event discriminants remain typed. Unknown variants retain
  their raw fields for inspection, but consumers must treat them as unavailable
  rather than infer a known rendering or action.
- Parsar maps a Codex `fileChange` observation to a `function_call` named
  `apply_patch`. The Web enables its read-only diff presentation only when arguments
  are an object containing a non-empty `changes` array and every change has a string
  `path`, a string `diff`, and a `kind` object whose `type` is `add`, `update`, or
  `delete` (with an optional string or null `move_path`). Empty, malformed, string,
  missing-field, extra-field, and alternate same-name payloads retain the generic
  JSON function rendering. This is a Parsar extension, not an OpenAI standard Item
  type, and it grants no browser access to apply, edit, approve, reject, revert, or
  read file contents from an executor Workspace.
- The internal `parsar-daemon` WebSocket and the public `self_hosted` executor
  transport are different protocols. Neither is a generic Environment Provider.
- Docker, E2B, and AWS Bedrock AgentCore Runtime each need an upstream lifecycle and
  capability contract before the Web can advertise them.
- `AGENTS_API_ENGINE` selects `codex` or an operator-enabled `claude_sdk` profile for
  new Sessions. The pinned `self_hosted` profile is Codex-only; the browser sends a
  model ID and Workspace path, not an engine or executor selector.
- Core has no standard model-catalog or capability-discovery route in this surface.
  Web model presets are editable suggestions. Known reasoning, tier, format,
  multi-agent, executable-tool-shape, and unattached-credential incompatibilities
  are authoritative at Session creation; a real Turn remains necessary to prove
  model/provider execution and conditional Codex `low`/`high` verbosity support.
  Web never sends a paid Turn merely as a capability probe. Claude SDK accepts
  medium verbosity only.

Environment creation and management beyond the narrow Session-scoped
`self_hosted` and basic managed `openai_hosted` creation flows, top-level
Environment list/CRUD, managed-provider selection/configuration, arbitrary
Workspace content mutation, Plugins, Skills, Artifacts, Credential profiles beyond
the bounded Vault static-bearer flow, populated Template initialization
(`env`, `setup_commands`, files, packages, skills, plugins, and capability
directories), Template editing and deletion surfaces, restricted-domain
networking, hosted MCP, key management, and broader Workspace lifecycle controls
remain unsupported or hidden. The bounded Source lifecycle and strictly gated
Environment `file_id` copy described above do not imply those broader surfaces.
Parsar's additional pinned handlers are not Web-supported merely because they exist
upstream.

## Dashboard and System boundary

- Dashboard is a Web-derived view over the last successfully traversed Agent and
  Session page-chain results for the configured Core access scope. Web follows every
  continuation with `limit=100&order=desc`, rejects duplicate identities and
  invalid or cyclic cursors, and allows at most 100 pages per collection. If Core
  reports that page 101 is required, the refresh fails closed and does not publish
  the partial result. Dashboard performs no additional Turn, Item, Environment,
  or execution-readiness requests and makes no writes.
- When both top-level collection reads fail through a gateway/network condition,
  Dashboard labels the local Agent Core backend as not ready and links the whole
  notice to connection recovery. An explicitly configured local Docker guide may
  display validated, non-secret `docker start` and loopback `/healthz` commands for
  pre-existing database/Core/daemon containers. The same panel distinguishes first-time
  setup and links the current in-repository container/daemon guides plus its non-secret image
  build command. Web never executes those commands, accesses the Docker socket, creates
  containers, invents credentials, or treats process health as authenticated API or
  execution readiness.
- A ready Agent or Session result means pagination reached Core's end marker within
  that safety limit. Loaded counts are exact only for that published page-chain
  result. Pages may change while they are traversed, so neither count is an atomic
  snapshot or a current Core total; the Agent and Session reads are also independent.
  Session-admissible Agents pass Web's known request-shape checks; this does not
  prove worker, executor, runtime, model, provider, Function, or MCP readiness.
  Session status counts preserve exact known Core statuses, while unknown values
  remain unavailable.
- Reported aggregate tokens sum only complete canonical Session Usage snapshots
  and state how many of the loaded Sessions reported Usage. Missing or malformed
  Usage remains `Unknown`, never zero. Needs-attention rows are the five most
  recent `requires_action` or `failed` Sessions; Recent Sessions contains
  at most eight rows ordered by valid Core-reported `last_active_at`. A
  `self_hosted` label identifies only the Session profile, not an executor
  connection. Row actions navigate to the exact loaded Session.
- Agent and Session collection states remain independent. A failed refresh may
  leave an explicitly labelled prior loaded result visible, including a previously
  confirmed empty result. An initial failed empty collection is unavailable
  rather than a confirmed zero. Switching Core generations clears both snapshots in
  the same state transition, and a collection response invalidated by a concurrent
  local/live revision is reread before it may be labelled ready.
- System presents browser-visible collection request state, the sanitized
  configured Core base, the immutable `dadf64a7` public capability matrix, and
  ownership boundaries. Its counts apply only to the last successfully traversed
  page-chain results and are explicitly not atomic snapshots or current Core totals.
  Its Core summary is derived from collection reachability;
  it is not an independent `/healthz`, build-version, capability-discovery, or
  execution-readiness probe. Refresh is explicit and user-triggered; System does
  not poll. The matrix separates restricted Function/MCP configuration and
  Function result handoff from Vault attachment and unavailable runtime-readiness
  proof.
- System always reports execution readiness as `Not exposed`. The self-hosted and
  managed-hosted build flags control Web presentation only. The optional
  self-hosted Docker command is an operator launcher aid; Parsar's distinct managed
  Docker adapter is Core-owned execution placement behind `openai_hosted`, never
  browser Docker control. E2B and AgentCore profiles remain hidden without a
  supported public admission contract.

## Session metadata and deletion

- Start Session exposes only the optional display title and writes it as
  `metadata.title`; it does not expose additional creation metadata. The
  existing-Session action surface can edit additional metadata, where every value
  must be a string. Web enforces Parsar's pinned limit of 16 pairs, 64 Unicode
  characters per key, and 512 per value before either write, and explicitly warns
  that metadata must never contain credentials or secrets.
- The Session action surface retrieves the latest durable Session when it opens and
  again immediately before an update. Every action read must return a complete
  canonical Session with the exact requested ID; a wrong-ID or malformed HTTP 200
  response leaves the current view/draft unchanged and cannot authorize a write or
  unlock an uncertain deletion retry. Runtime validation covers the Agent snapshot,
  known Environment shapes, required-action variants, Usage counters, metadata, and
  timestamps while preserving a structurally safe unknown Environment type as
  unavailable.
- `POST /agents/sessions/{session_id}` replaces the complete metadata map. To avoid
  silently erasing concurrent additions, the Web computes the user's changes from
  the form baseline, applies only non-conflicting changes to the latest retrieved map,
  and stops before POST when the same key diverged. The latest unrelated pairs are
  rebased into the preserved draft before a later explicit retry. A confirmed response
  must be a complete matching Session whose metadata exactly equals the submitted map;
  only that metadata is merged into the live UI so an overlapping SSE status or Usage
  snapshot is not regressed.
- Update and delete have no idempotency key and are sent at most once per explicit
  action. A missing Session or deterministic lifecycle conflict remains visible with
  its draft. A 5xx, timeout, response loss, malformed success, or network failure is
  treated as an unknown write result: the current Web view remains in place and no
  write is retried automatically. After an unknown delete, the Web performs exactly
  one read-only Session retrieval: 404 confirms removal, a canonical Session confirms
  it is still present and unlocks a later explicitly confirmed delete, and another
  failed read keeps deletion locked as unknown until the dialog is reopened and a
  durable retrieval succeeds. A 409 is supported for a compatible Core; the current in-repository Core
  permits active Session metadata updates and does not make 409 the expected
  active-Session path.
- `DELETE /agents/sessions/{session_id}` removes a row only after the exact canonical
  `{id, object:"agent.session.deleted", deleted:true}` confirmation. Deleting the
  selected Session immediately aborts/fences its fetch stream and pending
  Session/Item/Turn/Environment reads, clears its local Items, Turns, required actions,
  Environment observation, send failure, and draft, then selects the next item at the
  deleted position or the previous item at the end. Deleting an inactive Session does
  not change the selected ID, stream epoch, composer, or current conversation state.
- Parsar deletion is a public server lifecycle operation. It hides the durable public
  Session/Items/Turns, closes its stream, cancels queued work, and requests asynchronous
  cancellation of active work. It does not prove immediate native executor quiescence,
  physical SQL/native-history erasure, or deletion of executor Workspace files.

## Live stream and recovery

`GET /v1/agents/sessions/{session_id}/events` is live-only. The client opens it
before submitting input. A reconnect, including one with `Last-Event-ID`, does not
replay missed work.

Every accepted replacement stream follows this order:

1. reconnect the stream and buffer newly arriving events;
2. retrieve the persisted Session and every page of Items while independently
   starting the all-pages Turn read;
3. after the current Session supplies a valid supported Environment ID, retrieve its durable
   Environment resource;
4. apply the durable Session, Items, and Environment snapshot without making a slow
   or unavailable Turn endpoint block conversation recovery, then release buffered
   events;
5. when the independent Turn read settles, apply it only if its Core, request, and
   selected Session are still current, merging any newer live Turn snapshot by event
   revision;
6. inspect durable state before resubmitting an uncertain write.

At replacement-stream acceptance the Web clears the previous live Environment
observation before the durable reads. Supported Environment events arriving during
those reads are buffered and applied afterward, so a newer live state wins over the
earlier durable snapshot. Within the same identity and stream epoch, a terminal
`failed` live observation or terminal `failed`/`expired` durable observation is not
regressed by a later valid but stale non-terminal durable read. A malformed,
unavailable, wrong-ID, or wrong-type read still clears the observation to unavailable
instead of retaining a stale claim. A late stream callback or read is fenced by Core
generation, Session ID, Environment ID, Session and Environment request revisions,
Turn and Item event revisions, stream epoch, selection, and abort signal. A missing, unauthorized,
failed, or malformed Environment response clears stale connection claims and renders
status as unavailable without blocking Session, Items, or conversation use. The UI
never infers connected from health, stream state, Agent/model metadata, installation
arrays, or absence of an action.

For a same-ID Turn, a `completed`, `failed`, or `cancelled` snapshot does not
regress to a later-arriving non-terminal snapshot. Durable creation order remains
authoritative while a newer live snapshot can advance the same Turn. For a same-ID
Item, `completed`, `failed`, or `incomplete` beats `in_progress`
regardless of whether the terminal value came from the durable read or the live
buffer. Otherwise, the later live projection wins while durable ordering remains
authoritative. Duplicate, out-of-order, unknown, and no-op events do not stop later
events. A Session or Core switch aborts its fetch-based stream and durable reads;
generation checks also isolate any late result that could not be cancelled.

Terminal Session (`idle`, `requires_action`, `failed`), Turn (`completed`, `failed`,
`cancelled`), and live Environment (`ready`, `connected`, `disconnected`, `failed`)
events schedule a coalesced durable Session/Turns/Items/Environment refresh. They do
not restart the stream. The Turn read shares the refresh's abort signal and request
fence but settles independently, so a slow or failed Turn endpoint cannot delay
durable conversation Items or buffered Item events. A Turn-list failure is isolated
to its timeline: the last observed Turns remain visible, and a successful Session/Items
read keeps the existing conversation usable.

## Turn observability boundary

- The selected Session loads `GET /agents/sessions/{session_id}/turns` with
  `limit=100&order=asc`, follows `has_more` using the last returned Turn ID when the
  optional list cursors are absent, and rejects a repeated/cyclic cursor or a Turn
  scoped to another Session. Reads are abortable and never retried automatically.
- Trace keeps the complete Turn timeline in a collapsed **Turn diagnostics**
  disclosure so Conversation remains focused on messages and composing. The
  diagnostics present observed Core snapshots for `queued`, `in_progress`,
  `waiting`, `completed`, `failed`, and `cancelled`. Its all-pages read supplies the
  authoritative creation order, while a newer exact lifecycle SSE snapshot may
  advance a Turn before that read settles. Live projection is limited to exact
  `created→queued`, `in_progress→in_progress`, `waiting→waiting`,
  `completed→completed`, `failed→failed`, and `cancelled→cancelled` event/status
  pairs; Item/output or unknown Turn event names and mismatched snapshots are ignored.
  The UI therefore does not label the mixed projection as wholly durable. Items are
  counted against their owning Turn only by the protocol `turn_id`; unmatched Items
  remain in the conversation and are explicitly reported rather than hidden or
  guessed.
- Ended wall-clock duration is calculated only when both server `started_at` and
  `completed_at` are valid and ordered. `in_progress` and `waiting` Turns show a
  live, explicitly labelled running elapsed value from server `started_at` to the
  viewer's current clock. Missing, invalid, or reversed timestamps render as
  `Unknown`; Item `duration_ms` values are tool progress and are never summed or
  relabelled as Turn wall-clock time.
- A failed Turn's safe public `error.code` and `error.message` render beside its
  timeline entry without removing conversation Items. The Web does not expose Core,
  daemon, provider, or native-harness diagnostics absent from that resource.
- The Web displays `input_tokens`, `output_tokens`, `total_tokens`, cached input
  tokens, and reasoning output tokens. Session aggregate Usage and each Turn's Usage
  use separately labelled areas. A null resource, missing nested metric, malformed
  value, or unavailable measurement renders as `Unknown`, never inferred zero.
- Turn status, timings, Usage, errors, and tool progress are resource-level
  observability. They are not per-Item timing, monetary cost, provider attribution,
  or a complete OpenAI Trace waterfall.

## Trace workbench boundary

- Against the immutable Parsar `dadf64a7` capability baseline, the trace workbench
  is a read-only projection of the selected Session's Agent snapshot, all loaded
  Turn and Item snapshots, and accepted newer lifecycle events. A Turn can contribute
  its exact status, start/completion timestamps, and available token totals. An Item
  can present its exact type, status, role/phase, message content,
  command, working directory, tool identity, arguments/action, output/error, exit
  code, and tool `duration_ms` when those fields exist. Missing values stay
  unavailable. Unknown Item types and known discriminants with malformed required
  fields retain a generic inspectable presentation rather than acquiring a known
  semantic label.
- Conversation uses the same fail-closed discriminator boundary. Unknown
  non-message Items render only an inert `Unsupported <type> Item` row; their
  arbitrary arguments/output are not treated as Function, MCP, command, or search
  data and do not create an interactive control.
- Exact `agent.output.command_execution_output.delta` events append only to an
  already observed, same-Turn, in-progress `command_execution` Item whose current
  output is string-compatible. Orphan, cross-Turn, malformed, non-string, empty,
  or late fragments are ignored. A terminal Item snapshot remains authoritative.
  An in-band SSE `error` becomes a transient stream failure, triggers the existing
  durable reconciliation/reconnect path, and is never forwarded as an Item.
- Turn rows follow the durable all-pages creation order with the existing exact live
  status merge. Items retain their durable order and group under a Turn only by an
  exact `turn_id`; unmatched Items remain explicit. A function result may be paired
  with a function call only when the same non-empty `call_id` identifies exactly one
  call followed by exactly one result within that Turn. Ambiguous or out-of-order
  snapshots remain separate rows. Sequence labels, Turn/tool counts, bounded local
  search, and filters are Web-derived views over the currently loaded snapshots.
  They do not mutate Core state, establish an execution hierarchy, or make a partial
  or failed read complete.
- The instructions available to this view are the Session Agent snapshot's
  configured `instructions`. They are labelled **Configured instructions**, not
  **System prompt**, because the public contract does not expose the complete prompt
  assembled by the runtime, daemon, model provider, or native harness. Likewise, the
  configured model is not proof of the model or provider that executed a Turn.
- A function Tool schema is shown only when a known `function_call` Item's exact
  function name has one unambiguous, structurally valid function-tool match in the
  selected Session's Agent snapshot. Missing, duplicate, malformed, MCP, command,
  Web-search, and unknown Tool definitions do not receive an inferred schema.
- Summary, preview, payload, result, and raw-detail panels project allow-listed
  fields from those public resource snapshots. A merged function row keeps the call
  and result as two distinct raw snapshots. The Web does not add request headers,
  authentication material, daemon/native-harness diagnostics, or provider-private
  payloads that are absent from the resource. Nested public field values are shown
  as Core returned them and are not claimed to be recursively redacted. The Session
  SSE endpoint is live-only and does not replay history, so the assembled trace must
  not be described as an audit log or lossless record of every intermediate state.
- Core exposes Turn-level `created_at`, `started_at`, and `completed_at`, but no
  per-Item start/end timestamps, first-token timestamp, model-generation span,
  throughput, provider attribution, parent span, request/step hierarchy, or subagent
  relationship in this contract. Item `duration_ms` remains tool progress only;
  `phase`, `turn_id`, event arrival time, list position, and configured multi-agent
  fields cannot fill those gaps. Therefore the Web may render an execution-order
  overview, but it must not draw a time-scaled Input/Model/Tools waterfall or claim
  TTFT, generation time, tokens-per-second, provider, step, or subagent metrics until
  Core supplies versioned fields for them.

The client never retries a write automatically. For an input message that receives
an unexpected non-204 2xx, loses its network/response, or fails with HTTP 5xx or a
transient 408/409/425/429, the Web keeps the original payload and idempotency key in
memory. Only a later user-initiated Send of the byte-for-byte unchanged payload
reuses that key. Editing the payload, changing Session/Core, a successful response,
or a permanent 4xx starts a new operation with a new key. This state is intentionally
not stored in browser persistence, and the UI cannot prove whether an uncertain
request was accepted until durable Core state reconciles.

HTTP acceptance, `/healthz`, an open SSE connection, and successful Agent creation
do not prove that a daemon, native harness, model ID, or provider credential can
complete a Turn.

## Terminology

- **OpenAI Agents API** is the managed-harness API described in the official
  [Agents guide](https://developers.openai.com/api/docs/guides/agents). Parsar
  implements part of a pinned beta HTTP resource shape with its own runtime.
- **OpenAI Agents SDK** is an application-hosted orchestration SDK. It is not the
  wire protocol between this Web and Core.
- **Responses API** is a lower-level model API and is not this repository's Core
  contract.
- **Parsar daemon protocol** is a private reverse-WebSocket execution transport.
- **Codex app-server protocol** is JSON-RPC 2.0 over stdio/newline-delimited JSON.

Any change to Web-used resources, fields, event variants, defaults, error handling,
or reconnect semantics must update this matrix, typed fixtures, and integration
evidence against an immutable Core revision.
