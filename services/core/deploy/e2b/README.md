# E2B Runtime template and application-managed launch

An E2B template packages a qualified Runtime image so that its daemon, native Harnesses and workspace run together in one E2B sandbox. The template serves two ownership models:

- **Core-managed** (`openai_hosted`): the deployment selects E2B as its Sandbox Provider and Core creates, renews and destroys sandboxes from the template. [Sandbox deployment](../../../../contracts/agents-api/sandbox-deployment.md) owns the selection and the [E2B helper](../../tools/e2b-provider/README.md) owns the adapter.
- **Application-managed** (`self_hosted`): the application creates, renews and destroys its own sandbox and enrolls the daemon into a `self_hosted` Environment. Core receives neither the E2B account key nor an allocation request, and deleting the Session leaves the sandbox running.

This page covers building the template and the application-managed launch. Execution and Files always go through Core and the daemon; E2B commands and files are used only to start and inspect the sandbox.

## Build a template

Use Python 3.12 or newer, Docker and a qualified Linux amd64 Runtime image. Keep keys and build outputs outside the checkout, in private directories. Install the pinned SDK (`e2b` 2.51.0, [`requirements.txt`](requirements.txt)) and build:

```sh
python -m venv "$HOME/.oac/build/e2b-sdk"
"$HOME/.oac/build/e2b-sdk/bin/pip" install -r services/core/deploy/e2b/requirements.txt
"$HOME/.oac/build/e2b-sdk/bin/python" services/core/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_RUNTIME_IMAGE_DIGEST \
  --name your-runtime-build \
  --api-key-file "$HOME/.oac/secrets/e2b.key" \
  --output "$HOME/.oac/build/e2b-template.json"
```

[`build-template.py`](build-template.py) requires a `sha256:` image ID, a Linux amd64 image and the Runtime layout (`OAC_RUNTIME_WORKSPACE=/environment/workspace`). It copies the image's `/usr/local/bin`, `/opt` and, when present, `/usr/local/codex-resources` and `/etc/codex`, together with its `HOME` and `OAC_*` environment, onto a digest-pinned `node:22.23.1-bookworm-slim` base with `ca-certificates`, `bash`, `git`, `python3`, `python3-pip`, `ripgrep` and `util-linux`. It installs [`init.py`](init.py) and [`managed_init.py`](managed_init.py) read-only under `/opt/oac-e2b`, runs the daemon as UID/GID 1000 (`runtime`, home `/home/runtime`) and builds with 2 vCPUs and 2048 MiB. Only the `usr`, `usr/local` and `etc` archive ancestors it creates get traversable modes; Runtime file modes, private build contexts, key inputs and the output's umask stay unchanged.

The output file records `template` (the immutable `templateID:build_UUID`), the source `image`, the packaged `runtime_sha256` and the `base`. Use that exact `template` value. The template must qualify every Harness its image advertises. Install system dependencies into the image; the daemon runs as UID/GID 1000. No E2B account key, executor key or model credential belongs in a build, template environment, metadata, command argument or log.

## Launch an application-managed Runtime

1. Create a `self_hosted` Environment through Core and keep its ID and the exact returned `remote_url` (`wss://…/api/v1/agent-daemon/ws`). The VM must reach it. Set `workspace_directory` to `/workspace`, which the template binds to `/environment/workspace`.
2. Obtain a connect-only executor key for that Environment; [Environment executor credentials](../../../../contracts/agents-api/environment-executor-credentials.md) owns issuance. The key file is `{"key_id":"UUID","executor_token":"SECRET"}`, with an optional `environment_id` restriction. Store it and the E2B API key in private files with mode `0600`.
3. Generate an application launch UUID once and keep it. Run [`launch.py`](launch.py), a thin SDK example rather than a service:

```sh
"$HOME/.oac/build/e2b-sdk/bin/python" services/core/deploy/e2b/launch.py \
  --template 'TEMPLATE_ID:BUILD_UUID' \
  --remote-url 'RETURNED_REMOTE_URL' \
  --environment-id 'RETURNED_ENVIRONMENT_UUID' \
  --launch-id 'YOUR_APPLICATION_LAUNCH_UUID' \
  --executor-key-file "$HOME/.oac/secrets/executor-key.json" \
  --api-key-file "$HOME/.oac/secrets/e2b.key" \
  --record "$HOME/.oac/runtimes/YOUR_APPLICATION_LAUNCH_UUID.json" \
  --timeout 7200
```

