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
- **Web holds the installer and the Runtime files.** The command downloads the
  installer, the Runtime launcher and the image from Core's public URL, which the
  reverse proxy sends to Web (in a [split deployment](install.md#split-deployment), to
  the Web host); install from the offline bundle. A console without the self-hosted
  installer shows no **Connect a host** section at all.
- **The host** runs Linux amd64 with Python 3.9+, `curl`, `sha256sum` and the Docker
  CLI, with Docker usable through `/var/run/docker.sock` by the non-root user that runs
  the installer; the installer refuses root. It reaches the public URL over HTTPS.
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

2. **In Web**, open **Session log**, then the Session's page. In its
   **Executor credentials** section, **Connect a host** shows a command in a
   **Terminal** block; copy it and run it on the executor host:

   ```sh
   (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/self-hosted-install.pyz' -o "$d/install.pyz" &&
   printf '%s  %s\n' '<installer-sha256>' "$d/install.pyz" | sha256sum -c --status &&
   python3 "$d/install.pyz" --source-url 'https://core.example' --environment-id '<environment-id>' --remote 'wss://core.example/api/v1/agent-daemon/ws')
   ```

   The command contains no secret. It downloads the installer from your console and
   checks its SHA-256 before running it.
3. **Issue the credential.** Select **Issue credential**. The **Executor credential**
   dialog shows it once: choose **Copy credential**.
4. **Paste it at the installer's hidden prompt** and press Enter. The prompt accepts the
   compact or the pretty-printed credential and never echoes it. Then choose **Done** in
   the dialog; after that the credential can't be shown again. Closing the dialog with
   × keeps the credential on the page until you choose **Done**.

The installer downloads the Runtime from the console and checks it, starts a container
named `oac-selfhost-<32 hex digits>`, and waits until Core confirms that the
Environment is connected. The Session then runs its Turns there. Rerunning the same
command resumes the same installation. It refuses, with "This installation belongs to
another Environment or distribution", once Core runs another release or its public URL
changed: the host's installation is tied to both. Moving an executor to a new release
or address isn't supported yet. A fresh installation would start a new container
without the old workspace and native history, so the Session can't continue there:
stop the old container, create a new self-hosted Session and connect a host for it.

For automation without a terminal, choose **Download credential file**, which saves
`executor-credential-<first 8 characters of the environment ID>.json`, make it private
(`chmod 600`), and add `--credential-file /absolute/path/to/that-file.json` to the
installer line. The path must not go through a symbolic link.

The host keeps its state in `~/.oac/self-hosted/<environment-id>/`
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

In an archived project, Web shows "This project is archived, so executor credentials
can't be issued or rotated." Only **Revoke** remains; a host that already holds a
credential keeps it when you rerun the command.

## Without Web

Scripts on the Core host can issue credentials with the Core key through Core's
loopback port (`ports.core` in `config.json`, 8091 by default). Choose a new UUID for
the credential and keep it:

```sh
key_id=$(python3 -c 'import uuid; print(uuid.uuid4())'); echo "credential ID: $key_id"
(umask 077; curl -fsS -X POST \
  -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.oac/core/secrets/core.key")") \
  -H 'Content-Type: application/json' -d "{\"key_id\":\"$key_id\"}" \
  "http://127.0.0.1:8091/core/v1/projects/$PROJECT_ID/environments/$ENVIRONMENT_ID/executor-credentials" \
  -o executor-key.json)
```

Rotate with `{"key_id":"…","rotate":true}` on the same route; revoke with
`DELETE …/executor-credentials/<key_id>`. After an uncertain response, list the
credentials with `GET` before trying again. The
[credential contract](../../contracts/agents-api/environment-executor-credentials.md)
has every rule and error.


## Executors installed before the rename

An existing `parsar-selfhost-*` container keeps its own image, daemon and
`~/.parsar/self-hosted/<environment-id>` directory. It can keep serving its
Environment after Core is renamed; Core does not upgrade or manage this container.

The new installer refuses when that exact Environment still has a directory under
`~/.parsar/self-hosted`, including when a different `--install-dir` is supplied.
Executors for other Environments are unaffected. To replace it, inspect the old
installation's `started.json` to identify its container, stop and remove that
exact container, and preserve any history you need before removing the old
Environment's installation directory. Then rerun the new verified installer. It
starts `oac-selfhost` with state under `~/.oac/self-hosted/<environment-id>`;
it does not reuse the old executor's volumes or native history.

Credential replacement for executors created by the new installer still updates
and restarts the same owned container. The legacy refusal does not authorize
adopting an old container to replace its credential.
