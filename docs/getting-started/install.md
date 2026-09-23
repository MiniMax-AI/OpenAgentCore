# Install Core and Web

Install one matching Parsar Core distribution. By default it starts PostgreSQL,
Core and the existing Web console in containers, with zero execution nodes.
It does not import Runtime images, mount the Docker socket or host devices into
Core, or generate a managed Provider configuration. Add nodes through Web after
installation. A local sandbox provider is an optional installation choice.
No model key, Environment wizard or sample task is required during installation.

Recommended path: [install](#verify-extract-and-install) →
[sign in to Web](#sign-in-to-web) →
[add a node](#add-nodes-after-a-default-installation).
You can leave the deployment with zero nodes until you need execution.

## Host requirements

The first distribution targets Linux amd64 with Python 3.9+, Docker and Docker
Compose v2. Run the installer as a non-root user who can use Docker.
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

Installation creates private configuration under `~/.parsar/core`, a dedicated
PostgreSQL volume, an API caller key and a credential encryption key. It also
creates a separate console password and deployment administrator key. Secret values
are not printed.

Open the console address printed by the installer (`https://core.example` in
the example above). Sign in as `admin` using the password in
`~/.parsar/core/config/console.password`. The bundled console is already connected
to Core; you do not need to paste an API key.

Default local ports and private files:

- API: `http://127.0.0.1:8091/v1`
- Web upstream: `http://127.0.0.1:8080`; use the configured public URL in your browser
- Console password file: `~/.parsar/core/config/console.password`
- API caller key file: `~/.parsar/core/config/caller.key`
- Sandbox administrator key file: `~/.parsar/core/admin/sandbox-admin.key`

Use exactly the displayed console address; the production proxy validates its
configured browser origin. The console password authenticates to the Web server;
the server holds the independent Core key. Model keys remain API execution input.

Every Core installation creates a separate private deployment administrator key at
`admin/sandbox-admin.key`. Core loads its SHA-256 digest from
`AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`. Only the Core container mounts the
digest file read-only. The bundled Web server reads the separate administrator
key to proxy authenticated console operations; it never sends this key to the
browser. The migration service receives neither. A Web-only connection to an
external Core can enable management by configuring its administrator token
server-side through `CORE_CONSOLE_SANDBOX_ADMIN_TOKEN_FILE`; the Web page does not
ask the operator to enter another key. Use the same-origin console connection for
management. Choose English or Chinese through the System language selector.

## Add nodes after a default installation

1. Log in to the bundled Web console and open **Hosted Sandbox Manager**.
   The paired installation needs no second key or Core connection setup.
2. Choose Docker or microsandbox. The paired console address is used
   automatically. If your network requires a different address for nodes and
   guests, change it under advanced network settings during initial setup. When
   opening the console on localhost or an HTTP address, setup requires a
   non-loopback HTTPS address that nodes and sandbox guests can reach.
3. Select **Initialize sandbox deployment**. It takes effect without restarting Core and remains in
   PostgreSQL across restarts. All nodes in this deployment use the chosen type;
   this page does not switch providers. Microsandbox uses a five-minute idle
   timeout and one-day snapshot retention.
4. Click **Add node**, copy the command, and run it as a non-root user on the
   target Linux amd64 host. It downloads the matched bootstrap from your console and execution assets from
   the manifest's release location (or the offline console payload), verifies
   checksums, imports the Runtime image only when missing, writes the provider configuration,
   registers the node and starts a systemd user service. The installer waits for Core to confirm
   connection and provider readiness. Web refreshes node health
   automatically. Wait for the node to be online and its provider to be ready;
   registration alone does not mean it can accept work.

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
machine as a shared sandbox node. Create the Session through the public API, then
use your project caller credential to obtain a restricted credential for its
Environment. The [credential contract](../../contracts/agents-api/environment-executor-credentials.md)
describes issuance, rotation and revocation. Save the response to an owned,
mode-0600 file. Keep the project caller key on your application machine.

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
./install.sh --sandbox-provider true --provider microsandbox
./install.sh --sandbox-provider true --provider docker
./install.sh --core-only
./install.sh --core-only --sandbox-provider true --provider docker
```

`--sandbox-provider true` enables a local sandbox provider. If `--provider` is
omitted, it selects microsandbox. Supplying `--provider` without enabling the
sandbox provider is an error. `--core-only` installs Core and PostgreSQL without
Web and has no sandbox provider unless explicitly enabled.

The provider choice does not select a harness or alter the public `openai_hosted`
discriminator. Both providers reuse one colocated Runtime containing the native
harnesses. The Docker option grants
only Core access to the Docker socket; microsandbox uses the native service account's
KVM access. Web receives neither. An enabled local provider runs through Core's
embedded node connection and preserves its identity and owner epoch in the private
`state/sandbox-node` directory. Docker Core mounts only this state directory
writable in addition to its selected socket; `config` remains read-only.
Native Core uses the same persistent directory directly. The default zero-node
installation creates neither this state directory nor its mount.

### Local provider requirements

Optional microsandbox also requires glibc, a running systemd user manager with
linger enabled, and user read/write access to `/dev/kvm`. Nested cloud hosts must
expose hardware virtualization. With this option, Core runs as a native user
service so restarting it does not terminate the Provider's microVM processes.
The installer checks these
prerequisites; it does not grant host permissions or silently fall back to Docker.
The optional Docker sandbox provider keeps Core in Compose and requires neither
KVM nor systemd user services.

For optional microsandbox, reserve capacity for the native Runtime: its initial
profile uses
4 GiB RAM, 2 CPUs, an 8 GiB root disk and an 8 GiB environment disk per active sandbox, with at most 4 active
and 16 retained allocations. Limits are operator configuration, not model input.
Use a trusted, single-operator host and durable local storage. The installer does
not change host virtualization settings, install Docker, create an OS user or
expose a remote administration service.

### Separate Web installation

To install only Web on a Linux host, provide the existing Core origin and a
private caller-key file. A loopback Core uses the same host network namespace;
a remote Core must use HTTPS.

```sh
./install.sh --web-only \
  --install-dir "$HOME/.parsar/core-console" \
  --core-url http://127.0.0.1:8091 \
  --core-token-file "$HOME/.parsar/core/config/caller.key"
```

Web-only mode cannot enable a sandbox provider. It starts no database or Core and
requires no KVM. Its key remains on the server, outside the static Web files.
The input file must be private (0600).

Use `--install-dir /absolute/path`, `--core-port 8092` and `--web-port 8081` for
separate installations. Their database volumes, provider identities and Runtime
state are independent. Native Core connects to PostgreSQL through an automatically
selected loopback-only port, recorded in its private installation state. Repeating the same installation command retains its
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
hostname. Configure your TLS reverse proxy to forward that origin to the loopback Web port,
preserve Host, and support WebSocket upgrades. The bundled Web forwards the fixed
node and daemon transport routes to Core using their own credentials. Both the
node host and its sandbox guests must reach this address. Installation does not
create DNS records or certificates, nor expose a host port publicly. Without this
option, the console uses its loopback address for local access. Do not copy a
localhost download command to a different machine. Running plain `./install.sh`
is suitable for local console/API inspection; prepare the shared endpoint before
installing a deployment that will enroll nodes.

## After installation

Start with an optional [API request](quickstart.md). The read-only example works
with the default installation. The execution example requires an installation
with a connected, ready sandbox node, either installed locally or added through
Web. Session creation supplies the model,
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
