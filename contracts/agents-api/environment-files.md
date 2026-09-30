# Environment files and Artifacts

A Session's workspace holds live files that the agent and its tools change. `/agents/environments/{environment_id}/files` lists one workspace directory and creates files in it. When a Turn completes, Core copies the files under the workspace's `outputs/` directory into immutable Artifacts, read through `/agents/sessions/{session_id}/artifacts`. Artifacts outlive the Environment; workspace files do not.

The routes follow the SDK pinned in [upstream.json](upstream.json): the [Environment files resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py), its [list parameters](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/file_list_params.py), [EnvironmentFile](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_file.py) and [TokenPage](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/pagination.py). They need the `OpenAI-Beta: agents=v1` header.

## Where files work

- Environment files work on `openai_hosted` and `self_hosted` Environments. A `none` Environment returns 503 `execution_unavailable`.
- Public paths start at `/workspace`, which stands for the Environment's workspace directory wherever it is on the machine.
- The Environment is looked up in the caller's Project first; a missing or foreign ID returns 404.
- On an `openai_hosted` Environment that is still `pending`, both operations return 400 `the hosted environment is still provisioning; wait until it is connected before accessing files`. This check runs after request validation and before any source File lookup. Any other state proceeds, and an unreachable Runtime returns 503.
- Listing and writing never start a Turn or send input to the model.

## List files

`GET /agents/environments/{environment_id}/files` lists the regular files directly in one directory. It does not recurse. Directories, symbolic links and other entries are left out.

| Parameter | Rule |
| --- | --- |
| `path` | An absolute directory in clean form at or below `/workspace`; default `/workspace` |
| `limit` | 1–100; default 20 |
| `order` | `desc` (default) or `asc`, by path in byte order |
| `page` | The `next` token of the previous page, with the same `path`, `order` and `limit` |

The response is `{"object": "page", "data": [...], "next": …, "has_more": …}`. `next` is null on the last page, and `has_more` is true exactly when `next` is set. Each file has `environment_id`, `object: "agent.environment.file"`, the absolute `path` and `size_bytes`.

- A `path` that does not exist, names a regular file, or passes through a symbolic link returns an empty page. Links are never followed.
- Each page reads the directory again; there is no snapshot. If the directory's regular files (their names or sizes) or the request's parameters changed since the token was issued, the token is rejected. Unchanged names and sizes do not prove unchanged contents.
- A directory with more than 1,024 entries of any kind returns 503 and no partial page. Permission errors, a missing workspace root and transport failures also return 503.
- When the daemon has no local workspace binding, the Claude Code adapter answers the read instead: a missing path returns 404, and a regular file or symbolic link returns 503.

Query errors, all with type and code `invalid_request_error` and a null `param` unless noted:

