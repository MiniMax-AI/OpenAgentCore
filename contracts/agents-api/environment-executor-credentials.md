# Environment executor credentials

An executor credential lets `oac-daemon` enroll and connect for one `self_hosted` Environment. It authorizes only the private daemon transport (`/api/v1/agent-daemon/*`) for that Environment, never `/v1`, `/core/v1`, sandbox-node enrollment or Project resources. The Project's principal is its execution principal. Core stores only a digest of the secret.

A credential comes from one of two places:

- **The installation grant.** A `self_hosted` Session returns an install command. The installer uses the command's short-lived grant to claim one credential; it needs neither Web nor the Core key. The [self-hosted guide](../../docs/getting-started/self-hosted.md) shows the steps.
- **The Core-key routes.** An operator issues, rotates and revokes credentials through Web or `/core/v1`.

Core never creates, stops or reclaims the machine. Disconnecting, revoking a credential or deleting the Session does not prove that every native process has stopped; the machine's owner stops and cleans up its own compute.

## Installation grant

Session create, retrieve and update responses of a `self_hosted` Session carry `x_agents_core.installation`; Session lists do not. Web reads the same object with the Core key at `GET /core/v1/projects/{project_id}/environments/{environment_id}/installation` and shows its commands without changing them.

| Field | Meaning |
| --- | --- |
| `status` | `available`, or `unavailable` when this Core has no matching native installers; `message` then says so |
| `version` | The Core build the commands install |
| `expires_at` | Unix time when the grant expires, 30 minutes after the response |
| `commands.posix`, `commands.powershell` | The install command for Linux/macOS and for Windows PowerShell |

The grant is bound to the Environment, the Session creator's principal and the Core build. It stops working when it expires, when the Session is deleted, when the Project is archived or when Core runs a different build. Reading the Session again returns a fresh grant. Core stores no grant: each response signs a new one, and stored events never carry it. Treat the command as a temporary secret: it can claim the credential, but it cannot run work or read files.

The installer generates the secret and saves it privately as `daemon/executor-credential.json` in the installation directory before it claims the key. Core stores the digest under the key ID equal to the Environment ID. A lost response is safe to retry: the retry must present the same secret. A grant never replaces or restores a credential. If the Environment already has a different, rotated or revoked credential, the claim fails with 409 `executor_credential_exists`.

The installer calls these machine routes on Core:

| Route | Authorization | Purpose |
| --- | --- | --- |
| `GET /api/v1/agent-daemon/install/{version}/bootstrap.sh`, `bootstrap.ps1` | None | Platform bootstrap scripts |
| `GET /api/v1/agent-daemon/install/{version}/{os}-{arch}.sha256`, `{os}-{arch}.tar.gz` | None | Installer checksum and archive. Core serves a local copy, or redirects (307) to the versioned release URL in its catalog |
| `POST /api/v1/agent-daemon/installation` | Grant | The frozen binding: `version`, `protocol_version`, `environment_id`, `remote_url`, `workspace_directory`, `harness` |
| `POST /api/v1/agent-daemon/installation/claim` | Grant | `{"executor_token":"SECRET"}`; 204 |

An invalid or expired grant returns 401 `installation_authorization_invalid`. Without matching installers the grant routes return 503 `installation_unavailable`. Core signs each grant with the installation's [`secrets/credential.key`](../../docs/configuration.md#installation-directory); without a configured key, the Session responses and Core-key read above and the grant routes return 503 `credential_storage_unavailable`. A malformed secret returns 400. Artifact routes carry no credential, and the grant is sent only to Core, never to an artifact host.

## Core-key routes

All routes are under `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` and require the Core key. They apply only to a `self_hosted` Environment of that Project whose Session exists (is not deleted); any other Project, Environment type, missing Environment or deleted Session returns 404. Project API keys cannot use them.

| Operation | Request | Result |
| --- | --- | --- |
| List | `GET …/executor-credentials` | Credential metadata in `data`, plus the required `connection` object |
| Issue or rotate | `POST …/executor-credentials` with `{"key_id":"UUID","rotate":false}` | 201 credential file, returned once |
| Revoke | `DELETE …/executor-credentials/{key_id}` | 204 |

The list holds metadata only, oldest first, for the credentials restricted to this Environment; `revoked_at` is null while a credential is active. It never contains a secret.

`key_id` is a canonical nonzero UUID chosen and retained before the request. `rotate` is optional and defaults to false. The 201 response is the daemon credential-file format:

```json
{"key_id":"UUID","environment_id":"ENVIRONMENT_UUID","executor_token":"ONE_TIME_SECRET"}
```

Responses use `Cache-Control: no-store`. Save the response directly to an owned mode-0600 file; never place it in shell arguments, logs, a workspace or source.

Writes have two conflicts, both 409. `executor_credential_exists`: an issuance whose `key_id` already exists and does not set `rotate:true`, even after revocation. `project_archived`: the Project is archived, so it gets no new or rotated credential; listing and revocation remain available there, because revoking must always work.

An issuance or rotation is checked in this order, and the first failure is returned: the request body (400); the target Environment (404); an archived Project (409 `project_archived`); then the key itself (409 `executor_credential_exists` without `rotate`, or 404 when rotating a `key_id` that was never issued).

