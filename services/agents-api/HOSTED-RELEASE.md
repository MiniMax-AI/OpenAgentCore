# Docker-hosted Agents API

This package adds a qualified Linux amd64 Runtime image for Core-managed
Sessions on Docker. A registered Docker sandbox node, not Core, creates one
colocated Runtime per Session. Each Runtime contains the daemon, stock
Codex 0.153.4, local tools and workspace. Core, its PostgreSQL database and
operator secrets stay outside the Runtime. No source checkout, compiler,
Parsar service, product database, separate executor or manual daemon enrollment
is needed. Complete the common database, caller identity and private directory
setup in `README.md` first; do not start Core until the configuration below is ready.

## Load the qualified Runtime

The supported deployment profile was qualified on Docker 29.1.3 with local Linux
volumes and unprivileged user namespaces. The Docker engine must support volume
subpath mounts. Other hosts and security policies need their own isolation checks.
The Provider uses a non-root container, read-only root, dropped capabilities,
no-new-privileges, private PID namespace and bounded resources. Stock Codex uses
its inner bubblewrap sandbox with the bundled seccomp policy and container-specific
`apparmor=unconfined`; host-wide policy must remain unchanged. Docker availability
alone does not establish that this native sandbox can run safely.

From the extracted package directory, after checking `SHA256SUMS`:

```sh
export AGENTS_API_PACKAGE="$PWD"
docker image load --input "$AGENTS_API_PACKAGE/runtime/image.tar"
docker image inspect @RUNTIME_IMAGE@ --format '{{.Id}} {{.Os}}/{{.Architecture}}'
```

The result must be `@RUNTIME_IMAGE@ linux/amd64`. The image is selected by this
immutable ID; no registry pull or mutable tag is required. Package checksums
establish transferred bytes, not trust in their distributor or safety of another
image. Keep the package's `runtime/seccomp.json` available to the node service
(`seccomp_file` in its provider configuration).

## Configure Core

