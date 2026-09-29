# Native self-hosted Runtime

Use the same `oac-daemon` on a user-managed Linux, macOS or Windows machine.
Physical machines, VMs and user-owned sandboxes use the same installer. Core does
not create or reclaim these machines. Core-managed Docker, E2B and microsandbox
Providers remain Linux-only and receive prebuilt Runtime images or templates.

The daemon runs with its starting account's permissions, without an inner sandbox
or privilege elevation. Tools can access whatever that account can access. Use an
outer container or VM when isolation is required. Connection authentication,
Project/Environment authorization, credential permissions, Files path validation,
process cleanup and snapshot consistency still apply; they do not isolate tools
from their own account. `disabled` and `restricted` network modes require an outer
implementation that enforces them.

## Platforms and prerequisites

| Platform | Codex | Claude Code | MiniMax Code |
| --- | --- | --- | --- |
| Linux | Supported | Supported | Supported |
| macOS | Supported | Supported | Supported |
| Windows | Supported | Supported | Unsupported by the current adapter |

Use a distribution built for the machine's OS and architecture. Windows support
is validated on a native CI runner; manual Windows machine acceptance is not yet
recorded. Native Linux/macOS runs and native CI qualify the corresponding bundles.
Supported does not mean every model provider or optional native feature works in
every combination. Session capabilities are checked by the existing Harness contract.

The distribution contains Node 22.22.0/npm and the selected release's components:
Codex 0.153.4, Claude Agent SDK 0.3.269 with native Claude Code 2.1.269 and the
project's adapter, and the patched MiniMax Code 0.4.12 companion on Unix. Arbitrary
official CLI installations are not adopted. They remain untouched while the
installer creates its private, verified copy. Compatible components from this
same installation are reused after checking their version, contents and startup.

Claude on Windows requires Git Bash. Bash is also needed for Runtime setup and
MiniMax tools. Node/npm and MiniMax's ripgrep are included; Python/pip, when needed
by a Session's capability dependencies, must be available. Missing system
components are reported. Install those through the host's normal administration
process; the daemon never runs apt, sudo or an elevation command.

## Install and connect

Create a Session using the public Agents API with `environment.type: "self_hosted"`.
The response retains the official Environment `id` and `remote_url`, and adds
`x_agents_core.installation` with `commands.posix`, `commands.powershell` and
`expires_at`. Execute the command for your target platform. Core Web shows the
same commands in the **Self-hosted** Session's connection section; Web is not a
prerequisite for API callers.

The command downloads the distribution matched to this Core, verifies its archive,
asks which Harnesses to install and where, installs them, starts the daemon and
checks its authenticated connection. The Session's required Harness must remain
selected. Its workspace is frozen at Session creation; the installer creates that
directory if necessary using your existing permissions. To choose a different
workspace, create a Session with that path. No administrator privileges or Docker
are required.

For automation, append `--non-interactive --harness codex` and optionally
`--install-dir ABS` to the command. Multiple Harnesses use a comma-separated value,
for example `--harness codex,claude`. Missing required input fails without prompting.
Interactive installation defaults to a separate directory for each Environment:
`~/.oac/environments/<environment-id>` (or beneath `OAC_RUNTIME_HOME`).

The command carries a 30-minute authorization restricted to this Environment and
Core build. Treat it as a temporary credential. Refresh the Session detail or
copy a fresh Web command after expiry. It cannot execute tasks or read files.
The installer generates a private connect-only credential file before claiming
its key, so a lost response can be retried without losing the credential. The
long-term secret never appears in the command or terminal. A different machine
cannot use the command to replace an already claimed key. Session deletion,
Project archival, expiry or a different Core build invalidates the authorization;
new commands never revive revoked credentials.

Installation reports three separate results: **Installation**, **Daemon
connection**, and **Model configuration**. This workflow does not configure or
validate model access. If connection is not confirmed, inspect the reported local
log and the Session's connection status. Rerun with the same installation directory
to resume; completed components and credentials are retained and an existing
daemon is reused. After authentication failures, check the Environment credential
in Core. Do not remove the workspace or Session history to retry.

