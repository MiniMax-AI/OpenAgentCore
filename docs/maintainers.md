# Maintainers and advanced deployments

This page is for people who build and publish OpenAgentCore, or run Core without the
installer. To install Core and Web, use the
[installation guide](getting-started/install.md) instead.

## Build a distribution

Release builders need the repository's full Linux toolchain and Docker. Build from
clean, committed source, with the pinned Codex platform package and a matching MiniMax
companion prepared through the existing Runtime build instructions:

```sh
export AGENTS_RUNTIME_CODEX_PACKAGE=/absolute/path/to/codex-linux-package
export MCODE_HARNESS_BUILD_DIR=/absolute/path/to/mcode-harness-artifact
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://downloads.example/releases/COMMIT
make build-core-distribution
```

The builder reuses the existing Core, Runtime, SDK and Web build scripts. It records
the commit, immutable image identities, microsandbox binary hashes and the actual
Runtime OCI manifest digest. Output goes to `~/.oac/build/core-distribution/`; it is
not published anywhere automatically. Qualify the exact bundle before distributing it.
See the [contributor guide](../CONTRIBUTING.md).

The release base must serve the generated asset file names over HTTPS. Set
`CORE_DISTRIBUTION_OFFLINE=1` to also produce the offline bundle; a build without a
release base must select offline mode. Nodes obtain bootstrap metadata from the
console that generated their command. The console serves local artifacts or redirects
missing ones to the pinned HTTPS release URL. Published assets download anonymously.

Native daemon/Harness installers are separate `oac-native-<source>-<platform>.tar.gz`
Release assets with checksum files. The default Core archive and image carry only
`native-installers/catalog.json`; Session bootstrap downloads the machine's platform
on demand. The explicit offline archive includes these installers once, outside the
Core image. Core installation retains them locally for the same bootstrap endpoint.
The tag workflow assembles the catalog from matching native CI outputs. For a local
build with native onboarding, first run `scripts/build-native-catalog.mjs INPUT OUTPUT`
and set `OAC_NATIVE_INSTALLER_BUILD_DIR=OUTPUT`; missing or foreign native assets
prevent release publication. No Release or registry download occurs when Core starts.

A bundle carries a fixed set of docs (the build lists them). Links between them stay
relative; every other relative link is rewritten to the same file on GitHub at the
bundle's commit, and the build fails if a link or anchor does not resolve.

### Independent Core build artifacts

`make build-agents-api` produces `oac-core`, `oac-core-migrate`,
`oac-core-device` and `oac-core-environment-key` under `${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core`.
`OAC_DEV_CORE_BUILD_DIR` may select another absolute output directory. The build
uses only the explicit source set in `scripts/build-agents-api.sh`: the execution
service, its Go contracts and required shared daemon/logging packages, plus the
root Go module manifests. Product server/frontend, other applications and their
migrations/assets are absent from the temporary build context. Keep this boundary
explicit when introducing shared dependencies; do not copy the whole repository
to make an accidental product dependency compile.

The build uses Go directly with workspace discovery and CGO disabled, read-only
module manifests and trimmed paths. It requires no Node, Docker or product setup.
`make check-agents-api` runs this build before its tests, so the full `make check`
and the dedicated CI workflow enforce the same boundary. CI exercises the built
migration command and uses the built server for official-client HTTP checks.
`make docker-build-agents-api` reuses that build for Linux amd64 and sends only
its executables, the E2B helper and `services/agents-api/Dockerfile` to Docker. The
digest-pinned Debian slim runtime runs without root, product assets or an embedded
harness. Keep runtime credentials outside the image and migrations explicit.
`make check-agents-api-container` runs the existing official-client suite against
the image with a read-only root filesystem; it requires Linux Docker, a non-root
host user, the pinned SDK and a dedicated execution test database. Dedicated CI
runs this after binary validation. Changes to the image/build path require this
check in addition to `make check`; do not make ordinary Go builds require Docker.
Registry publication, additional runtime architectures, daemon packaging and
product cutover remain separate work.

`make build-agents-api-release` reuses the isolated build for a Linux amd64 archive
under `~/.oac/`, with its four commands, license, operator guide, source/tree and
protocol manifest, and file/archive checksums. It requires clean committed source
and Python 3.9+, stages output privately, and packages fixed artifacts deterministically.
Keep runtime configuration, credentials, product sources and separately installed
daemons/harnesses out of the archive. Archive changes require content/hash and
fresh-extraction operator checks plus `make check`; execution acceptance uses the
packaged operators and public protocol, not private Store provisioning. Preserve
the database and native history when replacing the API package. This target does
not publish a release or provide an installer/supervisor.