Core no longer uses a Docker socket or a managed Runtime file; it rejects
`AGENTS_API_MANAGED_RUNTIMES_FILE` at startup. PostgreSQL owns the hosted
deployment: provider, per-sandbox resources and one immutable Runtime release,
selected with the Core key through `POST /core/v1/sandbox/deployment` (see the
[deployment contract](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/sandbox-deployment.md)).
A Docker node registered with `bin/parsar-sandbox-node` owns the host's Docker
socket and creates the Runtimes; see
[register a host](HOSTED-SANDBOX-MANAGER.md#register-a-host).

A Docker Runtime release names all six identities of one matched distribution:
source commit, image ID, OCI manifest digest, microsandbox reference, Runtime and
firmware hashes. This package records only the image ID, so it cannot supply that
release by itself. Use the Core distribution and its
[installer](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/docs/getting-started/install.md),
whose manifest carries the complete release and whose Web console adds Docker
nodes.

For a manual deployment with a complete release, set these in addition to the
common configuration, using a stable installation UUID and the HTTPS origin that
nodes and sandbox guests reach through your TLS proxy:

```sh
export AGENTS_API_ADDR=0.0.0.0:8091
export AGENTS_API_SANDBOX_INSTALLATION_ID="<installation UUID>"
export AGENTS_API_PUBLIC_URL="https://core.example"
chmod 0600 "$AGENTS_API_CORE_KEY_DIGESTS_FILE"
"$AGENTS_API_BIN_DIR/agents-api-migrate"
"$AGENTS_API_BIN_DIR/agents-api"
```

Set the deployment default model provider with the Core key, for a
Responses-compatible endpoint reachable from the Runtime (a host loopback address
is not the container's host):

```sh
curl -fsS -X PUT http://127.0.0.1:8091/core/v1/harnesses/codex/model-provider \
  -H "Authorization: Bearer $CORE_KEY" -H "Content-Type: application/json" \
  -d '{"protocol":"responses","base_url":"https://<model endpoint>/v1","api_key":"<private model credential>"}'
```

Core encrypts it, never returns the key and freezes it into each new hosted
Session. Never put model credentials in Agent instructions, image layers or a
workspace.

Then select `docker`, the per-sandbox resources and the complete Runtime release
(the deployment's `core_url` comes from `AGENTS_API_PUBLIC_URL`), and register a node on a host where
this image is loaded. Hosted admission starts when a ready node has capacity.

Use your existing service supervisor for long-running operation. Migrations are
explicit. One Core execution worker owns each database; replicas do not provide
execution HA. `GET /healthz` checks liveness, not successful sandbox preparation.

## Execute through the public client

Use the pinned client described in `README.md`, with `OPENAI_BASE_URL` set to
`http://127.0.0.1:8091/v1` and `OPENAI_API_KEY` read from the private caller key.
The public model name must match the trusted endpoint's supported model:

```python
import base64
from openai import OpenAI

client = OpenAI()
session = client.beta.agents.sessions.create(
    agent={"model": "<configured model>"},
    environment={"type": "openai_hosted"},
    input="Create /workspace/result.txt containing a short greeting.",
)
print(session.id, session.environment.id)
```

Core provisions the Runtime automatically. Retrieve the Session and list its Turns
and Items to observe execution; wait for the Turn to complete before a file upload.
A connected Environment confirms the daemon transport, not native readiness.
Keep the Session ID for all subsequent operations:

```python
print(client.beta.agents.sessions.retrieve(session.id))
print(client.beta.agents.sessions.turns.list(session.id))
print(client.beta.agents.sessions.items.list(session.id))
print(client.beta.agents.environments.files.list(session.environment.id, path="/workspace"))
client.beta.agents.environments.files.create(
    session.environment.id,
    type="inline",
    path="/workspace/input.txt",
    data=base64.b64encode(b"Read these exact bytes.").decode(),
)
with client.beta.agents.sessions.stream(
    session.id, input="Read /workspace/input.txt with the native shell and quote it."
) as stream:
    for event in stream:
        print(event.type)
```

Cancellation of an accepted/running Turn uses the same public Session:

```python
client.beta.agents.sessions.events.create(
    session.id, events=[{"type": "agent.session.input.cancel"}]
)
```

Query until the Turn is terminal; a cancellation request alone is not completion.
Pre-Turn reservation cancellation remains a recorded gap. Reconnect clients after
Core restart and recover through Session/Turn/Items; SSE is live and does not replay
history. Preserve Runtime containers and both owned volumes for native history and
workspace continuation. Do not turn an uncertain interrupted execution into a new
request or delete native history to make a retry succeed.

When finished, `client.beta.agents.sessions.delete(session.id)` requests owned
Runtime cleanup. Core and the original node must remain running until its
labelled container and volumes are gone. Public deletion acknowledgment
is not physical cleanup confirmation. Do not use broad Docker pruning.

To change providers, resources or Runtime, follow the
[maintenance procedure](HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance): enter
maintenance at the current generation, archive retained hosted Sessions and verify
cleanup, submit the replacement, then resume. Maintenance blocks new compute
without deleting retained resources. One deployment runs exactly one provider,
E2B, Docker or [microsandbox](deploy/microsandbox/README.md), with no mixed configuration,
engine-to-provider routing or automatic Session migration.

Back up the independent PostgreSQL database
(including large objects) and retained Runtime state together under an operator
recovery plan; the archive itself contains no deployment data.

## Acceptance limits

The qualified basic profile covers public creation, native execution, inline and
source-file copies/listing, cancellation, retained-history restart and owned
cleanup. Network access defaults to enabled; explicit disabled confines native
tools while the trusted harness retains model/Core connectivity, and exact-host
restricted policies are supported. Environment Templates and inline
initialization are covered within their
[recorded limits](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/environment-templates.md), and Artifacts are captured on
accepted Docker profiles. Other hostname forms, service-origin hosted MCP and
complete protocol parity remain open. Files size and
directory bounds are local implementation limits, not verified upstream limits.

The same Runtime also serves caller-managed `self_hosted` Sessions. The application
creates the Session with its Project API key, the deployment operator issues the
Environment's [executor credential](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/environment-executor-credentials.md) with
the Core key, and the executor host runs the daemon with it. That path is
[qualified](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/user-managed-runtime-v1.md) on user-managed Docker and E2B for all
three harnesses. It accepts only `/workspace` with empty capability directories,
rejects service-origin HTTP MCP and has no Environment Templates; the application
owns and cleans up its compute. This package does not install
Docker/PostgreSQL/TLS/a supervisor, publish an image, migrate product execution or
add engines. See the
[versioned coverage ledger](https://github.com/MiniMax-AI/parsar-core/blob/@SOURCE_REVISION@/contracts/agents-api/README.md).
