# Files and Skills

Files (`/v1/files`) and Skills (`/v1/skills`) are Project resources with their own lifecycle, independent of Sessions. A File holds uploaded bytes that Environments copy by ID. A Skill holds immutable, versioned bundles that Templates and Sessions reference. Every API key of a Project shares them.

These routes follow the SDK pinned in [upstream.json](upstream.json): the [Files resource](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/files.py), [create parameters](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_create_params.py), [FileObject](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_object.py) and the [Skills resource](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/skills). They need a Project API key and no `OpenAI-Beta` header. A missing ID and another Project's ID return the same 404.

## Files

| Operation | Behavior |
| --- | --- |
| `POST /files` | Multipart upload with one `file` part and `purpose=user_data`, in either order. Returns 200 with the File |
| `GET /files` | Lists the Project's Files without reading their bytes |
| `GET /files/{file_id}` | Returns the File |
| `GET /files/{file_id}/content` | 400 `Not allowed to download files of purpose: user_data`, with a null code and param. The ID is checked first, so a missing File returns 404 |
| `DELETE /files/{file_id}` | Deletes the File and its bytes; returns `{"id": …, "object": "file", "deleted": true}` |

Use a File by passing its ID to [Environment files](environment-files.md#create-a-file) or to a Template's or Session's initial `files` ([Environments](environments.md)). Those copies read the bytes internally; the public download stays refused.

### Upload

- Only `purpose=user_data` is accepted. Other purposes, `expires_after` and the Uploads API are not supported.
- The file may be empty and holds up to 512 MiB; the whole multipart body may exceed that by 64 KiB. A larger upload returns 413 `request_too_large`. The transfer must finish within five minutes.
- A missing, repeated or unknown part, a `Content-Encoding` or `Content-Transfer-Encoding` header, or a filename that is empty, longer than 1,024 bytes, not UTF-8 or contains NUL returns 400. Core stores nothing until the whole request validates.
- Core does not deduplicate uploads. After a lost response, list Files before uploading again.

### File object

| Field | Value |
| --- | --- |
| `id`, `object` | File ID; `file` |
| `bytes` | Size in bytes |
| `created_at` | Unix seconds |
| `filename` | The uploaded name. It is metadata only and never becomes a filesystem path |
| `purpose` | `user_data` |
| `status` | `processed`, meaning the bytes are stored. Core does not parse, index or scan them |
| `expires_at`, `status_details` | `null` |

### List Files

| Parameter | Rule |
| --- | --- |
| `order` | `desc` (default) or `asc`, by creation time, then ID |
| `after` | ID of a File this Project can see |
| `purpose` | One of `user_data`, `assistants`, `batch`, `fine-tune`, `vision`, `evals`, `assistants_output`, `batch_output`, `fine-tune-results`. Any other value, including a different case, returns 400 with `param: "purpose"` before the cursor is resolved. Values other than `user_data` return an empty page. An empty value means no filter |

The response is `{"object": "list", "data": [...], "first_id", "last_id", "has_more"}`; an empty page has null IDs. `limit`, query parsing and their errors follow the shared [list rules](wire-semantics.md#lists).

### Errors

A missing or foreign File returns 404 with type `invalid_request_error`, a null code and `param: "id"` for retrieve, content and delete.

### Storage and deletion

Core stores File bytes as PostgreSQL large objects in its own database. An upload and a deletion each commit in one transaction, so a failure leaves neither partial bytes nor metadata. [Back up](../../docs/getting-started/operations.md#back-up) the database with its large objects; deleting a File does not remove it from write-ahead logs or earlier backups.

The source Files schema refuses a downgrade while File rows remain. Delete Files through the API first so their large objects are removed.

A copy into a workspace reads a consistent snapshot of the File and can finish after the File is deleted; later lookups fail. Deleting a File never changes a workspace copy.

## Skills

| Operation | Behavior |
| --- | --- |
| `POST /skills` | Uploads a new Skill. Its first version is both default and latest |
| `POST /skills/{skill_id}/versions` | Uploads a new version. A `default` form field of `true` makes it the default; `false` or omitted leaves the default unchanged |
| `GET /skills`, `GET /skills/{skill_id}` | Skill metadata, without decrypting any bundle |
| `POST /skills/{skill_id}` | `{"default_version": "<n>"}` changes the default version |
| `DELETE /skills/{skill_id}` | Deletes the Skill and every version |
| `GET /skills/{skill_id}/content` | ZIP of the default version |
| `GET /skills/{skill_id}/versions`, `GET /skills/{skill_id}/versions/{version}` | Version metadata. The list orders by version number, and `after` is a version ID (`skillver_…`), not a number |
| `GET /skills/{skill_id}/versions/{version}/content` | ZIP of that version |
| `DELETE /skills/{skill_id}/versions/{version}` | See [Delete a version](#delete-a-version) |

`limit`, cursors and query errors follow the shared [list rules](wire-semantics.md#lists).

### Upload a bundle

Send one ZIP as a `files` part, or a directory as repeated `files[]` parts whose filenames are relative paths such as `report/SKILL.md`. SDK 3.13.0 sends no part when `files` is a single file rather than a list, so upload a single ZIP with plain HTTP:

```sh
curl "$OPENAI_BASE_URL/skills" -H "Authorization: Bearer $OPENAI_API_KEY" -F files=@report.zip
```

A bundle has one top-level folder containing `SKILL.md` and any supporting files:

- `SKILL.md` is UTF-8, at most 256 KiB, and starts with YAML front matter. `name` is required: lowercase letters and digits, optionally separated by single `-` or `_`, at most 64 characters. `description` is required and non-empty. `license`, `compatibility` and a string-valued `metadata` map are optional; any other key is rejected.
- Entries are regular files or directories with clean relative paths. Links, special files, absolute paths, `..` components and duplicates are rejected.
- Limits: 5 MiB compressed, 20 MiB expanded, 500 files, and 1,000 ZIP entries including directories.

Core encrypts each version's bundle bound to its Project, Skill and version. ZIP uploads keep executable bits; directory uploads store files with mode 0644.

### Versions and metadata

- Version numbers start at 1, increase by one per upload and are never reused, even after the latest version is deleted. Template and Session selectors name versions by number, so a reused number could point a stored selector at different bytes.
- The Skill's `name` and `description` are those of its default version. Changing the default, by `POST /skills/{skill_id}` or by uploading with `default=true`, updates the pointer and both fields together; `id` and `created_at` stay the same.
- `latest_version` is the highest remaining version.
- Uploads and deletions of one Skill run one at a time, so a deletion never removes a version whose upload was acknowledged.

How Templates and Sessions select a version (default, `latest` or a number) and freeze its bytes is in [Environments](environments.md#skills-plugins-and-environment-mcp).

### Delete a version

| Version | Result |
| --- | --- |
| The default, and the only version | 200 `{"id": "skillver_…", "object": "skill.version.deleted", "deleted": true, "version": "1"}`. The Skill is deleted in the same transaction |
| The default, while other versions exist | 400, type `invalid_request_error`, code `invalid_value`, `param: "version"`, `Cannot delete the default skill version.` |
| Any other version | 200 with the same body. If it was the latest, `latest_version` falls back to the highest remaining |
| A missing or foreign Skill or version | 404 |

Deleting a Skill or a version does not change Sessions that already installed it; Templates keep the reference they stored.
