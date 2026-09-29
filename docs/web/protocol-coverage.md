# Protocol coverage

This matrix records which Core interfaces the OpenAgentCore console (`apps/web`)
consumes and for what. It is not a statement of public Agents API compatibility;
that inventory, its pinned baseline and its evidence live in the
[Agents API contract](../../contracts/agents-api/README.md).

The console is a management tool. It reads and deletes each project's assets and
manages projects and keys through the administrator API (`/core/v1/**`), and it
administers sandbox nodes through `/core/v1/sandbox/**`. It sends no request to
the Agents API (`/v1/**`). Routes, response shapes, pagination and audit records of
the administrator API are defined by the [administrator API contract](../../contracts/agents-api/admin-api.md).

## Interfaces

| Interface | Paths | Authentication | Console use |
| --- | --- | --- | --- |
| Console server | `/console/auth`, `/console/auth/{login,logout}`, `/console/config` | Core key at sign-in, then the console session cookie | Sign-in with the Core key and sign-out; the node installer (`node_installer`, `node_installer_sha256`) and the providers whose node assets it holds (`node_artifacts`) |
| Administrator API | `/core/v1/**` outside `/core/v1/sandbox` | Core key, added by the console server | Projects, keys, resource reads and deletion, executor credentials, provenance, summaries, Core metrics, the installation |
| Sandbox administration | `/core/v1/sandbox/**` | Core key, added by the console server | Nodes page; fleet and capacity figures on Overview and Sandbox metrics; Runtime observations of every project |
| Agents API | `/v1/**` | Project API key | Not used. Wherever a new key is shown, and without any key on an active project's page, the console gives shell exports of `OPENAI_BASE_URL` (the installation's `api_base_url`) and `OPENAI_API_KEY` (the new key, or a placeholder for a key of the project) with `curl` and Python examples for `GET /v1/agents` and `POST /v1/agents/sessions`, and sends none of them; when the installation is `local_only` it says the API is reachable only on the Core machine, and without an `api_base_url` it says to set `public_url` |

Browser requests are same-origin and carry only the console session. The browser
sends the Core key once, in the sign-in request body, and never stores it; it never
holds or sends an API key or an `OpenAI-Beta` header. Responses are validated: a malformed value is reported as a failure, or
marked as unrecognised where noted below, and never replaced by a guessed or zero
value.

## Projects and keys

| Operation | Route | Console use |
| --- | --- | --- |
| List projects | `GET /core/v1/projects` | Project filter on every project-scoped page; Projects and keys list; the Overview's Getting started (a project with an active key, and the newest active project, preferring one with an active key, whose call samples the first-Session step opens); `active_key_count` in the archive confirmation, which counts more when the project's key list shows more |
| Create project | `POST /core/v1/projects` | **Create project**, also from Getting started |
| Rename project | `POST /core/v1/projects/{project_id}` | **Rename** on an active project; the ID stays the same |
| Archive project | `POST /core/v1/projects/{project_id}/archive` | **Archive**: revokes every key; the project's assets stay readable and deletable |
| List keys | `GET /core/v1/projects/{project_id}/keys` | Key table of a project: name, prefix, status, creation and revocation time; the active keys the archive confirmation counts |
| Issue key | `POST /core/v1/projects/{project_id}/keys` | **Issue key** on an active project, also from Getting started; the plaintext is shown once |
| Revoke key | `DELETE /core/v1/projects/{project_id}/keys/{key_id}` | **Revoke**, with a warning when it is the project's last active key |

Names are checked for length (projects 1–128 characters, keys 1–80) and control
characters before sending. An issued key's plaintext stays in component memory
until the administrator confirms it was saved and is never written to browser
storage, URLs or logs. There is no project deletion and no plaintext recovery.

## Project resources

Routes are relative to `/core/v1/projects/{project_id}` and return the same
objects as the corresponding public `/v1` operations, so the console applies the
public client's strict projections. Runtime observation and history exist only
here. Archived projects remain readable.