Qualified release distributions contain Linux amd64, macOS arm64 and Windows
amd64 installers. Core serves these matched artifacts directly, including in a
private repository deployment. Unsupported platforms fail explicitly. Operators
running a standalone Core binary can set `OAC_NATIVE_INSTALLER_DIR` to its matched
`native-installers` directory. Without qualified artifacts, Session responses
report installation unavailable instead of selecting another version.

## Manual distribution installation

The same installer also accepts an already-extracted distribution and an explicitly
supplied private executor credential, without the bootstrap command:

```sh
./oac-daemon install --non-interactive --harness codex \
  --install-dir "$HOME/.oac/my-runtime" \
  --remote 'wss://core.example/api/v1/agent-daemon/ws' \
  --environment-id '11111111-2222-4333-8444-555555555555' \
  --workspace "$HOME/workspace" \
  --credential-file "$HOME/executor-credential.json"
"$HOME/.oac/my-runtime/bin/oac-daemon" start
```

Use `.\oac-daemon.exe` and native absolute paths in PowerShell. This manual mode
requires an existing workspace and starts only when `start` is invoked. Optional
`--capability-directory ABS` selects snapshot storage; `--tool-env-file ABS`
supplies tool/MCP variables. They do not introduce another installation workflow.
Build distributions on their target OS with `scripts/build-native-installer.mjs`;
`bundle.json` describes release content and checksums, not installation options.

## Add Harnesses and operate the installation

Run the original distribution's install command again with identical connection
options and the Harnesses to add. The installer retains already selected Harnesses,
checks compatible existing contents and adds only missing components. No default
deletion, replacement or upgrade occurs. All installation writes use one lock.
A component is published only after its copy passes checksum verification;
interrupted additions can reuse complete components on the next run. Installation
settings are committed only after all selected Harnesses pass readiness checks.

The installed `bin/oac-daemon` locates its own installation. Use that executable
for `start`, `status`, `logs -n 100`, `logs -f` and `stop`. Direct `connect` is
rejected for an installed Runtime; `start` validates its components and selection. An explicit
`OAC_RUNTIME_HOME` overrides this location; keep it consistent if set. If adding a
Harness while the daemon is running, restart it to refresh Harness discovery.
`status` reports local profile/PID-file information only. Check **Host connection**
in Core or the Environment connection API for authenticated connection state.

A connected Environment proves machine authentication, not model availability.
Configure the model provider through the existing Session/Agent mechanism and
send a Turn to verify execution. Rotate a credential by stopping the daemon,
replacing the JSON at the configured path with the rotated credential for the
same `key_id`, and starting again. Revocation blocks the old credential.

An incompatible daemon version, component version or modified installation is
an explicit error. Use a separate installation directory; there is no old-version
upgrade, migration or automatic repair. Preserve previous files and history.
Stopping a daemon, cancelling a Turn or deleting a Session never removes the
user's machine, workspace, native history or capability snapshot.

## Common preparation and execution

After authenticated connection every Runtime follows the same flow: Harness
availability, workspace and capability preparation, fixed `installed.json`, then
execution, cancellation and recovery. Provider image contents and self-hosted
`capability_directories` enter the same parser and installation result. Adapters
receive Skill paths, Plugin results and MCP declarations from that snapshot.
Reconnect reuses it; new Sessions capture new configuration. Preparation or
recovery errors never trigger silent reinstall, replay or replacement native Sessions.

Runtime initialization/package directories default to `initialization` and
`packages` under the installation. Managed images use their own storage layout
through `OAC_RUNTIME_INITIALIZATION_DIRECTORY` and `OAC_RUNTIME_PACKAGE_DIRECTORY`;
this does not change the preparation protocol or account permissions. npm and
Python dependencies use user-writable prefix/target directories. Setup uses Bash,
including Git Bash on Windows. Supplying `packages.system` in configuration is
rejected even when empty or null; managed images/templates must include system
dependencies before launch. Official API read responses retain the required
`system: []` field, which does not imply support for installing system packages.

On Windows, stdio MCP commands named npm or npx (including explicit .cmd
paths) run through the selected installation's JavaScript entrypoint with Node.
Other batch wrappers require an explicit cmd.exe command and its arguments.
