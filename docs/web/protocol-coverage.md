# Protocol coverage

This matrix records which Core interfaces the Parsar Core console (`apps/web`)
consumes and for what. It is not a statement of public Agents API compatibility;
that inventory, its pinned baseline and its evidence live in the
[Agents API contract](../../contracts/agents-api/README.md).

The console is a management tool. It reads and deletes each project's assets and
manages projects and keys through the administrator API (`/core/v1/admin/**`), and
it administers sandbox nodes through `/core/v1/sandbox/**`. It sends no request to
the Agents API (`/v1/**`). Routes, response shapes, pagination and audit records of
the administrator API are defined by the [administrator API contract](../../contracts/agents-api/admin-api.md).

## Interfaces

| Interface | Paths | Authentication | Console use |
| --- | --- | --- | --- |
| Console server | `/console/auth`, `/console/auth/{setup,login,logout}`, `/console/config` | Console account (session cookie) or legacy Basic authentication | Sign-in and sign-out; non-secret capability flags such as `sandbox_admin` |
| Administrator API | `/core/v1/admin/**` | Deployment administrator credential, added by the console server | Projects, keys, resource reads and deletion, provenance, summaries, Runtime observations |
| Sandbox administration | `/core/v1/sandbox/**` | Deployment administrator credential, added by the console server | Nodes page; fleet and capacity figures on Overview and Sandbox metrics |
| Agents API | `/v1/**` | Project API key | Not used. The first-run screen shows a `curl` example for `/v1/agents` with a `$PROJECT_API_KEY` placeholder; the console never sends it |

Browser requests are same-origin and carry only the console sign-in. The browser
never holds or sends the deployment credential, an API key or an `OpenAI-Beta`
header. Responses are validated: a malformed value is reported as a failure, or
marked as unrecognised where noted below, and never replaced by a guessed or zero
value.

## Projects and keys

| Operation | Route | Console use |
| --- | --- | --- |
| List projects | `GET /projects` | Project filter on every project-scoped page; Projects and keys list; first-run detection (no project opens the first-run screen) |
| Create project | `POST /projects` | **Create project**; first run (default name `Default`) |
| Rename project | `POST /projects/{project_id}` | **Rename** on an active project; the ID stays the same |
| Archive project | `POST /projects/{project_id}/archive` | **Archive**: revokes every key; the project's assets stay readable and deletable |
| List keys | `GET /projects/{project_id}/keys` | Key table of a project: name, prefix, status, creation and revocation time |
| Issue key | `POST /projects/{project_id}/keys` | **Issue key** on an active project and the first-run screen; the plaintext is shown once |
| Revoke key | `DELETE /projects/{project_id}/keys/{key_id}` | **Revoke**, with a warning when it is the project's last active key |

Names are checked for length (projects 1–128 characters, keys 1–80) and control
characters before sending. An issued key's plaintext stays in component memory
until the administrator confirms it was saved and is never written to browser
storage, URLs or logs. There is no project deletion and no plaintext recovery.

## Project resources

Routes are relative to `/core/v1/admin/projects/{project_id}` and return the same
objects as the corresponding public `/v1` operations, so the console applies the
public client's strict projections. Archived projects remain readable.

| Resource | Reads used | Deletion | Creator | Console surface |
| --- | --- | --- | --- | --- |
| Agents | `/agents`, `/agents/{agent_id}` | Agent | `agent` | Agents list with usage per Agent; Agent page with instructions, tools, generation settings and metadata |
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

## Provenance and monitoring

| Operation | Route | Console use |
| --- | --- | --- |
| Resource owners | `GET /projects/{project_id}/resource-owners` | The Creator column of every resource list and the creator fact of detail pages, in batches of up to 100 IDs. An asset an administrator copied in an earlier release shows **Admin copy**; a resource without a record shows **Unknown** |
| Write operations | `GET /projects/{project_id}/write-operations` | A project's write history, newest first, filtered by key and resource type, 50 per page |
| Summary | `GET /summary` | Overview (per project), the Agents list (`group_by=agent`), a project's page (per project and `group_by=key`), Agent metrics (to skip idle projects, and usage by creating key since the start of the range), the Projects list (last activity) |
| Runtime observations | `GET /runtime-observations` | Sandbox metrics: hosted Runtimes of every project, each labelled with its project |
| Core metrics | `GET /core-metrics?range=` | Core metrics page; the Core popover on Overview. A Core without the route (404) is shown as not reporting; the popover then shows only Core's status. Measurements are defined in the [Core metrics contract](../../contracts/agents-api/core-metrics.md); the Process section's CPU and resident memory are a [requested extension](core-process-metrics-requirements.md) and show as missing until Core reports them |