The archive stays Docker-free. Its former Docker-hosted variant, selected by a
retired release Runtime image variable, is gone and the builder refuses that variable:
an image ID alone is not the complete Runtime release a Docker deployment needs.
Docker-hosted deployments use the matched Core distribution below. The archive and
the standalone container are advanced paths for running Core alone; keep them off the
newcomer installation path and document them under
[Maintainers and advanced deployments](maintainers.md).

## Publish a version

Push a version tag on the reviewed commit to run `core-release`:

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

Tags use `vMAJOR.MINOR.PATCH`, optionally with a prerelease suffix such as
`-rc.1` and build metadata such as `+build.1`. A prerelease suffix creates a
GitHub prerelease. Tag creation is the maintainer's release decision.

The workflow checks that exact source with the shared `make check` workflow
while building the Linux amd64 Core, Web, Runtime and database images, installation
archives and versioned Runtime assets. Tag builds include the offline archive.
It uploads the matched files as an Actions artifact and automatically publishes
them in the same tag's GitHub Release. Downloads in the manifest refer to that
tag. Each Release also includes the standalone `install.sh` bootstrap and its
checksum. It defaults to the latest stable release and accepts `--version`;
the [installation guide](getting-started/install.md#install) owns its usage.
Images are shipped as archives; this workflow does not push an image registry.
Publishing a Release does not change the repository's visibility.

Checks and builds run concurrently, and publication requires both jobs to succeed.
Both check out the same full commit SHA (the tag event's commit or the explicit
manual input). A failed check never permits publication, even if its build succeeds.
The build may consume runner time before another job fails; this trades some failed-run
cost for shorter successful releases.

Both jobs use the same Go module and compiler-cache directories under
`~/.oac/cache/`. Cache keys include runner OS/architecture, all Go module manifests
and checksums (including the toolchain version), and the checked-out commit.
A dependency-matched older cache is only a compiler/download seed: Go resolves
inputs again, and all checks still run with their existing assertions and timeouts.
No test result, installation state or release archive is accepted from this cache.
Main-branch checks can populate the default-branch cache for later release runs;
GitHub's branch/tag cache visibility rules still apply. New keys are saved only
after successful jobs; concurrent writers for the same key may retain either
job's valid cache. Missing or evicted entries affect speed, not correctness.

Build and check jobs have read-only repository permissions. Only the publication
job receives `contents: write`. Before publication it verifies archive checksums
and confirms that the remote lightweight or annotated tag still resolves to the
built commit after uploading the draft assets. Publication sends one request for
that fixed Release ID. An upload failure cannot expose an incomplete public Release;
an ambiguous publication response leaves the Release intact for inspection.

Do not move release tags or overwrite published assets. A rerun refuses an
existing Release, including a partial draft, rather than replacing files. If the
publication job fails, inspect the Release first: it may have completed despite
a lost response. Leave a complete published Release intact. For an incomplete
draft, reconcile or remove only that draft before rerunning the failed publication
job, which reuses the original Actions artifact. Do not rerun the successful build
or recreate the tag to recover a failed upload.

Automated checks establish build and test results, not real-model qualification.
Keep live execution evidence separate and assess it before pushing the version
tag. No model credentials or private certificate authorities belong in CI inputs.

### Build a candidate without publishing

Manual runs accept a full source commit SHA. They run the same checks and build
steps, default to offline output, and never publish automatically:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/parsar-core --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

With `draft_release=true`, the result is an unpublished `build-<full SHA>`
Release. With `draft_release=false`, files remain in the Actions artifact only.
Use the exact matched asset set; do not mix builds or resolve components through
`latest`. The historical batch qualification tools are not part of tag publication.

### Continuous integration coverage

CI coverage has three owners: `core-check` runs the complete `make check` gate;
`api-acceptance.yml` adds the pinned official-client, migration-command and container
acceptance without repeating the full service test suite; `native.yml`
builds and tests the daemon, process lifecycle, Harness protocols and installer
bundle together on Linux, macOS and Windows. Native tests use the packaged
Harnesses and share one daemon build per platform. Changes to native sources,
shared dependencies or packaging inputs trigger that matrix; documentation-only
and unrelated Web changes do not. Manual native validation remains available.
Superseded native runs on the same ref are cancelled. Workflow syntax validation
and release qualification remain separate checks.

## Run Core without the installer

These paths give you Core alone, without Web, the `oac` command or `config.json`.
They are for development, testing and operators who manage Core's process themselves.
They are not an installation path for new users.

- [Standalone Core archive](../services/agents-api/RELEASE.md)
  (`make build-agents-api-release`): Core, its migrator and operator commands for your
  own PostgreSQL.
- [Standalone container](../services/agents-api/CONTAINER.md)
  (`make docker-build-agents-api`): the same in a Linux container image.
- [Service guide](../services/agents-api/README.md): building and running Core from
  source.

Core reads only its environment; the
[configuration appendix](configuration.md#appendix-core-environment-without-the-installer)
lists the variables. The Docker-hosted variant of the standalone archive is retired: it
could not describe a complete Runtime release by itself. Docker-hosted deployments use
the Core distribution and its installer, whose manifest carries the complete release.

## Distribution and installer rules

[Configuration](configuration.md) is the canonical operator parameter reference.
[Installation options](getting-started/install-options.md) owns installer usage;
README Quick start and the installation guide link there instead of copying option
lists. Generate its flag-to-key table and the configuration reference from the schema.
`host` selects the gateway listener for managed ingress and Core/Web listeners for external ingress; `ports.web` uses only `--port`, and
`ports.core` uses `--core-port`. Defaults, validation and flag mappings live in the
schema. Flags seed config.json; health checks, setup, apply and generated service
files derive their addresses from that same config. Apply uses the last applied
address to contact running services before changing listeners. External non-loopback
binds require an HTTPS public origin. Managed ingress initially exposes Web by IP
over HTTP; Core stays loopback and PostgreSQL remains private.
Every process setting has one home: the installation's private `config.json`,
described by `deploy/install/config.schema.json`. The operator edits that
file, or uses the installer-owned managed-domain action through Web/`oac domain`; `oac apply` validates it, derives `generated/` (Compose file, `core.env`,
native unit, Core key digest file, settings snapshot) and converges on what actually
runs: each service carries the digest of its inputs (Compose label
`io.oac.inputs`, native `OAC_INPUTS`), and exactly the services whose running
inputs differ are recreated or restarted. Decide restarts from what runs, never
from recorded bookkeeping, so the next apply finishes any interrupted one. Installation flags only seed it, and
rerunning the installer rejects them. Runtime settings stay in PostgreSQL and
change through Web or `/core/v1`. Secrets live once each in `secrets/`; identity and
install facts live in tool-written `state.json`. State format 2 uses an `oac-` Compose
project; config.json's independent schema format stays 1. The operator command is
`oac` (`oac_cli.py`, packaged as `oac.pyz`), with default installation directory
`~/.oac/core`, private `~/.oac`, generated `x-oac` annotations and `.oac.lock`.
The project has no historical installation compatibility or in-place version upgrade
contract. Install only into an empty directory, or repair the exact same source
revision. Refuse old formats, conversion journals and different revisions before
installation mutation; retain their data and direct operators to reinstall separately.
Distributions contain only current installation and maintenance code; historical
layout conversion, brand migration and native binary replacement implementations
are not packaged. Keep refusal checks and their tests when retiring these paths.
The installer and every mutating `oac` command share the stable `.oac.lock` inode.
The installer owns this lock across creation, payload/native/launcher repair and
apply, invoking the already-locked apply implementation without nested locking.
Never unlink or replace the lock, including after an interrupted fresh install.
Current-version interrupted apply and rotation retain their existing recovery path.
The packaged `oac.pyz` entrypoint embeds the build source revision and checks it
against the existing `state.json.source_commit`; this adds no installation state
format. Node `--update` is refused; runtime generation operations are unchanged.
 Core process settings use `OAC_*`, Web settings use `OAC_WEB_*`, and shared Go
logging uses `OAC_LOG_*`. Retired settings fail startup even when empty or when
the new name is also set; report every matching name without values. No supported installation entrypoint converts pre-rename files. Core and operator executables
are `oac-core`, `oac-core-migrate`, `oac-core-device` and
`oac-core-environment-key`; Web is `oac-web`, and the Core-host E2B helper is
`oac-e2b-provider` under `/opt/oac/e2b` in the image.
Core still reads only its
environment and has no config loader; it serves the non-secret snapshot at
`GET /core/v1/installation`. Keep the schema, the subset validator
(`config_model.py`), the generator and the generated reference table in
`docs/configuration.md` (`scripts/config-reference.py`) in step. Do not add a
second operator configuration file, loader precedence, hot reload, compatibility
reading of retired names, or an embedded Core node. `install.sh --sandbox` calls
the ordinary administrator API once; PostgreSQL owns the resulting selection.
Administrator-issued enrollment approves capacity (default two active/eight
retained); a node cannot supply or overwrite those limits. Downloaded specification
copies remain validated against the existing database-owned resources/Runtime
contract.

### Managed HTTPS ownership

A default combined Docker installation adds two Compose services from one pinned
installer image: `gateway` runs Caddy, and `installation` runs the packaged
`oac domain-server`. The latter uses the installing account's UID and existing
local Docker socket access to invoke the same locked apply implementation. Its
only request surface is the private `ingress/api/api.sock`, with Core-key
authentication and a typed domain action. Core and Web get no Docker socket,
host process authority or writable installation configuration. Web gets only the
private API socket directory. Caddy's separate admin socket is never mounted in Web.

The managed gateway owns ports 80/443 and the initial Web port. Caddy owns
certificate issuance and renewal; its private data persists in `ingress/data`.
`generated/Caddyfile` is derived from `config.json`, and apply reloads it through
the private Caddy socket even when container inputs already match. A successful
apply reconciles domain operation status after verifying the running services.
Domain preparation retains the old entry point while
verifying a trusted certificate and installation-specific response over HTTPS.
The existing operation record retains the last successfully applied public address.
Apply and start update it after gateway verification and service health checks;
generated files alone do not establish that a new address is active. Failed retries
restore through common apply even when a previous attempt partially changed services.
Only then does it update `public_url` and call the common apply path. Failure
restores the previous desired configuration and reports incomplete recovery.
Interrupted operations retain desired files and a visible failure/retry state;
they never create another service project or delete execution data.

The domain operation refuses unrelated pending config edits and shares `.oac.lock`
with CLI mutations. Its status file is operation bookkeeping and the public-address
recovery receipt; `config.json` remains the source of desired process settings.
A Web restart ends console sessions; the UI provides the new
HTTPS login address instead of treating a dropped request as proof of success.
Split/native installations use external ingress and explicitly report automatic
Web setup unavailable. Ingress is an installation concern, independent of Runtime
and Sandbox Provider selection.

The E2B template builder assigns traversable modes only to synthetic public archive
ancestors. Runtime file and directory permissions, private build contexts, key inputs
and output umask remain unchanged, including when invoked under umask 077.

The installer packages Core and the Web console together,
with independent `--core-only` and `--web-only` modes. `site/` is the public static
landing, separate from `apps/web`; it must not create an onboarding prerequisite,
call a model, or claim complete protocol compatibility. Use Web's page and action names in user docs, and `OPENAI_BASE_URL` and
`OPENAI_API_KEY` for application examples. The [documentation ownership](../CONTRIBUTING.md#documentation-ownership)
map defines the authored sources.

A distribution carries a fixed list of docs (`BUNDLED_DOCS` in
`scripts/core-distribution-manifest.py`). The build keeps relative links between
bundled docs, rewrites every other relative link to the same file on GitHub at the
bundle's commit (`@SOURCE_REVISION@`), and fails on a link or anchor that does not
resolve; `make check-distribution` runs the same check on the repository's docs. Keep
the list self-consistent when adding or moving a doc the installer or its output
refers to.

`make build-core-distribution` builds from clean committed source and reuses the
existing API, Runtime, SDK, helper and Web builders. Artifacts record source and
immutable image identities, the actual Runtime manifest digest, checksums and
microsandbox runtime/firmware hashes and executable native payloads. Local distribution builds do not publish. The tag-triggered release workflow
reuses the full repository check on the exact build source, then publishes the
matched assets automatically; manual runs remain artifact-only or draft-only.
Only publication receives repository write permission. Never overwrite release
assets or move an existing version tag. The [maintainer guide](#publish-a-version)
owns tag syntax, prereleases and failed-publication recovery.
Release assets include `deploy/install-release.sh` as standalone `install.sh`
with a checksum. This public downloader resolves latest once (or a selected tag),
verifies the control-plane archive before safe extraction, and delegates to that
bundle's installer. Default installation downloads Core, Web and PostgreSQL payloads,
never the Runtime image or node execution artifacts. Offline archives remain an
explicit distribution option. It introduces no separate installation state, upgrade path or login flow.
Build/test success is distinct from real-model qualification; maintainers assess
that evidence before pushing a release tag, and no synthetic result substitutes
for native execution acceptance.

Distribution `images` records each exported image's config digest;
`image_manifest_digests` records its OCI manifest/index digest. Derive and verify
both from the same archive, including its referenced config and layer bytes, and
require the build host's selected image ID to match one of them. Docker's classic
store identifies images by config, while its containerd store uses the OCI
descriptor. The builder therefore selects the digest from BuildKit's build metadata
that the local store resolves, never the `--iidfile` config digest alone, and
disables provenance attestations so each image and archive holds one platform
manifest in both stores. For the same reason the default PostgreSQL input is pinned
by its linux/amd64 platform manifest digest: a pulled multi-platform tag keeps its
whole index in the containerd store, and that export holds every platform. Core,
node and self-hosted installers share one resolver
for these required identities: confirm Linux amd64 and the returned immutable local ID,
then use that ID in service/provider configuration and Runtime launches. Tags do
not replace identity verification. The microsandbox-qualified `runtime_ref`
remains independent of Docker's local store identity.

The manifest is the shared download contract for Core, node and self-hosted
installers: flat versioned filenames, compressed Runtime size/hash and unpacked
size/hash. Nodes obtain bootstrap metadata from their configured console (or a local offline
bundle). Web serves locally available artifacts first; for missing declared execution
artifacts it redirects the node to the versioned HTTPS release base in the verified
distribution manifest. Web does not download or cache those bytes. Only artifact
requests may follow HTTPS redirects, without credentials or cookies; metadata and
enrollment requests must remain on the configured console. Nodes retain size and
SHA-256 verification, resumable transfers and immutable release selection. Download into
private temporary files, verify before atomic promotion, and reuse only verified
cache entries or exact image identities. Core's default image must not acquire
execution-only payloads. Python zipapps bundle the shared resolver with each
remote bootstrap; the console publishes only fixed non-secret files and declared
artifact names. Candidate build automation creates artifacts and may create an
unpublished draft; a successful build is not real execution qualification. A
separate existing-host batch controller may publish that draft automatically only
after directly supervised real qualification and verified batch landing. Repository
visibility is public. Published release downloads are anonymous and must not
require GitHub login or repository credentials.
Manual builds use the legal `build-<full source SHA>` release tag; tag-triggered
builds use the actual `v*` tag. The manifest download base and draft tag must match,
while artifact filenames and source provenance retain the full source SHA.
Qualify the exact downloaded production artifacts before publishing the draft;
keep qualified executable, image and source payload bytes and source identity
unchanged. A recorded release-address/checksum-only repack requires proof that
every other archive member is unchanged and verification of final published asset
digests and URLs. Never use an acceptance
image containing a private test CA or model credential as a release input.
Repository visibility is independent of publication. Do not add repository
credentials to installed node/Runtime configuration to bypass download access.

`scripts/promote-qualified-release.py` requires an explicit full candidate source
SHA, binds it to the archive manifests and matching `build-<SHA>` tag, and uses
existing local gh authentication and SSH. It uploads/downloads the complete
matching thin/offline/Runtime asset set and verifies archive members and asset
hashes. The separate qualification package is supplied by the maintainer with an
explicit reviewed manifest SHA256. Its complete file inventory, ordered Python
commands, bounded stage timeouts and private path/resource configuration are
verified before any Release mutation and again by the remote supervisor. Candidate
assets cannot select or replace this execution package. Keep host-specific
acceptance scripts, usernames and credential paths outside this public repository;
never put credential values in either manifest.

The reviewed adapter receives a fresh canonical UUID and exact inventory over the
authenticated command channel. It directly supervises fresh-install,
current-lifecycle, managed-native-smoke, diagnostics-observations-smoke and
node-runtime-smoke in that order. This batch uses one fresh container installation,
one completed managed Session, read-only diagnostics for that Session, and one
current Runtime Session on one new node. It does not rerun the full multi-host,
generation or GC matrix. Every child must exit successfully and
return only its own passed check, the current controller identity and its observed
owned resources. Resources and the previous result flow between live children;
a supplied pass file, skipped check or old report cannot release the candidate.
Verify package and candidate bytes again after each stage. The small shared
`qualification_control.py` is pinned to the reviewed tooling commit and private
package. A live SSH stdin channel carries the request then heartbeats; EOF, timeout,
SIGTERM or SIGHUP stops later work. Each local stage or remote worker has one
foreground process group and a waiting owner outside that group. The owner cleans
the group on success, nonzero exit, timeout and cancellation, including foreground
descendants orphaned by an inner timeout or SIGKILL. Nested foreground commands
inherit the group; only explicitly recorded background resources may detach.
Those retained background resources are outside foreground cleanup. Private nested
workers use the same channel.
Already-issued writes may have unknown outcomes: retain intents/resources and do
not replay or claim rollback. Control tests exercise
short-lived fixture children only and never establish live qualification.

The caller supplies the independently reviewed promotion-tooling commit. Its
changes from the candidate may only affect the exact promotion files enumerated
in the controller, including CONTRIBUTING, docs/maintainers and the current-batch
node-generation protocol wording correction; the Makefile
exception permits only registration of the controller and control-channel tests. Main must contain the
candidate source and have the reviewed tooling commit's tree. This permits normal
merge commit identity changes and release-only documentation updates without
rebuilding or relabeling the original candidate. The candidate's bundled docs and
source archive retain source 48 (CONTRIBUTING and the node-generation protocol
are present through the source archive, not as direct bundled docs); new release instructions live in the tooling
commit. Product changes or a different main tree block promotion of the old
candidate. Never infer batch membership from all open PRs or automatically merge
them in the publication command.

Use one controller invocation for the batch. After successful qualification it
waits in the same process, within the explicit merge-wait budget, for the exact
reviewed batch tree to reach main. An ancestor main waits; conflicting main changes
fail immediately. Cancellation or expiration retains evidence and cannot turn a
saved result into resume authority. It verifies unchanged draft identity,
target, tag and downloaded bytes immediately before publication. After the final
download it rechecks main/tree/tag and the same draft ID, then updates that verified
Release ID directly rather than resolving the tag again. It checks the
published bytes afterward. Conflicting assets are never overwritten. An interrupted
run is reconciled before another invocation; stored qualification output is evidence,
not a resumable permission to publish. Preserve its isolated local/remote evidence
and installation resources. No runner, background service, new GitHub secret or
repository-visibility change is required by this finite batch path.

Executor credentials are issued by the operator with the Core key, through Web or
a Core-key script, under
`/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials`
and reuse the existing restricted issuer. The target must be a self_hosted
Environment of that Project whose Session exists; anything else is 404. The
Project's principal is the credential's execution principal, its scope stays
daemon enrollment and connection for that one Environment, and issue, rotate and
revoke each record an administrator audit entry in the write's transaction without
the secret. Project API keys cannot issue them. Native self-hosted installation runs the daemon on Linux, macOS or Windows with
its starting account's permissions. It owns no sandbox node or Core allocation,
adds no isolation and retains user-owned native history after uncertain launches.
Report started, connected and real execution success separately.
Native installation uses `oac-daemon install` and `start` with an explicit
credential-file path and the same `OAC_RUNTIME_HOME` for lifecycle commands. It is
current-version only and does not adopt an older container installation. Rotation
replaces the configured credential file for the same key and restarts the daemon;
never create a replacement Session history to recover a credential. The console's
older container-installer command remains a distinct packaged workflow, not the
native installation interface. Report connection and actual execution separately.
Core-key executor credential lists expose a required connection observation with
never_enrolled, connected or disconnected status, immutable bound key identity,
enrollment time and last authenticated heartbeat time. Read credential metadata
and binding facts in a closed read-only snapshot, then reuse runtimeenrollment's
current authority and actual gateway peer checks. Recheck executor and device
authority after reading the peer; rotation, revocation or Environment retirement
must not inherit a former key's connected state. No gateway means not connected,
never an authentication bypass. Known authority loss is disconnected; storage
errors remain errors. Keep digests/device IDs internal and public /v1 unchanged.
Connection timestamps are history, not execution/native/model readiness.

Self-hosted installation confirms connection through the private daemon transport
using only its restricted executor credential. The read checks the exact live
Environment/key binding and current authenticated connection; it never enrolls,
allocates, wakes a sandbox or grants project resource access. It is an `/api/v1`
machine route that reaches Core directly, never through the console. Bounded
polling retains the original Runtime identity and history; timeout is a
diagnostic failure, not permission to replay initialization or replace history. The installation public URL
(`public_url` in the installation's `config.json`, seeded by `--public-url`, and
`OAC_PUBLIC_URL` for Core) is the one origin for
applications, nodes, sandbox guests and self-hosted executors, and also the console
origin. Core derives the daemon `wss` URL, the self-hosted `remote_url`, hosted
Runtime bootstrap and the deployment's read-only `core_url` from it; the deployment
API does not accept a Core address, and no deployment row stores one. Bootstrap
never uses request Host or caller-supplied placement fields. Enrollment names the
Core address the node uses; Core refuses one that is not the public URL (409,
token unconsumed) and records it. After the public URL changes, a node receives no
new sandboxes until re-added. This does
not widen sandbox network policies or change credential admission.

The distribution build sets umask 022 for non-root-readable payloads; installation
credentials and state retain their explicit private permissions.
For a system node installation, capture the trusted bootstrap bytes before
dropping to the service account. Pass those bytes through the fork; the service
account writes its own retained generation helper. Never make the caller's private
download directory accessible or let root write into service-owned state to
work around bootstrap access.

Installer progress describes the operation about to run. Do not imply fresh
health checks on a no-change repair. Keep terminal styling optional, honor
`NO_COLOR`, and preserve plain redirected logs. Summaries show credential file
locations, never their values. `install_display.py` owns shared terminal formatting;
`install_output.py` and `node_output.py` own their respective completion guidance.
Ship and checksum the display modules, including them in both the distributed node
bootstrap and retained helper. A node summary reports success only after Core
connection and provider readiness are confirmed. Service-user output stays plain
and passes through the existing terminal-control sanitizer.

The Core/Web installer uses the launching account, including root, and a writable
installation directory. It never invokes sudo, switches accounts or changes host
Docker permissions. Check actual platform, Docker and directory prerequisites;
root alone is not a reason to refuse installation. Native Core retains its
systemd user-manager and lingering prerequisites for that same account.

The first installer targets a trusted Linux amd64 Docker host. It installs a
private dedicated PostgreSQL service and separate Core and console services in
Compose by default, with zero execution nodes. The default requires neither KVM
nor systemd user services, imports no Runtime image, mounts neither the Docker
socket nor host devices into Core, and never adds its own host as a node.
`--sandbox docker|microsandbox|e2b|none` (default `microsandbox`; `none` and nothing
else with `--web-only`; `docker` prints its weaker isolation and needs a y/N
confirmation or `--accept-docker-risks` before anything is created) is a one-time
install action: once the services are healthy, the
installer POSTs `/core/v1/sandbox/deployment` as Web's setup would, and never on a
repair. It is not written to `config.json`; PostgreSQL owns the
selection. E2B needs a non-loopback HTTPS `public_url`, `--e2b-api-key-file` and
`--e2b-template`, and is refused before anything is installed. A loopback Docker or
microsandbox selection is saved, but no node can serve it until `public_url` is
guest-reachable HTTPS. `--sandbox-provider` and `--provider` are retired and fail.
The thin distribution supplies native Core binaries. Provider helpers, the node
agent, Runtime image and pinned msb runtime/firmware are separate, same-revision
assets. Web serves local offline artifacts or redirects the node to the verified
manifest's versioned HTTPS release. `/console/config` reports providers with a
complete set of local files or declared release downloads. Core packaging is independent of provider:
`--native-core` runs Core as a systemd user service, with PostgreSQL/Web in Compose
and a private loopback database port. Native Core needs no KVM or node assets. Core receives no
Docker socket or node identity mount in either mode. The ordinary standalone node
service owns its provider processes outside the Core container. Its `KillMode=process`
preserves resident microVM/helper processes across a node-service restart. User KVM
access and the Linux runtime libraries are prerequisites for microsandbox. The
installer runs as root and prepares the host: it creates or adopts the
`oac-node` system user, adds it to the `docker` or `kvm` device group (no other
group), and installs one root-owned system service per installation that runs the
same node program with `User=oac-node`. Sudo mode serves one Core per host,
because its nodes share that account. Docker group membership makes that user,
and so the node, root-equivalent on the host; that is inherent to Docker sandboxes,
not a least-privilege boundary. Microsandbox needs only `kvm`. Files the service
user owns are read, written and deleted only with its credentials, never by root,
in a child that starts its own session with /dev/null as input, so nothing it runs
can reach the administrator's terminal. That child also joins a new session
keyring and dies with its parent, and root shows its output only as plain text
(terminal controls become `?`). The installer turns SIGINT, SIGHUP and SIGTERM
into stopping that child and what it started, which would otherwise outlive a
closed terminal. Root never runs a file that user can write,
opens a URL it wrote, or follows a link in its home. Sudo mode
never installs Docker, KVM or packages, never changes device permissions, refuses
SELinux-enforcing hosts and a token in the environment, and changes nothing when a
check fails. `--uninstall` removes a node only after Core rejects its credential,
never touches sandboxes, volumes or images (it keeps the Runtime image and the
microsandbox store), deletes the account only when the installer created it and no
node remains, and otherwise removes only the groups it added. Do not add any other launcher, scheduler or
recovery path. Node services restart after failures without a start limit, so a node
outlasts a Core outage, and stop restarting when the node program exits 78
because Core answered 401 to its credential (a removed or retired node).
The basic API image and binary builds remain independent artifacts.
The standalone API release and Core distribution both include the nodes operator
reference (`HOSTED-SANDBOX-MANAGER.md`) at the relative path used by their packaged README. Include the
guide in each artifact checksum list so extracted documentation matches its build.
The node asset includes the `oac-node` binary. The installer's Docker and
microsandbox selections use Web's Standard size from
`apps/web/src/features/sandbox/standard-sizes.json`, which the distribution build
copies into the bundle; keep no second copy of those values. An existing database
selection is never overwritten by installer defaults. Node configuration and identity live under
`~/.oac/nodes/<installation-id>/` in the node account's home (`/var/lib/oac-node`
in sudo mode); microsandbox uses its separate short private
Runtime home. Zero-node installs create no node identity state but retain the paired
Core key for first setup.

Node installation refuses pre-rename resources for the same installation ID: old
records, node directories, units and Docker networks. It never adopts those
resources or removes another installation. Remove the node on its old Core, then
uninstall with the previous release before adding it again. The machine
configuration route rejects the retired product-named node header with
`400 invalid_request`; only
`X-OAC-Node-ID` identifies a retained node credential.

User-managed hosts use the native daemon installer on Linux, macOS and Windows.
It does not select a supplier or create compute resources. The retired Docker
self-hosted installer/launcher and console payload are not retained as fallback.
Existing environments, credentials, workspaces and native history are never
automatically deleted or adopted by a new installation.

One Runtime image contains the existing daemon, shared helpers and three native
harness packages. Their differences remain in the adapters. Core keeps exclusive
ownership of Session allocation, initialization, cancellation, snapshots and
cleanup. The node installer imports the Runtime image and prepares running
conditions; neither installer creates an execution Session or supplies a model
credential. Applications use the
existing write-only model execution extension, with the installation's persistent
credential encryption key. Provider identity/backend namespace and native history
must not change on a repeated install.

Installation state and secrets live in a private directory under `~/.oac/` by
default. No credential enters build arguments, image layers, browser bundles or
diagnostic output. Compose configuration is confidential. The generated database,
Projects and their issued keys, provider identity and encryption
key survive reruns; automatic
revision replacement and provider migration are outside this initial installer.
Reruns also refuse enabling or disabling a sandbox provider on an existing
installation, including adding one to the default zero-node installation.
Stopping control-plane services does not stop all Provider resources; use Core's
existing release operations for full cleanup. No native restart promise covers
host reboot or a lost running microVM. Do not delete data or issue broad
container/volume pruning as recovery.

`make check-distribution` covers the production proxy, installation rules and
release metadata. Real bundle validation covers default/provider selection,
component modes, existing Web connection, public native execution and restart
retention. Diagnostics report observed service health, not fabricated model or
complete environment readiness. Runtime observations are Core-owned; do not add
a duplicate monitoring/lifecycle framework to installation or the public landing.

## Native daemon and Harness installation

`oac-daemon install` owns interactive selection and CLI-only installation through
one options/validation path. No installation-options file input is supported.
Persisted installation state and explicitly supplied credential/tool-variable
files serve runtime operation, not a second installer configuration language.
The release bundles pinned Node/npm, native Harnesses and required adapter assets;
registration lives in CLI and native activation/readiness in each adapter's optional
`agent.Installation` descriptor. Core never selects native paths or OS-specific
installation steps. See [native installation](self-hosted-native.md).

Self-hosted onboarding extends authenticated Session creation/detail responses with
`x_agents_core.installation`; Web displays the same Core-produced commands. Lists
and durable event journals never retain installation authorizations. The command
uses a 30-minute, Environment- and build-scoped grant to claim one connect-only
credential. The installer persists its generated secret before claiming it; retries
must prove that same secret. Reserve the Environment UUID as the onboarding key ID.
Existing, rotated or revoked credentials are never replaced by onboarding. Machine
bootstrap routes use this grant, not an Environment ID as authentication. Public
artifact routes contain no credentials. Native bundles must match the Core source
revision and Runtime wire version. Core release qualification consumes the same
three-platform native CI artifacts and publishes them as independent, source-qualified
Release assets. Core images and default control-service archives carry only their
small catalog (build, protocol, platform, checksum and versioned HTTPS URL), never
native execution archives. The public artifact route redirects a requested platform
to its catalog URL without proxying or caching it; clients verify the Core-provided
checksum before extraction. Installation grants are sent only to Core, never to
artifact hosts. Explicit offline distributions include one copy of each native
archive outside the Core image. The Core installer retains this directory privately
and mounts it read-only for container Core, or points native Core at the same files.
Core verifies local archives before serving; missing online archives redirect, while
corrupt local content fails closed. Neither installation nor repair downloads native
execution payloads; Session bootstrap requests only the current machine's platform.
`make check-distribution` exercises catalog assembly with manifests larger than
Node's default subprocess output buffer; catalog reads allow up to 64 MiB.
Bootstrap scripts own platform download/extraction only; installation, startup,
connection verification and Runtime execution remain common. Serialize background
PID inspection and publication so concurrent starts cannot create duplicate daemons.
An installed daemon discovers and registers only the adapter kinds named by its
verified installation manifest. The host PATH stays available to tools; its other
Harness executables and activation variables cannot extend that installation.
Direct `connect` rejects an installation manifest and directs the operator to
`start`; unmanaged image bootstrap remains available without that manifest.

All mutations use the installation directory lock. Publish complete checksum-verified
components from staging, then commit configuration after native readiness passes.
Re-running with the same connection settings adds selected Harnesses and validates
existing contents. Never overwrite, upgrade, auto-repair or migrate installed
components. Missing, modified, wrong-platform or incompatible content is an explicit
error. A partial addition must preserve the old configuration and allow reuse of
complete components; it must not remove previous files or data.

Installers run as the current user in writable directories. Native subprocess
diagnostics must not expose sensitive parameters or environment values. Adapter
readiness checks receive the installer cancellation context and must reap owned
processes before returning after interruption. Readiness,
authenticated connection and model configuration are separate reported facts.
Starting execution must not download or install Harnesses. Ordinary stop/reconnect
must preserve capability snapshots and native Session state.

`scripts/build-native-installer.mjs` packages native inputs, validates pins/startup
and hashes every component file. It uses contained regular files and rejects
escaping links. Claude's dedicated frozen `pnpm deploy` export is reified with
the hoisted linker for this distribution before contained links are flattened;
the original standalone Claude archive contract remains unchanged. The native
installer workflow builds and tests on Linux, macOS and Windows, including actual
installation, addition/reuse, missing arguments and unsupported Windows MiniMax.
Native CI startup checks do not replace real model execution evidence or claim
manual Windows acceptance. Heavy builds belong on remote servers or CI.
