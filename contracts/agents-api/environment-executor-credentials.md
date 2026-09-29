# Environment executor credentials

An executor credential lets a daemon enroll and connect for one `self_hosted`
Environment. An application creates the Session with its Project API key and
receives an Environment-scoped installation command. The installer claims its
connect-only key and connects without requiring Web or a Core key. Operators
retain the explicit Core-key issuance, rotation and revocation routes below.
The command's short-lived authorization and the daemon's long-term credential
are separate; their lifecycle is defined in [native installation](../../docs/self-hosted-native.md).

## Routes

All routes are under
`/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials`
and require the Core key. They apply only to a `self_hosted` Environment of that
Project whose Session exists (is not deleted); any other Project, Environment
type, missing Environment or deleted Session returns 404.

| Operation | Request | Result |
| --- | --- | --- |
| List | `GET …/executor-credentials` | Credential metadata in `data`, plus required `connection` observation |
| Issue or rotate | `POST …/executor-credentials` with `{"key_id":"UUID","rotate":false}` | 201 credential file, returned once |
| Revoke | `DELETE …/executor-credentials/{key_id}` | 204 |

The list holds metadata only, oldest first, for the credentials restricted to this
Environment; `revoked_at` is null while a credential is active. It never contains
a secret.

The required `connection` object contains `status` (`never_enrolled`, `connected`,
or `disconnected`), `bound_key_id`, `enrolled_at`, and `last_seen_at`. All three
binding fields are null before enrollment. Once enrolled, the bound key and
enrollment time describe the existing device; a null `last_seen_at` means no
authenticated heartbeat has been recorded. Issuing another key does not change
the binding. Rotation/revocation can make the binding disconnected while its
history remains visible. Expired Environments remain readable under the existing
list rules but cannot have current executor authority.

Connected means the Environment is connected, its device and executor key still
have current Core authority, and the process-local gateway has an open peer
that authenticated with that current key. Core rechecks authority after observing
the peer. A former key's live socket, a device timestamp, or a ready-looking
Environment alone is insufficient; without a gateway, Core never returns
connected. These facts are an observation, not a reservation of connectivity or
native/model readiness. `last_seen_at` may lag by a heartbeat interval.

List metadata and binding facts use one read-only database snapshot. That snapshot
ends before the live authority checks, so a committed rotation/revocation is not
hidden by snapshot isolation. Known authority loss projects as disconnected;
observation/storage failures remain errors. Device IDs and credential digests are
internal and never serialized. The public `/v1` Environment shape is unchanged.

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
(`oac-core-environment-key`) without an Environment restriction cannot be
managed through these routes.

## Model provider

The Session carries its own model provider: `x_agents_core.model_provider` at
creation or a saved Agent that has one. Deployment default model providers do not
apply to `self_hosted` Sessions, and creation without a provider fails with 400
`model_provider_required`. Core freezes the bundle in the Session's encrypted
snapshot and sends it only over the connection of the executor enrolled for this
Environment with a current credential of the Session creator's principal. The
executor keeps it in the Runtime's native harness home. Public Files remains
scoped to the authorized workspace, but native tools and the host owner can read
whatever the starting account can access. The daemon provides no same-user
credential isolation. Revocation does not erase a
bundle already delivered. A saved Agent's provider key is delivered to the
executor of every `self_hosted` Session created with that Agent in the Project, so
anyone who can create `self_hosted` Sessions in the Project and run an executor
can read it.

## Executor host

The [native installer](../../docs/self-hosted-native.md) uses the same daemon and
pinned adapters on every supported platform. Session responses provide commands
in `x_agents_core.installation`; Web displays them without reconstructing them.
The bootstrap only downloads and extracts a qualified distribution, then invokes
the common installer to select Harnesses, install, start and verify connection.
The Session's Environment identity and workspace are fixed inputs. Installation
never creates or reclaims the user's machine, workspace or native history.

Explicit distribution installation with `--credential-file` remains available
for operator-managed credentials. Readiness and connection checks do not validate
model access. Runtime preparation and execution use the existing common protocol.

### Revoked or rotated credential

When Core permanently rejects enrollment or the WebSocket, the daemon reports the
reason and parks without retrying until stopped. A protocol mismatch requires the
matching current distribution; it does not trigger a migration. Transient transport
failures retain the existing reconnect behavior and never replay execution.

Rotate the same `key_id`, stop the daemon, replace the configured credential JSON
file, and start it again. Issuing a new key for an enrolled Environment fails with
409 because enrollment retains its original key binding. Revocation prevents the
old token from reconnecting. Rotation does not reinstall Harnesses, change the
workspace or replace native history.

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
