# Environment files

The complete protocol target remains the SDK pinned in [upstream.json](upstream.json).
The public GET and POST `/agents/environments/{id}/files` have partial coverage.
Inline and source-file (`file_id`) creation target a qualified V1 local Environment,
including the Core-managed Docker profiles for Codex, Claude Code and MiniMax Code.
[Source Files](source-files.md) have their own project-owned lifecycle. Managed
hosted provisioning and shared Artifacts are accepted within the
[recorded Docker MVP scope](README.md#accepted-milestone-and-evidence) and separate
historical Core-managed [E2B qualification](README.md#e2b-v1-qualification). The
new user-managed enrollment chain reuses local Files with separate
[real public acceptance](user-managed-runtime-v1.md); complete Files/Environment
semantics and other providers are not implied.

## Pinned contract

The [Files resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py)
and [list parameters](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/file_list_params.py)
specify optional absolute-directory filtering, limit 1–100, case-sensitive
path-component ordering (default descending), and an opaque `page` token with
unchanged path/order/limit across pages. Limit and path are nullable SDK inputs;
order and page are not nullable when supplied.

Each [EnvironmentFile](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_file.py)
has `environment_id`, `object: agent.environment.file`, absolute `path`, and integer
`size_bytes`. The pinned [TokenPage](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/pagination.py)
requires `data`; `has_more` and `next` are optional and nullable. This implementation
returns the observed official envelope `object: page`, `data`, `next` (null on the
final page) and `has_more`, which is true exactly when `next` is set.

## Current scope and local policies

- Read direct regular files in one authorized self-hosted or qualified local workspace
  directory. Local public paths are rooted at `/workspace`, independently of the
  physical path frozen into its dedicated Runtime.
  Omitted path selects the workspace root. Do not recurse or follow symlinks;
  directory, symlink and other non-regular entries are omitted. A path that is
  missing, a regular file or a symlink lists an empty page; the link is not followed.
  Daemons without a local workspace binding use the Claude SDK adapter reader,
  which keeps 404 for a missing path and 503 for a regular file or symlink.
- Omitted limit uses 20. Well-formed unknown query keys are ignored; a repeated
  `path`, `limit`, `order` or `page` returns the Beta duplicate-field error. Unlike
  the shared lists, which drop malformed pairs, malformed query encoding (such as
  `?foo=%GG` or a `;` separator) still rejects the request locally. Empty values are
  rejected by their own rule. Limit errors use the Beta `invalid_request_error` code. The pinned SDK's
  [query serializer](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/_qs.py)
  omits scalar `None` values, so nullable limit/path follow omission behavior.
  Literal `null` and empty scalar query values are not accepted.
- Require an absolute UTF-8 POSIX directory of at most 4096 bytes within the
  workspace, in cleaned form. Backslash, NUL, CR and LF are rejected. Redundant or
  trailing separators, `.` and `..` components are rejected rather than normalized;
  the exact directory binds the cursor and is passed workspace-relative to execution.
- Authorize through the existing tenant-scoped Environment lookup before inspecting
  directory paths, cursors or runtime availability. Existing project-shared reads
  remain permitted. The reader receives that exact Environment and rechecks its
  execution ownership; the API never selects a daemon or a local filesystem path.
- Accept only a complete validated native directory, bounded by the shared
  1024-entry limit. Truncation, unknown kinds, missing sizes, duplicate or unsafe
  names, and uncertain output return safe 503 without `data` or `next`. The bound
  applies before filtering non-regular entries, sorting or public pagination.
- Cursors are bounded base64url tokens tied to tenant, Environment, canonical
  directory, effective order/limit, and the full sorted regular-file path/size
  result. Every page rereads the directory. Changed files or parameters invalidate
  continuation with safe 400. No cache, durable cursor registry or snapshot is
  promised; unchanged path/size metadata does not prove unchanged contents.
- Reader errors reuse the existing safe error mapping: invalid input 400 and
  unavailable execution 503. Only the native reader's distinct `not_directory`
  result becomes an empty page; a missing workspace root or other native `not_found`
  keeps 404, and permission, transport and uncertain failures keep 503. Native error
  text never enters the response.
  Listing does not create a Turn or admit model input. Idle reads use temporary
  read-only preparation; actual transport disconnect/reconnect events remain visible.

Default limit, omitted-path scope, recursion, non-regular entries and cursor
invalidation behavior are local policies or remaining gaps, not verified hosted
semantics. The pinned source does not establish them. The sampled envelope, query,
path and empty-page rows are recorded in [wire alignment](#wire-alignment--september-23-2026).
Do not interpret the bounded direct-file implementation as complete Files.list
compatibility.

## Inline and source-file creation

The pinned create union requires `type: inline`, standard Base64 `data` and an
absolute destination `path` under `/workspace`, or `type: file_id`, `file_id` and
that path. Source IDs resolve only within the authenticated execution project;
filenames, URLs and filesystem paths cannot substitute for an ID. Both members
use the same destination writer. Required null/omitted fields, extra fields and
invalid Base64 are rejected; an unknown top-level field names itself as `param`
when its name is short and printable.
Unknown query keys are ignored. Empty bytes are valid. Paths must
be canonical and cannot name the workspace root; missing parent directories are
created. Inline data is limited to 5 MiB decoded, the official bound; a `file_id`
copy keeps the local 50 MiB destination limit, with bounded JSON and 64 KiB daemon
frames. See the [write semantics](#write-semantics--september-23-2026).

Creation uses the same tenant Environment lookup as listing. The Worker checks the
stored local profile, immutable exact device/Environment binding and live capability.
It never starts a model for upload or supplies a filesystem root from the request.
The deployment must qualify the protected sibling workspace/staging layout and
its selected native adapter. The [engine profile guides](README.md#public-engine-profiles)
describe accepted Docker configurations; the [E2B operator guide](../../services/core/deploy/e2b/README.md)
covers user-managed E2B Runtime packaging and links its separate real acceptance.
A capability or path declaration alone
does not establish isolation or public hosted admission.

Before sending any bytes, persist the mutation identity and request digest under
the Session lock. Pending input/execution and another unresolved upload exclude a
new mutation. The Runtime receives the complete body, verifies its digest and uses
the shared installer in its create mode: it creates missing parents and installs a
fresh mode-0600 file without replacing or following any existing path. Later
independent tool writes can change the installed file; the response does not
promise a snapshot.

Only an exact committed/rejected receipt settles durable ownership. Caller detach,
connection loss, timeout or missing output cannot be treated as rejection. Unknown
writes remain pending across Core restart and block successor mutation without
replay; read-only recovery remains available. Automatic uncertain-write recovery
and placement replacement are outside this batch. Controlled failures preserve
the destination only when the installer proves rejection, and only against this
operation, not independent workspace writers. Temporary-file cleanup is best effort.

Successful creation returns 201 with only the four EnvironmentFile fields. Reuse the
common safe error mapper; apart from the sampled rows below, current 400/409/413/503
policies and error timing are not evidence of exact upstream parity. This referenced-source milestone cannot close the complete Files
resource or Environment lifecycle requirements.

Source resolution reads an immutable snapshot before destination admission. A
source deleted before that lookup is unavailable; an already-resolved copy may
finish. Deleting a source never deletes a copied workspace file. A larger general
Files upload can be downloaded but is rejected before Environment dispatch when
it exceeds the destination limit. Exact hosted delete/copy timing is unverified.

## Acceptance boundary

API tests cover raw response fields, complete-result validation, filters, sorting,
pagination, local cursor policies, authorization order and safe errors. The separate
official-client fixture exercises flat directories generated through a real model,
raw HTTP and pinned SDK pagination, sizes, and two-tenant isolation. It does not
establish unspecified recursive, symlink or snapshot behavior. Runtime availability
and each engine's isolated placement require their own native and service checks.

The opt-in `services/core/tests/official_environment_files_create.py` reuses
the pinned SDK and raw HTTP listing assertions. Its stdin supplies the base URL,
preconfigured Environment ID, model-input text, and two private caller token
sources (`token_env` or `token_file`). The invoking native fixture supplies an
existing `uploads` directory and a staging symlink rejection probe, verifies exact
installed hashes, and has a real model consume source-copied text after source
deletion. Its source fixture also streams a 512 MiB upload, checks public download
denial, and verifies that it cannot bypass the smaller destination bound. Internal
large-object streaming integrity has separate PostgreSQL tests. This distinction
keeps private setup separate from public hosted creation acceptance. Mechanism tests
exercise detached/unknown outcomes and durable gates independently of model output.

## Wire alignment — September 23, 2026

The pin is unchanged: SDK 3.13.0, commit `d7c41ef`, `agents=v1`. This batch starts
from main `e4b124c` and aligns Files.create and Files.list wire behavior with the
hosted-environment campaign scan, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-2/hosted-env/` (`findings.json`
HE-10, 16, 18, 32, 34–39; raw records under `official/` and `run1/`). The probe
used three owned hosted Sessions, all deleted. The batch plan is
`~/.parsar/remediation/20260923/environment-files-wire/PLAN.md`.

| Row | Case | Core behavior | Evidence (finding: request ID) |
| --- | --- | --- | --- |
| F1 | Successful Files.create | 201 with the same four fields | HE-10: `req_cce5244cdd76471d85575b7bbcc3fdac`, `req_69a44a96866a4b52b637a5a5530dcd3c` |
| F2 | List envelope | `object: page`, `data`, `next`, `has_more`; `has_more` is true exactly when `next` is set. Paging and ordering are unchanged | HE-32: `req_c8c247cfd13145a2b92be5cad412508f`, `req_a311bf80ff0643db945aa5db0a78e95b` |
| F3 | Unknown list query key | Ignored; the page equals the request without it. Malformed query encoding (such as `?foo=%GG` or `;` separators) is still rejected locally, while the shared lists drop those pairs | HE-34: `req_c4cd4618f7264840ad7454e80ac7f83c` |
| F4 | Repeated `path`, `limit`, `order` or `page` | 400 `invalid_request_error`, param null, ``Failed to deserialize query string: duplicate field `<key>` `` | HE-35: `req_30b3c8bfced64383bf13c9d5bbc44a74` |
| F5 | `path` names a missing directory | 200 `{object: page, data: [], next: null, has_more: false}` with a local workspace reader (Claude SDK adapter exception below) | HE-36: `req_a311bf80ff0643db945aa5db0a78e95b` |
| F6 | `path` names a regular file or a symlink to a directory | The same empty page with a local workspace reader; the link is never followed | HE-37: `req_25fb0220fa7d4157a783711622b5d580`, `req_58b6784dc49640c194910c719d5c8102` |
| F7 | `path` not in cleaned form (trailing or repeated separator, `.` or `..`) | 400 `invalid_request_error`, param null, `path must identify a non-reserved directory inside /workspace` | HE-38: `req_a7ee5a49d1d24529b9de877c7ec5bc58` |
| F8 | Other validation errors | Code `invalid_request_error`, param null: list relative or outside path `path must be an absolute directory inside /workspace`; malformed, foreign or stale page token `Invalid file page token for this request`; create relative, root, outside or NUL path `environment.files[0].path must be an absolute POSIX path inside /workspace`; create empty, `.` or `..` components `environment.files[0].path cannot contain empty, . or .. path components`; unknown create body field `Unknown parameter: '<field>'.` with param `<field>` | HE-16: `req_3fb9feef630c442ba1c136506458362d`, `req_a41eb84c48594daf8fb55b95d8b118bd`, `req_f592639cfe28416395187ae76ad18228`; HE-39: `req_cbfed6a0336e47a395f42b82b45b20d7`, `req_5c2494e082714500bccbadbf60c6195d` |
| F9 | Files.create or list on an `openai_hosted` Environment still `pending` | 400 `invalid_request_error`, param null, `the hosted environment is still provisioning; wait until it is connected before accessing files` | HE-18: `req_938b99e48d3c4f4ab685dcf9b29baa6f`, `req_ff81d9155bb841618d5d1bbc1c987fdf` |

Decisions:

- F5/F6 are result mapping only. The native directory helper walks the requested
  path below the anchored root with `O_PATH | O_DIRECTORY | O_NOFOLLOW`. When that
  walk fails with `ENOENT` or `ENOTDIR`, it reports the distinct result
  `not_directory`: a missing component, a regular file, a symlink to a directory,
  a dangling or external symlink, and a path through any of them. A symlink fails
  at its own component, so no target is followed, opened or listed. Every
  component is validated before any is opened, so an invalid request cannot become
  an empty page. The daemon and gateway carry `not_directory` only for directory
  reads. Core lists nothing for it only after the same confirmed release that a
  listing needs.
- Real failures keep their errors. The existing `invalid_path` code also covers
  unsafe entry names inside a directory, and `not_found` also covers a missing
  workspace root and an entry removed during observation; mapping either would hide
  a failure as an empty listing. A missing or replaced root keeps 404 or 503. A
  root removed or replaced after it was opened fails lookups or reads as empty.
  So before reporting `not_directory` or an empty listing, the helper reopens the
  root path without following links and compares device and inode with the held
  root; a removed or replaced root keeps 404. Link counts are not used, because
  overlayfs can keep a nonzero count for a removed lower-layer directory. The
  check relies on local filesystem inode pinning; network filesystems such as NFS
  are not qualified.
  Permission denial, observation errors, transport loss and uncertain output keep
  503, and tenant and Environment authorization run first. The shared workspace
  path code and the write installer are unchanged.
- The list path is rejected instead of normalized. `..` and other non-clean forms
  use the F7 message. Outside paths, backslash, control characters, invalid UTF-8
  and paths over 4096 bytes use the F8 absolute-directory message; only the
  relative form was sampled.
- The create messages keep the observed `environment.files[0].path` field name,
  which comes from the official service's shared file validation. Backslash, CR,
  LF, invalid UTF-8 and overlong paths are unsampled and use the absolute-path
  message. The accepted path set is unchanged.
- The first unknown create field in document order is reported. Its name is
  repeated in the message and param only when it is at most 256 bytes of
  printable UTF-8; otherwise the error keeps code `invalid_request_error` with
  param null and the message `Unknown parameter.`, so the response stays bounded.
  A field of the other union member (such as `file_id` on `inline`), missing or null fields, an
  unknown `type` and invalid Base64 keep the local `invalid_request` code; none was
  sampled.
- Every rejected page token uses the sampled token message, including a valid token
  for other parameters or a changed directory.
- F9 runs after the tenant-scoped lookup and request validation, and before
  source-file resolution or execution. It applies only when the stored type is
  `openai_hosted` and its status is `pending`; a disconnected hosted Environment
  and every `self_hosted` status keep the existing execution path. The official
  order between validation and this check was not sampled.
- Core Web sends the cleaned form of a directory entered with a trailing slash.
  The client and Web accept only the new envelope and 201.

Deferred and unchanged: recursive listing (HE-30); creating parent directories,
overwrite and the inline limit (HE-11/12/15), since aligned by the
[write semantics](#write-semantics--september-23-2026) batch; the Environment
retrieve `files[]` projection (HE-03); Environment
events (HE-02); the `self_hosted` status value (HE-04); `self_hosted` Files
support, which the official service refuses and Core's daemon keeps; limit bounds,
the default path and ordering. Malformed query encoding (such as `?foo=%GG` or `;`
separators) stays a local rejection on this list, while the shared lists drop those
pairs; there is no official sample.
The deleted-Session 404 message is not adopted; the write semantics batch adopts
the conflict and size-limit messages. The Claude SDK adapter directory reader, used
only when a daemon has no local workspace binding, is unchanged: F5 and F6 there keep 404 for a missing
path and 503 for a regular file or symlink. A Runtime image built before this batch
keeps the same 404/503 results until it is rebuilt.

Go handler tests cover F1–F9 with foreign-equals-missing checks; real-PostgreSQL
Worker tests cover the `not_directory` mapping, its release confirmation and the
unchanged rejections; gateway, daemon and Rust helper tests cover the native
classification, including links to a directory and outside the workspace, dangling
links and a replaced root. The TS client and Web unit tests cover the envelope and
201. The opt-in official scripts replay the list and create rows through raw HTTP
and the pinned SDK. Live acceptance, the server gate and independent review are
recorded separately by the coordinator.

## Write semantics — September 23, 2026

The pin is unchanged: SDK 3.13.0, commit `d7c41ef`, `agents=v1`. This batch starts
from main `1eb60c27` and aligns Files.create writes with the hosted-environment
campaign scan, recorded privately in
`~/.parsar/remediation/20260923/campaign-scan-2/hosted-env/` (`findings.json`
HE-11, 12, 13 and 15; raw records under `official/` labelled
`fc02-nested-missing-parents`, `fc03-overwrite`, `fc06-5mib-plus-1`,
`fc15-onto-directory`, `fc20-overwrite-untracked`, `fc21-through-symlink-dir` and
`fc22-symlink-outside`), and with the "File limits" of the official Environment
files guide saved beside them. The batch plan is
`~/.parsar/remediation/20260923/workspace-file-writes/PLAN.md`.

| Row | Case | Core behavior | Evidence (finding: request ID) |
| --- | --- | --- | --- |
| FW1 | Missing parent directories | 201. Each missing component is created with mode 0700, the initial-file installer's convention, below the held no-follow walk | HE-11: `req_69a44a96866a4b52b637a5a5530dcd3c` |
| FW2 | The destination is a file that an earlier Files.create wrote | 400 `invalid_request_error`, param null, `environment.files paths must not traverse symlinks or overwrite existing files` (official: `file path conflicts with an existing environment file`). No bytes change | HE-12: `req_f661f17c5c714d829364e13da32db6b1` |
| FW3 | The destination is another existing file, such as one created by setup, a native tool or the model, or any other non-directory entry | 400 with the FW2 message. No bytes change | HE-12 `fc20`: `req_1242efc5adaa4ac5a854d2973ec81cd2` |
| FW4 | The destination is an existing directory | 400 `invalid_request_error`, param null, `file path conflicts with an existing environment file` | HE-12 `fc15`: `req_c835a06aa7d949e0ae7883ef83c3145b` |
| FW5 | A symlink anywhere in the parent chain or as the destination, or a non-directory parent component | 400 with the FW2 message. No link is followed | HE-13: `req_0c2c3cbf9e9947d79921cd198f42e9a6`, `req_02f00ab8a6c348aca0bfe47ac3704b18` |
| FW6 | Inline data above 5 MiB decoded | 400 `invalid_request_error`, param null, `environment.files[0].data exceeds the 5 MiB decoded limit`, before any Runtime work. Exactly 5 MiB is accepted; a `file_id` copy keeps the 50 MiB bound | HE-15: `req_088ba87e6e3e4436a7713015e37615b5`; guide file limits |
| FW7 | The destination or a link appears between the checks and the install | Never replaced or followed; the FW2 message | Rust race tests; not sampled officially |
| FW8 | Unknown outcome | The durable mutation gate and unknown handling are unchanged | — |
| FW9 | `self_hosted` | The daemon's local workspace writer serves Files.create for Core-managed Docker and user-managed Runtimes alike, so the same rules apply | — |
| FW10 | Initial Session files, Skills, Plugins and cold resume | Unchanged | — |
| FW11 | Tenant isolation, missing or foreign Environment | Unchanged 404 | — |

Decisions:

- The daemon's Go workspace writer now serves Files.create on all platforms.
  It verifies the complete body before creating parents, writes a temporary file,
  and publishes it with a no-overwrite hard link. Initial Session files use a
  separate atomic replacement operation. Skills and Plugins use
  [shared Runtime capability preparation](environments.md#runtime-capability-preparation).
  Completed recovery, including cold resume, never reinstalls files.
- Parents are created only after the complete body is verified, so incomplete or
  corrupt input creates nothing. A write failure can leave newly created empty
  parent directories, but never replaces an existing destination.
- Core cannot tell a file that an earlier Files.create wrote from any other file.
  Its durable write intent stores a digest of the path, size and content, not a
  path ledger, and a tool can remove and recreate a file afterwards; no cheap,
  race-safe lookup exists. FW2 therefore uses the untracked-file message, a
  message-only difference. A directory destination always uses the conflict
  message; the official sample was a directory created as a parent by an earlier
  write, and other directories are unsampled. Non-directory parent components and
  non-regular destinations are unsampled and use the FW2 message.
- The installer reports a fixed code, the daemon sends `write_rejected` with an
  optional `reason`, and Core settles the durable intent as `rejected`. A rejected
  write therefore leaves no committed receipt, releases the mutation owner,
  changes no bytes and reads but does not consume a Source File.
- The inline bound is checked after path validation and Base64 decoding, before
  the F9 provisioning check, Source File lookup and execution. The JSON body limit
  still admits Base64 for up to 50 MiB, so larger inline bodies up to that size get
  the official message; beyond it the local 413 remains.
- A Runtime image built before this batch keeps replacement and the existing-parent
  requirement, with the generic local 400, until it is rebuilt. An older Core
  ignores the new `reason` and keeps the generic 400. A new daemon with an older
  helper is refused as invalid input, without mutation.
- The TypeScript client and Core Web reject inline data above 5 MiB before sending.
  The client's strict Base64 check now scans linearly, because the previous pattern
  overflowed the regular-expression stack on inputs near 4 MiB and blocked an exact
  5 MiB upload.

Rust tests cover parent creation, existing files, directories, hard-link aliases,
FIFOs, symlink leaves and parents, dangling and inside links, non-directory parents,
races for the destination and a missing parent, a replaced held ancestor, the
`linkat` fallback and the explicit mode selection. Daemon tests cover the local
workspace writer's create mode and codes with a scripted helper; its native-helper
case runs only when `OAC_TEST_LOCAL_WRITE_HELPER` names a built helper, so it is
opt-in and was run for this batch by hand and through live Docker acceptance.
Dispatch and gateway tests cover the rejection reason. Go handler tests cover FW6 and the error mapping. A real-PostgreSQL
HTTP and Worker test covers the conflict messages, settled `rejected` intents with no
committed receipt, an unconsumed Source File, the inline bound before any intent,
tenant B isolation and an admitted successor. The opt-in
`official_environment_files_create.py` adds the nested, repeated, directory and
5 MiB + 1 rows, with an exact 5 MiB inline case while source copies keep 50 MiB.
Live acceptance, the server gate and independent review are recorded separately by
the coordinator.
