# Self-hosted executors

A `self_hosted` Session runs on a machine the application owns. The application
creates the Session through `/v1` and receives a command that installs and connects
`oac-daemon`. Web displays the same command in the Session; it is optional.
Linux, macOS and Windows use the same Runtime protocol. Core-managed Providers
remain Linux-only.

The daemon runs with its launching user's permissions and adds no inner
filesystem, permission or network sandbox. Its tools may access that user's files
and Runtime credentials. Managed isolation belongs to the outer Environment;
self-hosted operators choose any outer isolation they need. See the
[native installation guide](../self-hosted-native.md) for prerequisites, commands
and supported platforms.

An executor credential works for one Environment only. It cannot call the Agent
API, Core API or node enrollment. Default model providers never apply to
self-hosted Sessions: the request or Agent must carry its own model provider.
Otherwise creation fails with 400 `model_provider_required`.

## Connect a host

1. Choose an absolute workspace path on the target host. Create a Session with
   that path and the application's Project API key:

   ```python
   import os
   from openai import OpenAI

   client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
   session = client.beta.agents.sessions.create(
       environment={
           "type": "self_hosted",
           "workspace_directory": os.environ["EXECUTOR_WORKSPACE"],
       },
       extra_body={
           "agent": {"model": os.environ["MODEL_NAME"], "x_agents_core": {"harness": "codex"}},
           "x_agents_core": {"model_provider": {
               "protocol": "responses",
               "base_url": os.environ["MODEL_BASE_URL"],
               "api_key": os.environ["MODEL_API_KEY"],
           }},
       },
   )
   installation = session.model_dump()["x_agents_core"]["installation"]
   print(installation["commands"]["posix"])  # use "powershell" for Windows
   ```

2. Run the returned command on the target machine. Select the Harnesses and
   installation directory when prompted. Installation creates the workspace if
   needed, starts the daemon and checks its connection. For automation, append
   `--non-interactive --harness codex` and optionally `--install-dir ABS`.

In Web, open the **Self-hosted** Session and copy the command under **Connect a
host**. A command expires after 30 minutes; fetch the Session again for a fresh
one. The [native guide](../self-hosted-native.md#install-and-connect) covers retry,
platform prerequisites and credential storage. Core must be reachable from the
host with TLS outside loopback. Native installation does not require Docker.

A connected Environment proves only the machine connection. Send a Turn to check
the selected harness and model. Core supplies the Session's model provider over
this Environment's authenticated executor connection. The host's user and tools
running with that user's permissions can access locally stored Runtime data.

## Local capability directories

A self-hosted Session may supply `capability_directories` alongside its workspace:

```python
environment = {
    "type": "self_hosted",
    "workspace_directory": os.environ["EXECUTOR_WORKSPACE"],
    "capability_directories": [os.environ["EXECUTOR_CAPABILITIES"]],
}
```

Core accepts absolute Unix, Windows drive and UNC source paths without checking
its own filesystem. Runtime validates them using the executor host's path syntax.
Populate these directories before first execution. They are ordinary paths visible
to that process; naming a directory does not mount it or create a sandbox.
Self-hosted public input accepts local directories, not managed Skill/Plugin
archives or hosted Templates.

Runtime snapshots sources before native execution and uses the same parser and
`installed.json` format as managed bundles to supply Skills and Plugin MCP.
The native installer defaults the snapshot destination to `capabilities` under
`OAC_RUNTIME_HOME`; `--capability-directory` selects another local destination.
It is an operator setting, not a public API write destination. Reconnect reuses
installed contents even after source edits; a new Session captures its own
configuration. A missing or inconsistent snapshot fails preparation rather than
silently reinstalling. Local discovery does not create entries in the public
API-managed installation arrays. Snapshot file modes do not isolate the snapshot
from tools running as the same user.

Closing an executor, cancelling a Turn or losing its connection preserves the
snapshot and workspace. The compute owner remains responsible for explicit
cleanup. [Historical qualification](../../contracts/agents-api/user-managed-runtime-v1.md)
records only its stated inputs and binaries; it does not qualify the current native
platforms or local capability preparation.

## Rotate or revoke

| Action in Web | Effect |
| --- | --- |
| **Rotate** | The credential gets a new secret; the old secret stops working at once. Rotating a revoked credential restores it |
| **Revoke** | The credential stops working at once |

To reconnect a native installation, stop it with `oac-daemon stop`, rotate the same
credential, replace the JSON at its configured credential-file path, and run
`oac-daemon start`. Keep the same `OAC_RUNTIME_HOME` for every command. Issuing a
new credential does not reconnect an Environment already bound to its first
credential; rotate that credential instead. Do not run `install` again over the
existing installation.

Stopping the daemon keeps its workspace and native history. Deleting a Session
does not remove host files. In an archived Project, credentials cannot be issued
or rotated; revocation remains available.

## Operator credential management

Operators can still issue, rotate or revoke executor credentials using a Core key
through Core's loopback port. This is not required for one-command onboarding.
See the [credential contract](../../contracts/agents-api/environment-executor-credentials.md)
for those routes and uncertain-response handling.

## Historical executor installations

The native installer supports its current version only. It does not adopt or
upgrade an older daemon or container installation. Preserve old files, containers
and native history; use a fresh Runtime home and Session when moving versions.
Historical container-installer instructions and qualification records describe
those earlier artifacts, not acceptance of the current native daemon.
