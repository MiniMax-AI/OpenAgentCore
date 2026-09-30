# Self-hosted executors

A `self_hosted` Session runs on a machine your application owns: a workstation, a VM or a sandbox you manage. The application creates the Session through `/v1` and receives a command that installs `oac-daemon`, starts it and connects it to Core. Web shows the same command on the Session's page; it is optional. Core never creates, stops or reclaims the machine.

**The daemon is not a sandbox.** Tools run with the permissions of the account that starts it and can reach whatever that account can. Use a container or VM when you need isolation; see [Runtime and outer isolation](../design-principles.md#runtime-and-outer-isolation). The daemon does not restrict network access, so a Template that requires a network policy is rejected for a self-hosted Session.

The Session brings its own model provider; the installation default never applies ([why](../../contracts/agents-api/model-execution.md#saved-defaults-and-precedence)). The machine gets an executor credential that works for this one Environment and nothing else.

## Platforms

| Platform | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| Linux amd64 | Supported | Supported | Supported |
| macOS arm64 | Supported | Supported | Supported |
| Windows amd64 | Supported | Supported | Not supported |

The installer brings its own pinned Node.js and Harness versions (listed in [`scripts/build-native-installer.mjs`](../../scripts/build-native-installer.mjs)) and leaves other installations of those tools untouched. On a platform without a matching installer, the command fails.

The machine needs:

- HTTPS access to Core (plain HTTP only on loopback), and to the release download host unless Core carries an offline copy of the installers;
- Bash for environment setup and MiniMax Code tools; on Windows, Git Bash, which Claude Code also requires;
- Python and pip when the Session's packages need them;
- any system packages your setup needs. The daemon never runs apt, sudo or another elevation command, so install them through the host's normal administration.

No administrator privileges or Docker are needed.

## Connect a machine

1. Choose an absolute workspace path on the target machine. Create a Session with that path and the application's Project API key:

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
   print(installation["commands"]["posix"])  # use "powershell" on Windows
   ```

2. Run the command on the target machine with the account that should run the tools. It downloads the installer matched to this Core, verifies its checksum, asks which Harnesses to install and where, installs them, creates the workspace if needed, starts the daemon and checks its connection.
3. Send a Turn. A connected machine proves only authentication; the first Turn checks the Harness and the model.

In Web, open the Session and copy the command under **Connect a host**.

Keep the command private: it carries a short-lived [installation grant](../../contracts/agents-api/environment-executor-credentials.md#installation-grant) that claims the machine's credential. When it has expired, read the Session again or copy a fresh command from Web.

The installer reports three results:

| Result | Meaning |
| --- | --- |
| **Installation** | The selected Harnesses passed their readiness checks |
| **Daemon connection** | Core confirmed the daemon's authenticated connection |
| **Model configuration** | Not checked; the first Turn uses the Session's model provider |

If the connection is not confirmed within 45 seconds, the installer prints the path of the daemon's log. The daemon keeps reconnecting. Fix the cause and run the install command again with the same installation directory, copying a fresh one from Web if it has expired: completed components and the credential are kept and a running daemon is reused. Do not remove the workspace or the Session to retry.

### Options for automation

Append these to the command:

| Option | Effect |
| --- | --- |
| `--non-interactive` | Never prompt; missing input fails |
| `--harness codex,claude,minimax` | Harnesses to install, comma-separated. Must include the Session's Harness |
| `--install-dir ABS` | Installation directory. Default: `environments/<environment-id>` under `~/.oac`, or under `OAC_RUNTIME_HOME` when set |
| `--capability-directory ABS` | Where [capability snapshots](#local-capability-directories) are stored. Default: `capabilities` in the installation directory |
| `--tool-env-file ABS` | A JSON file of string variables for tools and MCP servers; see [explicit local tool environment](../../contracts/agents-api/environments.md#explicit-local-tool-environment) |

The workspace is fixed when the Session is created. For a different workspace, create another Session.

## Local capability directories

A self-hosted Session may supply `capability_directories` alongside its workspace:

```python
environment = {
    "type": "self_hosted",
    "workspace_directory": os.environ["EXECUTOR_WORKSPACE"],
    "capability_directories": [os.environ["EXECUTOR_CAPABILITIES"]],
}
```

Paths are absolute in the machine's own syntax (Unix, Windows drive or UNC); the daemon checks them, not Core. Fill these directories before the daemon connects. They are ordinary paths visible to the daemon; naming one does not mount it or create a sandbox.

Use `x_agents_core.environment` for the same Project-owned Skills, Plugin archives, files, packages, setup commands or Template used by a managed Session:

```python
session = client.beta.agents.sessions.create(
    agent_id=agent_id,
    environment=environment,
    extra_body={"x_agents_core": {
        "model_provider": model_provider,
        "environment": {"environment_template_id": template_id},
    }},
)
```

The same extension works with `environment={"type": "openai_hosted"}`. Do not repeat a field in both `environment` and the extension. Setup runs with the daemon's account permissions. Deployment model keys are never sent to your machine.

Before the first Turn the daemon copies these sources into a snapshot. Reconnecting reuses the snapshot even after you edit the sources; a new Session takes a new snapshot. The [preparation contract](../../contracts/agents-api/environments.md#runtime-capability-preparation) lists fields, merge rules, snapshot behavior and failures.

## Operate the installation

The installation's `bin/oac-daemon` finds its own installation. Use it for:

| Command | Effect |
| --- | --- |
| `oac-daemon start` | Validate the installed Harnesses and start the daemon in the background |
| `oac-daemon status` | Show the local profile and process; not the connection |
| `oac-daemon logs -n 100`, `oac-daemon logs -f` | Print or follow the daemon log |
| `oac-daemon stop` | Stop the daemon |

If you set `OAC_RUNTIME_HOME`, use the same value for every command. Check the connection under **Host connection** on the Session's page in Web, or with the [connection status](../../contracts/agents-api/environment-executor-credentials.md#connection-status).

To add a Harness, run the install command again (a fresh copy from Web if it has expired) with the same installation directory and the Harness to add. The installer checks the existing contents, adds only missing components and keeps the Harnesses already installed. Restart a running daemon afterwards so it discovers the new Harness.

Stopping the daemon, cancelling a Turn or deleting the Session never removes the machine's workspace, native history or capability snapshot. An installation from another daemon version, or one whose files were changed, is refused. The installer never upgrades, repairs or migrates it; install into a separate directory.

## Rotate or revoke

In Web, the Session's **Executor credentials** list the machine's credential:

| Action | Effect |
| --- | --- |
| **Rotate** | The credential gets a new secret; the old secret stops working at once. On a revoked credential the action is **Restore** |
| **Revoke** | The credential stops working at once |

To reconnect after a rotation, stop the daemon with `oac-daemon stop`, replace the JSON at its configured credential-file path with the new credential, and run `oac-daemon start`. Do not run `install` again over the existing installation, and rotate the existing credential rather than issuing a second one, which [cannot connect](../../contracts/agents-api/environment-executor-credentials.md#revoked-or-rotated-credential).

In an archived Project, credentials cannot be issued or rotated; revocation remains available. Operators can manage credentials with the Core key; see the [credential contract](../../contracts/agents-api/environment-executor-credentials.md#core-key-routes).

## Install from an extracted distribution

The same installer accepts an already extracted distribution and a credential file issued by an operator, without the install command:

```sh
./oac-daemon install --non-interactive --harness codex \
  --install-dir "$HOME/.oac/my-runtime" \
  --remote 'wss://core.example/api/v1/agent-daemon/ws' \
  --environment-id '11111111-2222-4333-8444-555555555555' \
  --workspace "$HOME/workspace" \
  --credential-file "$HOME/executor-credential.json"
"$HOME/.oac/my-runtime/bin/oac-daemon" start
```

Use the Session's `remote_url` and Environment ID. In PowerShell, run `.\oac-daemon.exe` with native absolute paths. This mode needs an existing workspace and does not start the daemon until you run `start`.
