# Console API usage

This page lists the Core routes each console page reads and writes, and how the console bounds its reads. The [administrator API contract](../../contracts/agents-api/admin-api.md) defines the routes, response shapes, pagination and audit records; [API namespaces and credentials](../api/README.md) defines the terms used here.

## Interfaces

| Interface | Paths | Authentication | Console use |
| --- | --- | --- | --- |
| Console server | `/console/auth`, `/console/auth/{login,logout}`, `/console/config`, `/console/installation/domain`, `/node-install/manifest.json` | The Core key at sign-in, then the console session cookie; `/node-install/manifest.json` needs no sign-in | Sign-in and sign-out; the node installer and node artifacts for Add node; domain setup on **System → Domain and HTTPS**; the distribution's Runtime release for Docker and microsandbox setup. See [console server](console-server.md) |
| Administrator API | `/core/v1/**` outside `/core/v1/sandbox` | The Core key, added by the console server | Projects, keys, resource reads and deletion, diagnostics, executor credentials and installation commands, provenance, summaries, Core metrics, the installation, default models |
| Sandbox administration | `/core/v1/sandbox/**` | The Core key, added by the console server | Sandbox configuration, Nodes, fleet and capacity figures on Overview and Sandbox metrics, Runtime observations of every project |
| Agents API | `/v1/**` | Project API key | Not used. The console shows developers how to call it (see [Provenance and monitoring](#provenance-and-monitoring)) |

Browser requests are same-origin and carry only the console session cookie. The browser sends the Core key once, in the sign-in request body, and never stores it; it never holds or sends an API key or an `OpenAI-Beta` header. The console reads through `AdminClient`, `SandboxAdminClient` and `CoreMetricsClient` from [`packages/agents-client`](../../packages/agents-client/README.md), which validate every response: a malformed value is reported as a failure, or marked as unrecognised where noted below, and never replaced by a guessed or zero value.

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

Names are checked for length (projects 1–128 characters, keys 1–80) and control characters before sending. An issued key's plaintext stays in component memory until the administrator confirms it was saved and is never written to browser storage, URLs or logs. There is no project deletion and no plaintext recovery.

## Project resources

Routes are relative to `/core/v1/projects/{project_id}` and return the same objects as the corresponding public `/v1` operations, so the console applies the public client's strict projections. Archived projects remain readable.

| Resource | Reads used | Deletion | Creator | Console surface |
| --- | --- | --- | --- | --- |
| Agents | `/agents`, `/agents/{agent_id}` | Agent | `agent` | Agents list with usage per Agent; Agent page with instructions, tools, the saved model provider (never its key), generation settings and metadata |
| Environment templates | `/environment-templates`, `/environment-templates/{id}` | Template | `environment_template` | Templates list; Template page with every safe section |
| Skills | `/skills`, `/skills/{skill_id}`, `/skills/{skill_id}/versions`, Skill and version `/content` | Skill and Skill version | `skill` (list) | Skills list; Skill page with versions and archive downloads |
| Files | `/files` | File | `file` | Files list (metadata only) |
| Vaults | `/vaults`, `/vaults/{vault_id}`, `/vaults/{vault_id}/credentials` | Vault and Credential | `vault`, `credential` | Vaults list; Vault page with Credential metadata |
| Sessions | `/sessions`, `/sessions/{session_id}`, `/sessions/{session_id}/items`, `/sessions/{session_id}/turns`, `/sessions/{session_id}/runtime-observation`, `/sessions/{session_id}/runtime-history` | Session | `session` | Session log; Session page; Agent metrics; hosted Runtime rows |
| Diagnostics | `/sessions/{session_id}/diagnostics`, `/sessions/{session_id}/turns/{turn_id}/diagnostics` | — | — | The classified reason under a failed Session's or Turn's status on Overview, the Session log and the Session page; Core's receipt time of each Item in the trace |

Resource-specific rules:

- **Environment templates.** `env` and setup commands are write-only and never returned, so the console cannot tell whether a Template has them. Inline files report only their size. A Template with a section or field the client does not recognise is marked; its recognised sections are still shown and nothing else is guessed.
- **Skills.** A version upload, a default-pointer change and every other Skill write belong to the project's keys. The console downloads the default or an exact version as a ZIP, deletes versions (the default version is blocked while others remain; deleting the only version deletes the Skill) and deletes a Skill after its name is typed.
- **Files.** The list is read 100 per page, newest or oldest first. The administrator API has no File content route, so the console offers no download.
- **Vaults.** Credential tokens are never returned. The console shows each Credential's name, MCP server URL, authentication type and update time.
- **Sessions.** A malformed Session fails the read of its project instead of being skipped.
- **Diagnostics.** The console translates Core's classified reason and never infers a cause from raw logs. An unavailable or mismatched diagnostic offers an explicit read retry; a retry never replays execution.

## Executor credentials and host connection

The **Executor credentials** section of a Session page appears only when the Session's environment is `self_hosted`, for that Session's `project_id` and `environment.id`. The [executor credential contract](../../contracts/agents-api/environment-executor-credentials.md) defines the routes, their 404 and 409 responses and the credential file.

| Operation | Route | Console use |
| --- | --- | --- |
| List credentials | `GET /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | Read every 5 seconds while visible. The table shows each credential's short `key_id` with its copy button, creation time (`created_at`, which rotation does not change) and status (Active, or Revoked with its time), active first; the credential itself is never listed. The same read's `connection` observation drives the host connection panel: never connected, connected, disconnected, bound credential revoked, and unknown stay distinct, and only a fresh connected read marks the host connected |
| Issue or rotate | `POST …/executor-credentials` with `{"key_id", "rotate"}` | Issue keeps the generated key ID before submission. Rotation confirms that the old credential stops working; rotating a revoked credential restores it with a new secret. The private JSON is shown once for download or copy, never stored in browser storage or the query cache, and forgotten on Done |
| Revoke | `DELETE …/executor-credentials/{key_id}` | **Revoke**, confirmed (the executor disconnects and does not retry; its daemon stays parked until the operator stops it), then the list is read again and shows the credential as Revoked |
| Installation commands | `GET /core/v1/projects/{project_id}/environments/{environment_id}/installation` | **Connect a host**: Core's short-lived Linux/macOS and PowerShell commands, shown as Core returned them with a platform selector and a link to the [native installation guide](../getting-started/self-hosted.md). The commands install the daemon and its Harnesses, start it and check its connection; their authorization expires after 30 minutes, and the console reads them again every 20 minutes. Without an available, unexpired answer the section says the command is unavailable. Archived projects do not read it |

In an archived project the section hides **Issue credential** and **Rotate** behind a note and keeps the list and **Revoke**, which Core still allows.

## Provenance and monitoring

| Operation | Route | Console use |
| --- | --- | --- |
| Resource owners | `GET /core/v1/projects/{project_id}/resource-owners` | The Creator column of every resource list and the creator fact of detail pages, in batches of up to 100 IDs: the creating key's name, **Admin copy** for an owner with source `admin_copy`, or **Unknown** when Core has no record |
| Write operations | `GET /core/v1/projects/{project_id}/write-operations` | A project's write history, newest first, filtered by key and resource type, 50 per page |
| Summary | `GET /core/v1/summary` | Overview (per project), the Agents list (`group_by=agent`), a project's page (per project and `group_by=key`), Agent metrics (to skip idle projects, and usage by creating key since the start of the range), the Projects list (last activity) |
| Installation | `GET /core/v1/installation` | System's Installation facts (`public_url`, `api_base_url`, `installation_id`, `source_commit`) and read-only Startup settings (`configuration.settings` under its `path`, `apply_command` and `applied_at`; a sensitive setting shows only whether it is `configured`); `api_base_url` in the call samples; `public_url` as the download origin and `--source-url` of the node install and uninstall commands (and the install command's `--core-url`); `path` and `apply_command` beside a sandbox configuration Core rejected. A sensitive setting with a value, or an unknown member, fails the read; `configuration: null` shows a note |
| Core metrics | `GET /core/v1/metrics?range=` | Core metrics page; the Core popover on Overview. A Core without the route (404) is shown as not reporting, and the popover then shows only Core's status. The [Core metrics contract](../../contracts/agents-api/core-metrics.md) defines every measurement |

`local_only`, or a `public_url` that is not an HTTPS origin, stops Add node from issuing a command and Clean up the host from giving one. Overview, Nodes and System then show a visible warning with Core's configuration path and apply command as copyable values; when `configuration` is null, they state that the path and command are unavailable. Nodes disables Add node with a visible reason, and Getting started leaves its sandbox step to do.

Wherever a new key is shown, and without any key on an active project's page, the console gives shell exports of `OPENAI_BASE_URL` (the installation's `api_base_url`) and `OPENAI_API_KEY` (the new key, or a placeholder for a key of the project), with `curl` and Python examples for `GET /v1/agents` and `POST /v1/agents/sessions`, and sends none of them. When the installation is `local_only` it says the API is reachable only on the Core machine, and without an `api_base_url` it says to set `public_url`.

Summary figures are cumulative per Session and are not billing records. Sessions without reported usage count toward coverage but not toward token sums, and the console shows missing values as missing, never as zero.

## Default models

| Operation | Route | Console use |
| --- | --- | --- |
| List harnesses | `GET /core/v1/harnesses` | System's Default model cards: each harness's read-only `enabled` and `default`, its model configuration without the key, and Usage details from the configuration's `last_used_at`, `last_error_code` and `last_error_at`; the Overview's Getting started (a default model on the default harness, or on any enabled harness when none is default) |
| Set or replace | `PUT /core/v1/harnesses/{harness}/model-configuration` | **Set** or **Replace**: the complete model configuration with its write-only provider key, never prefilled and never retried; a 400 shows Core's message in the form, and a 503 `credential_storage_unavailable` says Core has no credential encryption key; then the list is read again |
| Clear | `DELETE /core/v1/harnesses/{harness}/model-configuration` | **Clear**, confirmed, then the list is read again |

The list carries each harness's configuration, so the console does not read `GET /core/v1/harnesses/{harness}/model-configuration`.

## Sandbox administration

| Operation | Route | Console use |
| --- | --- | --- |
| Deployment | `GET`, `POST`, `PUT /core/v1/sandbox/deployment` | Read the provider, the read-only `core_url` (config.json's `public_url`, shown in the setup review and never sent), reset state, installation ID and specification; a 409 `sandbox_configuration_error` (E2B with a loopback `public_url`) shows the shared client's fixed safe address-configuration message in the setup wizard, with the installation's config file and apply command, and leaves nothing to confirm; initialize the deployment with `resources` and the Docker or microsandbox `runtime` release, or with the E2B account and no `resources` (Core adopts the template build's CPU and memory); change its settings with the expected generation. E2B's `e2b.template_build` (status, CPU, memory, disk) shows on System, the Sandbox configuration summary and Sandbox metrics, and sizes each sandbox when `specification.resources` is missing; microsandbox's `suspension` (idle and retention seconds) shows on System and the Nodes summary |
| E2B discovery | `POST /core/v1/sandbox/e2b/templates`, `POST /core/v1/sandbox/e2b/templates/{template_id}/builds` | The setup wizard lists the templates the entered E2B key can see, then the selected template's ready builds. The key travels only in these request bodies and the deployment write |
| Reset | `POST`, `DELETE /core/v1/sandbox/deployment/reset` | Explicitly clear hosted resources, or cancel the remaining clear at the observed generation; show Core's remaining and offline projection |
| Nodes | `GET /core/v1/sandbox/nodes` | Nodes page; fleet on Overview; node capacity on Sandbox metrics. An online node's `diagnostic` (`docker_unavailable`, `docker_limits_unsupported`, `runtime_image_unavailable`, `kvm_unavailable`, `microsandbox_artifacts_unavailable`, `capacity_insufficient`, `provider_unavailable`; any other value reads as `provider_unavailable`) marks it degraded and names the reason and fix in the help tip beside its status on each of these and on the node's page. A node whose `core_url` (the address it enrolled with) differs from the deployment's `core_url` is named on the Nodes page as bound to an old address, to be removed and added again, and its status there and on its page reads Old address instead of its health; an empty `core_url` (a node Core did not enroll) is unknown, not old. **Add node** follows only the node whose `enrollment_id` equals its command's |
| Node detail | `GET /core/v1/sandbox/nodes/{node_id}?range=1h\|6h\|24h` | Sandbox metrics node dialog: the host's CPU busy share and memory from its last heartbeat, and their history over the page's range. **Edit node** reads `host.effective_cpu_cores` and `host.total_memory_bytes` to show the host beside each sandbox's size and at most how many of those fit |
| Allocations | `GET /core/v1/sandbox/nodes/{node_id}/allocations` | Nodes page; Sandbox metrics. Under microsandbox, a node's page shows from `compute_phase_changed_at` how long each allocation has been in its compute phase and, while suspended, about when Core reclaims it (that time plus the deployment's `suspension.retention_seconds`); a null time shows a dash |
| Enrollment | `POST /core/v1/sandbox/enrollment-tokens` | **Add node**: the administrator sets the node's sandbox limits (`max_active`; `max_retained` only for microsandbox, equal to `max_active` for Docker) before Core issues a single-use token inside a command that verifies the installer checksum, with the command's `enrollment_id`, which the node it registers reports. The command runs the installer with sudo (a system service) and passes the token on standard input; root runs it directly. No ordinary-user installation or removal entry is exposed, and the log hint always names the system service. The command downloads the installer from the installation's `public_url`. No token is requested until the installation is read, when it cannot be read, when it is `local_only` (or its `public_url` is not an HTTPS origin), or when `/console/config` lists `node_artifacts` without the deployment's provider. The dialog reads both again on opening and when the window regains focus |
| Update node | `PATCH /core/v1/sandbox/nodes/{node_id}` | **Edit node**: the name and sandbox limits together (the retained limit only for microsandbox; under Docker, Core sets it to the active limit) |
| Remove node | `DELETE /core/v1/sandbox/nodes/{node_id}` | Confirmed node removal; the row goes only after Core acknowledges the deletion, and a Clean up the host dialog then gives the host's uninstall command (requiring root or sudo; for a node enrolled with another address than the deployment's, also with `--force`, which skips the installer's confirmation with Core) |
| Runtime observations | `GET /core/v1/sandbox/runtime-observations` | Sandbox metrics: hosted Runtimes of every project, each labelled with its project; an E2B sandbox's dialog adds its `observation.disk` as used / limit (null elsewhere) |

An E2B deployment has no nodes; its API key is write-only. Overview and Sandbox metrics count its running and starting sandboxes from the deployment's `resources.allocations` and `resources.pending`, while the hosted Runtime rows come from Runtime observations. The two sources refresh independently, so the console does not infer retention or cleanup from their difference. The Runtime release sent for Docker and microsandbox comes from the console's own `GET /node-install/manifest.json`; without it the administrator enters the release under advanced settings.

## Writes

- Deletion uses the administrator API with the same preconditions as the public delete operation. Every deletion is confirmed. A 4xx keeps the dialog open with Core's reason, a 404 counts as already deleted, and any other failure is reported as uncertain and followed by a fresh read.
- The console offers Session deletion only for idle or failed Sessions without required actions and never cancels work to make a Session deletable.
- Project, key, executor credential, deletion and sandbox writes are sent once per explicit action and never retried automatically. An uncertain result stays visible until the administrator reads the state again and decides.
- An executor credential issuance with an unknown outcome (no answer, a 30-second timeout, a 5xx) opens an error dialog whose next step is **Refresh list**. If the kept `key_id` is then listed, it was issued and its secret lost: the console offers to rotate it (`rotate: true`) for a fresh secret, shown once. If it is not listed, the next Issue sends the same `key_id` with `rotate: false`; should that return 409 because the first request was issued after all, the console reads the list again and offers the same rotation only if the credential is listed as active in an active project, and otherwise reports the issuance as rejected. A kept `key_id` that is already listed is never sent again, and rotating or revoking it from its row forgets it: the next Issue generates a new `key_id`.

## Read bounds

The console assembles several figures in the browser from bounded reads of each project. Session history is read in pages and polled; there is no management event stream.

| Page | Reads | Bound |
| --- | --- | --- |
| Resource lists | Every page of the selected project, or of every project in parallel | 10,000 entries per project; a failed project is named and the rest still show |
| Session log | Every Session page of the selected projects, newest first | 10,000 per project; refreshed on request |
| Session page | Session, Items and Turns | 10,000 Items and Turns; polled every 5 s while the Session is in progress or waiting, backing off to 60 s on failures |
| Overview | Summary; Session lists for the 24-hour activity and the Sessions needing attention | 1,000 Sessions per project; idle projects are skipped; refreshed every 30 s while visible. Failed project or Session reads show Retry instead of a synthesized empty result; any retained or partial data is visibly qualified |
| Agent metrics | Summary; Session lists; Turns and Items of the most recently active Sessions | 2,000 Sessions listed per project; 200 Sessions read per load, 10 Turn and 5 Item pages each, 15 s per Session and 45 s per load |
| Sandbox metrics | Nodes and allocations; Runtime observations; hosted Sessions by ID; Runtime history | 100 hosted Sessions read per refresh; history for at most 24; refreshed every 30 s while visible |

Agent metrics has these limits:

- A request is one root Agent Turn; Subagent Turns and deleted Sessions are not counted. HTTP request counts, status codes and API latency are not available.
- The model of a request comes from the Session's Agent snapshot, not from the Session's execution configuration.
- Busy projects exceed the Session caps, so long ranges can be partial.
- Usage by API key counts Sessions created in the range by their creating key; Sessions without a creation record count as Unknown.
- Turn times up to 15 minutes after the end of the range are accepted, to allow for clock differences between the browser and Core.

Its help tips and coverage notes state what a request is and what is not counted, where the model comes from, which projects were cut short or Sessions skipped, and how usage by key is grouped. They do not mention HTTP request metrics or the clock allowance.

## Not consumed

- Any `/v1/**` route, including Session creation, Session events and their stream, message input, function results and cancellation.
- Creation or update of Agents, Environment templates, Skills, Files, Vaults or Credentials, including uploads and Credential token replacement.
- Single Turn reads, Artifacts, Session execution configuration, Environment Files and administrative Session archive.
- The administrator audit log (`GET /core/v1/audit-log`). System shows the installation, each harness's default model and the sandbox deployment instead.
