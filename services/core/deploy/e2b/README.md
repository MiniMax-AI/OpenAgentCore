# E2B Runtime deployment

E2B supports two separate ownership choices. Core-managed `openai_hosted` uses
the deployment-wide E2B Provider. Application-managed `self_hosted` uses the
startup example below; in that path the application creates, renews and destroys
its own sandbox, and Core receives neither the E2B account key nor an allocation
request. The sandbox runs the existing V1 daemon, selected native harness, tools
and workspace together. Codex, Claude Code and MiniMax Code use the same startup
contract and their respective qualified Runtime images.

This deployment uses OpenAgentCore daemon enrollment, not Codex `exec-server` or Noise.
Public execution and Files/Artifacts continue through Core and the daemon;
E2B commands/files are used only for deployment, initialization and inspection.
A public Session deletion does not destroy an application-owned VM; managed
compute cleanup remains Core's responsibility.

## Core-managed hosted deployment

Select E2B in Core's hosted deployment setup and supply the account key and an
immutable `templateID:build_UUID`. One deployment uses one managed Provider;
E2B needs no physical node enrollment. Changing backend requires explicit reset
and confirmed cleanup of every owned allocation, pending creation and snapshot.
Do not infer execution readiness from saved configuration or running compute.

The pure-Go adapter invokes the packaged official Python SDK helper. The Core
image and native installation include its runtime dependencies; configuring E2B
does not require installing Python or pip on the server. The account key is
write-only server configuration, passed to the helper over stdin. It never enters
a template, command argument, inherited environment, receipt or public response.

Keep the helper's private receipt directory on durable storage, owned by the Core
service user with mode 0700. Manual deployments using the default non-root Core
image must give its service UID ownership of that directory. SDK connection
credentials and one-shot allocation claims are stored there; they are not Core
execution state. Preserve them across upgrades and failures. An empty cloud
lookup cannot settle an unknown Create, and inspection never creates, resumes
or reboots compute. Initialization uncertainty requires reclaiming the original
allocation rather than replaying startup. See the [helper contract](../../tools/e2b-provider/README.md).

Rebuild old templates with this directory's `build-template.py` before managed
use: it includes protected `init.py` and `managed_init.py`. The latter consumes
Core's existing managed daemon auth profile, while application-managed startup
continues to use the executor credential flow below. Both reuse the same daemon,
native harnesses and colocated workspace. A qualified combined Runtime image can
support several harnesses; provider selection is independent of harness choice.

## Build the packaged Runtime

Use Python 3.12+, Docker and a qualified Linux amd64 Runtime image containing the
environment-aware daemon `connect` command. Keep keys and build outputs outside
the checkout, in private directories. Install the pinned SDK from this directory:

```sh
python -m venv "$HOME/.oac/build/e2b-sdk"
"$HOME/.oac/build/e2b-sdk/bin/pip" install -r services/core/deploy/e2b/requirements.txt
"$HOME/.oac/build/e2b-sdk/bin/python" services/core/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_RUNTIME_IMAGE_DIGEST \
  --name your-runtime-build \
  --api-key-file "$HOME/.oac/secrets/e2b.key" \
  --output "$HOME/.oac/build/e2b-template.json"
```

The builder preserves the existing image's binaries, native configuration and
private workspace layout. Its `template` output is an immutable
`templateID:build_UUID`; use that exact value. The build must qualify every harness it advertises; a combined Runtime image
can include several harnesses. No E2B account key, executor key or model credential belongs in a
build, template environment, metadata, command argument or log. The builder gives
traversable modes only to the public archive ancestors it creates (`usr`,
`usr/local`, `etc`). Runtime file and directory modes, private build contexts,
key inputs and the umask of its output stay unchanged, including under umask 077.

System dependencies must be installed when building the template. The builder may
use root during image construction, but the running daemon remains UID/GID 1000
with no automatic apt, sudo or privilege escalation. Runtime `system_packages`
is unsupported; missing dependencies fail their consuming operation. The template
no longer installs bubblewrap or socat for inner sandboxing.

## Application-managed: start an existing self-hosted Environment

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
execution. Managed startup uses the separate [Runtime bootstrap contract](../../../../docs/runtime-bootstrap.md).

Generate and retain an application launch UUID once. `launch.py` is a thin SDK
example, not a service or a replacement lifecycle owner:

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
assert info.metadata['oac_launch_id'] == record['launch_id']
assert info.metadata['oac_environment_id'] == record['environment_id']
Sandbox.set_timeout(sandbox_id, 7200, api_key=api_key)  # When renewing the live VM.
# When the application is finished, or explicitly abandons this allocation:
Sandbox.kill(sandbox_id, api_key=api_key)
```

If Create's response was lost before its ID was saved, discover candidates using
`Sandbox.list(query=SandboxQuery(metadata={'oac_launch_id': launch_id}),
api_key=api_key)`, importing `SandboxQuery` from `e2b`. Consume pages while
`paginator.has_next` via `paginator.next_items()`. Verify both metadata fields
against the private record, retain every matching provider ID, and explicitly
inspect or destroy those allocations. An empty lookup is not permission to retry
an uncertain Create. Do not select an arbitrary candidate or rotate its identity.

An uncertain startup result requires inspection of the same VM or explicit
cleanup. Root-only `/root/.oac/e2b/launch.json` records the startup claim;
`ready.json` records only successful process handoff (`daemon_started`), even if
the daemon subsequently exits. Neither proves enrollment, native readiness or a
successful Turn. Check public Core Environment status and the private daemon log
at `/home/runtime/.oac/daemon/default/daemon.log`. The daemon's separate
`/home/runtime/.oac/daemon/environment.json` records the verified
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

E2B clears `/run` at boot, so startup records live under `/root/.oac/e2b`.
The E2B VM is the outer isolation boundary. The daemon and native harness add no
inner filesystem, permission or network sandbox; tools have UID 1000's access to
Runtime state. Verify the actual outer boundary, process cleanup and native
execution for each template. Prior private-file denial results describe the old
inner sandbox and do not qualify the current behavior.
The one-shot receipt cannot be reused to replace the daemon or overwrite history.

## Verification scope

Run the focused local tests with the pinned SDK environment:

```sh
python -m unittest discover -s services/core/deploy/e2b -p '*_test.py' -v
```

These are controlled startup-contract tests: input binding, protected key output,
unchanged URL, no secret in argv/environment/record, one-shot claim, and retained
provider ID on unknown outcomes. They do not create billable resources or qualify
E2B security, enrollment or model execution. The [qualification record](../../../../contracts/agents-api/harness-capabilities.md)
records historical three-harness deployment results and their verification limits,
including shared Core credential lifecycle checks and explicit application-owned
cleanup. `tests/official_user_runtime.py` supplies the shared
public execution checks. The former Core-managed `official_e2b_v1.py` fixture is
retired; its original source and evidence remain in Git history.