Rotation replaces the secret of an existing key restricted to this Environment, keeps that Environment, invalidates the previous secret at once and restores a revoked key. Revocation is idempotent and returns 204 each time. It denies further enrollment and connection.

After an uncertain result, such as a timeout, do not retry automatically. List the credentials, then either rotate the same `key_id` (it was issued but its secret was lost) or issue it again (it was not issued).

Issue, rotate and revoke each record an administrator audit entry (`resource_type:"executor_credential"`, the key ID as `resource_id`, action `issue`, `rotate` or `revoke`) in the same transaction as the write. The audit never contains the secret.

### Break-glass command

`oac-core-environment-key` issues, rotates or revokes a credential directly in the database when the Core API is unavailable. It reads Core's database settings (`OAC_DATABASE_URL`, and `OAC_DATABASE_PASSWORD_FILE` when set) and needs the Project's execution principal: `--tenant` (the Project's tenant UUID in the `projects` table), `--organization core`, `--project proj_<Project UUID>`, `--subject-kind service_account`, `--subject-id project:<Project UUID>` and `--key-id`. Without another flag it issues a new credential, and `--environment` restricts it to one Environment. `--rotate` replaces the secret of an existing credential, including a revoked one; `--revoke` revokes it without printing a secret. Rotation and revocation keep the stored restriction and refuse `--environment`, and the two flags are mutually exclusive. Issuance and rotation print the credential file once on standard output; redirect it to a new mode-0600 file. The command bypasses the Core API: it skips the archived-Project check and writes no audit entry, so use the Core-key routes whenever Core is running. A credential issued without an Environment restriction cannot be managed through the routes above.

## Connection status

The list's required `connection` object contains `status` (`never_enrolled`, `connected` or `disconnected`), `bound_key_id`, `enrolled_at` and `last_seen_at`. All three binding fields are null before enrollment. Once enrolled, the bound key and enrollment time describe the existing device; a null `last_seen_at` means no authenticated heartbeat has been recorded. Issuing another key does not change the binding. Rotation or revocation can make the binding disconnected while its history remains visible. Expired Environments remain readable under the existing list rules but cannot have current executor authority.

Connected means the Environment is connected, its device and executor key still have current Core authority, and the process-local gateway has an open peer that authenticated with that current key. Core rechecks authority after observing the peer. A former key's live socket, a device timestamp or a ready-looking Environment alone is insufficient; without a gateway, Core never returns connected. These facts are an observation, not a reservation of connectivity or of native or model readiness. `last_seen_at` may lag by a heartbeat interval.

List metadata and binding facts use one read-only database snapshot. That snapshot ends before the live authority checks, so a committed rotation or revocation is not hidden by snapshot isolation. Known authority loss projects as disconnected; observation and storage failures remain errors. Device IDs and credential digests are internal and never serialized. The public `/v1` Environment shape is unchanged.

### Private connection confirmation

`GET /api/v1/agent-daemon/connection?environment_id=UUID` uses the executor bearer, sent directly to Core (the reverse proxy routes `/api/v1` to Core; the console does not serve it). It is part of the private daemon transport, not the public Agents API. It reads existing authorization and binding only; it never enrolls a device, starts execution or changes resources. The no-store response contains only the requested `environment_id` and `status` (`connected` or `disconnected`). Connected requires the Environment observation, its exact Session and device binding, current executor authority and a live gateway socket authenticated with that same credential. A stale observation or a socket carrying a rotated key cannot confirm connection.

Invalid, revoked, foreign or deleted-Session authority returns 401; a different key for an already bound Environment returns 409. Responses do not expose the actual binding or database diagnostics.

The installer derives this route from the returned `remote_url` and does not follow redirects. After starting the daemon it polls once a second for up to 45 seconds. A 401 or 409 fails at once. On timeout it prints the path of the daemon's `connect.log` and asks you to rerun the same command; the daemon keeps reconnecting, and the installation, credential and history stay in place. A confirmed connection proves authentication only, not model access, Harness capability or completed execution.

## Revoked or rotated credential

When Core permanently rejects the daemon (enrollment 401 or 409, a permanent WebSocket rejection, or a daemon version from another Core distribution), the daemon prints the reason once and makes no further requests until it is stopped; it then exits successfully, so a supervisor that restarts on exit does not loop. When started again, it tries enrollment once and parks again. Transient failures keep the normal reconnect behavior and never replay execution.

A machine reconnects only with its bound `key_id`, rotated to a new secret that replaces the credential file at its configured path; the [self-hosted guide](../../docs/getting-started/self-hosted.md#rotate-or-revoke) gives the steps. A new `key_id` cannot reconnect an Environment that is already bound: issuing it succeeds, but enrollment with it returns 409. Rotation does not reinstall Harnesses, change the workspace or replace native history; never create a new Session history to recover a credential.

## Model provider

A `self_hosted` Session carries its own model provider; deployment defaults never apply. [Model execution](model-execution.md) owns the delivery rules. A saved Agent's provider key is delivered to the executor of every `self_hosted` Session created with that Agent in the Project, so anyone who can create `self_hosted` Sessions in the Project and run an executor can read it.
