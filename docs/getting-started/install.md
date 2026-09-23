# Install Core and Web

Install one matching Parsar Core distribution. By default it starts PostgreSQL,
Core and the existing Web console in containers, with zero execution nodes.
It does not import Runtime images, mount the Docker socket or host devices into
Core, or generate a managed Provider configuration. A local sandbox provider is
an explicit installation option.
No model key, Environment wizard or sample task is required during installation.

## Host requirements

The first distribution targets Linux amd64 with Python 3.9+, Docker and Docker
Compose v2. Run the installer as a non-root user who can use Docker.
The default installation requires neither KVM nor systemd user services.
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

## Verify, extract and install

Obtain the archive and checksum from a trusted distributor. Until a release is
published, build an archive using [Build a distribution](#build-a-distribution);
a source checkout alone is not an installable binary bundle. Do not substitute an
unpublished download URL.

```sh
sha256sum -c parsar-core-<commit>-linux-amd64.tar.gz.sha256
mkdir -p "$HOME/.parsar/releases"
tar -xzf parsar-core-<commit>-linux-amd64.tar.gz -C "$HOME/.parsar/releases"
cd "$HOME/.parsar/releases/parsar-core-<commit>-linux-amd64"
./install.sh
```

The bundle contains the same-revision native Core binaries and service image,
unchanged Web build, production Web proxy and colocated Runtime. It includes microsandbox's pinned runtime and
firmware. It also contains image archives, source provenance and checksums;
installation does not need Go, Node, Rust or a product checkout. The default
installation loads only the Core, Web and PostgreSQL images; Runtime and
microsandbox payloads are used only when a sandbox provider is enabled.
The matched `parsar-sandbox-node` executable is included in the native payload
and Core service image; packaging it does not start or register a node.

Installation creates private configuration under `~/.parsar/core`, a dedicated
PostgreSQL volume, an API caller key and a credential encryption key. It also
creates a separate console password and deployment administrator key. Secret values are not printed. On success:

- API: `http://127.0.0.1:8091/v1`
- Web console: `http://127.0.0.1:8080`, username `admin`
- Console password file: `~/.parsar/core/config/console.password`
- API caller key file: `~/.parsar/core/config/caller.key`
- Sandbox administrator key file: `~/.parsar/core/admin/sandbox-admin.key`

Use exactly the displayed console address; the production proxy validates its
configured browser origin. The console password authenticates to the Web server;
the server holds the independent Core key. Model keys remain API execution input.

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

Every Core installation creates a separate private deployment administrator key at
`admin/sandbox-admin.key`. Core loads its SHA-256 digest from
`AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`. Only the Core container mounts the
digest file read-only. The bundled Web server reads the separate administrator
key to proxy authenticated console operations; it never sends this key to the
browser. The migration service receives neither. A Web-only connection to an
external Core still requires its separate administrator key.

### Add nodes after a default installation

1. Log in to the bundled Web console and open **Hosted Sandbox Manager**.
   The paired installation needs no second key or Core connection setup.
2. Choose Docker or microsandbox and enter the HTTPS Core origin reachable from
   both node hosts and their sandbox guests. The address must expose Core's API
   and WebSocket routes, not just the Web console. HTTP loopback is available for
   local development only; loopback inside a sandbox is not the Core host.
3. Save the selection. It takes effect without restarting Core and remains in
   PostgreSQL across restarts. All nodes in this deployment use the chosen type;
   this page does not switch providers. Microsandbox uses a five-minute idle
   timeout and one-day snapshot retention.
4. Generate and copy the node command, then run it as a non-root user on the
   target Linux amd64 host. It downloads the matched files from your console,
   verifies checksums, imports the Runtime image, writes the provider configuration,
   registers the node and starts a systemd user service. Web refreshes node health
   automatically. A registered but unavailable node cannot accept work.

The target host needs Python 3.9+, a systemd user session with lingering enabled,
and either Docker socket access or microsandbox's KVM/native-library prerequisites.
The command checks host access before downloading the Runtime and verifies
microsandbox's shared libraries after downloading its native programs.
The console serves only fixed, non-secret distribution files at `/node-install/`;
private installation configuration is never part of this payload. Retain the
installed `node-payload/` directory.

See the [Hosted Sandbox Manager guide](https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/HOSTED-SANDBOX-MANAGER.md)
for host prerequisites and the registration command. The browser does not install
software on another machine or receive SSH credentials. Adding a remote node does
not add a Docker socket or KVM permissions to Core. Node installation and Runtime
storage remain on the selected host.

When a Session needs a sandbox, Core asks the
enabled Provider to create one from the prepared Runtime image and initializes
the colocated daemon, native harness and workspace.

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

For remote nodes, install with the intended shared HTTPS endpoint:

```sh
./install.sh --public-url https://core.example
```

Configure your TLS reverse proxy to forward that origin to the loopback Web port,
preserve Host, and support WebSocket upgrades. The bundled Web forwards the fixed
node and daemon transport routes to Core using their own credentials. Both the
node host and its sandbox guests must reach this address. Installation does not
create DNS records or certificates, nor expose a host port publicly. Without this
option, the console uses its loopback address for local access. Do not copy a
localhost download command to a different machine.

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
make build-core-distribution
```

The builder reuses existing Core, Runtime, SDK and Web build scripts. It records
the commit, immutable image identities, microsandbox binary hashes and the actual
Runtime OCI manifest digest. Build output lives under `~/.parsar/build/`; it is
not automatically published to GitHub, an image registry or a website. Qualify the
exact bundle before distribution. See the [contributor guide](https://github.com/MiniMax-AI/parsar-core/blob/main/CONTRIBUTING.md).
