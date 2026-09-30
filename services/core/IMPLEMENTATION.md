# Agents API implementation constraints

These rules describe how the Core service (`services/core`) currently
implements the public, administrator and machine contracts. They are contributor
constraints, not user documentation. Wire behavior and qualification evidence
stay in the linked [contracts](../../contracts/agents-api/README.md); Runtime
message semantics stay in the [Core–Runtime protocol](../../docs/runtime-protocol.md).

## Public request handling

Session creation requires initial input for `none`, and for streaming creation
outside `self_hosted`. Report inline agent protocol errors first, then check these
conditions before creation retry lookup or
resource resolution. The parser remains shared with subsequent message admission;
non-streaming hosted and self-hosted requests may omit input. Do not retain an
idle-none creation compatibility exception. Valid requests retain their documented
local idempotency behavior; clients may use the same request/key with stream=false
to recover a lost creation response. Session metadata updates require a supplied
metadata field, with null/empty clearing it. Validate an empty update before any
resource lookup, after authentication.

List order parsing distinguishes omission from an explicit empty value. Lists and
single-resource routes ignore unknown query keys; a repeated supported list key
still rejects. The Environment Files list keeps its own path and cursor parsing but
uses the same unknown-key and duplicate-key rules, except that it still rejects
malformed query encoding (such as `%GG` or `;` separators) that the shared lists
drop. Reuse the shared parser and error serializer, preserving the observed Beta, Files and Skills error fields and
per-family limit bounds rather than applying one policy to every resource. Change
page bounds, cursor ownership or parent lookup order only with owned evidence for
that family. Record uncertain range/lookup behavior separately; do not reproduce
observed upstream server failures as compatibility behavior. See
[list rules](../../contracts/agents-api/wire-semantics.md#lists).

Every Agents API JSON route reads its body through the shared gate
(`readJSONObject`) before route decoding, validation or lookup. It requires a JSON
Content-Type, applies the route's body limit and rejects invalid UTF-8, malformed
JSON (including unpaired surrogate escapes), repeated keys and non-object roots
with the official messages; an empty body or null becomes `{}`. DELETE, multipart,
Core extension and internal routes keep their own readers. Member names match
exactly: decode request objects with `decodeInputObject`, or check
`inexactMember` before another decoder, so that encoding/json never matches
a case variant to a field. See
[request bodies](../../contracts/agents-api/wire-semantics.md#request-bodies).
Report validation failures with official evidence through the typed field error,
which emits `invalid_request_error` with the observed param and message; keep
other local codes until their official fields are sampled. Every 409 has type
`conflict_error`. Session input conflicts and changed tool results also use code
`conflict_error`; documented Core-only conflicts, such as Idempotency-Key reuse,
sandbox administration and Environment input states, keep their local codes. Agent configuration
(saved create/update and the inline Session agent) uses one path-tracking
validator of the pinned shapes before its parsers and harness admission, which
keep their local codes; do not grow it into a JSON Schema engine. A malformed path
identifier must produce exactly the response of a well-formed missing one on that
route, including invalid bodies, queries and storage availability: resolve it to
the never-assigned maximum UUID and let the missing path run, or reject it
directly only where the lookup is the next check. Request-body references keep
their own errors. An `after` cursor that does not resolve inside its already
resolved parent, malformed ones included, returns that list family's observed
error: the missing-resource 404 on lookup lists, otherwise the typed store cursor
error. Foreign and missing cursors stay identical; see
[list rules](../../contracts/agents-api/wire-semantics.md#lists). Reject U+0000 in metadata
explicitly with its `metadata.<key>` param; other stored strings rely on the
PostgreSQL error mapping, so keep each request's writes in one transaction. See
[validation errors](../../contracts/agents-api/wire-semantics.md#validation-errors).

Serve requests on their canonical path and never redirect. `api.CanonicalPaths`
wraps the complete server handler in both configurations (the daemon ServeMux and
the API router alone), so every route group, middleware, authentication check and
handler sees one path. It starts from the request's own spelling, never a path
re-escaped from its decoded form: invalid bytes are percent-encoded, unreserved
escapes decoded, empty and dot segments resolved with ServeMux semantics, the
trailing slash kept, and other escapes such as `%2F` and `%5C` left encoded;
`Path` and `RawPath` are set consistently for chi and the ServeMux. Do not route
or authorize on a path outside that wrapper. On the Beta
group the constant OpenAI-Beta check (exactly one `agents=v1` value) runs before
authentication, and authentication still precedes every Beta handler, 404 and 405.
Every Agents API 401 has type `invalid_request_error`: null code on Beta routes; on Files
and Skills `invalid_api_key` only for a rejected Bearer credential. Agents API responses carry a fresh `X-Request-Id` (also in the log
context), `OpenAI-Version`, `OpenAI-Processing-Ms` and nosniff through the API
router's own middleware, not the shared log middleware. HEAD runs GET routes;
streaming, content-download, live directory, Runtime observation and Runtime
history routes register an explicit HEAD 405 instead. Every 405 of the API router, unknown methods included, has the JSON
body and lists the route's methods in `Allow`.

## Source Files and Artifacts

Source Files belong to the execution project and have an independent lifecycle
from copied workspace files. Store immutable source metadata and PostgreSQL large
objects in the execution database with the pinned pgx driver. Upload validation,
metadata insertion and bytes commit atomically; deletion removes metadata and
unlinks the object in one transaction. Keep OIDs private and authorize every
metadata/content/delete lookup by tenant before opening a body. Stream bounded
chunks; never hold an entire general Files upload in memory or use filenames as
filesystem paths. Public source download admission is separate from internal byte
consumption: reject direct downloads of the supported `user_data` purpose after
tenant-scoped metadata lookup; initialization and workspace copies keep their
authorized Store read. Core Web must not offer that unavailable download action.
A read-only repeatable-read transaction preserves an admitted
source snapshot across concurrent deletion. Resolve that snapshot before entering
the existing Environment write path; deleting a source does not undo a completed
workspace copy. Bound request/transaction lifetimes, roll back incomplete bodies,
and never automatically retry ambiguous commits. Backups must include PostgreSQL
large objects; live deletion does not erase WAL or historical backups. Schema
rollback must not orphan existing source objects. Do not reuse product capability
tables or introduce a second destination writer for file_id.

Session Artifacts are immutable published output copies, separate from live
workspace files and general source Files. A private output exporter must reuse
the authorized workspace path boundary and stream bounded bytes. Require complete
capture and confirmed helper/transport success before publication; valid archive
syntax alone is insufficient. The daemon owns and drains the exporter stdout pipe
separately from child reaping, so pull-transport backpressure cannot consume a
process-exit I/O deadline. After helper exit, bound each actual pipe read to one
second to reject inherited pipes that never close; reset that allowance after
consumer delays. Cancellation closes the owned reader and the dispatch consumer,
then waits for child settlement. An arbitrary blocked writer cannot be interrupted
by the exporter itself. Never extract an output archive into Core's
filesystem or hold the global execution lease through a large transfer. Keep
publication ordered with Turn completion, and authorize stored reads independently
of Environment availability so published outputs can survive its expiration.
Capture bytes into private PostgreSQL large objects without a Session admission
lock; after confirmed export, lock and recheck the live Turn before staging metadata.
Before capture, seal native input under that lock using the private capture marker.
Later messages reuse the existing Environment input reservation and await the next
Turn; the public Turn stays in progress until publication settles. Directory reads
during capture use an independent authorized read-only preparation, not the released
native Run; they do not request model credentials or mutate the workspace. Cancellation
retains the existing Turn/reservation semantics. Do not introduce a second queue.
Publish metadata in the same transaction as Turn completion. Failed/cancelled Turns
discard private objects, and Session deletion removes both private and published
copies. The exporter skips output symlinks by their `lstat` type without following,
opening or resolving them; hard links, other special files, device crossings and
concurrent changes still reject the capture. In that completion transaction, drop
staged paths whose sha256 equals the newest remaining published Artifact for the
path in the Session, so later Turns publish only new, changed or no-longer-published
paths and never modify existing Artifacts. Reuse the source-file snapshot reader
pattern and common content response; artifact deletion does not alter workspace
files. Hosted execution requires the
Runtime's bounded output-export capability and exact read-only preparation binding;
capability advertisement alone does not qualify an operator's deployment.
Exporter component checks do not establish public Artifact compatibility.

## Dispatch and pending input

Environment initialization is owned by the leased Worker's common preparation
scheduler, independently of any RuntimeAllocation. Both managed and enrolled
connections use the same frozen files/setup/capability snapshots and typed Runtime
operations. Preparation has bounded concurrency separate from Turn scheduling;
a blocked Runtime must not block resource cleanup or other connections. Check
Harness availability before installing. A persisted running initialization whose
owner is lost fails without replaying side effects. Completion requires the same
authorized Environment/device binding. Failure settles pending input and preserves
compute ownership and user files. Connection observations do not imply completion.


Successful input commits send a coalesced hint to the existing Worker scheduler.
The scheduler keeps lease, capacity, cursor fairness and per-Session ownership
checks; a hint does not admit work itself. If capacity is occupied, preserve one
rescan for completion without turning failed preparation into a busy retry loop.
HTTP readiness waits and active input delivery subscribe before reading relevant
state and wake after committed promotion or input. They recheck storage after each
hint. Polling remains the fallback for external writers, expiry and lost hints;
notifications contain no execution authority and no durable input data.

Core readiness, Start acknowledgement and input-to-first-text logs use a single
process monotonic clock. Start acknowledgement confirms adapter ownership, not
model input consumption.
Runtime logs identify Executor creation, reuse, idle and close independently of
Turn completion. Do not call these durations model-only latency or subtract clocks
from different machines. Native process creation and same-owner successive Turns,
real provider results and same-condition timings must substantiate reuse claims.

The Dispatcher prepares pending Environment input only on its exact enrolled or
managed device. Preserve the same physical peer and preparation handle through
readiness, atomic promotion/claim and the first non-replay Start. Derive workspace
and Environment identity from Store ownership, never caller-selected private paths.
Initial prompt/cursor come from the reserved batch; later messages use ordinary
steering. Never hold a database lock during native preparation.

Observe the original pending deadline, cancellation, deletion and peer loss while
waiting for readiness. Preparation failure leaves pending input and its deadline
intact unless storage has settled it; it creates no failed Turn or input history.
During Start, consume preparation controls alongside the ordinary Run stream so a
control-only rejection or pending-start cancellation can settle promptly. Reuse
ordinary journal, receipt and completion/native-history persistence. Once cancellation
is sent, preparation errors/closure cannot replace its receipt or timeout path.
Pending-start cancellation uses the adapter's observed outcome after daemon handoff
and output forwarding. Missing or unconfirmed outcomes still fail conservatively;
preparation control errors cannot substitute for the cancellation receipt.
Do not fabricate an empty cancellation outcome or infer native quiescence.
The connection owner spans preparation and the transferred Run without a reservation-derived Run
deadline; every exit releases it.

The existing Worker scans pending inputs using the same bounded scheduling slots,
Session locks, durable deadlines and engine capability checks. Keep the one-second
Environment-input scan cadence and at most 100 candidates per scan. At EOF after a
nonempty cursor, refill the first page once in the same scan; an empty queue must
not spin. Advance the cursor before readiness checks so an unavailable Runtime
cannot starve later candidates. Preserve the configured execution concurrency (default four) and alternation
between ordinary Turns and Environment inputs. A self-hosted Session
waits for its dedicated enrolled device; it cannot select an arbitrary same-tenant
device or migrate an existing binding. Preparation failure can retry while still
pending without extending the deadline. Preparation retries share this scan cadence;
execution concurrency bounds simultaneous work, not attempt frequency. Managed-provider lifecycle
polling retains its separate five-second interval. Unknown promotion results or
errors after admission retain the existing no-replay settlement rules.

`OAC_PUBLIC_URL` enables the private gateway; Core derives the public
`remote_url` from it. `OAC_HARNESSES` explicitly adds deployment-supported
engines to the default engine and configured managed profiles; advertising a
heartbeat alone does not enable an engine. The three native profiles share enrollment
at `/workspace`. Their new user-managed public chain requires fixed-client/raw HTTP,
real-model, recovery, cancellation and credential-lifecycle acceptance separately
from prior Docker or retired remote-executor evidence.

## Current implementation constraints

The constraints below describe existing code, not requirements to preserve legacy
design. The [protocol assessment](../../contracts/agents-api/README.md#known-gaps)
identifies replacements and gaps. Update these rules when their implementation is
replaced; do not carry obsolete compatibility code forward to satisfy this section.

### Gateway, Session deletion and schema ownership

- `internal/agentdaemon/gateway` is the shared daemon connection implementation.
  Its persistence interfaces use `internal/agentdaemon/device`, never product Store
  types. Shared protocol frames and validators live in `internal/agentdaemon/proto`.
- Session deletion uses a durable `sessions.deleted_at` marker. Public deletion
  accepts only a durably idle or failed Session without required actions: no
  queued, in-progress or waiting root Turn and no pending input reservation, the
  same settlement rule as the creation stream. Subagent child Turns and pending
  Environment file writes are not checked, as before this rule; their official
  behavior is unobserved. Take that decision and commit the marker
  under the tenant Session lock that orders Turn and input admission, so either
  admission commits first and deletion conflicts, or admission observes the
  deletion. A busy Session returns 409 `conflict_error` with the observed official
  message and nothing changes: no cancellation, marker, event or cleanup. Callers
  cancel first (`agent.session.input.cancel`), wait until the Session is idle and
  delete it. Core admits a Turn synchronously, so it also conflicts right after an
  `events.create` 202, where the official service was observed to return 200.
  Deleting a provisioning hosted Session with reserved input used to release its
  sandbox node placement at once; it now conflicts, and the placement counts
  toward node capacity until the input is admitted or its five-minute deadline
  expires. A later allowed deletion releases an unallocated placement.
  The owner's repeated deletion returns the same 200 confirmation without writing;
  foreign, missing and malformed identifiers keep the byte-identical 404. Public
  reads, metadata changes, event streams and input admission exclude deleted
  Sessions; admission checks visibility under that lock before retry lookup.
  Creation keys remain reserved and cannot resurrect deleted Sessions; reuse of a
  deleted creation identity returns 409. Existing streams close when removal is
  observed without a fabricated deletion event; overlapping stream timing remains
  unverified. Earlier releases also deleted busy Sessions after requesting
  cancellation, so upgraded databases can hold markers with hidden work.
  Internal Turn/receipt/finalization and restart reconciliation retain access so
  that work settles under the existing execution lease; queued work cannot be
  claimed, and Runtime cleanup still cancels pending work. Confirmation does not
  guarantee native quiescence. Never revoke a shared device,
  remove a saved Agent or touch product data as part of Session deletion. Physical
  SQL/native history cleanup remains a separate required implementation gap; these
  records are retained, not claimed purged, and purging may end repeat idempotency.
  Do not deploy a pre-deletion service
  against a database with deletion markers; migration rollback refuses to remove
  the column while deleted records exist, preventing public resurrection.
- `services/core` owns its SQL schema, sqlc queries and embedded goose
  migrations. `OAC_DATABASE_URL` is required; never fall back to the product
  database URL. It stores tenant-scoped Sessions with a stable engine and durable
  creation retry identity.

### Agents, Vaults and credentials

- Reusable Agents have their own tenant-scoped `agents` records, independent of
  Session snapshots, engine bindings and product Agent definitions. The Store
  persists caller-validated non-secret configuration and metadata without applying
  harness capability restrictions or model defaults. Resource identity and equal
  initial creation/update timestamps come from the persistence boundary. The
  create primitive creates a fresh resource; public retry conformance remains
  unverified. Internal storage admission is 512 KiB for configuration and 64 KiB
  for metadata, not a claim about upstream limits.
- Vaults have their own tenant-scoped records in the execution database. Initial
  `POST /v1/vaults` and `GET /v1/vaults/{vault_id}` operations persist and read the
  resource without creating Sessions or contacting an engine. Omitted name is
  null; a supplied non-null string is trimmed and limited to 1–256 UTF-8 bytes.
  Omitted/null metadata becomes an empty object. Reuse the 64 KiB encoded metadata
  storage bound, without applying Session-specific pair/character limits. This is
  a local bound, not hosted parity. `GET /v1/vaults` lists the authenticated project's
  records using creation-time/ID keysets, default descending order and a default
  limit of 20 clamped to 1–100. Other resource limit policies are unchanged.
  Status accepts `active`/`archived` as a scalar or SDK `status[]` array, with both
  included by default. Private stored classification defaults existing/new rows
  to active; it is never exposed in the Vault response. Listing reads no Credentials
  and needs no encryption key or execution service connection. A scalar status
  combined with `status[]` filters by their union; a repeated scalar parameter is
  rejected. Exact hosted errors, equal-time
  ordering and changes between pages remain unverified. Private archived fixtures
  prove filtering only: there is no public archive writer, archive timestamp or
  inferred delete-to-archive behavior. Retrieval, Session binding and dispatch retain
  their existing rules. Archive/revocation lifecycle remains a separate gap;
  do not introduce product roles or speculative lifecycle fields. Migration rollback
  refuses to discard classification while archived rows exist.
- Vault `DELETE /v1/vaults/{vault_id}` removes the project-owned parent and all
  Credentials through the existing foreign-key cascade in one SQL mutation. Do not
  loop through child deletions, decrypt secrets, require the storage key or call
  providers. Deletion applies to both stored classifications. Local missing/repeated
  deletion returns not-found; subsequent parent/child reads and new attachments
  cannot use the removed resources. Preserve Session snapshots, frozen choices,
  history and recorded retries. Later secret lookups fail without selecting another
  attached Vault or anonymous MCP. Already-resolved tokens and running Sessions
  are not revoked. Exact hosted archive, visibility, overlapping-mutation and error
  semantics remain unverified; row removal does not prove physical storage erasure.
- Static-bearer and OAuth Credentials are children of tenant-owned Vaults in the execution
  database. Creation admits the owner in the same SQL statement as the insert;
  retrieval joins the owning Vault and selects public metadata only. Public reads do not decrypt or return a token. OAuth replacement authenticates
  the stored grant before applying a partial update. Encrypt before passing secret values to
  SQL, using the execution service's separately configured random 32-byte key and
  the standard library's random-nonce AES-GCM. The versioned authenticated binding
  includes tenant, Vault, Credential, authentication purpose and exact destination.
  Never reuse product master-key conventions or daemon transport encryption for
  this storage boundary. Missing key configuration disables credential writes;
  malformed explicit configuration fails startup. See
  [Vaults and Credentials](../../contracts/agents-api/vaults.md#storage-key) for
  key persistence and current limits. Storage-key rotation remains
  separate work; resource creation never contacts the destination.
- `GET /v1/vaults/{vault_id}/credentials` lists safe metadata only, with both
  project and Vault ownership enforced on the parent, cursor and row query. An
  inaccessible parent returns not-found, even when the collection would be empty.
  Reuse the Vault status/limit parser and Credential metadata mapping. SQL must
  never select ciphertext for listing; no encryption key or execution is needed.
  Credential status is a separate private active/archived classification, defaulting
  historical/new records to active and never derived from the parent Vault's status.
  Synthetic archived fixtures prove filtering only. There is no public archive writer,
  timestamp or delete-to-archive inference; existing create/retrieve/token replacement,
  Session bindings and dispatch keep their rules. Migration rollback refuses to lose
  archived classification. Archive lifecycle and hosted query/concurrency
  semantics remain gaps.
- For static auth, Credential `POST /v1/vaults/{vault_id}/credentials/{credential_id}`
  replaces only the token and update time. Require `auth.type=static_bearer` and a
  string `auth.token`, preserving opaque bytes; reject extra mutation fields before
  writing. Reuse safe metadata for the immutable encryption binding, then scope the
  atomic SQL mutation independently by tenant, Vault, Credential, static auth type
  and exact destination. Never decrypt the previous token or send plaintext to SQL.
  Missing encryption configuration or a failed write preserves the old row. Return
  the existing safe metadata projection; identity, name, destination, creation time
  and Session snapshots stay unchanged. Subsequent dispatch reads use the committed
  replacement through existing scoped lookup; already-resolved requests may retain
  the old token. This is not storage-key rotation, in-flight revocation or hot reload.
  Exact hosted concurrent-update/retry/timestamp semantics remain gaps.
- OAuth grant ownership stays in Core. The application performs authorization and
  provider revocation; do not add public login/callback/refresh/revoke routes.
  Store access/refresh/client secrets together under existing authenticated tenant,
  Vault, Credential, auth-type and destination encryption. Authenticate refresh
  metadata against its encrypted copy before using an endpoint or grant. Read/list
  queries still select safe metadata only. Shared MCP selection admits both auth
  types and freezes one identity without changing native adapter contracts.
  At dispatch, a known-expired grant is refreshed through the declared endpoint
  auth method, with stored scope/resource, then persisted before returning access.
  Serialize refresh and replacement with the same PostgreSQL Credential row lock;
  deletion and Vault cascade cannot be undone by a stale refresh. Network exchanges
  are bounded and fail closed; never return provider error bodies or claim an
  uncertain grant exchange was committed. No background scheduler, 401 retry,
  hot replacement, output repair or harness-specific OAuth path is introduced.
  Refresh uses verified HTTPS, rejects redirects, and checks resolved addresses
  before dialing them. Private issuer origins need explicit operator configuration
  in `OAC_OAUTH_TRUSTED_ORIGINS`; tenants cannot relax that boundary and TLS
  verification remains mandatory. Keycloak is acceptance infrastructure only.
  Preserve the pinned update omission/null and immutable-field rules described in
  [OAuth credentials](../../contracts/agents-api/vaults.md#oauth); record unspecified
  hosted semantics. Native processes receive only access tokens. Provider revocation,
  withdrawal of already-dispatched tokens and Session cancellation remain distinct.
- Credential `DELETE /v1/vaults/{vault_id}/credentials/{credential_id}` removes one
  owned row, including ciphertext, with tenant/Vault/ID checked in the same SQL
  mutation. It needs no encryption key, secret read or network call. Local reads,
  updates, listings and subsequent dispatch lookups cannot use that ID; missing
  and repeated deletion return not-found. Preserve frozen Session choices, retry
  identities and history without fallback to another credential or anonymous MCP.
  A token already read before deletion may remain in a dispatched request. Deletion
  does not revoke provider tokens, cancel Sessions or prove physical erasure from
  native history, WAL or backups. Do not infer a delete-to-archive mapping; exact
  hosted archive, post-delete visibility and repeat/error semantics remain unverified.
- Public reusable Agent create/retrieve uses `/v1/agents` and the same authenticated
  tenant/Beta-header boundary as Sessions. The resource envelope owns identity,
  timestamps and metadata, separately from saved configuration and Session state.
  Resolve known defaults and validate supported schema before writing. Preserve
  model/name/instructions verbatim, nullable fields and structured JSON numbers.
  Stored reasoning/service tiers, enabled multi-agent settings, JSON Schema output,
  enabled web-search modes and deferred/tool-search/programmatic tools do not imply
  execution support. Reuse function wire validation, keeping Session execution
  restrictions separate. Model-derived reasoning effort is unresolved when omitted;
  do not infer it from the selected harness. Omitted/null service tier currently
  uses `auto`; complete upstream default/error/retry conformance and remaining MCP
  variants remain gaps. Unknown/unsupported variants fail explicitly. No product
  lookup is permitted.

### Service-origin MCP

- Service-origin public HTTP MCP uses the native harness client and tool loop on
  trusted service-owned `environment:none` compute, with Codex or Claude SDK.
  The V1 colocated `self_hosted` profile rejects it: user-owned compute cannot be
  relabeled service-origin or receive its attached Vault credentials. Hosted
  service-origin MCP also remains unsupported. Environment-origin Plugin MCP uses
  its separate qualified local Runtime transport and isolation contract; do not
  disable that path or infer optional-feature equality across engines.
  Admission, device selection and preclaim still require the exact supported MCP
  capabilities. Native declarations do not widen public placement authorization.

- Keep accepted public MCP credential profiles separate from private adapter
  capabilities. The execution service declares the verified public bearer profiles
  centrally; a daemon capability alone cannot open a public profile. Reuse frozen
  binding validation at admission and later input, then the same MCP capability and
  placement checks at device selection, final preclaim and request construction,
  before scoped decryption. Native configuration and token injection stay in the
  adapters. Add bounded shared checks while changing the related execution path;
  do not defer known duplication to a general engine or plugin framework.
- The shared MCP resolver preserves omitted/null `allowed_tools` as unrestricted
  and an explicit empty list as deny-all. Saved HTTP transport output includes
  `headers:{}`; the effective Session transport omits headers, matching the two
  pinned resource types. Saved-Agent updates never change existing Session
  snapshots; per-Session tools replace the whole field. The initial profile admits
  HTTP(S), boolean `required` (default false), empty/null metadata and empty/null headers.
  Static and OAuth bearer authentication require HTTPS and the attached-Vault rules below.
  An omitted/null `connection_origin` on HTTP transport is stored as `service`
  before the other checks, identical to an explicit declaration, as observed
  officially. Inline authorization, URL userinfo/query/fragment, the `environment`
  origin and stdio remain explicitly unsupported.
- Codex required MCP initialization additionally needs `mcp_http_required`, advertised
  only for the verified native pin and checked during selection, final preclaim
  and daemon dispatch/preparation. Preserve the boolean through typed messages,
  native rendering and exact configuration preflight. Reuse native required-server
  initialization during root thread creation and cold resume; send no native Turn
  until it succeeds, and never replace a failed strict resume with a new thread.
  Public work may already be accepted/queued/in progress while native initialization
  waits. This is not a continuing health monitor or a new public readiness state;
  exact hosted Session creation timing and initialization errors remain unverified.
- Send MCP declarations through typed daemon fields, independently of function
  callbacks. In Codex, a non-nil declaration replaces operator MCP options; use the existing
  native renderer and original tool names for `enabled_tools`, including `[]`.
  Before thread creation/resume, query native `config/read` with the exact cwd and
  reject additional servers or effective configuration differences. Disable native
  plugins/apps and select file-only MCP credentials; reject existing credentials
  in the private native home without deleting them or native history. Native
  reserved labels are an adapter restriction, not a saved-resource schema rule.
  Requests without this typed field keep the existing product behavior. The check
  is a snapshot on trusted service compute, not an atomic barrier against concurrent
  operator configuration changes. Discovery of a declared deny-all server can still
  contact it; deny-all governs tool exposure. Reuse neutral tool observations and
  the existing public `mcp_call` projection, never add a second MCP/model loop.
- The private Codex adapter's HTTPS MCP bearer authentication requires
  `mcp_http_bearer_auth` and the existing MCP/environment capabilities, checked
  before the factory. It is restricted to trusted service-side Codex with
  `environment:none`. A transient
  per-server `bearer_token` becomes a fresh daemon-owned `bearer_token_env_var`
  reference for each native process. Put the exact secret only in that app-server
  child's environment, after auxiliary launch probes; never in global environment,
  arguments, configuration/history, public snapshots or logs. Preflight accepts
  only the expected server/reference pairing and retains the existing rejection
  of ambient credential sources. Service-side bearer variables must not enter
  generated commands, files or public history/snapshots. Use the native HTTP client
  with TLS verification.
  This execution profile rejects empty values and bytes outside RFC 6750 b64token
  syntax with generic errors; it never trims tokens or narrows opaque Credential
  storage. Core-managed OAuth uses this same access-token path; native OAuth
  login/refresh and hosted redirect/error equivalence remain separate work.

### Session resources and model providers

- Session `vault_ids` omission/null/empty means `[]`; nonempty attachments must all
  belong to the authenticated tenant. Preserve caller order and the stored caller
  `credential_id`. Saved Agents may store a nullable/nonempty credential reference
  without authorizing its use. Session admission resolves an explicit credential
  only inside attached Vaults for the exact declared URL, or selects the unique
  matching static or OAuth credential when the ID is omitted/null. No match remains
  anonymous. Selection errors use the observed official messages with a null param:
  a reference without attachments, one outside the attached Vaults and one for
  another URL are 400 `invalid_request_error`; several implicit matches are 409
  `conflict_error`. Missing, foreign-tenant, unattached and malformed references
  share one message; echo caller values only within `internal/echotext`. Unknown
  or foreign Vaults keep the same 404. Resolve after the input requirement and
  before any Session, initial input or event write. Freeze safe bindings,
  including anonymous decisions, in private Session configuration. Session
  projections, never the stored configuration, show an implicitly selected ID in a
  null/omitted public `credential_id`, also after deletion. At actual dispatch, recheck
  tenant, attached Vault, selected ID, frozen auth type and exact URL before scoped
  decryption. Metadata queries select no ciphertext; tokens enter only the existing
  transient daemon request. Selected authentication requires `mcp_http_bearer_auth`
  during device selection and the final preclaim check. Missing/wrong keys or
  binding failures never fall back to anonymous execution. Exact URL equality,
  immutable selection timing and hosted redirect semantics remain local decisions
  or unverified gaps. No new MCP loop is permitted.
- Saved Agent execution defaults use separate input and safe-output Core extensions.
  Keep model-provider bundles whole at every replacement boundary: endpoint, key,
  protocol and limits must never be independently inherited. Ordinary Agent JSON
  contains only safe provider fields and an output-only configured flag; encrypt the
  complete bundle separately with tenant/Agent binding and a distinct purpose.
  Commit configuration and secret changes together under the Agent row lock. Merge
  only the extension's supplied members; omission preserves, provider null clears
  its bundle, and extension null clears both defaults and secret. Model-only edits
  require no key. Validate the merged harness/protocol/limits without reading keys.
  Read safe defaults and ciphertext in one database snapshot for Session creation;
  a complete Session override need not decrypt the inherited bundle.
- Session provider selection resolves explicit bundle, saved bundle, then the
  deployment default of the resolved harness, and freezes it in the existing
  encrypted Session-owned row. The deployment default is a runtime setting: one
  complete bundle per harness in PostgreSQL, encrypted with its own
  harness-bound purpose, managed only with the Core key through
  `/core/v1/harnesses/{harness}/model-provider`, audited as a deployment-wide
  write without the key, and never read back. It applies to `openai_hosted` and
  operator-registered `none` Sessions, never to `self_hosted`, whose executor host
  belongs to the application. Caller bundles apply to `openai_hosted` and
  `self_hosted`. Hosted and self-hosted Sessions with no bundle are rejected at
  creation with `model_provider_required`; legacy rows without a snapshot are
  rejected at message admission and dispatch, never sent to a harness that would
  fall back to a built-in endpoint. There is no operator options file: its
  retirement fails startup. Sessions retain only the common frozen provider
  bundle; historical native-option snapshots are unsupported. Keep runtime dispatch on the common adapter path and fail closed for
  missing/decryption-failed snapshots. Agent edits/deletion, default changes,
  restart and idle suspend/resume never resolve defaults again. Record caller
  intent for every new hosted Session before resolving defaults; other inline
  Sessions keep the resolved-request rule, whose hash leaves out the deployment
  default. Provider keys enter retry hashes only as keyed fingerprints. Matching
  retries return committed state without replay. No Turn-level overrides or provider catalog is
  included. Public input/null semantics and examples live in
  `contracts/agents-api/model-execution.md`.
- Deployment-default observations use a private UUID generated on every PUT,
  including identical replacements. Read its ciphertext/revision together and freeze
  the pair only at new Session creation; retries and historical Sessions never gain
  or replace revision metadata. After a successful root terminal commit, one
  independent pool operation has at most one second to update the matching current
  revision. SQL verifies tenant, root Turn and committed outcome; only completed
  Turns and fixed native provider failures with authoritative `engine_failed` qualify.
  Never observe cancelled work, Core/runtime errors or input-policy classifications.
  The metadata-only transaction sets server statement/lock timeouts within the
  remaining client budget and issues one UPDATE, so a client timeout cannot leave
  an indefinitely waiting statement. The update locks only the default and samples
  DB receipt time after the lock.
  Errors throttle for 30 seconds regardless of code; ordinary successes throttle
  for 30 seconds, with one immediate recovery write after each accepted error.
  An unchanged revision has at most three effective writes in any half-open
  30-second interval of nondecreasing DB time. Clock rollback may suppress ordinary
  writes; private recovery state permits only one recovery without clamping time.
  Observations never change `updated_at`, readiness or execution truth. They can be
  lost or stay stale indefinitely without another eligible Turn; there is no queue,
  retry, probe, history backfill or provider text in the safe fields.
- Session execution-configuration reads use a separate immutable safe projection,
  written with provenance in the same creation transaction as Session resources.
  Read no credential ciphertext and never recompute sources from current Agents
  or operator defaults. Model/harness sources are independent; provider bundles
  retain one source. The read is Core-key only, under `/core/v1`; `/v1` has no
  execution-configuration read. Old Sessions expose persisted model/harness with
  unknown sources and unavailable provider metadata, without backfill. Projection metadata does not alter retry
  identity; retries cannot replace it. Keep this administrator query separate from
  runtime observations and do not touch activity or wake sandboxes. The versioned
  contract is [execution configuration](../../contracts/agents-api/admin-api.md#execution-configuration).
- Model communication uses native direct connections only. Core sends one frozen
  confidential `model_provider` bundle, independent of engine and placement;
  adapters apply it through their native provider configuration. The shared
  `internal/harnessconfig` descriptor declares one ordered `protocols` list,
  with the first entry as the default. Core admission and Runtime enforce it.
  There is no built-in model API proxy, passthrough gateway or cross-protocol
  conversion, including inside individual Harnesses. Unsupported combinations
  fail explicitly; saved configurations and immutable Session snapshots are
  never rewritten, aliased or migrated to another protocol. Provider validation,
  credential encryption, capability checks and native lifecycle ownership remain
  mandatory. See `contracts/agents-api/model-execution.md` for the current
  protocol matrix. Go 1.26.8 is the pinned build toolchain.
- Provider input validation uses the adapter-owned rules in `internal/harnessconfig`.
  Keep one internal registry for protocol and token-limit validation; Core owns
  credential environment and endpoint admission policy. These rules are not a
  public discovery API or Runtime registration descriptor. Operation qualification
  and live readiness retain their existing owners. There is no startup
  configuration read; Session frozen execution-configuration reads remain a
  separate administrator read.

### Agent updates, listing and tenancy

- Public Agent updates use `POST /v1/agents/{agent_id}` with the same tenant/Beta
  boundary and shared saved-field validation. Preserve omission separately from
  null; only supplied fields replace saved values. Metadata is a separate whole-map
  replacement, with null/empty clearing it. Lock the tenant-owned Agent row while
  merging validated fields and enforcing the complete configuration bound, then
  commit configuration, metadata and update timestamp together. Never write a stale
  full snapshot over another update. No-field updates read without changing timestamps.
  Except for the Core execution-default extension described above, supplied nested
  fields replace the whole field and explicit null uses
  existing saved defaults; exact hosted nested/null and no-op timestamp semantics
  remain unverified. Model-derived reasoning defaults remain a separate gap.
  Neither updates nor retries modify existing Session snapshots or execution state.
- Public Agent deletion uses `DELETE /v1/agents/{agent_id}` and one tenant-scoped
  `DELETE RETURNING id` statement. Return the stored canonical ID with
  `object=agent.deleted` and `deleted=true`; missing/repeated deletion locally
  returns not found. It never deletes Sessions, history or runtime state and does
  not cancel accepted execution. Recorded creation identities still recover the
  accepted Session; new references cannot resolve an absent source. Historical
  identities retain their documented limitation. Exact hosted errors and ordering
  of overlapping source creation/deletion remain unverified; no tombstone or
  successful result is fabricated for an absent resource. Reject body data; unknown
  query keys are ignored.
- Reusable Agent listing uses the same tenant/Beta-header and response mapping as
  create/retrieve. Page by `(created_at, id)` with a same-tenant saved-Agent cursor;
  listing never resolves Sessions, product objects or execution capabilities.
  Reuse shared list-query parsing and its per-family limit policy. Agent, Session,
  Item, Subagent Item and Template lists treat limit 0 as 1 and larger limits as
  100; Vault and Credential lists also clamp negative limits; Turn, Subagent,
  Subagent Turn and Artifact lists reject limits outside 1–100; Skill lists accept
  0–100, where 0 returns an empty page; Files accept 1–10000. Pages hold at most
  100 records (Files 10000) with accurate continuation. The local default is 20
  (Files 10000). Return the list envelope with data/has_more and first/last IDs
  (null for empty pages).
  Exact pinned upstream default/cap, empty-envelope and error semantics remain
  unverified; do not present local limits or generic SDK parsing as full conformance.
- Session `agent_id` lookup uses the authenticated tenant. Copy the saved resource
  ID and effective configuration into the immutable Session snapshot; saved metadata
  does not become Session metadata. Never look up the source Agent when reading or
  executing an existing Session. Omitted override fields inherit; supplied objects
  and arrays replace whole fields before defaults and execution admission apply.
  Reuse saved configuration validation and keep native capability restrictions at
  Session admission. Reject unsupported effective options instead of dropping them;
  an explicit supported replacement may make a saved configuration executable.
  Inline Sessions use the same admission path. New saved-reference Sessions and
  inline requests with Vault attachments or credential references record a separate
  caller-intent hash: source ID (empty for inline),
  supplied overrides (including field presence), environment/vaults, original metadata
  and normalized initial input. Exclude response streaming and resolved source values.
  Compare that same-tenant retry identity before looking up the source. A matching
  retry returns the existing Session without input admission or source revalidation;
  ordinary reads include current activity. Stream retries use the row's committed
  event cursor and emit no created event. Recheck after source resolution failure
  for a concurrently committed creator; do not hold a lock across resolution.
  The unique creation upsert remains authoritative when concurrent resolutions differ.
  Unrelated inline requests keep their existing resolved/default equivalences.
  Recorded credential-bound retries recover before reading mutable Vault contents,
  so another same-URL Credential cannot change an accepted selection. Rows with a
  known creator but without recorded request intent retain resolved-hash behavior;
  original overrides cannot be reconstructed, so no backfill is permitted. Source
  mutation-independent retries apply only to recorded identities. Exact hosted
  retry/error semantics remain separate work.
- Tenant scope must come from authenticated service identity before calling the
  execution Store. Product workspace/user references in metadata grant no access.
  Keep credentials and effective execution options out of Session metadata.
  Store resolved, non-secret Agent/environment configuration in the Session's
  immutable configuration snapshot. Inline and historical creation identities include
  the resolved configuration; new saved references use the separate caller intent.
  Session metadata updates replace only metadata under the authenticated tenant:
  a supplied metadata field is required, null/empty clears, and a nonempty object
  replaces all pairs.
  Keep execution state, timestamps and the original creation request hash unchanged;
  creation retries return the current resource without restoring its old metadata.
  Public schema validation belongs to the API; the Store validates JSON structure.
- Session listing optionally filters by the immutable root `configuration.agent.id`
  within the authenticated tenant. IDs are opaque and include inline Agents; never
  require a surviving saved Agent or resolve product ownership. Apply filtering
  before pagination and activity projection, using the tenant/Agent/creation index.
  Omission retains unfiltered listing; a supplied empty string remains a filter.
  Preserve the existing tenant-owned cursor and creation-time/ID ordering rules.
  Hosted empty-filter, mismatched-filter cursor and exact error semantics remain
  unverified. Other resource lists do not accept this parameter.
- `make sqlc-generate` and the drift gate cover this service. Run
  `make check-core` with `OAC_TEST_DATABASE_URL` pointing to a
  dedicated `oac_*_tests` database for Session integration tests.
  CI provides a separate PostgreSQL service. Migration immutability and ordering
  apply independently to each service directory.

### Turns, input, schema and clients

- Turn writes serialize on the tenant-scoped Session row. An idle message starts
  a Turn; messages during queued/running/waiting work belong to that same Turn.
  Store input retry identities and order durably. Cancellation retains its first
  target, including an idle no-op, so retries cannot stop later work. Queued work
  can cancel before dispatch; active work needs an executor outcome. Terminal
  states and outcomes cannot be overwritten. Public event admission uses these primitives; live output streams remain separate.
- Input requests are ordered batches committed under the same Session lock. A
  retry key identifies the complete ordered batch; changed length/order/content
  conflicts and a failed transaction leaves no partial inputs or cancellation.
  Existing single-event requests retain their identities at batch position zero.
  Internal admission limits are 64 events and 512 KiB of payload per request;
  the public API must still validate the upstream event schema.
- The external protocol reference is `openai/openai-python`'s `beta/agents`, pinned
  in `contracts/agents-api/upstream.json`. Follow its Session/Turn/event semantics
  and verify supported behavior using the official client. Track current coverage
  in `contracts/agents-api/README.md`; SDK workflow objects are not this contract.
- Shared supported wire types live in `contracts/agents-api/v1`. `make openapi`
  writes `openapi.yaml` (`/v1`), `core.openapi.yaml` (`/core/v1`) and
  `runtime.openapi.yaml` (`/api/v1`); never mix their routes or authentication
  schemes. Review the generated diffs; contract tests hold `openapi.yaml` to the
  pinned routes and fields.
- The standalone service uses `OAC_DATABASE_URL`. PostgreSQL stores Projects,
  their immutable execution scopes and API-key digests. Keys in the same Project
  resolve to one shared service-account principal and tenant. Project/key writes
  go through Core-key `/core/v1` routes, not configuration files.
  Optional `OpenAI-Organization` and `OpenAI-Project` headers must match the key;
  repeated/conflicting values fail authentication. Metadata, forwarded identities
  and product session cookies grant no access. Issue/revoke operations take effect
  without restarting Core. Deployment credentials cannot authenticate public calls.
  Every new Session requires an explicit typed creator at the Store boundary,
  including internal callers. Public creation derives it only from the authenticated
  principal. Persist creator kind/ID in the creation transaction and never rewrite
  them on retry, update or source mutation. The tenant remains the project partition;
  do not duplicate project identifiers or create a product identity dependency.
  Both early saved-reference recovery and the authoritative creation upsert require
  matching creator kind/ID before returning a Session or event cursor. Different
  credentials for the same principal can retry; another principal using the same
  project/key receives the local idempotency conflict. This does not introduce
  creator-only resource reads or mutations, or claim verified hosted retry parity.
  Pre-migration Sessions retain null creator columns and remain project-readable;
  creation retries cannot claim them. Missing creator and missing request intent
  are distinct. Never infer historical ownership from keys, metadata or product
  records. Retire older API writers before serving the creator-enforced deployment;
  mixed-version writers are not supported. Tests must supply explicit synthetic
  creators; only controlled historical fixtures may seed unknown ownership.
  The operator-selected
  `OAC_DEFAULT_HARNESS` is separate from the requested model.
  Public execution supports the enabled `none` profiles, the three colocated
  self-hosted profiles and qualified three-harness Docker hosted profiles; reject unsupported
  input/environment/agent options explicitly.
- `packages/agents-client/v1` configures the pinned official `openai-go` Session
  service. Use SDK request/response types, pagination and errors directly rather
  than reimplementing transport or copying wire types. Supply an explicit service
  base URL/key and creation retry key; SDK retries are disabled by default. Product
  integration is a later cutover, not a side effect of constructing this client.
- `services/core/tests/official_client.py` verifies the actual server with
  the pinned SDK and strict response validation. It requires a dedicated test DB
  prepared by the Store tests and `OAC_TEST_SERVER_BIN`; it never starts Docker.
  The same harness runs the official Go client with fresh execution tenants and
  checks its created Sessions through the Python SDK.

### Execution dispatch and Runtime capabilities

- Execution devices are operator-provisioned in the Agents API database with
  tenant ownership and a credential digest. Their internal daemon gateway uses
  `/api/v1/agent-daemon/*`, separately from the official `/v1/agents/*` surface;
  device credentials grant no Session API or product permissions. The optional
  `OAC_PUBLIC_URL` enables that gateway. It is a single-process registry,
  not a claim of multi-pod execution or stock `exec-server` interoperability.
  Self-hosted enrollment uses this gateway with an exact Environment binding.
  Session/device bindings are tenant-scoped and immutable. Revocation denies new
  connections and binding reads; an existing connection closes on its next
  heartbeat. Connectivity comes from the live registry, not a persisted online
  flag. `last_seen_at` is diagnostic only. Product gateway behavior is unchanged.
- `services/core/internal/execution` dispatches internally resolved Turns
  through that gateway. Claim `queued` to `in_progress` before subscribing/sending;
  never automatically replay a claimed or interrupted Turn. Ordered extra inputs
  require native steering receipts. Commit terminal outcome and native Session ID
  together under the admission lock; unapplied messages prevent successful completion.
  Resolve credentials separately from the immutable non-secret snapshot.
- Internal execution requires advertised durable Turns, strict resume, preparation,
  applied input receipts and the qualified operation capabilities. Reject
  unadvertised peers before claiming; failed strict resume cannot start unrelated
  history. A completed Turn closes steering admission, settles existing native
  operations and receipt sends, and closes its output before the retained Executor
  can accept another Turn. Normal completion does not cancel the Executor.
  Cached input identities and conflicts remain readable while completing;
  queue/write success is not consumption. The receipt worker stays busy through
  its send, and router shutdown cancels its native and transport waits.
  The per-input `durable_receipt` opt-in requires a phased adapter. Its ten-second
  transport timer stops only after a complete native write; a separate `written`
  acknowledgement stops the API's thirty-second delivery timer. Neither phase
  advances the input cursor. Await final native acceptance/consumption under the
  Turn lifetime without automatic redelivery. Receipt sends retain a separate
  five-second shutdown-aware context, and Done retains a fifteen-second final
  settlement bound. Once cancellation is sent, its receipt owns the terminal
  outcome even if an input becomes unknown first. Calls without the opt-in retain
  their existing response deadlines. Native history still requires the device's
  persisted engine files; IDs alone cannot restore deleted history. Cancellation
  receipts carry the stopped Turn's confirmed continuity snapshot when no Done is
  emitted. Preserve separately reported usage on failure; do not add the same
  counters again when Done also includes them.
  Usage frames carry cumulative snapshots for the current execution, not deltas.
  Adapters publish observed snapshots promptly through the same ordered stream;
  waiting for Done unnecessarily loses known measurements if the Runtime stops.
  Core replaces complete valid token breakdowns and preserves the last committed
  measurement on interruption. Missing measurements remain unknown. Do not infer
  token consumption from context occupancy or estimated costs, or parse native
  Raw payloads in Core. Public Session totals cover recorded root Turns and are
  null while any root Turn has not ended or once one ends with unknown usage;
  Runtime telemetry uses the separate measured sum of recorded snapshots. Subagent
  Turn listings are not a summable accounting ledger. A cumulative native total that has not advanced
  past the Turn's baseline is not a measurement of that Turn. Native measurement coverage
  and exact provider/model attribution remain explicit qualification boundaries.
  No separate public usage event or historical SSE replay is introduced.
- The dispatcher is an internal entry point used by the standalone service worker.
  Legacy internal `daemon` configuration is not a public Environment type.
  Self-hosted enrollment uses exact local binding; further pending interactions
  remain separate slices. Unexpected interaction requests fail explicitly until supported.
- `environment_none` advertises an adapter's explicit environment-disable
  path. Execution snapshots with public `environment.type=none` require that
  capability and set `disable_execution_environment` on the internal prompt.
  For Codex, the daemon forces `CODEX_EXEC_SERVER_URL=none` after caller environment options
  and confirms native `local` and `remote` environments are unknown before starting
  or resuming a thread. Unsupported binaries fail closed. The bound device hosts
  the engine process; it is not a user execution environment. This is not an OS
  isolation guarantee, and engine state still lives on that host. Ordinary product
  requests retain their existing environment. The public worker selects an authenticated same-tenant engine host for this mode.
- `execution_controls` advertises the typed search/verbosity block on the daemon
  prompt. Agents API requires it in addition to the selected engine's required capabilities
  before binding/claiming work. Older peers with only option-based capabilities
  must not receive controls they would ignore. The API sends resolved search and
  text verbosity values; native option names belong to adapters. Codex translates
  them using its existing validation/catalog path after cloning adapter options,
  so explicit controls take precedence without mutating those options. Omitting
  the entire block preserves ordinary product behavior; a supplied block requires
  both valid fields. This internal contract does not add public configuration or
  engine support. Future native adapters must verify the same semantics before
  advertising the capability.
- Explicit public `programmatic_tool_calling.enabled=false` uses the common
  `ExecutionControls.DisableProgrammaticToolCalling` field and the operation-specific
  `programmatic_tool_calling_disable` capability. Both public qualification and
  Runtime support are required for that request; omission creates no prerequisite.
  New and resumed executions retain the frozen setting. Codex disables native
  code-mode features and checks managed requirements before starting/resuming a
  thread, rejecting a conflicting requirement. Claude and MiniMax retain their
  restricted native inventories, which exclude programmatic execution. This does
  not remove unrelated native utilities or claim enabled programmatic support.
  Explicit `web_search.mode=disabled` reuses the existing disabled search control.
  Search remains off when omitted. Optional search settings are resource data and
  do not cause execution while disabled. Saved Agents keep every pinned search mode;
  enabled search remains unqualified and rejects at Session admission.
- `web_search_control` advertises the Codex adapter's explicit `web_search` option
  (`disabled`, `cached`, or `live`). Agents API requires this capability before Codex dispatch;
  the typed execution controls force search off on new and resumed Turns. Native configuration translation stays in the
  adapter. Product requests that omit the option inherit their existing defaults.
  This is tool selection, not a network isolation guarantee.
- Inline Agent `text.verbosity` accepts `low`, `medium` and `high`; omitted or
  null values resolve to `medium` in the immutable configuration snapshot. The
  Codex dispatcher requires `text_verbosity` support and sends the effective value in
  typed execution controls through the Codex adapter for both new and resumed Turns. The adapter queries
  the native active catalog with `codex debug models`, checks model support and
  pins that catalog snapshot for execution. The probe requires Unix process-group
  cancellation; other daemon hosts do not advertise this capability. For models
  without declared verbosity support, including the native unknown-model fallback,
  `medium` selects native default text generation by omitting the override. The
  pinned protocol defines `medium` as the default text amount. Supported models
  still receive explicit `medium`, even when their catalog default differs.
  Unsupported `low`/`high` and unreadable catalogs fail before model execution;
  unsupported non-default levels remain an explicit implementation gap.
  Product requests that omit the native option retain their existing defaults.
  Structured output has a separately qualified profile described in
  [Structured output execution](../../contracts/agents-api/execution-tools.md#structured-output).
- `subagent_control` advertises native subagent tool control. Agents API requires
  it when resolved `multi_agent.enabled` is false and sends the typed internal
  `disable_subagents` policy on both new and resumed Turns. Native translation
  stays in the adapter: Codex disables both multi-agent feature generations,
  overriding operator feature preferences. Product prompts that omit the policy
  retain their defaults. Enabled multi-agent observations require separate operation qualification; the Agent tools list is not
  proven to enumerate every harness-internal utility.
- Subagent resources use the common observations in
  `internal/agentdaemon/proto/subagents.go`: verified identity, successful lifecycle
  effects, native-owned Turns/Items and neutral coordination operations. Core
  assigns public IDs and projects them under the existing Session lock and leased
  execution journal. Native names, history parsing and outcome proof stay in
  adapters. Public GETs read persisted resources without starting native work.
  Child Turns have a native writer and a separate table from the Core queue.
  Session Turn reads and the Session event stream carry root work only: read child
  Turns and Items through the Subagent routes, and never publish child Turn or Item
  events on the Session stream. A child Turn's `agent_id` is the Session's Agent
  ID; `subagent_id` names the child. The migration-defined `public_execution_turns`
  view has no public reader; do not reintroduce mixed Session Turn pages.
  Session Items stay root-owned; copied parent transcripts never become child work.
  Repeated effects are idempotent. Active includes idle; task completion, process
  release and cancellation cannot fabricate public closure. Native timestamps
  retain their actual precision and unknown Usage stays null.
  Reuse the existing native owner for child settlement and cancellation, freeze
  root output first, and deliver child Items before their terminal Turn snapshot.
  Do not add another scheduler or a broad recovery framework. Capability
  advertisements do not qualify unsupported native facts. The exact read contract,
  admission limits and remaining evidence are in [Subagents](../../contracts/agents-api/subagents.md).
- `function_tools` advertises the optional native function-call bridge. Explicit
  prompt definitions become Codex dynamic tools; unchanged prompts carry none.
  Requests and ordered text/image results are scoped by Run and native call ID.
  Normalize string results into one text part at the public execution boundary;
  the internal result carries a typed content array, and adapters translate it
  to native content without fetching images or dropping parts. Validate content
  before consuming a pending call. Retry identity includes the complete ordered
  content and success flag. Reuse
  application receipts and conflict detection; a receipt confirms the native
  reply was written, not that an external side effect succeeded. Pending calls
  end with their Run; the execution service owns persistence and recovery, while
  the product client retains business approval and credential-owner authorization. Do not
  map native approval requests to invented official protocol resources.
- Function-call storage is scoped by authenticated tenant, Session and Turn, with
  immutable public/executor call identities and arguments. Result admission and
  application receipts serialize on the same Session lock as cancellation and
  terminal transitions. Store the complete caller-validated result object; wire
  validation and native translation belong to their API and execution boundaries.
  Identical retries return the saved decision; changed results conflict. Pending
  reads exclude applied calls and cancelling/terminal Turns while history remains
  readable. Persistence does not imply transparent native-process recovery.
  Recording a call moves the Turn to `waiting`; the last application receipt
  resumes it. Session reads use one database snapshot for Turn, actions and usage.
  Session state events retain their action snapshot, without private executor IDs
  or results. Cancelling/terminal Turns expose no actionable calls. A waiting Turn
  can fail or cancel before a result arrives; successful execution requires resume.
  Actions remain visible until native application is acknowledged. This timing is
  an implementation choice, not verified upstream event sequencing.
- Function results can join internal message/cancel input batches. Their explicit
  Turn/call identity selects an existing call; admission never creates a Turn for
  a result. Save the complete result and its input retry record in the same Session
  transaction. Any invalid target, conflicting result or later batch error rolls
  back the whole request. Resolve targets only after the tenant Session lookup:
  an unknown call or a call of another Turn is 400 `invalid_request_error`, and
  missing or foreign Sessions keep one 404. Identical saved results remain retryable after termination
  without applying them again. The execution input cursor skips function results;
  their separate native receipts still determine application. Public result events
  validate variant-specific fields and required values before admission; store
  omitted versus null error/output and ordered text/image parts. Inline function
  tools resolve into the immutable configuration with explicit
  `defer_loading=false`. Validate required strings and parameter objects before
  persistence; reject unsupported deferred discovery. Omitted/null/empty tool
  lists resolve to no tools. The public worker selects or waits for a same-tenant
  device advertising `function_tools` when the Session has functions.
  Function results are Session input Items: emit `item.added` with a null output
  index, and never emit `item.done`, whose upstream union only allows agent output.
  Project their public output/error from the saved submission; the wire always
  carries both, null when not submitted, while stored payloads keep the submitted
  presence. Native content normalization must not change public history.
- Codex function application requires a matching live native dynamic-tool completion,
  including root thread/Turn/call identity, function, success and ordered content.
  Writing its JSON-RPC response is not application. The adapter owns pending
  receipts without holding their state lock across IO or waiting; terminal state,
  cancellation and native loss settle unconfirmed submissions before release.
  Uncertain receipt timeout ends that native execution without resending the result.
  The common Runtime interface and router continue to own delivery identity,
  retry/conflict and terminal ordering; Core never parses native tool events.
  Record native confirmation before potentially blocking observation publication;
  output backpressure cannot turn a known application into an unknown outcome.
  The function submission owns raw write completion and receipt failure together;
  its writer never applies an independent timeout/close decision. A confirmed
  receipt releases submission even if writer completion has not yet been scheduled.
- Internal function execution requires an advertised `function_tools` capability
  before claiming a Turn. Translate resolved definitions in the execution adapter,
  persist declared callbacks before exposing actions, and deliver each saved result
  once per live dispatch. Keep its success flag and ordered text/image output;
  append a non-null error as a final text part because the native result has no
  separate error field. Retain the original complete result in storage. Do not
  treat transport delivery as application or automatically replay an uncertain
  result. The adapter waits for outstanding application receipts even when Done
  arrives first. Waiting Turns still accept execution observations and cancellation.
  The public function workflow is verified with the pinned SDK and a real daemon
  and Codex process against a synthetic model endpoint. This does not verify
  other tool types, deferred discovery or upstream service timing.
- `message_items` advertises native assistant-message observations. Agents API
  opts in with `observe_messages` only for advertised peers; ordinary product
  requests retain their existing frame sequence. Opted-in text deltas carry their
  native item ID, and `output_message` records start/completion, phase and the
  completion text snapshot. A snapshot is not another delta; uncompleted messages
  remain partial when their Turn ends. Keep these observations in the journal
  before projecting public Items. This does not promise daemon event replay.
- `tool_observations` advertises engine-neutral tool snapshots. The opt-in
  `observe_tool_observations` attaches the typed `observation` to tool-call frames.
  Native adapters own discriminator/status/action translation and preserve raw
  structured values; reuse the shared function-result content type. Kinds are
  `command`, `mcp`, `function` and `web_search`; observation status is
  `in_progress`, `completed`, `failed` or `incomplete`. A present empty function
  content array remains distinct from missing content. These are
  execution facts, not public Items: the API owns public IDs, schema projection,
  lifecycle events and persistence. Product requests that omit the opt-in keep
  their frame sequence and fields. Agents API requires this capability before
  claiming work and always requests neutral observations. Its Item projector
  validates this shared contract and never decodes engine-native tool snapshots.
- Codex callers opting into neutral tool observations also receive `command_output`
  fragments with the existing native command identity. The adapter filters the root
  Thread/Turn; the service requires an already indexed command in the same Turn.
  Journal and Item updates commit with `agent.output.command_execution_output.delta`
  events, retaining original fragments and the command's stable output index.
  Completion output replaces accumulated drafts; absent completion output retains
  observed text. Terminal Items ignore late fragments, and cancellation preserves
  partial output without inventing successful command completion. Native text
  conversion and output quotas still apply; this is not a byte-complete stdout/stderr
  guarantee. Pinned native 0.153.4 also has an early-output subscription window;
  missing native notifications/aggregate bytes remain a separate execution gap,
  not output to reconstruct from model tool-result prose. Older peers may supply
  only completion snapshots. Product requests
  without the observation opt-in retain their existing frames.

### Observations and streaming

- Execution observations are written to tenant-scoped `turn_events` in ordered,
  idempotent batches before they can back recovery or publication. Keep daemon
  payloads intact; this internal journal is not the public SSE protocol. Flush at
  least every 100 ms while consuming events and before terminal persistence;
  uncommitted observations can be lost on a hard process crash. Terminal outcome,
  journal entry and native continuity commit together. Preserve partial text on
  cancellation, including frames queued before a separate cancellation receipt.
  Do not infer successful completion after a persistence error or stream overflow.
- Agents API uses the gateway's durable subscription; overflow or disconnection
  closes it with an explicit error. Product subscriptions retain their existing
  best-effort behavior. Journal limits are 512 KiB per payload, 1 MiB per batch,
  65,536 observations and 32 MiB per Turn; terminal persistence reserves one
  additional outcome entry. These are internal admission limits, not promises
  about upstream API limits or durable daemon-to-service replay.

- Live Session SSE reads execution-owned `session_events`, committed with the
  corresponding input, Item or lifecycle transition under the Session lock.
  Store immutable transition snapshots; never render an old event from a later
  Turn state. Reuse the API's response mapping and keep internal snapshots out of
  wire payloads. Historical index rebuilding emits no live events.
- The notification buffer retains at most 256 events and 64 MiB per Session
  after each transaction, retaining a single oversized event if necessary.
  Read batches are bounded to 32 events / 1 MiB, with the same single-event
  exception. This buffer is not a public replay log: GET begins at the committed
  high-water mark, ignores Last-Event-ID, and polls committed events every 100 ms.
  Missing sequence positions produce a safe stream error and close; recover via
  Session/Turn/Items queries. Socket writes have a five-second deadline and hold
  no database connection. Client disconnect releases the handler; comments keep
  idle connections alive. GET SSE does not close merely because one Turn finishes;
  only creation responses end on settlement (below).

- Session creation with `stream=true` reuses atomic input admission and the live
  event loop. The upsert returns its cursor under the Session lock, before initial
  inputs; never replace it with a post-commit cursor lookup. A new response emits
  one request-local `agent.session.created` with the committed Session projection
  that the JSON 201 response returns (read after the commit), then committed
  changes from that cursor exactly once. A fresh creation stream ends right
  after the first `agent.session.idle` recorded when a Turn ends or an input
  reservation stops being pending (expired, cancelled or failed), or any
  `agent.session.failed`, and never sends the events after it. A self-hosted
  connection clearing pending input to idle, `requires_action`, function results
  and resumed work keep it open. A creation that admitted nothing (no Turn or
  reservation) ends right after `created`. Settlements that record no event use
  a fallback: after an empty drain the stream reads the JSON-path projection and
  the event cursor in one database snapshot and, if the Session is idle or failed
  with no queued, running or waiting Turn and no pending reservation, sends only
  events up to that cursor, then ends. Accepted follow-ups: another client's work
  drained before that read can still be sent, and idles recorded by an older
  binary during a rolling deploy carry no settled marker and rely on the
  fallback. An input reservation made while the ending Turn captured Artifacts
  can start a later Turn that the stream does not follow. The settled marker and
  pending-input flag are Store-internal, never wire fields, and add no events.
  Re-read the projection after a sent Session status event and otherwise at most
  once a second. The local creation retry key excludes response mode; a same-key
  `stream=true` retry of an existing creation returns 201 with only the
  connection comment and ends at once, admitting nothing and following no work,
  because official same-key requests create distinct Sessions. Retry the same
  request/key with `stream=false`, or use the GET events stream, to recover. GET
  event streams keep their live-only start and never end on settlement or a Turn
  failure; the only server-side end is the terminal `agent.session.failed` of a
  hosted provisioning failure (and Session deletion), since that Session can
  never run again.
  Disconnect never cancels admitted work. Official observations cover `none`
  creation; self-hosted, hosted and no-input stream lifetimes and the retry
  behavior are local choices, and the separate SDK one-Turn helper does not
  define this endpoint. Do
  not present local retry behavior as replay. A new Turn records `turn.created`,
  its user input Items, then Session activity in one transaction. Terminal Turn
  events carry top-level `usage` copied from their Turn snapshot, null when
  unknown; never derive or sum it.

### Public Turns, Items and events

- Public Turn retrieve/list project persisted execution state and the immutable
  Session Agent identity. Scope both resources and pagination cursors to the
  authenticated tenant and Session, ordering by creation time then ID. Do not
  expose adapter outcomes, native IDs or raw errors. Failure uses a customer-safe
  category; usage is nullable when a complete upstream breakdown is unavailable. Submission uses the separate Session events endpoint.


- Public Items list reads a persisted execution-owned projection, updated in the
  same Session transaction as admitted messages and journal batches. IDs derive
  from the Turn and source identity; the first-observation timestamp and stable
  tie breakers never change when content or status changes. Allocate each new
  Item's Session position under the Session lock, preserving observation order
  for equal timestamps. Allocate a separate zero-based `output_index` per Turn;
  inputs do not consume output indexes. Updates and retries retain both values.
  Persisted indexed history keeps its deterministic order; missing original
  ordering cannot be reconstructed. Cursors are scoped to the authenticated Session.
  Terminal Turns expose unfinished Items as `incomplete`, preserving completed
  message/tool states independently of the Turn outcome.
- Public history reads use the persisted index; the private pre-Items journal
  backfill is retired. Migration 15 and its historical validation remain evidence,
  not a supported upgrade procedure. An old installation is unsupported and must
  be retained separately from a fresh installation. Never mark unprepared history
  indexed by hand or replay engine execution to convert it.
- Project only the declared public Item variants; native adapter metadata is not
  a response schema. Preserve structured tool JSON without float conversion.
  Completion text replaces accumulated deltas. Item merging must not mutate the
  incoming observation or the previous snapshot: public text delta events read the
  original fragment after merging, while Items retain the accumulated text.
  Copy the content slice before replacing its text pointer. Assistant text
  Items follow the official event sequence: `item.added` in progress with empty
  content, an empty `content_part.added`, deltas, then the done events. A first
  observation without its own fragment (a non-streamed native final) carries its
  unchanged text in one delta; this frames the text and never alters it. Wire-only
  explicit nulls (`phase`, function result `output`/`error`, Item event
  `output_index`, Agent `reasoning` keys) come from response marshalling. Stored
  Item payloads keep their original encoding through `Item.MarshalStored`, so
  replayed child Items still compare equal, and stored configuration keeps
  omitting unset reasoning keys. Keep partial output on termination;
  do not turn an unfinished call into a successful result. Thinking fragments are
  internal observations, not a claim of upstream reasoning-item support.

- Public `POST /v1/agents/sessions/{session_id}/events` accepts ordered text-message
  and cancellation batches through the pinned official client. Preserve individual
  input messages in the Item index while deriving text for native dispatch. Batch
  idempotency and cancellation targets remain durable; unsupported variants fail
  before admission. Session creation accepts initial text as a string
  or user-message array through the same parser and admission path. Commit the
  Session, initial input, first Turn and Item/event projections in one transaction.
  A creation retry returns the existing Session without re-admitting initial work,
  including after terminal or later Turns. Omitted/null input is permitted only
  for non-streaming hosted creation and self-hosted creation.
  Creation streaming uses the shared live path above. Image support requires the
  qualification in [the message-input contract](../../contracts/agents-api/message-content.md#images).

### Worker ownership

- Enabling daemon transport with `OAC_PUBLIC_URL` also starts a bounded execution worker. Select
  only connected, capable devices owned by the authenticated tenant; bind once and
  preserve native continuity. Metadata cannot select a device. Offline work stays
  queued and can be cancelled. An engine host is not a self-hosted environment.
- One worker service owns an execution database through a dedicated PostgreSQL
  advisory-lock connection. Its execution Store view uses that same connection for
  every Session transaction: binding, claim/reconciliation, journal/Items/Usage,
  function callbacks/application receipts and terminal/native continuity. Serialize
  these short transactions and lease pings; execution transactions have a five-second
  deadline including gate and Session-lock waits. Sandbox reset snapshots bound the
  deployment relation explicitly to its legal singleton row before resource joins.
  Keep this cardinality visible even on fresh databases without statistics: inflated
  join estimates can trigger expensive JIT compilation inside the lease deadline.
  Never hold a transaction across daemon/model work, reconnect the writer or fall
  back to the pool after lease loss.
  The original Store handles public admission and device/auth maintenance on pooled
  connections. Execution reads may also use the pool; a read grants no write authority.
  At startup, reconcile previously claimed work as failed, preserve queued inputs
  and never replay uncertain execution. Shutdown cancels active dispatch and attempts
  terminal persistence before releasing the lease; a lost owner cannot commit it.
  Lease Close invalidates its writer and waits for pgx connection cleanup within
  the caller deadline. A later Close can resume that wait after a timeout. This
  drains client resources; it does not acknowledge remote advisory-lock release.
  Tests that immediately transfer ownership must observe the previous owner's
  exact database advisory lock disappearing before starting its successor. Bound
  that wait and fail on query errors; do not retry Worker startup to mask competing
  owners or change production lease behavior for a test's timing assumption.
  Worker shutdown retains its existing bounded best-effort close policy.
  This fences database writes, not already queued daemon commands or native effects.
  Native quiescence/reconnect and recovery of unreported outcomes remain separate
  gaps; this is not distributed exactly-once side-effect execution.
- Session state and last activity derive from its latest persisted Turn. Queued or
  active work is `in_progress`, successful/cancelled work is `idle`, and failures
  use a safe public error. The worker does not replace product dispatch, business
  authorization, or the separate approval/environment lifecycle work.

## Hosted sandbox nodes and optional suspension

Default installation includes Core, Web and PostgreSQL but no execution node.
It always creates the Core key. Core receives only its digest; the paired console
server receives the private key, uses it for sign-in and injects it only on
`/core/v1` requests after console login and same-origin checks. The browser
never receives that key. Node and daemon connections use `/api/v1`
with their own credentials; the reverse proxy sends them directly to Core, never
through Web. Zero-node Core receives neither the Docker socket nor KVM.
The Web and deployment administrator API select one provider, per-sandbox
resources and immutable Runtime release; the Core address comes from the
installation public URL. PostgreSQL owns this complete,
generation-tagged selection under the existing execution lease and deployment lock.
The shared `sandbox.DeploymentSpec` defines required CPU/memory and supported disk
limits plus Runtime provenance; neither a node file nor the installer owns another
selection. Request `resources` describes limits; response `specification.resources`
contains those limits, while response `resources` counts retained allocations and
pending hosted Environments. Keep these meanings distinct in clients and UI.
See the [deployment contract](../../contracts/agents-api/sandbox-deployment.md).

Docker and E2B accept CPU/memory but reject independent nonzero disk capacities;
do not claim hard root/workspace disk quotas for them. Docker creation and native
inspection enforce the declared CPU/memory and exact image. E2B setup verifies the
exact ready template build and matching CPU/memory through the pinned SDK before
saving its encrypted account key, and records the build as read for the safe view;
an omitted E2B `resources` adopts that build's CPU and memory. Loading a committed selection reconstructs its
provider from its owned generation, current committed credential and receipts without repeating candidate
template validation; a template endpoint outage must not block cleanup of existing
sandboxes. Creation and instance inspection still enforce the saved resources.
E2B uses direct placement without a node. Node
providers require one immutable distribution with source commit, Docker image ID,
OCI manifest digest, microsandbox image reference, Runtime and firmware hashes.
These identities are distinct and cannot substitute for each other.

Startup claims the stable installation identity and a new owner epoch before
provider selection. The runtime manager retains generation-aware provider facades.
Initial setup and replacement prepare and validate candidates before database
writes. Rejected candidates preserve the active configuration and workers. A node
replacement uses the existing mutation gate, pauses manager admission, drains old
calls and loops, then repeats the resource/generation guards in the commit
transaction. A changed selection, its generation and retirement of old nodes and
unused enrollment tokens commit together. Publish the prevalidated configuration
and shared observation/bootstrap cache under the manager mutex without further
external work or a fallible activation step. Request cancellation after commit
cannot discard that publication. Interrupted drains remain barriers for retries
and resume. Keep the Worker and runtime manager as single owners; provider I/O and
draining hold no database transaction or manager map mutex.

A locally unavailable provider dependency keeps hosted admission closed while the
existing scan waits for repair; administrator recovery remains available, including
on restart. Database and ownership errors remain failures. Unconfigured hosted
admission creates no Session state. Derive Runtime bootstrap and daemon WebSocket
addresses from the validated installation public URL, never inbound Host headers. Read the
current selection from the live deployment API; there is no startup configuration
read.

All sandbox writes require the observed generation, including initial POST at
zero; reject stale state before provider preparation, reset or no-op checks, and
repeat it under the committing row lock. Same-provider PUT advances the target
without draining execution or retiring nodes, tokens or the owner epoch. Node
providers prepare independently and keep their old qualified serving pin. A different backend or E2B team requires reset. Historical selections without a
valid specification are unsupported and rejected at startup; keep their original
Core responsible for retained resources and install the current release separately.
Preserve historical allocation ownership and placement, never migrate a Session.

Persist immutable allocation and placement deployment generations, distinct from
compute generations. Node allocations copy their placement; E2B binds at reservation.
Keep superseded specification/build rows without credentials while current, owned by
an unreleased allocation/placement, or pinned by any nonremoved node. A durable node
serving pin survives offline state and zero resources. GC uses the deployment lock
and bounded pages; reset clears pins only after release. Never downgrade facts needed
for generation routing. Bind node readiness to exact generation, current connection
and owner epoch. Promote a durable pin only for readiness of the then-current target
under deployment serialization. Late superseded readiness cannot acquire a pin.
Filter online/readiness/address/capacity before preferring the newest eligible pin;
newest-full must not mask older-free. Route Create, restore and cleanup through the
immutable allocation/placement generation. Fixed-configuration manual nodes serve
only their enrolled generation; generation management is an explicit hello capability
on the same current wire protocol, never a historical-version fallback.

Use sparse generation control batches of at most eight entries with no lifetime cap. Omitted
facts never authorize deletion. Correlate whole retention grants to connection,
epoch, sequence, generation and digest; recheck queued/inflight/helper references.
Permanent generation flock files survive updates and GC. Before any helper starts,
the installer durably binds the original inode to installation/generation/specification
identity; Python and Go openers verify it after every restart and never adopt a
replacement or missing record. Preparation plans stay distinct from write-once final
provider configurations: pending-only entries may recover or collect under Core
authority, never serve. Resolve Docker's supported local image ID before publication.
Only transfer/checksum/provenance errors report runtime_download_failed; preserve
other fixed causes without parsing native error text. Retained Runtime bytes and
the console's exact-release HTTP allowlist must agree. See
`contracts/agents-api/node-generation-protocol.md` for recovery, immutable artifacts
and conservative retention of unproven historical helper ownership.
Interrupted node collection keeps an exact private generation journal and immutable
configuration. Restart treats it only as a candidate for a fresh correlated Core
drop grant, never as preparation or serving readiness. Persist native cleanup
before removing its executable and durable file cleanup before the dropped marker;
CLI errors and unknown native ownership cannot establish absence. Docker daemon
images are shared host content, so automatic node GC retains them; only the host
administrator can establish whole-host authority to remove them. Microsandbox
image GC remains scoped to the installation-private store. Fresh nodes persist
verified original Runtime file ownership separately from the host program; collect
only exact unreferenced Runtime paths, keeping program, identity, base configuration
and manifests readable across interrupted cleanup and restart.

Use one E2B classifier. Omitted key preserves the current key; identical selection
with omitted key is a no-op. Explicit key submission, including identical bytes,
verifies and increments generation. Check the candidate and retained builds, paginated
team-owned template membership and every settled live receipt under the candidate key.
Initial E2B selection must also belong to the submitted key's team. Before online
replacement, verify the current exact template under the committed key, then under
the candidate key: shared membership is the ownership anchor, including for legacy
installations with no retained resources. The pinned SDK exposes team-owned template
listing, not an authenticated team ID; never invent or persist an inferred team ID.
Public readability cannot anchor ownership. A legacy selection outside the committed
key's team, or a revoked committed key, requires reset without blaming the candidate
key. Transport uncertainty remains unconfirmed. Keep the old key valid until the PUT
returns 200; revoke it only afterward. Missing/unsettled receipts and
unconfirmed reads reject. Fence credential commits against all provider calls and
actual helper subprocess completion after cancellation, then reverify. Helper exit
never settles unknown remote Create. Bounded failure keeps the original key and
lifecycles. Resolve each allocation's immutable specification and current key in one
snapshot; no stale-key cache, fallback to current spec, or provider-map unloading.
Audit the committed change without secret fields.

Project rollout and reset from the same database snapshot. Old retained resources
alone never imply preparation or high-frequency polling. Rollout state is settled
unless actual target preparation is active. Offline/unconfirmed nodes are unknown;
only exact current connection/epoch/generation readiness is ready. A durable pin is
not connectivity. Fixed diagnostics alone may explain failed preparation. Report
preparing only from an actual target observation; an online fixed-configuration node can be
update_required while its valid pin remains available for admission.

Reset is durable execution state, advanced by the existing manager outside its
counted work. Serialize start, escalation, cancel, setup, update and finalization
through the mutation gate. Auto waits only for in-progress/waiting root/subagent
Turns and pending file writes; queued work, idle and suspended Sessions can archive.
Recheck idleness with Session then deployment locks; never invert that order during
finalization. Keyset-page bounded work, bind candidates to the reset request time
as well as generation, and persist the original absolute deadline and validated
audit provenance. Cancellation cannot revive archived work. Auto's deadline
escalates to force durably; self-hosted Sessions are excluded.

Use one snapshot and timestamp for the held-resource partition: unreleased
allocations plus pending hosted Environments without any allocation row. Cleanup
has precedence, then busy/idle. Count deleted/expired receipts until release.
Offline ownership comes from allocation or active placement node identity, with
the existing 45-second connection/owner-epoch predicate; provider readiness is
independent. Project every offline node and make its sum equal on_offline_nodes.
No cleanup failure, offline state or empty read authorizes a synthetic release.

At zero held resources, drain outside database transactions, recheck under the
deployment lock, and atomically clear all provider policy, E2B credential/build
metadata and specification, retire nodes/tokens, advance generation/owner epoch
and audit completion. Publish a generation-bearing nil provider tombstone without
fallible work after commit, preventing delayed loads from reviving the old provider.
On a failed final write or interrupted drain, restore the committed provider using
a bounded owner context before releasing the mutation gate; recovery failure keeps
admission fenced and stops the owner. Ordinary migration resumes old Web-managed
maintenance with reset null. Refuse downgrade during reset or for an unconfigured
completed reset with generation above zero; never discard the generation.

Explicit administrator Session archive requires a Web-managed deployment and its
current generation, without a reset precondition. Keep Project scope and hosted
eligibility checks in its Session-first transaction, alongside Environment expiry,
cancellation, Runtime authority revocation and audit. Background reset reconstructs
the actual Project scope from trusted records while retaining requester provenance.
The ordinary provider lifecycle owns compute/snapshot release. Retain history and
persisted Files/Artifacts; archived Sessions cannot resume unpersisted workspace.
The archive GET reports resource disposition, not provenance or Turn finalization.

Archive cancellation must not destroy a healthy receipt path before its terminal
commit. Only the archive that first revokes a device may persist its exact
`archive_cancel_turn_id`; ordinary revocation clears it, and repeated cleanup
preserves rather than recreates it. The existing authenticated delivery can drain
its cancellation for at most 20 seconds from the Turn's original
`cancel_requested_at`. Track that delivery through Done, cancellation ACK and
terminal commit, independently of subscription removal. Heartbeats and both normal
and checkpoint cleanup use the same identity/deadline check; no new connection,
input, file/MCP authority or lease renewal is granted. Do not hold a transaction or
lifecycle gate waiting for the receipt. Lost peers, expiry and restart retain the
ordinary failure/cleanup fallback, never a fabricated cancelled outcome. Preserve
unknown Create ownership and reject schema downgrade with unsettled markers.

There is no file-managed startup path or embedded local node. An older file-managed database is not automatically adopted after its environment variable is removed. Settle and drain that deployment with its previous release and original backend, preserving business data, private receipts, identities and storage. The legacy file-managed path has no automatic adoption or force conversion. The supported current path is a database-managed deployment. Harness selection and public/self-hosted contracts remain unchanged.

The paired console serves only matched, non-secret distribution artifacts for node
installation. Never serve private installation files or arbitrary paths. Installation
reads `GET /api/v1/sandbox-node/configuration` using an unconsumed enrollment token,
or a retained node credential with `X-OAC-Node-ID`. Reads never consume enrollment;
registered nodes can read their matching configuration during reset. Validate
installation, generation, specification digest and release before writing node files,
registering or reconnecting. Reject drift rather than overwriting retained identity
or using local resource defaults. Registration consumes a token only after these
checks. The node installer requires root or sudo, verifies downloaded files and
starts a root-owned system service for the dedicated `oac-node` user it prepares.
It performs no SSH installation, Session creation or model call. Ordinary-user
installation and removal are rejected before reading credentials or mutating state.
Internal generation preparation and collection still run as the service account.
Web exposes one root/sudo command; do not retain a user-service alternative. This
boundary is specific to Sandbox Provider nodes, not native self-hosted daemons.

Node management (Web's **Nodes** page; see the
[nodes guide](../../docs/getting-started/nodes.md)) is a
deployment-level admin surface, separate from Project credentials. The paired
console's Core key stays on its server. Enrollment credentials authorize
initial node configuration reads and registration; durable node credentials authorize
retained configuration reads and node transport. Project keys cannot read nodes,
placement or allocations.

One execution owner manages every node through the same finite Provider protocol.
The Core host joins through the same standalone node installer and service as any
other host; every node needs a guest-reachable, non-loopback HTTPS origin.
All managed nodes actively connect over authenticated TLS. Persist private node
identity and highest owner epoch; refuse another process using the same identity
or a changed backend namespace. Reserve each NodeID before transport upgrade and
retain that reservation through disconnect cleanup; a duplicate connection must
not replace a live or opening connection. Keep one private state directory per
node and never copy its identity to another host. This is connection exclusion,
not host attestation. The Hub's global mutex protects only in-memory connection
state. Authentication, ownership and Store callbacks run synchronously outside
that mutex, respect cancellation and have a five-second limit; never detach
database writes. Closing the Hub cancels opening and live connections without
waiting for database callbacks. Keep each node reservation until its fenced
disconnect cleanup finishes. Register database presence in an explicit transaction:
a canceled statement must not later publish presence through autocommit. Disconnect
cleanup first locks the node row by identity, then applies the connection/epoch
fence with a fresh READ COMMITTED statement so an in-flight commit cannot be
missed. These transactions must not acquire the deployment-wide manager lock.
Heartbeats establish provider readiness and
last-observed host metrics, never Session activity. An unready provider reports
one fixed diagnostic code, classified by typed probe errors where the node detects
the cause; probe text and host paths stay on the node, and Core stores unknown
codes as `provider_unavailable`. Keep that set closed. Transport reconnects use
bounded backoff. Send relative operation budgets, anchored to the node clock at
receipt and consumed while queued; clocks on different hosts need not agree. Core
still bounds its own response wait. Do not replay mutations after a timeout or
lost response. Retain allocation
and checkpoint operation receipts and observe the original operation instead.
Disconnects and read timeouts are unavailable/uncertain, never resource absence.
Node transport preserves an exact-reference, explicit `CreateSettled` receipt
alongside its original provider error. Settlement authorizes eventual allocation
release, not execution. A confirmed native Create rejected by the subsequent
read-only configuration check, before bootstrap starts, can return that receipt.
Do not infer settlement from timeout, missing compute or successful Kill. Strict
configuration rejection must not prevent already-authorized cleanup: Kill still
checks ownership independently, and release still requires creation settlement.
Runtime resource observation uses the same immutable node placement through one
bounded read-only Provider operation. Preserve main's Runtime observation/history
service and authorization boundaries. The node delegates only to a provider-owned
observation source; absent capability or transport returns unavailable, never a
Core-local fallback. Observation must not create, renew, restore, or touch Session
activity. Preserve the durable compute receipt and provider timestamps; existing
observation clock validation can reject skewed samples without changing idle policy.
Online-state writes compare the handshake epoch atomically in PostgreSQL so a
stale Core cannot publish readiness for a new owner. Node-managed allocations
do not expire merely because the internal observation keepalive is an hour old;
explicit deletion and configured snapshot retention still authorize cleanup.
Persist only bounded, sanitized observation codes for offline, missing or
unconfirmed resources; keep these separate from the lifecycle and do not invent
a successful running observation after a host restart.

Managed lifecycle state is owned by one serial worker per registered node:
its gate, allocation and pending cursors, connections and
wake hints are not shared with other nodes. A thin coordinator discovers nodes
and owns worker shutdown; it never holds its map mutex during database, provider
or wait operations. Each worker advances independently, including when another
node is online but its provider is stuck. Do not add a shared scan barrier or
global provider pool: lifecycle concurrency is at most one operation per node,
and grows with the registered node count. This is not a fixed global limit.
Keep offline workers so retained resources remain observable after reconnect.

Allocation scans filter by the fixed node before their 32-row page limit; pending
scans join the unreleased committed placement. Each node advances its own cursor,
including failed observations, and wraps once at EOF. Direct provisioning resolves
the tenant-scoped existing placement before entering that same node's gate; an
existing allocation must agree with the placement. Never choose another node.
E2B direct allocations use one serial lifecycle without a node identity.
The coordinator stops accepting work and cancels and drains all node workers and
direct callers before releasing the sole execution lease. Lease loss is global;
ordinary provider failures stay within their node. A planned deployment drain or
inventory retirement cancels lifecycle contexts synchronously between leased
operations, using the existing lease gate with its five-second bound. Include
an active manual reconcile's cancel function: an asynchronous `AfterFunc` alone
can cancel a later leased query after the gate reopens. Never cancel an in-flight
leased query merely to change deployment configuration. A failed cancellation
fence synchronously closes manager admission and reports owner failure, even
before its coordinator starts. Failed inventory retirement retains the original
lifecycle identity and gate through owner shutdown; gate availability is not
proof that cancellation succeeded. The drain barrier stays closed and cannot
activate a replacement. Release the lease gate before waiting for provider
settlement or lifecycle accounting. Ordinary caller deadlines and owner shutdown retain their
existing cancellation and fail-closed lease-loss behavior. Session locks, deployment
capacity transactions and revision/one-shot receipts remain authoritative, with
no external operation holding a database lock.

Commit environment-to-node placement with Session creation and its creation retry
identity. Placement is automatic: Core chooses an eligible node, and callers cannot
select one. Existing retries keep their original node even when it is offline. Node capacity counts pending reservations and
unresolved resources; new placement and suspended-to-restoring admission share a
database lock. Confirmed cleanup releases placement capacity. Retained ownership requires exact
provider evidence; a socket path, missing instance or empty listing cannot prove
cleanup or authorize replacement. Historical file-managed ownership must be resolved
using its previous release and original backend before retiring that configuration.
Do not add a startup adoption path to bypass the database-managed selection.

Do not add node-level drain controls. Refuse node removal with pending allocations,
instances, snapshots, unknown results or cleanup resources. Offline ownership is
retained. Removing a node does not delete compute. Local and remote nodes share
the same resource guard. Keep the deployment-wide reset and configuration
change guard. This boundary does not add cross-node Session
migration, Core multi-active, autoscaling, Kubernetes or harness residency.

The common
`services/core/internal/sandbox` contract owns the five required operations and optional capabilities documented
in [the Provider guide](../../docs/sandbox-provider.md). Core orchestration must not import an adapter or SDK. Exact compute
identity, generation construction, inspection, full snapshot capture, restore,
thaw and owned artifact cleanup use that common capability. Initialization and
execution use the authenticated Runtime peer.
Provider-specific names and snapshot identities are opaque to Core. Self-hosted
compute and providers without checkpoint support keep their existing behavior.

The [microsandbox deployment profile](deploy/microsandbox/README.md)
pins the SDK, runtime, firmware and image. Core remains a pure-Go binary. The
one-shot native Linux helper contains the SDK/FFI and runs under the same private
service namespace; it is not another scheduler or network control plane. Ordinary
pause does not release RAM. Suspension captures and verifies a full snapshot,
stops the exact source, and removes its writable compute closure only after the
artifact is durably identified. Explicit network policy applies on create and
restore. Do not inherit undeclared host resources.
The native SDK owns a dedicated ext4 disk mounted at `/environment`, separately
bounded by `environment_disk_mib` alongside `root_disk_mib`. Workspace, staging
and outputs must share that filesystem; do not weaken cross-device or link
checks to accommodate the layered root. Creation uses `/` until bootstrap creates
the workspace. Existing full snapshots and sandbox cleanup own the disk, with
no external mount or separate storage lifecycle. Native restore may omit a configured
root-disk size because it inherits the verified full snapshot. Accept that omission
only with matching snapshot resource proof and exact source/target identity; inspect
other native limits before retaining the inherited proof on the restored target.
Never treat a missing root size as unlimited capacity or resize retained state.
Snapshot receipt observation uses ownership and artifact integrity checks, so a
resource mismatch cannot hide an existing snapshot from authorized cleanup.
Restore recovery may finish a missing derived resource proof on the exact target
of the verified snapshot, after checking its native limits. It must not repeat
Restore, start stopped compute or alter resource limits; reread the same target
strictly before returning success.

Suspend only after at least one Turn is terminal, no queued/in-progress/waiting
root or subagent Turn, pending input/file operation or initialization remains,
and real activity has been idle for the configured interval. For node-managed
allocations, record the first root or child terminal transition in the same
transaction using Core's database clock and the existing compute activity field.
For every Environment source, positive native completion timestamps remain
unchanged in public history, even when host clock skew places them before Core's
Turn creation time. They cannot drive idle admission across hosts; repeated terminal projections never reset that timer.
Read activity together with the database observation time. Candidate filtering and
the Session-locked phase recheck compare elapsed database time with the configured
idle duration; callers must not supply a Core-wall-clock cutoff. Anchor the initial
snapshot retention deadline to that same database observation. Core and database
host clocks need not be synchronized for these decisions.
Heartbeats do not reset activity. The daemon must close admission and drain native
cleanup, output receipts and file work before acknowledging planned suspension. Never change a
harness or keep an agent process alive across Turns solely to meet this feature.
The acceptance boundary is a next Turn in the same Session with history, files
and configuration intact, without replaying an earlier request.

The existing Worker lease, Session lock and per-node lifecycle gates own both providers.
New Turn claims, file-write intents and capture admission serialize under the
Session lock. Turn and file-write admission share the same compute-phase check;
existing receipts remain readable. New pending work cancels capture and wakes
the same source. Normal preparation waits for the
compute phase to be running, after the authenticated resume handshake; a pending
input remains pending if its promotion conflicts with a lifecycle transition.
Private compute phases and revision-checked JSON receipts live on the existing
allocation. Persist quiesce/capture/restore intent before effects; only the fresh
receipt performs a capture or restore. Recovery observes the exact attempt and
never retries an unknown creation, capture or restore. A consumed snapshot cannot
roll a running generation back. Deletion, revocation and retention expiry take
precedence over wake, including at the final database compare-and-swap. Retain
unknown cleanup identities until owned resources are confirmed absent.

Database-owned guest CPU/memory and supported disk settings, max_active reservations, max_retained
allocation count and snapshot retention bound each assigned node. Unknown operations
retain capacity reservations. Source teardown must be confirmed before releasing
active capacity. Delete consumed artifacts and old compute closures; do not grow
a chain of old writable disks across suspension cycles. No Kubernetes, distributed
scheduler or snapshot replication belongs in this V1 profile.

Queued work and live Environment file access request wake. History and published
artifact reads do not. Planned suspension uses private daemon wire 0.8.0 with an
Environment and suspension token; a PID/start-time fenced local control signal
wakes the parked daemon, which reauthenticates before admitting new work. A
transient disconnect before confirmation retries the same armed suspension with
bounded attempts and backoff; permanent authentication or protocol rejection
still closes it. Snapshot
lifetime has no daemon wall-clock timer: Core owns its retention deadline. A lost
quiesce acknowledgement may thaw the same source using explicit rollback control;
it does not authorize capturing it. Ordinary disconnect keeps the existing
conservative shutdown behavior. Authentication rejection cannot create a new
Runtime or replay a request.

All normal `make check` gates still apply. Linux qualification additionally runs
the pinned helper module tests/build through `check-microsandbox-provider`, a real
KVM full-snapshot/reclamation probe, and idle-to-next-Turn integration acceptance.
Synthetic process-memory probes support the backend claim only; they do not prove
agent continuity. Independent blind review uses the clarified idle-only scope.

Managed Runtime allocation, dedicated daemon credential hash and exact Session
binding commit atomically before Provider.Create, using the existing execution
lease and Session lock. Only the fresh allocation receipt permits Create; retries
and Core restart observe that same reference without replay or credential rotation.
The operator's stable provider key identifies one backend/installation; retain its
adapter for cleanup, and use a different key when changing the target. Never treat
absence on another backend as successful reclamation.
Disabling the default provider stops new hosted admission/bootstrap; it must not
block existing Session cancellation, tool results or input retry outcomes.
Input HTTP response budgets follow the persisted Environment type, covering the
admission wait for both hosted and self-hosted Sessions independently of operator
creation switches or remote executor configuration.

With an explicitly configured default managed provider, the same Worker scans
committed pending hosted Environments that have no allocation. This includes idle
Session creation and recovery after commit-before-bootstrap interruption; an
existing allocation never enters that startup path. Keep the scan bounded and
serialized by the existing lifecycle owner. Hosted provisioning requires no caller
connection action. An initial reservation without a Turn leaves its Session idle,
as allowed by the pinned contract; do not emit an in-progress event before a Turn
starts or treat a daemon connection as native readiness.

The same serialized scan publishes authenticated connection observations using
the existing durable generations after verifying the exact Session/device binding
and settled bootstrap. Socket loss remains observable during a provider outage;
Core restart fences old observations. Do not create a separate connection owner.

Terminal managed cleanup atomically revokes authority, persists Environment failure
or expiry, settles pending input and requests cancellation before external cleanup.
Preserve original input deadlines and retry outcomes. Temporary provider outages,
unknown Create results and stopped compute do not prove permanent failure. The
pinned stream has no Environment expired event; do not invent one. Exact hosted
failure codes and ordering remain explicitly unverified.

Allocation state is private compute ownership, separate from public Environment
connection/native readiness. Adapters qualify bootstrap completion; Core does not
infer it from an engine or provider name. Connected, observed compute receives
service keepalives between Turns. Keepalives cannot revive a one-hour lapse or a
cleanup request. The Docker provider keeps its current idle behavior; only an
explicit checkpoint policy may suspend completed, idle work as described below. A stopped/missing container
does not authorize discarding retained workspace or history. Session deletion or
expiry requests cleanup, revokes the scoped device and cancels pending work before
Provider.Kill; the existing Worker serializes these lifecycle operations and drains
them before releasing its execution lease.

Keep the allocation after public Session deletion. Mark it released only after
owned compute/volume cleanup and evidence that its original Create has settled.
An unknown creation retains cleanup ownership even after an absence observation;
continue bounded scans for late resources without issuing another Create. This
conservative internal lifecycle does not define user-managed enrollment or prove
complete upstream expiry/error semantics.

Qualify the actual Runtime before cutover: execution, file access, owned
cancellation, restart with retained history and files, and clear failure when
required history is missing. Daemon tools run with the launching account's full
permissions. Managed isolation is provided by the outer Environment. A user who
installs on a host does not receive a sandbox or protection from their own tools.
Keep failed probes and unverified platform combinations explicit.

## Public and administration API constraints

These constraints implement the rules in the [Agents API contracts](../../contracts/agents-api/README.md) and the [Core administration API](../../contracts/agents-api/admin-api.md).

### Wire validation mechanics

- Stored strings other than metadata rely on PostgreSQL rejecting U+0000 and invalid UTF-8: map SQLSTATE `22021` (text parameter) and `22P05` (`\u0000` in jsonb) to the 400 unstorable-text error, including query filters such as `agent_id`. The failing statement aborts its transaction, so keep each request's writes in one transaction.
- An `after` cursor that cannot name a resource on a lookup list (Agents, Sessions, Turns, Templates, Vaults, Credentials, the Core Runtime observation list) resolves to the never-assigned maximum UUID and runs the normal lookup, so storage failures and missing rows behave as for a well-formed cursor. Resolve every cursor only inside its already resolved parent and tenant.
- Lists whose parent and cursor lookups are separate statements (Artifacts, Skill versions) re-check the parent before reporting a cursor 400, so a parent deleted in between still returns its 404. Item and Subagent lists read both inside one locked Session transaction. The Skill version cursor lookup is tenant-wide so another Skill's version can be told apart from a missing one; another tenant's version stays missing.
- Saved Agents parse tools with the saved-form parser, which keeps every pinned `web_search` mode. Session admission re-resolves the effective tools with the execution parser, which admits only disabled search; Worker device selection and the final preclaim also refuse a non-disabled search control.

### Message text and result targets

- Admission and the Claude bridge share one whitespace set, the union of Go `unicode.IsSpace` and ECMAScript `String.prototype.trim` (`blankTextRune` in `services/core/internal/execution/message_support.go`). A shared table test keeps both sides equal; change them together.
- Whitespace-only admission is a field of the engine profile (`WhitespaceOnlyText`) checked with the image profile during Worker admission. Never branch on the harness name in handlers.
- `MessageInput.Validate` treats any non-empty text part as content and never trims. It runs in Core admission, Worker delivery, daemon steering and prepared start, and in the Codex and MiniMax adapters.
- Resolve a tool result's target under the tenant Session lock, after the Session lookup: a well-formed `turn_id` is looked up in that Session and must own the call; otherwise the Session's own calls decide between "unknown call" and "different Turn". Never reject a malformed `turn_id` before the Session lookup, so missing, malformed and foreign Sessions keep one 404, and read only the caller's Session.
- The recorded Environment failure reason is composed only from a fixed step label and integers (setup command index, exit status 1-255), so commands, environment values, package names, paths and process output cannot reach the reason, events, logs or responses.

### Environment files, Skills and Artifacts

- Environment file list tokens bind a digest of the tenant, Environment, requested directory, effective order and limit, plus a fingerprint of the full sorted regular-file path and size list and an offset that is a multiple of the limit. Every page rereads the directory; there is no cursor registry, cache or snapshot. Reject every token mismatch with the single official token message.
- The directory helper checks each requested path component with `Root.Lstat` below `os.OpenRoot(workspace)` and opens the final directory with `O_NOFOLLOW`. A missing component, a regular file or a symbolic link maps to the distinct `not_directory` result, which the daemon and gateway carry only for directory reads; Core turns it into an empty page. `not_found` (Claude SDK adapter reader), permission, transport and uncertain results keep their errors.
- A Files.create write intent stores a digest of the path, size and content, not a path ledger, so Core cannot tell a file an earlier Files.create wrote from any other file; an existing regular file therefore gets the untracked-file message. Reserve the intent under the Session lock before dispatch. The daemon verifies the complete body's SHA-256 before calling the writer, and the writer creates parents with `Root.MkdirAll(0700)`, writes `.oac-write-<uuid>` in the workspace root and publishes it with `Root.Link`, which never replaces an existing entry. Known refusals return `write_rejected` with `reason` `destination_directory` or `unsafe_destination`; Core settles the intent as `rejected`, which leaves no committed receipt and releases the mutation owner. Only an exact committed or rejected receipt settles an intent; nothing settles an unknown one automatically.
- Check the 5 MiB inline bound after path validation and Base64 decoding, and before the pending-hosted check, the source File lookup and execution. The JSON body limit still admits the Base64 form of 50 MiB so that oversized inline bodies up to that size get the official message.
- Serialize Skill version uploads, default changes and version deletion on the owning Skill row lock. Deleting the default version deletes the Skill only when no other version row exists, through the same cascade as Skill deletion, so every encrypted version row goes in the same commit. `next_version` only increases.
- Decide Artifact republication in the Turn's terminal transaction, not the capture transaction: capture commits and releases the Session lock before the Turn completes, and the terminal transaction holds the Session lock that also orders Artifact deletion. Drop staged rows whose SHA-256 equals the newest remaining published Artifact for the path, ordered by the producing Turn's creation time and then ID (publication time can come from the Runtime and does not order Turns), and unlink their large objects in the same transaction. Published rows are never modified.
- A malformed `environment_id` Artifact filter resolves to the never-assigned maximum UUID, so it matches nothing without a text comparison.

### Credential encryption and OAuth refresh bounds

- `credentialcrypto` ciphertext is a format version byte followed by the standard AEAD nonce, ciphertext and tag. The authenticated data holds a fixed domain and version plus the binding (tenant, Vault, Credential, auth type, exact destination). Keep the domain string unchanged: existing rows must still decrypt.
- Random-nonce GCM allows at most 2^32 encryptions per key. `secrets/credential.key` also seals model providers, the E2B key, Skills, initial files and environment setup, so every sealed write counts toward that bound; there is no rotation or re-encryption path.
- OAuth dispatch refresh holds the Credential row lock and the external exchange under one 20-second context (`store.oauthRefreshTimeout`). The refresh HTTP client has a 10-second overall timeout and 5-second TLS handshake and response-header timeouts, uses no proxy and treats any redirect as failure.

### Core administration errors, metrics and write provenance

- Core error details are scoped by the `/core/v1` router's writer mark, never by a request path test. Write Core errors with `writeCoreError` and typed `CoreErrorDetails` values (string, number, boolean, null and string-array constructors); invalid or empty details are omitted as a whole. The mark preserves error observation, flushing and `http.ResponseController` access. A shared handler or a Core-looking path alone never changes a public or machine error envelope. Core authentication runs before operation configuration checks, and unknown paths keep their status and admission rules. When adding a code with details, document its fixed keys in `contracts/agents-api/core-errors.md`, and pass only safe Core-owned facts: never submitted values, secrets, native text or provider bodies.
- Operation validators keep their original error text, sentinel identity and validation precedence. Package-owned typed errors carry fixed field metadata; only the marked Core error mapper translates it into operation codes and safe bound or catalog details. Keep Project and key rune limits separate from node byte limits. Sandbox validation metadata travels through its store wrapper without changing transaction or provider authority. Public Session provider validation stays byte-for-byte unchanged; cover it with handler-level golden responses. The Core clients ignore malformed optional details and never retry a write.
- Core metrics instrument the existing worker and job owners without changing scheduling, lease or retention behavior. Count `execution_unavailable` at the HTTP error writer, once per rejected response; never capture request or response bodies and never infer the count from other 503s or failed Turns. Process CPU, RSS and cgroup limits are sampled by the 30-second Core metrics loop into the same bounded in-memory ring; the first CPU interval and restart gaps stay null, and host usage never substitutes for process usage. Root Turn history is queried read-only from PostgreSQL with native timestamps. Builds inject the source commit with `-ldflags` into `main.buildRevision`. Keep the response shape aligned with `packages/agents-client/src/core-metrics.ts`.
- Public resource writes carry the authenticated key's provenance separately from the execution principal. Record the operation and any creation ownership in the business transaction, never in response middleware or an asynchronous queue; a failed record rolls back the write. Internal lifecycle and refresh work never acquires public provenance, and retries never replace ownership. Environment uploads persist the safe request origin before dispatch and record success with the confirmed Runtime receipt, not the native filesystem call. Never put payloads, paths or secrets in audit metadata. Do not confuse key identity with the Session creator identity used for retries.
- Administrator writes reuse the public resource deletion and serialization code and record their administrator audit entry in the same transaction.
