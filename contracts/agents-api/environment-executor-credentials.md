# Environment executor credentials

An executor credential lets one self-hosted executor host enroll its daemon and
connect for one `self_hosted` Environment. The application creates the
`self_hosted` Session with its Project API key; the operator then issues the
credential with the Core key, through Web or a Core-key script, and gives the
returned credential file to the executor host. Project API keys cannot issue
credentials; the former Project-key route
`/core/v1/environments/{environment_id}/executor-credentials` is removed.

## Routes

All routes are under
`/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials`
and require the Core key. They apply only to a `self_hosted` Environment of that
Project whose Session exists (is not deleted); any other Project, Environment
type, missing Environment or deleted Session returns 404.

| Operation | Request | Result |
| --- | --- | --- |
| List | `GET …/executor-credentials` | `{"data":[{"key_id","created_at","revoked_at"}]}` |
| Issue or rotate | `POST …/executor-credentials` with `{"key_id":"UUID","rotate":false}` | 201 credential file, returned once |
| Revoke | `DELETE …/executor-credentials/{key_id}` | 204 |

The list holds metadata only, oldest first, for the credentials restricted to this
Environment; `revoked_at` is null while a credential is active. It never contains
a secret.

`key_id` is a canonical nonzero UUID chosen and retained before the request.
`rotate` is optional and defaults to false. The 201 response is the daemon
credential-file format:

```json
{"key_id":"UUID","environment_id":"ENVIRONMENT_UUID","executor_token":"ONE_TIME_SECRET"}
```

Responses use `Cache-Control: no-store`. Save the response directly to an owned
mode-0600 file; never place it in shell arguments, logs, a workspace or source.
Only its digest is persisted in Core. The Project's principal is the credential's
execution principal. The secret authorizes daemon enrollment and connection
(`/api/v1/agent-daemon/*`) for this exact Environment only, never `/v1`, `/core/v1`,
sandbox-node enrollment or project resource operations.

Writes have two conflicts, both 409. `executor_credential_exists`: an issuance
whose `key_id` already exists and does not set `rotate:true`, even after
revocation. `project_archived`: the Project is archived, so it gets no new or
rotated credential; listing and revocation remain available there, because
revoking must always work.

An issuance or rotation is checked in this order, and the first failure is
returned: the request body (400); the target Environment, which must be a
`self_hosted` Environment of this Project whose Session exists (404); an archived
Project (409 `project_archived`); then the key itself (409
`executor_credential_exists` without `rotate`, or 404 when rotating a `key_id`
that was never issued).
Rotation replaces the secret of an existing key restricted to this Environment,
keeps that Environment, invalidates the previous secret and restores a revoked
key; rotating an unknown `key_id` returns 404. Revocation is idempotent and
returns 204 each time. It denies further enrollment and connection; it does not
stop executor-owned compute or prove that an existing process has stopped.

After an uncertain result, such as a timeout, do not retry automatically. List
the credentials, then either rotate the same `key_id` (it was issued but its
secret was lost) or issue it again (it was not issued).

Issue, rotate and revoke each record an administrator audit entry
(`resource_type:"executor_credential"`, the key ID as `resource_id`, action
`issue`, `rotate` or `revoke`) in the same transaction as the write. The audit
never contains the secret. Credentials issued by the operator CLI
(`agents-api-environment-key`) without an Environment restriction cannot be
managed through these routes.

## Model provider

The Session carries its own model provider: `x_agents_core.model_provider` at
creation or a saved Agent that has one. Deployment default model providers do not
apply to `self_hosted` Sessions, and creation without a provider fails with 400
`model_provider_required`. Core freezes the bundle in the Session's encrypted
snapshot and sends it only over the connection of the executor enrolled for this
Environment with a current credential of the Session creator's principal. The
executor keeps it in the Runtime's native harness home, which tools and public
Files cannot read; the executor host's owner can. Revocation does not erase a
bundle already delivered. A saved Agent's provider key is delivered to the
executor of every `self_hosted` Session created with that Agent in the Project, so
anyone who can create `self_hosted` Sessions in the Project and run an executor
can read it.

## Executor host

The distribution's `self-hosted-install.pyz` downloads the matching
`parsar-runtime` launcher, Runtime image and seccomp profile. The local launcher
uses the same Docker isolation and workspace layout as Core-managed V1, then
invokes the existing daemon `connect` with the unchanged returned Environment
ID and `remote_url`. Docker onboarding requires an externally reachable `wss`
URL; host loopback addresses are not rewritten inside the container. It creates
no sandbox node or Core-managed allocation. Workspace, credential and native
history volumes belong to the operator. Failed or uncertain launches retain
their volumes and installation receipt for inspection instead of replacing
history or retrying enrollment. Session deletion does not reclaim these volumes.
Rerunning the installer inspects a previously started container only after its
installation and Environment labels match. Both first launch and rerun wait up
to 60 seconds for authenticated Core connection confirmation; a running container
alone does not establish connection. A stopped container receives a command to
start that same container before rerunning the installer.
An uncertain launch without a success receipt gives label-filtered container
and volume inspection commands and never creates a replacement. A cached image
with the exact distribution digest and platform skips image download and import.

## Private connection confirmation

`GET /api/v1/agent-daemon/connection?environment_id=UUID` uses the existing
executor bearer, sent directly to Core (the reverse proxy routes `/api/v1` to
Core; the console does not serve it). It is part of the private
daemon transport, not the public Agents API. It reads existing authorization and
binding only; it never enrolls a device, starts execution or changes resources.
The no-store response contains only the requested `environment_id` and `status`
(`connected` or `disconnected`). Connected requires the existing Environment
observation, its exact Session/device binding, current executor authority and a
live gateway socket authenticated with that same credential. A stale observation
or a socket carrying the former rotated key cannot confirm connection.

Invalid, revoked, foreign or deleted-Session authority returns 401; a different
key for an already bound Environment returns 409. Responses do not expose the
actual binding or database diagnostics. The installer derives this HTTPS route
from the validated returned `remote_url`, rejects redirects, retries transient
read failures within its deadline and polls at two-second intervals. Permanent
rejections fail immediately. On timeout or rejection it retains the container,
volumes, credential and receipts, and prints bounded Docker log inspection and
same-command retry guidance. This confirms authenticated connectivity, not model
credentials, harness capabilities or completed execution.
