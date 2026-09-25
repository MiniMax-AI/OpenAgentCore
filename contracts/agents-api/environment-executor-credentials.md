# Environment executor credentials

These Core extensions are outside the pinned upstream Agents API. They use the
same project caller authentication as Session creation, without `OpenAI-Beta`.
They require the exact authenticated creator of a live `self_hosted` Session;
project-shared Session read access does not grant issuance authority.

`POST /core/v1/environments/{environment_id}/executor-credentials` accepts:

```json
{"key_id":"CLIENT_GENERATED_UUID","rotate":false}
```

`key_id` is a canonical nonzero UUID chosen and retained before the request.
`rotate` is optional and defaults to false. The 201 response is the existing
daemon credential-file format:

```json
{"key_id":"UUID","environment_id":"ENVIRONMENT_UUID","executor_token":"ONE_TIME_SECRET"}
```

Responses use `Cache-Control: no-store`. Save the response directly to an owned
mode-0600 file; never place it in shell arguments, logs, a workspace or source.
Only its digest is persisted in Core. The secret authorizes daemon enrollment
and connection for this exact Environment, never public Session API calls,
sandbox-node enrollment or project resource operations. The application caller
key stays on the application machine and is never given to the Runtime.

Ordinary issuance with an existing `key_id` returns 409, even after revocation.
After an uncertain issuance response, explicitly send the same `key_id` with
`rotate:true` to replace the secret; do not automatically retry a rotation or
choose another key. Rotation preserves principal and Environment restriction,
invalidates the previous secret, and can restore an explicitly revoked key.
Credentials created without an exact Environment restriction by the operator
CLI cannot be managed through these routes.

`DELETE /core/v1/environments/{environment_id}/executor-credentials/{key_id}`
returns 204 and revokes the exact caller-owned restricted key. Repeated
revocation is safe. Other principals, targets and deleted Sessions return 404.
Revocation denies further authorized connection and dispatch; it does not stop
user-owned compute or prove that an existing process has stopped.

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
