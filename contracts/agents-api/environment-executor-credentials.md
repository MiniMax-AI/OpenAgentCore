---
title: "Environment executor credentials"
---

An executor credential lets `oac-daemon` enroll one `self_hosted` Environment and serve it over the [sandbox Link](../../docs/sandbox-link-protocol.md). It authorizes only the private daemon routes (`/api/v1/agent-daemon/*`) for that Environment and, once enrolled, the Serve of the Environment's enrollment resource on the Link, never `/v1`, `/core/v1`, sandbox-node enrollment or Project resources. The Project's principal is its execution principal. Core stores only a digest of the secret.

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

An invalid or expired grant returns 401 `installation_authorization_invalid`. Without matching installers the grant routes return 503 `installation_unavailable`. Core signs each grant with the installation's [`secrets/core/credential.key`](../../docs/configuration.md#compose-installations). A malformed secret returns 400. Artifact routes carry no credential, and the grant is sent only to Core, never to an artifact host.

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

Rotation replaces the secret of an existing key restricted to this Environment, keeps that Environment and `key_id`, invalidates the previous secret at once and restores a revoked key. It also advances the generation of the Environment's enrollment and the epoch of its Session's assignment, so the machine serves again only after it enrolls with the new secret, and a running Turn loses the Environment ([Session assignments](../../docs/runtime-protocol.md#session-assignments)). Revocation is idempotent and returns 204 each time. It denies further enrollment and closes the machine's Serve.

After an uncertain result, such as a timeout, do not retry automatically. List the credentials, then either rotate the same `key_id` (it was issued but its secret was lost) or issue it again (it was not issued).

Issue, rotate and revoke each record an administrator audit entry (`resource_type:"executor_credential"`, the key ID as `resource_id`, action `issue`, `rotate` or `revoke`) in the same transaction as the write. The audit never contains the secret.

### Break-glass command

`oac-core-environment-key` issues, rotates or revokes a credential directly in the database when the Core API is unavailable. It reads Core's database settings (`OAC_DATABASE_URL`, and `OAC_DATABASE_PASSWORD_FILE` when set) and needs the Project's execution principal: `--tenant` (the Project's tenant UUID in the `projects` table), `--organization core`, `--project proj_<Project UUID>`, `--subject-kind service_account`, `--subject-id project:<Project UUID>` and `--key-id`. Without another flag it issues a new credential, and `--environment` restricts it to one Environment. `--rotate` replaces the secret of an existing credential, including a revoked one; `--revoke` revokes it without printing a secret. Rotation and revocation keep the stored restriction and refuse `--environment`, and the two flags are mutually exclusive. Issuance and rotation print the credential file once on standard output; redirect it to a new mode-0600 file. The command bypasses the Core API: it skips the archived-Project check and writes no audit entry, so use the Core-key routes whenever Core is running. A credential issued without an Environment restriction cannot be managed through the routes above.

## Connection status

The list's required `connection` object contains `status` (`never_enrolled`, `connected` or `disconnected`), `bound_key_id` and `enrolled_at`. Both binding fields are null before enrollment. Once enrolled, `bound_key_id` is the key that enrolled the Environment and `enrolled_at` is when it first enrolled; neither is readiness. Issuing another key does not change the binding. Rotation or revocation can make the binding disconnected while its history remains visible. Expired Environments remain readable under the existing list rules but cannot have current executor authority.

Connected means the enrolled key still has current Core authority and the Environment's live Link resource is that key's enrollment, Serving at its current generation: the relay holds the serve peer of the machine's Sandbox I/O service. Core rechecks authority after observing the serve peer, so a peer that still serves with a former secret is not connected. This is the same rule as for a hosted Environment ([readiness facts](../../docs/sandbox-provider.md#four-distinct-readiness-facts)). It is an observation, not a reservation of connectivity or of native or model readiness.

List metadata and binding facts use one read-only database snapshot. That snapshot ends before the live authority checks, so a committed rotation or revocation is not hidden by snapshot isolation. Known authority loss projects as disconnected; observation and storage failures remain errors. Enrollment IDs and credential digests are internal and never serialized. The public `/v1` Environment shape is unchanged.

### Private connection confirmation

`GET /api/v1/agent-daemon/connection?environment_id=UUID` uses the executor bearer, sent directly to Core (the reverse proxy routes `/api/v1` to Core; the console does not serve it). It is part of the private daemon transport, not the public Agents API. It reads existing authorization and enrollment only; it never enrolls, starts execution or changes resources. The no-store response contains only the requested `environment_id` and `status` (`connected` or `disconnected`). `connected` follows the [connection status](#connection-status) rule for the presented credential: the Environment's live Link resource is the enrollment of that same credential and is Serving. A stale observation or a serve peer of a rotated secret cannot confirm connection.

Invalid, revoked, foreign or deleted-Session authority, or a failed or expired Environment, returns 401; a different key for an already enrolled Environment returns 409. Responses do not expose the actual binding or database diagnostics.

The installer derives this route from the returned `remote_url` and does not follow redirects. After starting the daemon it polls once a second for up to 45 seconds. A 401 or 409 fails at once. On timeout it prints the path of the daemon's `connect.log` and asks you to rerun the same command; the daemon keeps reconnecting, and the installation, credential and history stay in place. A confirmed connection proves that the machine serves the Environment, not model access, Harness capability or completed execution.

## Revoked or rotated credential

When Core permanently rejects the machine's enrollment (401 or 409), the daemon prints the reason once and makes no further requests until it is stopped; it then exits successfully, so a supervisor that restarts on exit does not loop. When started again, it tries enrollment once and parks again. Transient failures, including an exited Sandbox I/O service, are retried with backoff and never replay execution.

A machine reconnects only with its bound `key_id`, rotated to a new secret that replaces the credential file at its configured path; the [self-hosted guide](../../docs/getting-started/self-hosted.md#rotate-or-revoke) gives the steps. A new `key_id` cannot reconnect an Environment that is already bound: issuing it succeeds, but enrollment with it returns 409. Rotation does not change the workspace or the Session's native history; never create a new Session to recover a credential.