Summary figures are cumulative per Session and are not billing records. Sessions
without reported usage count toward coverage but not toward token sums, and the
console shows missing values as missing, never as zero. The administrator audit
log (`GET /audit-log`), Runtime history capabilities
(`GET /runtime-history/capabilities`) and startup configuration
(`GET /startup-configuration`) are not consumed; System shows the sandbox
deployment only.

## Sandbox administration

| Operation | Route | Console use |
| --- | --- | --- |
| Deployment | `GET`, `POST`, `PUT /core/v1/sandbox/deployment` | Read the provider, Core origin, maintenance state, installation ID and specification; initialize the deployment with `resources`, the Docker or microsandbox `runtime` release, or the E2B account; change its settings with the expected generation |
| Maintenance | `PATCH /core/v1/sandbox/deployment/maintenance` | Enter or leave maintenance to change the provider |
| Nodes | `GET /core/v1/sandbox/nodes` | Nodes page; fleet on Overview; node capacity on Sandbox metrics |
| Node detail | `GET /core/v1/sandbox/nodes/{node_id}?range=1h\|6h\|24h` | Sandbox metrics node dialog: the host's CPU busy share and memory from its last heartbeat, and their history over the page's range |
| Allocations | `GET /core/v1/sandbox/nodes/{node_id}/allocations` | Nodes page; Sandbox metrics |
| Enrollment | `POST /core/v1/sandbox/enrollment-tokens` | **Add node**: a single-use token inside a command that verifies the installer checksum |
| Update node | `PATCH /core/v1/sandbox/nodes/{node_id}` | **Edit node**: the name and sandbox limits together (the retained limit only for microsandbox; Docker keeps its saved one, raised to at least the active limit) |
| Remove node | `DELETE /core/v1/sandbox/nodes/{node_id}` | Confirmed node removal; the row goes only after Core acknowledges the deletion |

These pages appear only when `/console/config` reports `sandbox_admin: true`. An E2B
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
- Project, key, deletion and sandbox writes are never retried automatically.

## Read bounds

| Page | Reads | Bound |
| --- | --- | --- |
| Resource lists | Every page of the selected project, or of every project in parallel | 10,000 entries per project; a failed project is named and the rest still show |
| Session log | Every Session page of the selected projects, newest first | 10,000 per project; refreshed on request |
| Session page | Session, Items and Turns | 10,000 Items and Turns; polled every 5 s while the Session is in progress or waiting, backing off to 60 s on failures |
| Overview | Summary; Session lists for the 24-hour activity and the Sessions needing attention | 1,000 Sessions per project; idle projects are skipped; refreshed every 30 s while visible |
| Agent metrics | Summary; Session lists; Turns and Items of the most recently active Sessions | 2,000 Sessions listed per project; 200 Sessions read per load, 10 Turn and 5 Item pages each, 15 s per Session and 45 s per load |
| Sandbox metrics | Nodes and allocations; Runtime observations; hosted Sessions by ID; Runtime history | 100 hosted Sessions read per refresh; history for at most 24; refreshed every 30 s while visible |

The aggregate endpoints that would replace these browser reads are proposed in
[Administrator metrics: backend requirements](admin-metrics-backend-requirements.md).

## Not consumed

- Any `/v1/**` route, including Session creation, Session events and their SSE
  stream, message input, function results and cancellation.
- Creation or update of Agents, Environment templates, Skills, Files, Vaults or
  Credentials, including uploads and Credential token replacement.
- Environment resources, Environment Files, executor credentials and Artifacts.

## Terminology

- **OpenAI Agents API** is the managed-harness API described in the official
  [Agents guide](https://developers.openai.com/api/docs/guides/agents). Parsar Core
  implements part of its pinned beta resource shape under `/v1`.
- **Administrator API** (also called the Web API) is Parsar Core's management
  extension under `/core/v1/admin`. It is not part of the public Agents API.
- **OpenAI Agents SDK** and **Responses API** are different interfaces and are not
  used by the console.

Any change to a consumed route, field, error or bound must update this matrix, the
client tests and the console's fixtures in the same change.

## Evidence and changes

The console's route allowlists live in
[admin_routes.go](../../services/core-console/admin_routes.go) and
[sandbox_admin.go](../../services/core-console/sandbox_admin.go); authentication,
origin checks and header handling live in [server.go](../../services/core-console/server.go).
Update this matrix when those boundaries or the console's reads change, and keep
detailed wire semantics in the administrator contract.

Backend HTTP tests, console login and proxy checks and Project isolation
acceptance establish backend behavior. The console's unit tests and fixture-backed
browser tests cover its screens; they do not prove execution readiness.
