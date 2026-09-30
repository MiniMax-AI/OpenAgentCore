# Codex colocated Runtime

A managed Session runs the daemon, Codex and workspace in a dedicated outer
Environment. Core stays outside it. Use Codex 0.153.4 and its matching
`codex-resources`. The same daemon supports native self-hosted installations; see
[the self-hosted guide](../../../../docs/getting-started/self-hosted.md#platforms) for platform status.

Codex tools run with the daemon user's existing permissions. There is no inner
filesystem, permission or network sandbox, no immutable Codex requirements file,
and no managed Bash wrapper. Tools may read Runtime state that the user can read.
Model credentials still belong in authenticated Runtime configuration, never image
layers. Do not mount another Session's history, host home, Docker socket or Core
credentials into the Environment.

The existing outer Docker configuration is unchanged: non-root user, dropped
capabilities, no-new-privileges, read-only root, private PID namespace, dedicated
bridge networking, bounded tmpfs and process/memory/CPU limits. Its existing
`nested_sandbox` setting and seccomp file are deployment settings, not evidence
that the daemon adds inner isolation. The container-specific AppArmor exception
in that configuration removes an outer LSM layer; do not change host-wide policy.

Seccomp source: [Moby profiles, revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json), Apache-2.0.
Unmodified source SHA-256:
`785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`.
The retained extra syscall rule is unchanged by removal of the inner sandbox.
Historical qualification of private-file denial under the former native sandbox
does not describe current tool permissions or qualify the new deployment.

The packaged image retains `/environment/workspace`, initialization, package and
staging directories plus daemon/native state under `/home/runtime`. Docker also
mounts the workspace at `/workspace`. Runtime binds public Files/Artifacts to its
configured workspace; native tools execute with ordinary user permissions. The
shared helper artifacts remain in the matched Runtime bundle. The Docker engine
must support volume subpath mounts; its recorded qualification used 29.1.3.

Published Artifacts are immutable Core-owned copies of successful Turn outputs;
listing and downloading them requires no running Environment. Validate actual
execution, file operations, cancellation, process settlement and retained native
history using the current binary and outer deployment. An alive container alone
is not acceptance.

## Managed Runtime image and Docker adapter

The [maintainer guide](../../../../docs/maintainers.md#runtime-images-and-helpers)
builds the Linux amd64 image from the official `@openai/codex@0.153.4-linux-x64`
package. It contains the unmodified native executable and matching resources, and
no product server, product CLI, credentials or workspace data.

The service's `internal/sandbox` interface has five operations. Its Docker adapter
uses the official Moby Go client and an operator-selected immutable image digest,
network, installation UUID and the contents of this directory's `seccomp.json`.
The Runtime authenticates outward through the ordinary daemon bootstrap path;
`CoreURL` includes the existing `/api/v1` gateway prefix. The image's default
entrypoint is the same daemon connect command used by a user-managed Runtime.
No socket, host home or product configuration is mounted inside the Runtime.

The caller persists a fresh allocation UUID with the authorized tenant and
Environment before Create, and serializes lifecycle operations for that allocation.
Create returns the reference even on failure. Duplicate allocation creation does
not rewrite credentials or restart the container. After a lost response, inspect
the allocation and reconcile its actual state; do not blindly replay Create.
Core owns durable allocation reconciliation through its internal managed Runtime
coordinator. Public admission requires the explicit operator configuration below.

Two labelled named volumes retain native state and the workspace/staging pair.
The trusted daemon auth profile is copied with restrictive permissions before
startup; it does not enter image layers, environment variables, labels or arguments.
GetInfo describes observed compute state, not daemon or native readiness. Docker
has no renewable provider lease: Renew verifies the allocation, while Core owns
keepalives and expiry. Kill verifies allocation ownership, removes
the container, explicitly removes its named volumes and confirms absence. Keep the
reference and retry cleanup when an operation fails; an HTTP timeout is not proof
that a resource disappeared. Never use broad container or volume pruning.

Provider commands are limited to bootstrap and resource lifecycle. Runtime owns
initial files, configuration, npm/Python packages, setup and capabilities over its
authenticated protocol. An unconfirmed bootstrap result requires allocation
cleanup before reuse; compute readiness does not prove Runtime readiness.

## Standalone operator configuration

Use the [installation guide](../../../../docs/getting-started/install.md) and
[deployment configuration API](../../../../contracts/agents-api/sandbox-deployment.md).
Web or the deployment administrator API selects Docker, uniform per-sandbox CPU
and memory, and the matched immutable Runtime release in PostgreSQL. Docker does
not accept independent hard root or workspace disk quotas through this contract.
The saved public Core origin must be reachable from sandbox guests.

Enroll an ordinary sandbox node with Web's one-command [Add node](../../../../docs/getting-started/nodes.md)
flow. It fetches and validates the deployment configuration, imports the matched
Runtime image and actively connects to Core. Core can run independently without a
Docker socket; the node owns its local Docker access. Node files contain the installed
selection and host-specific paths, never a separate provider or resource choice. The
Core host joins through the same command as any other host.

Same-provider resource and Runtime edits currently require no active reset,
the current generation and verified zero held allocations or pending Environments.
A backend change requires explicit reset and confirmed cleanup, then a new setup.
See the [reset procedure](../../../../docs/getting-started/nodes.md#change-the-sandbox-configuration).
Stopped compute, snapshots and unknown operations remain blockers. Keep the original
node identity, paths and credentials until cleanup is confirmed. Explicit archive
preserves history and persisted Files/Artifacts but discards unpersisted workspace;
ordinary Session deletion has different retention behavior. Existing Sessions never
migrate to another backend.
Core rejects `AGENTS_API_MANAGED_RUNTIMES_FILE`; restarting or editing
an old file does not replace database configuration ownership.

Node providers use explicit local Unix Docker sockets, ignoring ambient
`DOCKER_HOST`. Optional `extra_hosts` is trusted node configuration. No Docker
socket is mounted in a Runtime. Caller-managed `self_hosted` enrollment retains
its separate public lifecycle.

Hosted Codex needs a model provider: either the Session's
`x_agents_core.model_provider` or the deployment default for `codex`, set with the
Core key in Web or through `PUT /core/v1/harnesses/codex/model-provider`. Core
freezes the bundle in the Session's encrypted snapshot and delivers it as the
common `model_provider` bundle. Runtime automatically adapts Chat Completions or
Anthropic to Codex Responses; a hosted Session without a provider is rejected with 400
`model_provider_required`. Do not place credentials in images.

With the qualified Codex image and Docker provider configured, create an idle or
initial-text Session using `environment: {"type": "openai_hosted"}`. Core commits
its identity before automatically provisioning it. Queries expose durable
connection status; execution separately prepares the native harness. Session
deletion revokes authority before owned container/volume cleanup. The daemon does
not enforce `disabled` or `restricted` networking; combinations without matching
outer enforcement are unsupported. Templates and inline configuration share
initial files, env, npm/Python packages, ordered setup and capabilities; see the
[supported fields and limits](../../../../contracts/agents-api/environment-templates.md).

### Environment initialization

Templates and inline env/setup/npm/Python configuration share the daemon's Go
Runtime preparation implementation on all platforms. The packaged image sets
`OAC_RUNTIME_INITIALIZATION_DIRECTORY=/environment/initialization` and
`OAC_RUNTIME_PACKAGE_DIRECTORY=/environment/packages` to retain its resource layout.
No Python initialization wrapper is shipped. It runs commands directly as UID/GID 1000 using configured tool
variables; it does not inject those variables into the daemon or native harness
launcher. Cancellation and finite receipts remain Runtime responsibilities.

System dependencies must be installed during image/template construction. The
daemon never runs apt, sudo or another privilege escalation, and
`system_packages` is unsupported. A missing executable or library fails the
operation requiring it. The image no longer contains a system-root seed or
system-package launcher. Native self-hosted users prepare their own dependencies
before starting the daemon. See
[initialization contract and limits](../../../../contracts/agents-api/environment-templates.md).