| Resource | Reads used | Deletion | Creator | Console surface |
| --- | --- | --- | --- | --- |
| Agents | `/agents`, `/agents/{agent_id}` | Agent | `agent` | Agents list with usage per Agent; Agent page with instructions, tools, the saved model provider (never its key), generation settings and metadata |
| Environment templates | `/environment-templates`, `/environment-templates/{id}` | Template | `environment_template` | Templates list; Template page with every safe section |
| Skills | `/skills`, `/skills/{skill_id}`, `/skills/{skill_id}/versions`, Skill and version `/content` | Skill and Skill version | `skill` (list) | Skills list; Skill page with versions and archive downloads |
| Files | `/files` | File | `file` | Files list (metadata only) |
| Vaults | `/vaults`, `/vaults/{vault_id}`, `/vaults/{vault_id}/credentials` | Vault and Credential | `vault`, `credential` | Vaults list; Vault page with Credential metadata |
| Sessions | `/sessions`, `/sessions/{session_id}`, `/sessions/{session_id}/items`, `/sessions/{session_id}/turns`, `/sessions/{session_id}/runtime-observation`, `/sessions/{session_id}/runtime-history` | Session | `session` | Session log; Session page; Agent metrics; hosted Runtime rows |

Resource-specific boundaries:

- **Environment templates.** `env` and setup commands are write-only and never
  returned, so the console cannot tell whether a Template has them. Inline files
  report only their size. A Template with a section or field the client does not
  recognise is marked; its recognised sections are still shown and nothing else is
  guessed.
- **Skills.** A version upload, a default-pointer change and every other Skill write
  belong to the project's keys. The console downloads the default or an exact
  version as a ZIP, deletes versions (the default version is blocked while others
  remain; deleting the only version deletes the Skill) and deletes a Skill after
  its name is typed.
- **Files.** The list is read 100 per page, newest or oldest first. The
  administrator API has no File content route, so the console offers no download.
- **Vaults.** Credential tokens are never returned. The console shows each
  Credential's name, MCP server URL, authentication type and update time.
- **Sessions.** A malformed Session fails the read of its project instead of being
  skipped. The console does not read single Turns, Artifacts, execution
  configuration or Environment resources.

## Executor credentials

The credential operations below also serve the native daemon on Linux, macOS and
Windows. The existing **Connect a host** download command is the Linux container
installer; use the [native installation guide](../self-hosted-native.md) for
`oac-daemon install` and lifecycle commands. Native credential rotation replaces
the configured credential file and restarts the daemon, without rerunning install.

Core issues the credentials of a self_hosted executor, and the console is
where the administrator does it: the **Executor credentials** section of a
Session page, shown only when the Session's environment is `self_hosted`, for
that Session's `project_id` and `environment.id`. The routes accept only the
`self_hosted` environment of an existing (not deleted) Session in that project;
anything else returns 404. Writes have two conflicts, both 409:
`project_archived` (issuing or rotating in an archived project) and
`executor_credential_exists` (issuing an existing `key_id` without
`rotate: true`). In an archived project the section hides **Issue credential**
and **Rotate** behind a note and keeps the list and **Revoke**, which Core still
allows.