The example creates the private launch record exclusively before Create, so reusing a record path fails before any cloud call. It saves the sandbox ID before startup and creates the sandbox with `on_timeout=kill` and auto-resume disabled; its metadata holds only `oac_launch_id` and `oac_environment_id`. It never repeats Create or startup, replaces a sandbox or deletes failure evidence. After an uncertain result, inspect the record and the sandbox; do not start again with a new record path. Choose a lease your E2B account supports and renew it before it expires.

## Inspect, renew and destroy

The application owns the lease and cleanup, including after Session deletion or daemon failure. These SDK calls use the recorded sandbox ID and never connect to, resume or recreate a sandbox:

```python
import json
from pathlib import Path
from e2b import Sandbox

record = json.loads(Path('/private/launch.json').read_text())
api_key = Path('/private/e2b.key').read_text().strip()
sandbox_id = record['sandbox_id']
info = Sandbox.get_info(sandbox_id, api_key=api_key)
assert info.metadata['oac_launch_id'] == record['launch_id']
assert info.metadata['oac_environment_id'] == record['environment_id']
Sandbox.set_timeout(sandbox_id, 7200, api_key=api_key)  # When renewing the live VM.
# When the application is finished, or explicitly abandons this allocation:
Sandbox.kill(sandbox_id, api_key=api_key)
```

If Create's response was lost before its ID was saved, list candidates with `Sandbox.list(query=SandboxQuery(metadata={'oac_launch_id': launch_id}), api_key=api_key)` (`SandboxQuery` comes from `e2b`) and read every page while `paginator.has_next`, using `paginator.next_items()`. Check both metadata fields against the record, keep every matching sandbox ID and inspect or destroy each of those sandboxes. An empty listing does not permit another Create. Do not pick one candidate arbitrarily.

After an uncertain startup, inspect the same VM or destroy it. The SDK's `Sandbox.connect` can resume a paused sandbox, so do not use it to inspect. The VM keeps these records:

| Path | Content |
| --- | --- |
| `/root/.oac/e2b/launch.json` | The startup claim, written before any other startup step |
| `/root/.oac/e2b/ready.json` | Process handoff only (`daemon_started`, with the daemon PID), even if the daemon exits later |
| `/home/runtime/.oac/daemon/default/daemon.log` | The daemon log |
| `/home/runtime/.oac/daemon/environment.json` | The daemon's verified Environment and Session binding |

Neither record proves enrollment, native readiness or a successful Turn; check the Environment status in Core. Keep the records and native history after a failure. `init.py` refuses to run again once any launch record exists. VM expiry destroys the history in it; a replacement VM never recovers the original Session.

## Startup and security boundary

`init.py` runs once as root. It restores the ownership and modes that E2B finalization changes under `/usr/local` and on `envd`, `/etc/inittab` and `/etc/init.d/rcS`, locks E2B's passwordless `user` account, checks that the image environment is not already bound to an Environment or Session and bind-mounts `/environment/workspace` at `/workspace`. It writes the executor key to `/home/runtime/.oac/daemon/executor-key.json` (mode 0600, owned by UID 1000), deletes the startup input and starts `oac-daemon connect --profile default --remote … --environment-id … --credential-file …` as UID/GID 1000. No credential enters the daemon's arguments or inherited environment. The daemon owns enrollment and the local binding. E2B clears `/run` at boot, so the records live under `/root/.oac/e2b`.

The E2B VM is the isolation boundary; tools have UID 1000's access to Runtime state ([Runtime and outer isolation](../../../../docs/concepts.md#runtime-and-outer-isolation)).

## Tests

```sh
make check-e2b-provider
```

With `OAC_TEST_E2B_SDK_PYTHON` pointing at the pinned SDK environment, this runs this directory's tests and the helper's. They cover startup input binding, the protected key file, the unchanged URL, credentials kept out of arguments, environment and records, the one-shot claim, and retained sandbox IDs after unknown outcomes. They create no billable resources. `make check-core` runs `managed_init_test.py` with only the standard library.
