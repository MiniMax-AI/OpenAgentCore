# Install Core and Web

Install one matching Parsar Core distribution. By default it starts PostgreSQL,
Core and the existing Web console in containers, with zero execution nodes.
It does not import Runtime images, mount the Docker socket or host devices into
Core, or generate a managed Provider configuration. Select the provider, per-sandbox
resources and immutable Runtime through Web or the administrator API after installation. A local node is an optional installation
choice and uses the same database-managed configuration.
No model key, Environment wizard or sample task is required during installation.
See [Configuration](../configuration.md) for setting ownership, paths, defaults,
units, change restrictions and restart behavior.

Recommended path: [install](#verify-extract-and-install) →
[sign in to Web](#sign-in-to-web) →
[add a node](#add-nodes-after-a-default-installation).
You can leave the deployment with zero nodes until you need execution.

## Host requirements

The first distribution targets Linux amd64 with Python 3.9+, Docker and Docker
Compose 2.26.0 or newer. Run the installer as a non-root user who can use Docker.
The default installation requires neither KVM nor systemd user services.
The optional local provider has additional requirements described under
[installation choices](#installation-choices). Node-host requirements are listed
in the [add-node steps](#add-nodes-after-a-default-installation).

## Verify, extract and install

Download the matching Linux amd64 archive and its `.sha256` file from
[GitHub Releases](https://github.com/MiniMax-AI/parsar-core/releases). Use one
release for the entire installation. If GitHub requires sign-in, use an authenticated
browser or `gh release download RELEASE --repo MiniMax-AI/parsar-core`.
Choose the ordinary `.tar.gz` for a zero-node installation, or `-offline.tar.gz`
when you also need all execution assets locally. A source checkout alone is not
an installable binary bundle; [build a distribution](#build-a-distribution) for
unreleased changes.

For the recommended node workflow, choose an HTTPS address that both node hosts
and their sandbox guests can reach, such as `https://core.example`. Configure
your DNS/TLS reverse proxy as described in [Expose Core and Web](#expose-core-and-web),
and pass that address on the first install. The installer does not create DNS
records or certificates. It does not change an existing installation's public URL.
The command below still installs zero execution nodes.

```sh
sha256sum -c parsar-core-<commit>-linux-amd64.tar.gz.sha256
mkdir -p "$HOME/.parsar/releases"
tar -xzf parsar-core-<commit>-linux-amd64.tar.gz -C "$HOME/.parsar/releases"
cd "$HOME/.parsar/releases/parsar-core-<commit>-linux-amd64"
./install.sh --public-url https://core.example
```

The thin bundle contains same-revision Core and Web service images, PostgreSQL,
native Core binaries, installer bootstraps, documentation and checksums. Node,
Runtime and microsandbox binaries are separate prebuilt assets. Installing the
default zero-node deployment downloads none of those execution assets and needs
no Go, Node, Rust or source checkout.

The manifest identifies every asset by immutable revision, SHA-256 and byte size.
Adding a node downloads only its selected provider's assets. Runtime images use
compressed archives; verified local files and already imported images are reused.
Downloads use temporary files and bounded retries, so a truncated response is
never promoted into the cache. An optional `-offline.tar.gz` bundle contains the
same assets locally. For console-based distribution without a release host, build
that offline bundle with no release URL. A bundle that records a release URL
retains that URL for remote node downloads; it does not silently change mirrors.

## Sign in to Web

The management backend and client require the corresponding Web screen migration
before release. See [console integration status](../web/README.md).

Installation creates private configuration under `~/.parsar/core`, a dedicated
PostgreSQL volume and a credential encryption key. Installation creates no Project
or application API key. Projects and their keys are managed in the database;
configuration files contain deployment settings only. New installations create one
management credential, the [Core key](operations.md#core-key). Secret values are
not printed.

Open the console address printed by the installer (`https://core.example` in
the example above) and sign in with the Core key from
`~/.parsar/core/admin/core.key`. The console has no user accounts or passwords, and
one role: administrator, with access to every console operation. The paired
console already connects to Core with the same key on the server side.

Use the administrator API to create a Project, then issue a named API key within
it and save the one-time plaintext response privately. Core stores only its digest.
The corresponding Web management screens remain pending. Multiple keys in a Project
share its assets and execution principal; writes record the actual key separately.
Rotate by issuing another key in that Project and revoking the old one. Archiving
the Project disables all its keys and retains assets for inspection and deletion.
API callers use their own keys and the public API endpoint. The Core key cannot
call `/v1`; the console cannot execute or create Agent resources on their behalf.
See the [management contract](../../contracts/agents-api/admin-api.md).

Hosts registered through the console supply sandbox resources for hosted Sessions;
self-hosted Sessions use application-managed environments. Neither installation
nor key creation calls a model. Run examples separately with the issued API key
and your model provider key, as described in the [API guide](quickstart.md).

Default local ports and private files:

- API: `http://127.0.0.1:8091/v1`
- Web upstream: `http://127.0.0.1:8080`; use the configured public URL in your browser
- Core key: `~/.parsar/core/admin/core.key`
- Core key digest: `~/.parsar/core/admin/core-key-digests.json`

Use exactly the displayed console address; the production proxy validates its
configured origin. Sign-in sessions live in the console's memory. They expire after
12 hours, and you sign in again after a console restart or a Core key rotation.
Sign-in, status and sign-out use private `/console/auth` routes and do not extend
the public Agent API. The console rejects every `/v1` request, including requests
carrying an explicit Bearer token; it holds no caller key.

Core loads the Core key digest from `AGENTS_API_CORE_KEY_DIGESTS_FILE`. Only the
Core container mounts the digest file, read-only. The bundled Web server reads the
Core key through `CORE_CONSOLE_CORE_KEY_FILE` to check sign-in and to proxy
authenticated console operations; it never sends the key to the browser. The
migration service receives neither. A Web-only connection to an external Core uses
that Core's key, configured server-side. Existing installations from earlier
releases must rename these files and settings before upgrading; see
[Upgrade an existing installation](operations.md#upgrade-an-existing-installation).
Choose English or Chinese through the System language selector.

## Add nodes after a default installation

1. Log in to the bundled Web console and open **Hosted Sandbox Manager**.
   The paired installation needs no second key or Core connection setup.
2. Choose **E2B cloud** or **Own machines**. Own machines use Docker or
   microsandbox; select their per-sandbox resources and matched immutable Runtime
   release. E2B uses an account key and exact ready template build whose CPU/memory
   match the requested limits, with no node installation. The paired console address is used
   automatically. If your network requires a different address for nodes and
   guests, change it under advanced network settings during initial setup. When
   opening the console on localhost or an HTTP address, setup requires a
   non-loopback HTTPS address that nodes and sandbox guests can reach.
3. Select **Initialize sandbox deployment**. It takes effect without restarting Core and remains in
   PostgreSQL across restarts. The provider, limits and Runtime are one saved
   specification; node files cannot override it. Later changes require global
   maintenance and completed cleanup. Microsandbox uses a five-minute idle timeout
   and one-day snapshot retention.
4. For own-machine hosting, click **Add node**, copy the command, and run it as a non-root user on the
   target Linux amd64 host. It downloads the matched bootstrap from your console and execution assets from
   the manifest's release location (or the offline console payload), verifies
   checksums, reads the saved generation and specification without consuming enrollment,
   verifies the payload matches that Runtime, imports the image only when missing,
   and writes the matching provider configuration,
   registers the node and starts a systemd user service. The installer waits for Core to confirm
   connection and provider readiness. Web refreshes node health
   automatically. Wait for the node to be online and its provider to be ready;
   registration alone does not mean it can accept work. A registered retry uses its
   retained node credential and refuses changed resource or Runtime settings.

The target host needs curl, sha256sum, Python 3.9+, a systemd user session with lingering enabled,
and either Docker socket access or microsandbox's KVM/native-library prerequisites.
The command checks host access before downloading the Runtime and verifies
microsandbox's shared libraries after downloading its native programs.
The console serves only fixed, non-secret distribution files at `/node-install/`;
private installation configuration is never part of this payload. Retain the
installed `node-payload/` directory. Manual console deployments enable the same
flow with `CORE_CONSOLE_NODE_PAYLOAD_DIR` pointing to the matched distribution
payload. TLS verification stays enabled; deployments using a private certificate
authority must provision that trust on the target hosts and Runtime image.

See the [Hosted Sandbox Manager guide](https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/HOSTED-SANDBOX-MANAGER.md)
for host prerequisites and the registration command. The browser does not install
software on another machine or receive SSH credentials. Adding a remote node does
not add a Docker socket or KVM permissions to Core. Node installation and Runtime
storage remain on the selected host.

When a Session needs a sandbox, Core asks the
enabled Provider to create one from the prepared Runtime image and initializes
the colocated daemon, native harness and workspace.

Once a node is ready, you can [make an API request](quickstart.md). The model
credentials are supplied with execution requests, not during node installation.

## Connect a user-managed Runtime

A `self_hosted` Session uses your own execution machine; it does not enroll that
machine as a shared sandbox node. The application creates the Session through
`/v1` with its Project API key. The deployment operator then issues a restricted
credential for the Session's Environment with the Core key, in Web or with a
script against Core's loopback port:

```sh
umask 077
curl -fsS -X POST \
  "http://127.0.0.1:8091/core/v1/projects/$PROJECT_ID/environments/$ENVIRONMENT_ID/executor-credentials" \
  -H "Authorization: Bearer $CORE_KEY" -H "Content-Type: application/json" \
  -d "{\"key_id\":\"$(python3 -c 'import uuid; print(uuid.uuid4())')\"}" \
  -o executor-key.json
```

The response is the credential file; its secret appears only once. If the
outcome is uncertain, list the credentials and rotate the same `key_id` instead
of issuing another. The [credential contract](../../contracts/agents-api/environment-executor-credentials.md)
describes issuance, rotation and revocation. Give the file to the executor host
as an owned, mode-0600 file. The Core key and the application's
`$PROJECT_API_KEY` never leave their own machines.

After saving the restricted credential on your execution machine, run one command
with your Core origin, Environment ID and returned `remote_url`. This downloads
and verifies the matching bootstrap before starting it:

```sh
(
  set -eu
  core=https://core.example
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  curl -fsS "$core/node-install/self-hosted-install.pyz" -o "$work/self-hosted-install.pyz"
  curl -fsS "$core/node-install/SHA256SUMS" -o "$work/SHA256SUMS"
  (cd "$work"; awk '$2 == "self-hosted-install.pyz"' SHA256SUMS | sha256sum -c -)
  python3 "$work/self-hosted-install.pyz" --source-url "$core" \
    --environment-id ENVIRONMENT_UUID \
    --remote wss://core.example/api/v1/agent-daemon/ws \
    --credential-file /absolute/private/executor-key.json
)
```

Use the exact Environment ID and reachable `remote_url` returned by your Session.
The Linux amd64 host needs Python 3.9+ and Docker access as a non-root user.
The installer prepares the matched daemon, native harnesses and local workspace
inside the same isolated Runtime used for hosted execution. No shared node,
model credential or source build is needed. Model access remains execution input.

The command waits for Core to confirm that this Environment and its restricted
credential are connected. It distinguishes a running container from a connected
Environment. Connection failure or timeout exits with diagnostic and retry
instructions, preserving the same container, credentials and native history.
Rerun the command after correcting the reported problem; it does not create
replacement history. A connected Environment does not prove model availability.
Submit your task through the Session API to test execution.
Installation state stays under `~/.parsar/self-hosted/ENVIRONMENT_UUID`; retain its
credentials, volumes and native history. An uncertain launch gives inspection
instructions instead of creating replacement history. Session deletion does not
reclaim user-owned Docker resources.

## Installation choices

```sh
./install.sh --sandbox-provider true --provider microsandbox --public-url https://core.example
./install.sh --sandbox-provider true --provider docker --public-url https://core.example
./install.sh --core-only
./install.sh --native-core --public-url https://core.example
./install.sh --core-only --sandbox-provider true --provider docker --public-url https://core.example
```

`--sandbox-provider true` installs a local node through the ordinary node installer. If `--provider` is
omitted, it selects microsandbox. Supplying `--provider` without enabling the
sandbox provider is an error. `--core-only` installs Core and PostgreSQL without
Web and has no sandbox provider unless explicitly enabled.

Local opt-in requires a non-loopback HTTPS `--public-url` reachable from both the
node service and its guests. Set up the reverse proxy before installation; the
installer does not create DNS or certificates. It starts Core, initializes an empty
deployment through the administrator API and invokes the ordinary node installer.
An existing database specification is never replaced by installer defaults.

The provider choice does not select a harness or alter the public `openai_hosted`
discriminator. Both providers use the same colocated Runtime. Core has no embedded
node or file-managed provider selection. Docker access and microsandbox KVM/native
paths belong to the separate node service; the Core container receives no Docker
socket or node state mount. Web receives neither provider authority nor node secrets.

Local and remote nodes keep configuration and identity under
`~/.parsar/nodes/<installation-id>/`. Microsandbox stores its private Runtime home
under `~/.parsar/m/<installation-hash-prefix>/`. Preserve these directories and
backend storage across restarts. A missing identity is a recovery incident, not
permission to register over existing resources.

### Local provider requirements

Both node providers require a systemd user session with lingering enabled.
Docker requires access to the host's Unix socket. Microsandbox additionally requires
glibc, the matched native libraries and user read/write access to `/dev/kvm`;
nested cloud hosts must expose hardware virtualization. The installer checks these
prerequisites without granting permissions or falling back to another provider.

`--native-core` selects a native Core user service independently of the provider;
PostgreSQL/Web stay in containers. Native Core itself requires no KVM or node
Runtime files. Without this flag, Core stays in Compose. The standalone node
service owns provider processes separately from Core.
Stopping Core does not stop the node or prove its resources have been reclaimed.

See [Configuration](../configuration.md) for initial per-sandbox resources,
administrator-approved node capacity and the five-minute idle suspension policy.
Change saved resources or Runtime only through the
[drained deployment procedure](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance).
Installation defaults are not evidence of model or workload acceptance.

### Separate Web installation

To install only Web on a Linux host, provide the existing Core origin and a
private file containing that Core's Core key. A loopback Core uses the same host network namespace;
a remote Core must use HTTPS.

```sh
./install.sh --web-only \
  --install-dir "$HOME/.parsar/core-console" \
  --core-url http://127.0.0.1:8091 \
  --core-key-file "$HOME/.parsar/core/admin/core.key"
```

Web-only mode cannot enable a sandbox provider. It starts no database or Core and
requires no KVM. Its key remains on the server, outside the static Web files.
The input file must be private (0600) and contain a Core key of at least 32 characters.

Use `--install-dir /absolute/path`, `--core-port 8092` and `--web-port 8081` for
separate installations. Their database volumes, provider identities and Runtime
state are independent. The microsandbox installation mode connects native Core to PostgreSQL through an
automatically selected loopback-only port, recorded in private installation state. Repeating the same installation command retains its
identities, secrets and data. The installer refuses mode, sandbox-provider and
revision changes on an existing installation. This includes enabling a sandbox
provider on an installation originally created without one; rerunning with new
flags does not migrate it.

## Expose Core and Web

For nodes added through Web, install with the intended shared HTTPS endpoint:

```sh
./install.sh --public-url https://core.example
```

The installer also uses this origin for the `wss` connection URL returned by
self-hosted Sessions, so remote Runtime hosts never receive a Compose-only
hostname. Configure your TLS reverse proxy with these routes:

| Path | Destination | Callers |
| --- | --- | --- |
| `/v1`, `/v1/*` | Core (loopback 8091) | Applications, with a Project API key |
| `/api/v1/*` | Core (loopback 8091) | Nodes and Runtime daemons, with their own machine credentials |
| Everything else, including `/core/v1/*` and `/node-install/*` | Web (loopback 8080) | Browsers and the console |

Web forwards signed-in `/core/v1` requests to Core with the Core key; operator
scripts call `/core/v1` on Core's loopback port instead of the public entry.
Preserve Host and support WebSocket upgrades. Web answers 404 on `/v1` and `/api/v1` and never
forwards them. A proxy that sends those paths to Web breaks application calls,
node enrollment and every Runtime daemon connection (`/api/v1/agent-daemon/ws`)
for Docker, microsandbox, self-hosted and E2B sandboxes alike. When upgrading from
a release whose Web forwarded node and daemon traffic, change this routing as the
new release goes live; see [Data and upgrades](operations.md#data-and-upgrades).
Both the node host and its sandbox guests must reach this address. Installation
does not create DNS records or certificates, nor expose a host port publicly.
Without this option, the console uses its loopback address for local access. Do not copy a
localhost download command to a different machine. Running plain `./install.sh`
is suitable for local console/API inspection; prepare the shared endpoint before
installing a deployment that will enroll nodes.

The distribution includes the pinned E2B SDK helper, so selecting E2B does not
require Python or pip installation on the Core host. Its private receipts live in
`state/e2b` and are mounted only into Core. Preserve them together with the database
and credential key when backing up or moving this installation. E2B template
preparation is described in the [E2B guide](../../services/agents-api/deploy/e2b/README.md).

## After installation

Start with an optional [API request](quickstart.md). The read-only example works
with the default installation. The execution example requires an installation
with either E2B configured through Web or a connected, ready Docker/microsandbox node.
The Hosted Sandbox Manager selects one scheme for the whole deployment. E2B uses
an account API key and a qualified immutable Runtime template; Core provisions
sandboxes without a node installer. Own machines use the existing node command.
Provider, per-sandbox resources and Runtime changes all require maintenance and
completed resource cleanup; see
[provider switching](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance).
Session creation supplies the model,
harness and write-only model credentials. Core owns sandbox preparation and
Runtime startup. Configuration is never injected into a public Agent instruction
or baked into a Runtime image.

[Operations](operations.md) covers health, restarts, data and provider changes.
A service health check proves neither model availability nor complete protocol
compatibility.

## Build a distribution

Release builders need the repository's full Linux toolchain and Docker. Build from
clean, committed source, with the pinned Codex platform package and a matching
MiniMax companion prepared through the existing Runtime build instructions:

```sh
export AGENTS_RUNTIME_CODEX_PACKAGE=/absolute/path/to/codex-linux-package
export MCODE_HARNESS_BUILD_DIR=/absolute/path/to/mcode-harness-artifact
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://downloads.example/releases/COMMIT
make build-core-distribution
```

The builder reuses existing Core, Runtime, SDK and Web build scripts. It records
the commit, immutable image identities, microsandbox binary hashes and the actual
Runtime OCI manifest digest. Build output lives under `~/.parsar/build/`; it is
not automatically published to GitHub, an image registry or a website. Qualify the
exact bundle before distribution. See the [contributor guide](https://github.com/MiniMax-AI/parsar-core/blob/main/CONTRIBUTING.md).

The release base must host the generated flat asset filenames over HTTPS. Use
`CORE_DISTRIBUTION_OFFLINE=1` to also emit a full offline archive; a build without
any release URL must select offline mode. A fully disconnected console deployment
uses that empty-URL offline build; remote node installers then obtain assets from
the console. The release workflow prepares pinned
harness dependencies, builds versioned assets, and uploads an Actions artifact.
An explicit manual option can create an unpublished draft release. Neither a
successful build nor a draft makes a private repository anonymously downloadable;
publish qualified assets through your chosen distribution channel before sharing
installation instructions with external users.

## Produce and qualify a release

The `core-release` GitHub Actions workflow builds production assets from a full
committed source SHA. It uses the existing pinned Runtime builders; acceptance
credentials and private test certificate authorities must never enter its inputs.
Run the workflow from the repository's Actions page, or use:

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/parsar-core --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

The workflow uploads the matched files as an Actions artifact and creates a draft
Release whose tag is that full SHA. The manifest records the same tag in every
asset URL. Do not mix files across releases or resolve individual components
through `latest`. The node command comes from its connected Core, which selects
the matching release automatically.

Download the draft assets using repository access, verify their checksums, and
qualify a fresh installation plus the node/self-hosted connection paths before
publishing the draft. A workflow build alone is not live acceptance. Retain the
exact tested assets when publishing; do not rebuild or replace files under the
same release identity. Publishing a Release does not change repository visibility.

For an offline installation, provide the extracted matching archive through the
existing `--offline-root` option where supported. Remote node commands use the
manifest's release URL; use the explicitly configured console-hosted offline
build described above when node hosts cannot access that URL. Download access
errors should be fixed at the distribution source, without passing repository
credentials into Runtime or changing its executor authorization.
