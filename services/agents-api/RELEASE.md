# Standalone Core archive (advanced)

This is not the installation path for new users. To install Core with Web, nodes and
the `parsar` command, use the
[Core distribution and its installer](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/install.md).

This Linux amd64 package contains the independent API, embedded migrator, operator
commands and `parsar-sandbox-node`. It needs your own PostgreSQL and separately
installed execution software, and it has no Web console.
It does not need a source checkout, Go, Node, the Parsar product or its database.
The [coverage ledger](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/README.md)
describes supported workflows and remaining protocol gaps. Packaging does not
establish complete OpenAI Agents API compatibility.

## Verify and extract

Verify the archive checksum supplied alongside the package, then extract into a
new directory under `~/.parsar/`. Keep deployment configuration outside the extracted
package so replacing binaries does not replace credentials or state.

```sh
sha256sum -c @ARCHIVE_NAME@.tar.gz.sha256
mkdir -p "$HOME/.parsar/releases"
tar -xzf @ARCHIVE_NAME@.tar.gz -C "$HOME/.parsar/releases"
cd "$HOME/.parsar/releases/@ARCHIVE_NAME@"
sha256sum -c SHA256SUMS
export AGENTS_API_BIN_DIR="$PWD/bin"
```

`manifest.json` records the source revision/tree, target platform, fixed upstream
protocol and binary hashes. The package also includes its license. Checksums
detect changed bytes; obtain the archive and checksum from a trusted distributor.

## Start a new API installation

Provision a dedicated PostgreSQL database and account. Use neither the product
database nor its migrations. The following local example assumes an unused port
8091. For remote clients, place the API behind TLS and set the advertised daemon URL to
the reachable WSS service address.

```sh
umask 077
export PARSAR_HOME="$HOME/.parsar/agents-api-deployment"
mkdir -p "$PARSAR_HOME"
export AGENTS_API_DATABASE_URL='postgres://<account>:<password>@<host>/<execution-db>'
export AGENTS_API_CORE_KEY_DIGESTS_FILE="$PARSAR_HOME/core-key-digests.json"
export AGENTS_API_ADDR=127.0.0.1:8091
export AGENTS_API_ENGINE=codex
```

Create `core-key-digests.json` as a JSON array containing the SHA-256 digest of a
random Core key. Keep the Core key separately in private operator storage. After startup, use it to create a Project and issue an
application key through the [administrator API](../../contracts/agents-api/admin-api.md).
Projects and application keys live only in PostgreSQL. Keys in one Project share
its scope and principal; rotation uses issuance and revocation without a restart.

Set the reachable public origin; Core derives the daemon endpoint from it:

```sh
export AGENTS_API_PUBLIC_URL=http://127.0.0.1:8091
```

Keep the configuration and key files mode 0600. Run migrations explicitly, then
start the API in the foreground or through your existing service supervisor:

```sh
"$AGENTS_API_BIN_DIR/agents-api-migrate"
"$AGENTS_API_BIN_DIR/agents-api"
```

`GET /healthz` provides liveness. Project creation establishes its immutable execution scope. One API execution worker owns
each database; starting replicas does not provide execution HA. Native history
belongs to the harness host and must survive API replacement.

## Use the public client

Install the official Python client at the commit in `manifest.json` (SDK 3.13.0).
In a client process, set `OPENAI_BASE_URL=http://127.0.0.1:8091/v1` and supply the
private caller key as `OPENAI_API_KEY`. Create an empty self-hosted Session:

```python
from openai import OpenAI

client = OpenAI()
session = client.beta.agents.sessions.create(
    agent={"model": "<model configured on the harness>"},
    environment={"type": "self_hosted", "workspace_directory": "/workspace"},
)
print(session.id, session.environment.id, session.environment.remote_url)
```

Keep the API running. In a separate operator shell on the API host, issue an
executor credential restricted to this Environment with the Core key, read from
the private file named by `CORE_KEY_FILE`. `PROJECT_ID` is the Project whose key
created the Session; `KEY_ID` is a new canonical
lowercase UUID that you retain for listing, rotation and revocation. The response
is the credential file and is returned only once, so save it to a new private
file:

```sh
umask 077
KEY_ID=$(python3 -c 'import uuid; print(uuid.uuid4())')
curl -fsS -X POST \
  -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$CORE_KEY_FILE")") \
  -H 'Content-Type: application/json' -d "{\"key_id\":\"$KEY_ID\"}" \
  "http://127.0.0.1:8091/core/v1/projects/$PROJECT_ID/environments/$ENVIRONMENT_ID/executor-credentials" \
  > "$PARSAR_HOME/executor-key.json"
```

If the response is uncertain, do not retry automatically: list the credentials
with `GET` on the same path, then rotate the same `KEY_ID` with `"rotate":true` or
issue it again. Revoke with `DELETE …/executor-credentials/$KEY_ID`. See
[executor credentials](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/environment-executor-credentials.md)
for the rules, including the 404 and 409 cases.

Break-glass only: `agents-api-environment-key` issues, rotates or revokes the
same credential directly in the database when the Core API is unavailable. It
needs the private database configuration and the Project's execution principal
(tenant UUID from the `projects` table, organization `core`, project
`proj_<Project UUID>`, subject `service_account/project:<Project UUID>`). It
bypasses the Core API: it skips the archived-Project check and writes no
administrator audit entry, so use the Core-key route whenever Core is running.

Deploy the qualified V1 Runtime containing our daemon, selected native harness,
local tools and workspace. Transfer only its scoped key into the protected daemon
state directory as an owned mode-0600 file. Keep API caller and database credentials
outside Runtime. Configure the model through the existing private adapter options;
native tools must not inherit model credentials or read native history.

Inside that Runtime, use the exact values returned by Session creation:

```sh
parsar-daemon connect --remote "$REMOTE_URL" \
  --environment-id "$ENVIRONMENT_ID" \
  --credential-file "$PARSAR_HOME/parsar-daemon/executor-key.json"
```

The daemon fills the executor role. No separate Codex executor or service-side
harness is required. This is our private daemon transport, not stock exec-server
wire interoperability. Use WSS outside loopback. Runtime packaging must provide
`/environment/workspace`, its `/workspace` alias, helpers and native isolation;
a directory or key binding alone does not isolate same-user processes. User-owned
E2B deployment uses the [official-SDK startup example](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/services/agents-api/deploy/e2b/README.md).
Core does not allocate or reclaim that compute.

In the same Python client, stream a Turn after connecting the executor:

```python
with client.beta.agents.sessions.stream(
    session.id, input="Run a command in the workspace and explain its result."
) as stream:
    for event in stream:
        print(event.type)
```

Keep the Session ID for later Turns. After an API restart, reconnect the client
and recover through Session, Turn and Items queries; SSE does not replay history.
Reuse the database, caller identities, daemon profile and native history. Do not
resubmit uncertain execution as new work. Graceful shutdown or connection closure
does not by itself prove all native descendants have exited.

For key rotation, executor-key revocation, existing-database upgrades and other supported
profiles, use the [versioned service guide](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/services/agents-api/README.md).
This package does not install PostgreSQL, daemons, harnesses, TLS or a supervisor,
and it does not switch Parsar's product execution path.

## Sandbox nodes

The release includes `parsar-sandbox-node` for local and remote hosts. See the
[nodes and sandbox backends reference](HOSTED-SANDBOX-MANAGER.md) for provider
selection, manual registration, administrator credentials, fixed Session placement
and maintenance.
