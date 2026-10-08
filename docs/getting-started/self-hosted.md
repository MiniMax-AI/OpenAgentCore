---
title: "Self-hosted executors"
---

A `self_hosted` Session runs on a machine your application owns: a workstation, a VM or a sandbox you manage. The application creates the Session through `/v1` and receives a command that installs `oac-daemon`, starts it and connects it to Core. Web shows the same command on the Session's page; it is optional. Core never creates, stops or reclaims the machine.

**The daemon is not a sandbox.** Tools run with the permissions of the account that starts it and can reach whatever that account can. Use a container or VM when you need isolation; see [Runtime and outer isolation](../concepts.md#runtime-and-outer-isolation). The daemon does not restrict network access, so a Template that requires a network policy is rejected for a self-hosted Session.

The Session's Harness runs on the deployment's agent host and reads files and runs tools on this machine through the machine's [Sandbox I/O service](../sandbox-bootstrap.md), so no model key reaches the machine. The machine gets an executor credential that works for this one Environment and nothing else.

## Platforms

Self-hosted installations support Linux amd64 only. On other platforms, including macOS, Windows and Linux arm64, `oac-daemon install` and `oac-daemon start` return `UnsupportedPlatformError`; installation refuses before any credential claim.

The installer contains the `oac-daemon` launcher, `oac-sandbox-io` and their version and platform metadata. Harnesses and their dependencies are packaged on the agent host; the self-hosted machine does not install them.

The machine needs:

- HTTPS access to Core, and to the release download host unless Core carries an offline copy of the installers. Core's [public URL](../configuration.md#changing-the-public-url) must be https, or Core refuses the machine's enrollment;
- Bash for environment setup and MiniMax Code tools;
- Python and pip when the Session's packages need them;
- any system packages your setup needs. The daemon never runs apt, sudo or another elevation command, so install them through the host's normal administration.

Docker is not required. The installer creates the Session workspace, `/environment/{workspace,initialization,packages}` and `/home/runtime` and verifies that its account can write them. If that account cannot create the required directories under `/environment` and `/home`, an administrator must prepare those directories with suitable ownership before installation; the installer does not elevate privileges. The download command also uses `curl`, `tar`, `gzip`, a SHA-256 tool and `flock`. The Runtime home must allow executable files to run. On a `noexec` mount, choose another absolute directory with `OAC_RUNTIME_HOME` before running the command.

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
   print(installation["commands"]["posix"])
   ```

2. Run the command on the target machine with the account that should run the tools. It downloads the installer matched to this Core, verifies its checksum, asks for the installation directory, installs the launcher and Sandbox I/O service, prepares the required directories, starts the launcher and checks its connection.
3. Send a Turn. A connected machine proves only authentication; the first Turn checks the Harness and the model.

In Web, open the Session and copy the command under **Connect a host**.

Keep the command private: it carries a short-lived [installation grant](../../contracts/agents-api/environment-executor-credentials.md#installation-grant) that claims the machine's credential. When it has expired, read the Session again or copy a fresh command from Web.

The installer reports three results:

| Result | Meaning |
| --- | --- |
| **Installation** | The launcher and Sandbox I/O service are installed and their required directories are writable |
| **Host connection** | Core confirmed that the machine serves this Environment over the [sandbox Link](../sandbox-link-protocol.md) |
| **Model configuration** | Not checked; the first Turn uses the Session's model provider |

Downloads retry temporary network failures up to three attempts and show progress in a terminal. Disk space is checked before downloading, extracting and copying components. If a download or installation is interrupted, rerun the command: it clears unfinished temporary copies while preserving completed components, credentials and the workspace. Download staging lives in `native-download` under the Runtime home; its small `download.lock` file remains for concurrency control. An active download or installation is never cleared by another run. If the command expires, copy a fresh one from the Session.

If the connection is not confirmed within 45 seconds, the installer prints the path of the daemon's log. The daemon keeps reconnecting. Fix the cause and run the install command again with the same installation directory, copying a fresh one from Web if it has expired: completed components and the credential are kept and a running daemon is reused. Do not remove the workspace or the Session to retry.

### Options for automation

Append these to the command:

| Option | Effect |
| --- | --- |
| `--non-interactive` | Never prompt; missing input fails |
| `--install-dir ABS` | Installation directory. Default: `environments/<environment-id>` under `~/.oac`, or under `OAC_RUNTIME_HOME` when set |

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

Paths are absolute. Before the machine connects, fill these directories on the machine. The Environment owner on the agent host validates and reads them through the sandbox Link; naming a directory does not mount it or create a sandbox.

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

The same extension works with `environment={"type": "openai_hosted"}`. Do not repeat a field in both `environment` and the extension. Setup runs with the daemon's account permissions.

Before the first Turn the Environment owner prepares a capability snapshot through the sandbox Link. Reconnecting reuses the snapshot even after you edit the sources; a new Session takes a new snapshot. The [preparation contract](../../contracts/agents-api/environments.md#runtime-capability-preparation) lists fields, merge rules, snapshot behavior and failures.

## Operate the installation

The installation's `bin/oac-daemon` finds its own installation. Use it for:

| Command | Effect |
| --- | --- |
| `oac-daemon start` | Enroll the machine and start the daemon in the background. The daemon runs the [Sandbox I/O service](../sandbox-bootstrap.md) and, when it exits, enrolls again and restarts it |
| `oac-daemon logs -n 100`, `oac-daemon logs -f` | Print or follow the daemon log |
| `oac-daemon stop` | Stop the daemon |

If you set `OAC_RUNTIME_HOME`, use the same value for every command. Check the connection under **Host connection** on the Session's page in Web, or with the [connection status](../../contracts/agents-api/environment-executor-credentials.md#connection-status).

Stopping the daemon, cancelling a Turn or deleting the Session does not remove the machine's workspace. Native Harness history belongs to the agent host. An installation from another daemon version, or one whose files were changed, is refused. The installer never upgrades, repairs or migrates it; install into a separate directory.

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
./oac-daemon install --non-interactive \
  --install-dir "$HOME/.oac/my-runtime" \
  --remote 'wss://core.example/api/v1/agent-daemon/ws' \
  --environment-id '11111111-2222-4333-8444-555555555555' \
  --workspace "$HOME/workspace" \
  --credential-file "$HOME/executor-credential.json"
"$HOME/.oac/my-runtime/bin/oac-daemon" start
```

Use the Session's `remote_url`, Environment ID and workspace. `--workspace` prepares that directory during installation; the launcher does not persist a second workspace setting. This mode does not start the launcher until you run `start`.
