# Self-hosted executors

A `self_hosted` Session runs on a machine the application owns, the executor host,
instead of a node. The application creates the Session through `/v1`; the
administrator issues an executor credential for it in Web; one command on the host
starts a Runtime container that connects to Core. The Runtime is the same one nodes
use: daemon, native harness and workspace.

An executor credential works for one Session's environment only. It can't call the
Agent API, the Core API or node enrollment.

## Before you start

- **Core has an HTTPS public URL.** The Session's `remote_url` is
  `wss://<public host>/api/v1/agent-daemon/ws`, and the installer connects only over
  `wss://`. Web shows no command until Core has a reachable public URL.
- **Web holds the Runtime files.** The installer downloads the Runtime launcher and
  image from your console, as nodes do; install Core from the offline bundle.
- **The host** runs Linux amd64 with Python 3.9+, `curl` and `sha256sum`, and Docker
  usable by a non-root user through `/var/run/docker.sock`. It reaches the public URL
  over HTTPS.
- **The Session carries its model provider**, in the request or saved on its Agent.
  Default models never apply to self-hosted Sessions, and creating one without a
  provider fails with 400 `model_provider_required`.

## Connect a host

1. **The application creates the Session**, with its Project API key and its own model
   provider:

   ```python
   import os
   from openai import OpenAI

   client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
   session = client.beta.agents.sessions.create(
       environment={"type": "self_hosted", "workspace_directory": "/workspace"},
       extra_body={
           "agent": {"model": os.environ["MODEL_NAME"], "x_agents_core": {"harness": "codex"}},
           "x_agents_core": {"model_provider": {
               "protocol": "responses",
               "base_url": os.environ["MODEL_BASE_URL"],
               "api_key": os.environ["MODEL_API_KEY"],
           }},
       },
   )
   print(session.id, session.environment.id, session.environment.remote_url)
   ```

2. **In Web**, open **Session log**, then the Session's page. Its
   **Executor credentials** section shows **Connect a host** with the
   **Executor install command**. Copy it and run it on the executor host:

   ```sh
   (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/self-hosted-install.pyz' -o "$d/install.pyz" &&
   printf '%s  %s\n' '<installer-sha256>' "$d/install.pyz" | sha256sum -c --status &&
   python3 "$d/install.pyz" --source-url 'https://core.example' --environment-id '<environment-id>' --remote 'wss://core.example/api/v1/agent-daemon/ws')
   ```

   The command contains no secret. It downloads the installer from your console and
   checks its SHA-256 before running it.
3. **Issue the credential.** Select **Issue credential**, then **Copy credential**.
   Web shows it once.
4. **Paste it at the installer's hidden prompt** and press Enter. The prompt accepts the
   compact or the pretty-printed credential and never echoes it.

The installer downloads the Runtime from the console and checks it, starts a container
named `parsar-selfhost-<32 hex digits>`, and waits until Core confirms that the
Environment is connected. The Session then runs its Turns there. The command is safe
to rerun; it resumes the same installation.

For automation without a terminal, choose **Download credential file**, make it private
(`chmod 600`), and add `--credential-file /absolute/path/executor-key.json` to the
installer line. The path must not go through a symbolic link.

The host keeps its state in `~/.parsar/self-hosted/<environment-id>/`
(`--install-dir` chooses another absolute directory). Keep it with the container's
volumes: they hold the credential, workspace and native history. The Runtime's log is
`docker logs <container>`. A connected Environment proves the connection, not that the
model works; send the Session a Turn to test execution.

Core delivers the Session's model provider only over this Environment's executor
connection. The host keeps it in the Runtime's harness home, where the model's tools
and the Files API can't read it but the host's owner can.

## Rotate or revoke

| Action in Web | Effect |
| --- | --- |
| **Rotate** | The credential gets a new secret; the old secret stops working at once. Rotating a revoked credential restores it |
| **Revoke** | The credential stops working at once |

After either, the Runtime disconnects and stops retrying: its log shows one message
that names the fix, and the container keeps running without a restart loop. To
reconnect the host:

1. **Rotate** the same credential in Web and copy the new secret.
2. Rerun the same install command on the host and paste it.

The installer puts the new credential into the same container, which reconnects with
its workspace and history. Issuing a *new* credential doesn't reconnect an Environment
that has already connected: it stays bound to its first credential, and the installer
says to rotate that one.

To remove the Runtime instead, stop its container:
`docker --host unix:///var/run/docker.sock stop <container>`. Deleting the Session does
not remove containers or volumes on the executor host.

## Without Web

Scripts on the Core host can issue credentials with the Core key through Core's
loopback port. Choose a new UUID for the credential and keep it:

```sh
umask 077
key_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
curl -fsS -X POST \
  -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.parsar/core/secrets/core.key")") \
  -H 'Content-Type: application/json' -d "{\"key_id\":\"$key_id\"}" \
  "http://127.0.0.1:8091/core/v1/projects/$PROJECT_ID/environments/$ENVIRONMENT_ID/executor-credentials" \
  -o executor-key.json
```

Rotate with `{"key_id":"…","rotate":true}` on the same route; revoke with
`DELETE …/executor-credentials/<key_id>`. After an uncertain response, list the
credentials with `GET` before trying again. The
[credential contract](../../contracts/agents-api/environment-executor-credentials.md)
has every rule and error.
