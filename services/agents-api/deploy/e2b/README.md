# User-managed E2B Runtime

The application creates, renews and destroys its own E2B sandbox using the
maintained E2B SDK. Core receives neither the E2B API key nor an allocation
request. The sandbox runs the existing V1 daemon, selected native harness, tools
and workspace together. Codex, Claude Code and MiniMax Code use the same startup
contract and their respective qualified Runtime images.

This deployment uses Parsar daemon enrollment, not Codex `exec-server` or Noise.
Public execution and Files/Artifacts continue through Core and the daemon;
E2B commands/files are used only for application-controlled deployment and
inspection. A public Session deletion does not destroy the user-owned VM.

## Build the packaged Runtime

Use Python 3.12+, Docker and a qualified Linux amd64 Runtime image containing the
environment-aware daemon `connect` command. Keep keys and build outputs outside
the checkout, in private directories. Install the pinned SDK from this directory:

```sh
python -m venv "$HOME/.parsar/build/e2b-sdk"
"$HOME/.parsar/build/e2b-sdk/bin/pip" install -r services/agents-api/deploy/e2b/requirements.txt
"$HOME/.parsar/build/e2b-sdk/bin/python" services/agents-api/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_RUNTIME_IMAGE_DIGEST \
  --name your-runtime-build \
  --api-key-file "$HOME/.parsar/secrets/e2b.key" \
  --output "$HOME/.parsar/build/e2b-template.json"
```

The builder preserves the existing image's binaries, native configuration and
private workspace layout. Its `template` output is an immutable
`templateID:build_UUID`; use that exact value. Each engine needs its qualified
image/build. No E2B account key, executor key or model credential belongs in a
build, template environment, metadata, command argument or log.

## Start an existing self-hosted Environment

Create a public `self_hosted` Environment through Core and retain its ID and exact
returned `remote_url`. Obtain an authorized connect-only executor key scoped to
that Environment (or its owning principal) through the operator credential flow.
The key JSON is `{"key_id":"UUID","executor_token":"SECRET"}` with an optional
`environment_id` restriction. Store both this JSON and the separate E2B API key
in private files with mode `0600`.

The current packaged profile uses public `/workspace`, backed by
`/environment/workspace`. The remote endpoint must be reachable from the VM;
use the returned `wss://.../api/v1/agent-daemon/ws` unchanged. The native profile
comes from the image, and model credentials arrive through authenticated Core
execution. Do not supply the old Core allocation/Bootstrap JSON or `auth.json`.

Generate and retain an application launch UUID once. `launch.py` is a thin SDK
example, not a service or a replacement lifecycle owner:

```sh
"$HOME/.parsar/build/e2b-sdk/bin/python" services/agents-api/deploy/e2b/launch.py \
  --template 'TEMPLATE_ID:BUILD_UUID' \
  --remote-url 'RETURNED_REMOTE_URL' \
  --environment-id 'RETURNED_ENVIRONMENT_UUID' \
  --launch-id 'YOUR_APPLICATION_LAUNCH_UUID' \
  --executor-key-file "$HOME/.parsar/secrets/executor-key.json" \
  --api-key-file "$HOME/.parsar/secrets/e2b.key" \
  --record "$HOME/.parsar/runtimes/YOUR_APPLICATION_LAUNCH_UUID.json" \
  --timeout 7200
```

Choose a lease supported by your E2B account and renew it before expiry. The
example creates an exclusive private launch record before Create, stores the
returned sandbox ID before startup, and sets `on_timeout=kill` with auto-resume
disabled. Metadata contains only the application launch ID and Environment ID.
It never repeats Create/start, replaces a sandbox or deletes failure evidence.
Reusing the record path rejects before any cloud call. Do not bypass that guard
by supplying a new path after an uncertain result.

## Inspect, renew and destroy

The application remains responsible for the lease and cleanup, including after
Session deletion or daemon failure. These SDK calls use the retained exact ID
and do not connect to, resume or recreate a sandbox:

```python
import json
from pathlib import Path
from e2b import Sandbox

record = json.loads(Path('/private/launch.json').read_text())
api_key = Path('/private/e2b.key').read_text().strip()
sandbox_id = record['sandbox_id']
info = Sandbox.get_info(sandbox_id, api_key=api_key)
assert info.metadata['parsar_launch_id'] == record['launch_id']
assert info.metadata['parsar_environment_id'] == record['environment_id']
Sandbox.set_timeout(sandbox_id, 7200, api_key=api_key)  # When renewing the live VM.
# When the application is finished, or explicitly abandons this allocation:
Sandbox.kill(sandbox_id, api_key=api_key)
```

If Create's response was lost before its ID was saved, discover candidates using
`Sandbox.list(query=SandboxQuery(metadata={'parsar_launch_id': launch_id}),
api_key=api_key)`, importing `SandboxQuery` from `e2b`. Consume pages while
`paginator.has_next` via `paginator.next_items()`. Verify both metadata fields
against the private record, retain every matching provider ID, and explicitly
inspect or destroy those allocations. An empty lookup is not permission to retry
an uncertain Create. Do not select an arbitrary candidate or rotate its identity.

An uncertain startup result requires inspection of the same VM or explicit
cleanup. Root-only `/root/.parsar/e2b/launch.json` records the startup claim;
`ready.json` records only successful process handoff (`daemon_started`), even if
the daemon subsequently exits. Neither proves enrollment, native readiness or a
successful Turn. Check public Core Environment status and the private daemon log
at `/home/runtime/.parsar/parsar-daemon/default/daemon.log`. The daemon's separate
`/home/runtime/.parsar/parsar-daemon/environment.json` records the verified
Environment/Session binding. Keep these records and native history on failure.
Do not rerun `init.py`; it refuses any claimed attempt, including interrupted ones.
The SDK's `Sandbox.connect` can resume paused sandboxes, so it is not used as an
automatic recovery/inspection step here. VM expiry destroys volatile history;
never claim a replacement VM recovered the original Session.

## Startup and security boundary

The protected image initializer uses the image's explicit environment, restores
E2B-finalized executable/service permissions, locks the unused privileged `user`
account, and binds `/workspace`. It writes the executor key to a mode-`0600` file
inside the protected daemon directory, then starts the existing daemon as UID
1000 with that file path. The input is removed before startup. No bearer enters
the daemon's argv or inherited environment. Enrollment and immutable local binding
remain daemon responsibilities; startup does not invent device/Session IDs.

E2B clears `/run` at boot, so startup records live under `/root/.parsar/e2b`.
Native sandboxing remains mandatory. Each actual template must verify private
credential/history isolation, protected binary/config ownership, privilege denial
and stopped descendant effects, rather than infer safety from file modes alone.
The one-shot receipt cannot be reused to replace the daemon or overwrite history.

## Verification scope

Run the focused local tests with the pinned SDK environment:

```sh
python -m unittest discover -s services/agents-api/deploy/e2b -p '*_test.py' -v
```

These are controlled startup-contract tests: input binding, protected key output,
unchanged URL, no secret in argv/environment/record, one-shot claim, and retained
provider ID on unknown outcomes. They do not create billable resources or qualify
E2B security, enrollment or model execution. The [qualification record](../../../../contracts/agents-api/user-managed-runtime-v1.md)
identifies actual three-harness deployment results and their verification limits,
including shared Core credential lifecycle checks and explicit application-owned
cleanup. `tests/official_user_runtime.py` supplies the shared
public execution checks. The former Core-managed `official_e2b_v1.py` fixture is
retired; its original source and evidence remain in Git history.