| Operation | Route | Console use |
| --- | --- | --- |
| List credentials | `GET /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | The section's table, through the query cache: each credential's short `key_id` with its copy button, creation time (`created_at`, which rotation does not change) and status (Active, or Revoked with its time), active first. The credential itself is never listed |
| Issue or rotate | `POST /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` with `{"key_id", "rotate"}` | Issue retains the generated key ID before submission. Rotation confirms that the old credential stops working. The private JSON is shown once for download or copy, never stored in browser storage or the query cache, and forgotten on Done. Save it as the credential file. Rotation keeps the same key ID: stop the daemon, replace that file, then start again. An existing key without rotation returns 409 `executor_credential_exists` |
| Revoke | `DELETE /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials/{key_id}` | **Revoke**, confirmed (the executor disconnects and won't retry; its daemon remains parked until the operator stops it), then the list is read again and shows the credential as Revoked; revoking again returns 204 |

**Connect a host** offers Linux/macOS or PowerShell commands for the native
installer, prefilled with the Session's Environment ID, remote URL and workspace.
It links the native installation guide rather than inventing a release URL.
The command contains no credential: download the one-time JSON and supply its
absolute path to the installer. `wss` and loopback `ws` are accepted; missing or
invalid connection facts suppress the command. Installation does not start the
daemon; use the installed `bin/oac-daemon start` afterward.

## Provenance and monitoring

| Operation | Route | Console use |
| --- | --- | --- |
| Resource owners | `GET /core/v1/projects/{project_id}/resource-owners` | The Creator column of every resource list and the creator fact of detail pages, in batches of up to 100 IDs. An asset an administrator copied in an earlier release shows **Admin copy**; a resource without a record shows **Unknown** |
| Write operations | `GET /core/v1/projects/{project_id}/write-operations` | A project's write history, newest first, filtered by key and resource type, 50 per page |
| Summary | `GET /core/v1/summary` | Overview (per project), the Agents list (`group_by=agent`), a project's page (per project and `group_by=key`), Agent metrics (to skip idle projects, and usage by creating key since the start of the range), the Projects list (last activity) |
| Installation | `GET /core/v1/installation` | System's Installation facts (`public_url`, `api_base_url`, `installation_id`, `source_commit`) and read-only Startup settings (`configuration.settings` under its `path`, `apply_command` and `applied_at`; a sensitive setting shows only whether it is `configured`); `api_base_url` in the how-to-call samples under a new key and on an active project's page; `public_url` as the download origin and `--source-url` of the node install and uninstall commands (and the install command's `--core-url`) (the reverse proxy sends `<public_url>/node-install/*` to the console); `local_only`, or a `public_url` that is not an HTTPS origin, stops Add node from issuing a command and Clean up the host from giving one; `path` and `apply_command` beside a sandbox configuration Core rejected. Overview, Nodes and System show a visible `local_only` warning with those repair instructions as copyable values; when `configuration` is null, they state that the path and command are unavailable. Nodes disables Add node with a visible reason, and Getting started leaves its sandbox step to do. A sensitive setting with a value, or an unknown member, fails the read; `configuration: null` shows a note |
| Core metrics | `GET /core/v1/metrics?range=` | Core metrics page; the Core popover on Overview. A Core without the route (404) is shown as not reporting; the popover then shows only Core's status. Measurements are defined in the [Core metrics contract](../../contracts/agents-api/core-metrics.md); the Process section's CPU and resident memory are a [requested extension](core-process-metrics-requirements.md) and show as missing until Core reports them |

Summary figures are cumulative per Session and are not billing records. Sessions
without reported usage count toward coverage but not toward token sums, and the
console shows missing values as missing, never as zero. The administrator audit
log (`GET /core/v1/audit-log`) is not consumed; System shows the installation,
each harness's default model and the sandbox deployment.

## Default models

| Operation | Route | Console use |
| --- | --- | --- |
| List harnesses | `GET /core/v1/harnesses` | System's Default model cards (each harness's read-only `enabled` and `default`, and its `model_provider` view without the key); the Overview's Getting started (a default model on the default harness, or on any enabled harness when none is default) |
| Set or replace | `PUT /core/v1/harnesses/{harness}/model-provider` | **Set** or **Replace**: the complete provider with the write-only key, never prefilled and never retried; a 400 shows Core's message in the form, and a 503 `credential_storage_unavailable` says Core has no credential encryption key; then the list is read again |
| Clear | `DELETE /core/v1/harnesses/{harness}/model-provider` | **Clear**, confirmed, then the list is read again |

The single-provider read (`GET /core/v1/harnesses/{harness}/model-provider`) is not
consumed; the list carries each provider.

## Sandbox administration

| Operation | Route | Console use |
| --- | --- | --- |
| Deployment | `GET`, `POST`, `PUT /core/v1/sandbox/deployment` | Read the provider, the read-only `core_url` (config.json's `public_url`, shown in the setup review and never sent), reset state, installation ID and specification; a 409 `sandbox_configuration_error` (E2B with a loopback `public_url`) shows the shared client's fixed safe address-configuration message in the setup wizard, with the installation's config file and apply command, and leaves nothing to confirm; initialize the deployment with `resources` and the Docker or microsandbox `runtime` release, or with the E2B account and no `resources` (Core adopts the template build's CPU and memory); change its settings with the expected generation. E2B's `e2b.template_build` (status, CPU, memory, disk) shows on System, the Sandbox backend summary and Sandbox metrics, and sizes each sandbox when `specification.resources` is missing; microsandbox's `suspension` (idle and retention seconds) shows on System and the Nodes summary |
| Reset | `POST/DELETE /core/v1/sandbox/deployment/reset` | Explicitly clear hosted resources or cancel the remaining clear at the observed generation; consume Core’s remaining/offline projection |
| Nodes | `GET /core/v1/sandbox/nodes` | Nodes page; fleet on Overview; node capacity on Sandbox metrics. An online node's `diagnostic` (`docker_unavailable`, `docker_limits_unsupported`, `runtime_image_unavailable`, `kvm_unavailable`, `microsandbox_artifacts_unavailable`, `capacity_insufficient`, `provider_unavailable`; any other value reads as `provider_unavailable`) marks it degraded and names the reason and fix in the help tip beside its status on each of these and on the node's page. A node whose `core_url` (the address it enrolled with) differs from the deployment's `core_url` is named on the Nodes page as bound to an old address, to be removed and added again, and its status there and on its page reads Old address instead of its health; an empty `core_url` (a node Core did not enroll) is unknown, not old. **Add node** follows only the node whose `enrollment_id` equals its command's; a node enrolled before Core recorded it reports null and never matches |
| Node detail | `GET /core/v1/sandbox/nodes/{node_id}?range=1h\|6h\|24h` | Sandbox metrics node dialog: the host's CPU busy share and memory from its last heartbeat, and their history over the page's range. **Edit node** reads `host.effective_cpu_cores` and `host.total_memory_bytes` to show the host beside each sandbox's size and at most how many of those fit |
| Allocations | `GET /core/v1/sandbox/nodes/{node_id}/allocations` | Nodes page; Sandbox metrics. Under microsandbox, a node's page shows from `compute_phase_changed_at` how long each allocation has been in its compute phase and, while suspended, about when Core reclaims it (that time plus the deployment's `suspension.retention_seconds`); a null time shows a dash |
| Enrollment | `POST /core/v1/sandbox/enrollment-tokens` | **Add node**: the administrator sets the node's sandbox limits (`max_active`; `max_retained` only for microsandbox, equal to `max_active` for Docker) before Core issues a single-use token inside a command that verifies the installer checksum, with the command's `enrollment_id`, which the node it registers reports. The command runs the installer with sudo (a system service) and passes the token on standard input; root runs it directly. No ordinary-user installation or removal entry is exposed, and the log hint always names the system service. The command downloads the installer from the installation's `public_url`. Until the installation is read, when it can't be read, when it is `local_only` (or its `public_url` is not an HTTPS origin), or when `/console/config` lists `node_artifacts` without the deployment's provider (null reads as none; an absent field blocks nothing), no token is requested; Nodes disables Add node when the installation reports `local_only`, and the dialog retains its guards for pending or failed reads and the other blockers. It reads both again on opening and when the window regains focus |
| Update node | `PATCH /core/v1/sandbox/nodes/{node_id}` | **Edit node**: the name and sandbox limits together (the retained limit only for microsandbox; under Docker, Core sets it to the active limit) |
| Remove node | `DELETE /core/v1/sandbox/nodes/{node_id}` | Confirmed node removal; the row goes only after Core acknowledges the deletion, and a Clean up the host dialog then gives the host's uninstall command (requiring root or sudo; for a node enrolled with another address than the deployment's, also with `--force`, which skips the installer's confirmation with Core) |
| Runtime observations | `GET /core/v1/sandbox/runtime-observations` | Sandbox metrics: hosted Runtimes of every project, each labelled with its project; an E2B sandbox's dialog adds its `observation.disk` as used / limit (null elsewhere) |

Signing in grants administration, so `/console/config` reports only the node
installer (`node_installer`, `node_installer_sha256`) and the providers
whose node assets the console holds (`node_artifacts`). These pages appear unless
the console has no `/console/config` (404) or reports `sandbox_admin: false`. An E2B
deployment has no nodes; its API key is write-only. The Runtime release sent for
Docker and microsandbox comes from the console's own `GET /node-install/manifest.json`
(the distribution manifest the node installer uses); without it the administrator
enters the release under advanced settings.

## Writes

- Deletion uses the administrator API with the same preconditions as the public
  delete operation. Every deletion is confirmed. A 4xx keeps the dialog open with
  Core's reason, a 404 counts as already deleted, and any other failure is reported
  as uncertain and followed by a fresh read.
- The console offers Session deletion only for idle or failed Sessions without
  required actions and never cancels work to make a Session deletable.
- Project, key, executor credential, deletion and sandbox writes are never
  retried automatically. An executor credential issuance with an unknown
  outcome (no answer, a 30-second timeout, a 5xx) opens an error dialog whose
  next step is **Refresh list**. If the kept `key_id` is then listed, it was
  issued and its secret lost: the console offers to rotate it (`rotate: true`)
  for a fresh secret, shown once. If it is not listed, the next Issue sends the
  same `key_id` with `rotate: false`; should that return 409 because the first
  request was issued after all, the console reads the list again and offers the
  same rotation only if the credential is listed as active in an active project,
  and otherwise reports the issuance as rejected. A kept `key_id` that is
  already listed is never sent again, and rotating or revoking it from its row
  forgets it: the next Issue generates a new `key_id`.

## Read bounds

| Page | Reads | Bound |
| --- | --- | --- |
| Resource lists | Every page of the selected project, or of every project in parallel | 10,000 entries per project; a failed project is named and the rest still show |
| Session log | Every Session page of the selected projects, newest first | 10,000 per project; refreshed on request |
| Session page | Session, Items and Turns | 10,000 Items and Turns; polled every 5 s while the Session is in progress or waiting, backing off to 60 s on failures |
| Overview | Summary; Session lists for the 24-hour activity and the Sessions needing attention | 1,000 Sessions per project; idle projects are skipped; refreshed every 30 s while visible. Failed project or Session reads show Retry instead of a synthesized empty result; any retained or partial data is visibly qualified |
| Agent metrics | Summary; Session lists; Turns and Items of the most recently active Sessions | 2,000 Sessions listed per project; 200 Sessions read per load, 10 Turn and 5 Item pages each, 15 s per Session and 45 s per load |
| Sandbox metrics | Nodes and allocations; Runtime observations; hosted Sessions by ID; Runtime history | 100 hosted Sessions read per refresh; history for at most 24; refreshed every 30 s while visible |

The aggregate endpoints that would replace these browser reads are proposed in
[Administrator metrics: backend requirements](admin-metrics-backend-requirements.md).

## Not consumed

- Any `/v1/**` route, including Session creation, Session events and their SSE
  stream, message input, function results and cancellation.
- Creation or update of Agents, Environment templates, Skills, Files, Vaults or
  Credentials, including uploads and Credential token replacement.
- Environment resources, Environment Files and Artifacts.

## Terminology

- **OpenAI Agents API** is the managed-harness API described in the official
  [Agents guide](https://developers.openai.com/api/docs/guides/agents). OpenAgentCore
  implements part of its pinned beta resource shape under `/v1`.
- **Administrator API** (also called the Web API) is OpenAgentCore's management
  extension under `/core/v1`. It is not part of the public Agents API.
- **OpenAI Agents SDK** and **Responses API** are different interfaces and are not
  used by the console.

Any change to a consumed route, field, error or bound must update this matrix, the
client tests and the console's fixtures in the same change.

## Evidence and changes

The console's `/core/v1/*` prefix forwarding lives in
[core_routes.go](../../services/core-console/core_routes.go); authentication, origin
and path checks and header handling live in [server.go](../../services/core-console/server.go),
with Core key sign-in in [auth.go](../../services/core-console/auth.go).
Update this matrix when those boundaries or the console's reads change, and keep
detailed wire semantics in the administrator contract.

Backend HTTP tests, console login and proxy checks and Project isolation
acceptance establish backend behavior. The console's unit tests and fixture-backed
browser tests cover its screens; they do not prove execution readiness.