| Case | Message |
| --- | --- |
| `path` relative, outside `/workspace`, longer than 4,096 bytes, not UTF-8, or containing a backslash, NUL, CR or LF | `path must be an absolute directory inside /workspace` |
| `path` not in clean form: a trailing or repeated `/`, `.` or `..` | `path must identify a non-reserved directory inside /workspace` |
| A malformed token, another request's token, or a token whose listing changed | `Invalid file page token for this request` |
| `limit` outside 1–100 | `limit must be between 1 and 100`; malformed integers and invalid `order` use the shared [Beta list errors](wire-semantics.md#lists) |
| A repeated `path`, `limit`, `order` or `page` | The shared duplicate-field error ([list rules](wire-semantics.md)) |

Unknown query keys are ignored. An explicit empty value is invalid for every key. Malformed query encoding, such as `%GG` or a `;` separator, returns 400 `invalid_request`.

## Create a file

`POST /agents/environments/{environment_id}/files` takes either form:

```json
{"type": "inline", "data": "<standard Base64>", "path": "/workspace/data/input.csv"}
{"type": "file_id", "file_id": "file-…", "path": "/workspace/data/input.csv"}
```

Empty inline `data` is valid and creates an empty file. It returns 201 with the four EnvironmentFile fields. `file_id` names a [File](source-files.md) of the same Project; Core reads its bytes before contacting the Runtime.

| Case | Result |
| --- | --- |
| Missing parent directories | Created with mode 0700; the file gets mode 0600 |
| Parent directory is a symbolic link to a directory inside the workspace | The link is followed and the file is created at its target |
| Destination exists as a file, a symbolic link or another non-directory entry | 400 `environment.files paths must not traverse symlinks or overwrite existing files`. Nothing changes |
| Destination is an existing directory | 400 `file path conflicts with an existing environment file` |
| A parent component is a regular file, or a symbolic link leading outside the workspace | 400 `invalid_request`, `Invalid resource identifier or request limits.` |
| `inline` data above 5 MiB after decoding | 400 `environment.files[0].data exceeds the 5 MiB decoded limit`, before any Runtime work. Exactly 5 MiB is accepted |
| `file_id` File above 50 MiB | 413 `request_too_large` |
| Request body larger than the Base64 form of 50 MiB plus 16 KiB | 413 `request_too_large` |
| `path` relative, the root itself, outside `/workspace`, longer than 4,096 bytes, not UTF-8, or containing a backslash, NUL, CR or LF | 400 `environment.files[0].path must be an absolute POSIX path inside /workspace` |
| `path` with an empty, `.` or `..` component | 400 `environment.files[0].path cannot contain empty, . or .. path components` |
| An unknown top-level field | 400 `Unknown parameter: '<field>'.` with `param` set to the field. A name longer than 256 bytes or with unprintable characters gets `Unknown parameter.` and a null `param`. Only the first unknown field in the body is reported |
| A missing or null required field, the other form's field, an unknown `type`, or invalid Base64 | 400 `invalid_request` |
| Missing or foreign `file_id` | 404 `not_found_error` |

Unless the table names a code, the 400 errors have type and code `invalid_request_error` and a null `param`. An existing destination is never replaced: the Runtime writes a temporary file and publishes it with a hard link that fails if the destination exists. Later tool writes can still change the created file.

### Write ordering and uncertain outcomes

- Before sending any bytes, Core records the write under the Session lock. While input is pending, a Turn is running or an earlier write is unsettled, a new write returns 409 `turn_conflict`. An unsettled write also makes new messages to the Session return 409.
- The Runtime checks the complete body against its digest before creating anything, so incomplete input creates nothing. A write that fails later can leave newly created empty parent directories.
- Only a definite receipt from the Runtime settles a write, as committed or rejected. A rejected write changes nothing and releases the Session. If the connection drops, the request times out or no receipt arrives, the request returns 503 and the write stays unsettled, across Core restarts. Core never resends it and has no automatic recovery, so the Session accepts no further writes or messages. Reads still work.
- Deleting the source File after its bytes were read does not affect the copy.

## Artifacts

### Capture

When a Turn completes on an `openai_hosted` or `self_hosted` Environment, Core copies every regular file under `/workspace/outputs/` and publishes the copies in the same transaction that completes the Turn. Each Artifact's `path` is the file's absolute workspace path, such as `/workspace/outputs/report.md`. Failed and cancelled Turns publish nothing. Without an `outputs/` directory there is nothing to capture.

- Symbolic links anywhere below `outputs/` are skipped by their own type: never followed, opened or resolved, and never an Artifact. The remaining files are still captured.
- A later Turn in the same Session publishes a path only when no Artifact remains for it in the Session, or when its bytes (SHA-256) differ from the newest remaining Artifact for that path. Newest follows the order of the producing Turns. An unchanged path keeps its existing Artifact and ID. Published Artifacts are never modified.
- The capture fails, and the Turn fails with `artifact_capture_failed` (or ends `cancelled` when cancellation was requested), when `outputs` is not a directory (including a symbolic link to one), an entry is a FIFO, socket or device, a file changes size or modification time while it is copied, or a limit is exceeded: 4,096 entries, directory depth 64, a 4,096-byte path, 200 MiB per file or 500 MiB per Turn.

### Read and delete Artifacts

| Operation | Behavior |
| --- | --- |
| `GET /agents/sessions/{session_id}/artifacts` | Lists the Session's Artifacts |
| `GET /agents/sessions/{session_id}/artifacts/{artifact_id}` | Returns `id`, `object: "agent.session.artifact"`, `session_id`, `turn_id`, `environment_id`, `path`, `size_bytes` and `created_at` (publication time, Unix seconds) |
| `GET /agents/sessions/{session_id}/artifacts/{artifact_id}/content` | Streams the bytes as `application/octet-stream`, with the file's base name as the attachment filename |
| `DELETE /agents/sessions/{session_id}/artifacts/{artifact_id}` | Returns `{"id": …, "object": "agent.session.artifact.deleted", "deleted": true}` |

- Reads work whether or not the Environment still exists, including after it expires.
- Deleting an Artifact leaves the workspace file alone. A content read already in progress can finish; later reads return 404. Removing a file from the workspace leaves its Artifacts in place.
- Deleting the Session deletes its Artifacts.

List parameters:

| Parameter | Rule |
| --- | --- |
| `order` | `desc` (default) or `asc`, by publication time, then ID |
| `after` | An Artifact ID of this Session |
| `environment_id` | Only Artifacts produced in that Environment. A malformed or unknown ID returns an empty page; an empty value means no filter |

`limit` and cursor errors follow the shared [list rules](wire-semantics.md#lists). The response is `{"object": "list", "data": [...], "first_id", "last_id", "has_more"}`; an empty page has null IDs. The Session is looked up first, so a missing or foreign Session returns 404 whatever the query.
