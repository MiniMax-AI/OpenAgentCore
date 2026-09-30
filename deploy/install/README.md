# Installer design rules

This directory holds the Core/Web installer, the `oac` command and the node installer. The self-hosted daemon installer (`oac-daemon install`) lives in `apps/daemon/internal/cli`; its rules are in [Native daemon installer](#native-daemon-installer). These are the rules to keep when you change them. Operator usage is in the [installation guide](../../docs/getting-started/install.md), the [node guide](../../docs/getting-started/nodes.md) and the [self-hosted guide](../../docs/getting-started/self-hosted.md); the version policy is in [Operations](../../docs/getting-started/operations.md#installation-version-policy). Building and publishing distributions is in the [maintainer guide](../../docs/maintainers.md). `make check-distribution` runs this directory's tests.

| Module | Role |
| --- | --- |
| `install.sh`, `install.py` | Core/Web installer: host checks, fresh installation and same-bundle repair |
| `oac_cli.py` | The `oac` command (`status`, `start`, `stop`, `apply`, `domain`, `rotate-core-key`), packaged as `oac.pyz` |
| `config.schema.json`, `config_model.py` | The `config.json` schema, its defaults and the subset validator |
| `configuration.py` | Everything under `generated/` and the service input digests |
| `ingress.py`, `ingress_config.py` | Managed HTTPS gateway and the domain operation |
| `native_service.py` | Native Core as a systemd user service |
| `native_installers.py` | Retains the native daemon installer catalog and offline archives |
| `sandbox_setup.py` | The install-time sandbox selection through Core's administrator API |
| `distribution.py` | Shared manifest verification, verified downloads and image identity |
| `node_install.py`, `node_spec.py`, `node_generations.py` | Node installer and generation helper, packaged as `node-install.pyz` |
| `install_display.py`, `install_output.py`, `node_output.py` | Shared terminal formatting and completion guidance |
| `model_provider_sessions.py` | Read-only count of Sessions without a frozen model provider |
| `installer_fakes.py`, `acceptance.py`, `test_*.py` | Test fakes, opt-in real-model acceptance and tests |

## Scope

- Installers prepare hosts and services. Core alone owns Session allocation, initialization, cancellation, snapshots and cleanup. No installer creates an execution Session or supplies a model credential.
- The Core/Web installer never adds its own host as a node, imports no Runtime image and gives Core neither the Docker socket nor host devices. Nodes are added afterwards from Web, this host included.
- Ingress is an installation concern, independent of Runtime and Sandbox Provider selection.
- A repeated installation or repair never changes provider identity, the backend namespace or native history. The database, Projects and their keys, provider identity and the credential encryption key survive repair.
- Recovery never deletes data and never prunes containers, volumes or images.
- Do not add another launcher, scheduler, supervisor or recovery path.

## Configuration and apply

- Every process setting has one home: the installation's private `config.json`, described by `config.schema.json`. Keep the schema, `config_model.py`, the generator and the reference tables that `scripts/config-reference.py` renders into [configuration](../../docs/configuration.md) and [installation options](../../docs/getting-started/install-options.md) in step.
- Installation flags only seed `config.json`. A rerun of the installer accepts only `--install-dir` and repairs.
- `oac apply` validates `config.json`, derives `generated/` and converges on what actually runs. Each service carries the digest of its inputs (Compose label `io.oac.inputs`, native `OAC_INPUTS`), and exactly the services whose running inputs differ are recreated or restarted. Decide restarts from what runs, never from recorded bookkeeping, so the next apply finishes an interrupted one. Apply contacts running services at the last applied address before changing listeners.
- Health checks, setup, apply and generated service files derive addresses from the same `config.json`. `host` selects the gateway listener for managed ingress and the Core and Web listeners for external ingress. A non-loopback external bind requires an HTTPS public origin.
- `configuration.listeners` lists every host listener from the same helpers that render the port mappings and listen addresses, so port checks and real binds cannot diverge. A new installation checks them all before it hashes the bundle or loads images, and `oac apply` checks those a change adds before it touches a service; the installation's own listeners do not count. The probe binds with `SO_REUSEADDR`, as the services do. Where a port overlaps one of the installation's own listeners, or the account may not bind a port below 1024, it reads the kernel's listening sockets instead.
- Runtime settings stay in PostgreSQL and change through Web or `/core/v1`. Secrets live once each in `secrets/`; identity and installation facts live in the tool-written `state.json` (format 2, with an `oac-` Compose project). `config.json` has its own format, 1.
- Core reads only its environment and has no configuration loader; it serves the non-secret snapshot at `GET /core/v1/installation`. Do not add a second operator configuration file, loader precedence, hot reload, fallback to earlier setting names or an embedded Core node.
- Names: Core settings use `OAC_*`, Web settings `OAC_WEB_*`, shared Go logging `OAC_LOG_*`. A renamed setting fails startup even when empty or when the new name is also set; report every matching name without its value. Executables are `oac-core`, `oac-core-migrate`, `oac-core-device`, `oac-core-environment-key`, `oac-node`, `oac-web`, and the Core-host E2B helper `oac-e2b-provider` under `/opt/oac/e2b` in the image. The default installation directory is `~/.oac/core`; generated files carry `x-oac` annotations.

## Versions and the lock

- Install only into an empty directory, or repair the same source revision. Refuse older formats and different revisions before changing anything; keep their data and direct the operator to install separately. Distributions carry only current installation code: no conversion, migration or binary replacement. Keep the refusal checks and their tests.
- The packaged `oac.pyz` embeds its build revision and refuses a `state.json` whose `source_commit` differs.
- The installer and every mutating `oac` command share `.oac.lock`. The installer holds it across creation, payload, native service and launcher repair, and apply, calling the already-locked apply implementation without locking again. Never unlink or replace the lock file, even after an interrupted fresh installation; its inode must stay stable.

## Install-time sandbox selection

`--sandbox docker|microsandbox|e2b|none` (default `microsandbox`; only `none` with `--web-only`) is a one-time action. After the services are healthy, the installer posts `/core/v1/sandbox/deployment` once, as Web's setup would, and never on a repair. The choice is not written to `config.json`; PostgreSQL owns it, and an existing database selection is never overwritten.

- Docker and microsandbox use Web's Standard size from `apps/web/src/features/sandbox/standard-sizes.json`, which the distribution build copies into the bundle. Keep no other copy of those values.
- `docker` prints its weaker isolation and needs a y/N confirmation or `--accept-docker-risks` before anything is created.
- `e2b` needs a non-loopback HTTPS `public_url`, `--e2b-api-key-file` and `--e2b-template`; otherwise the installer refuses before installing anything.
- A Docker or microsandbox selection with a loopback `public_url` is saved, but no node can serve it until `public_url` is guest-reachable HTTPS.
- `--sandbox-provider` and `--provider` fail with a message naming `--sandbox`.

## Accounts and permissions

- The Core/Web installer runs as the launching account, root included, in a writable installation directory. It never invokes sudo, switches accounts or changes Docker permissions. Check the actual platform, Docker and directory prerequisites; root alone is no reason to refuse.
- `--native-core` runs Core as a systemd user service of that account, with lingering, and keeps PostgreSQL and Web in Compose with a private loopback database port. Native Core needs no KVM or node assets.
- Installation state and secrets are private under `~/.oac/`. No credential enters build arguments, image layers, browser bundles or diagnostic output. The Compose file is confidential.
- The distribution build uses umask 022 so non-root service users can read the payload; installation credentials and state keep their private modes.

## Managed HTTPS

A default combined Docker installation adds two Compose services from one pinned image: `gateway` runs Caddy, and `installation` runs the packaged `oac domain-server`. The latter runs with the installing account's UID and its Docker socket access and calls the same locked apply implementation. Its only request surface is the private `ingress/api/api.sock`, with Core-key authentication and one typed domain action. Core and Web get no Docker socket, host process authority or writable installation configuration. Web gets only the private API socket directory, never Caddy's admin socket.

- The gateway publishes the initial Web port, and ports 80 and 443 only for HTTPS (`ingress_config.published`). Caddy issues and renews certificates and keeps its private data in `ingress/data`. `generated/Caddyfile` is derived from `config.json`, and apply reloads it through the private Caddy socket even when container inputs already match.
- A domain change first checks that the hostname resolves and that no other program holds port 80 or 443; ports the gateway publishes while it runs as written are its own. It confirms a public URL change from the applied address, because it returns Core there before switching. It keeps the old entry point while the common apply renders the candidate address: the gateway publishes 80 and 443 and serves the candidate, and the verification needs a trusted certificate and an installation-specific response over HTTPS. Only then does it set `public_url` and apply again. Failure restores the previous configuration, including the gateway without 80 and 443 when HTTPS was off, and reports incomplete recovery; failed retries restore through the common apply even after a partial change.
- The operation record keeps the last successfully applied public address. Apply and start update it after gateway verification and service health checks; generated files alone never prove that a new address is active. A successful apply reconciles the domain operation status after verifying the running services.
- The domain operation refuses unrelated pending `config.json` edits and shares `.oac.lock` with the CLI. Its status file is bookkeeping and the recovery receipt; `config.json` stays the source of desired settings. An interrupted operation keeps its desired files and a visible failure and retry state; it never creates another service project or deletes execution data.
- A Web restart ends console sessions, so the UI gives the new HTTPS sign-in address instead of treating a dropped request as success.
- Split and native installations use external ingress and report that automatic Web domain setup is unavailable.

## Output

- Progress describes the operation about to run. Do not imply fresh health checks on a no-change repair.
- Terminal styling is optional: honor `NO_COLOR` and keep redirected logs plain.
- Summaries show credential file locations, never their values.
- `install_display.py` owns shared terminal formatting; `install_output.py` and `node_output.py` own the completion guidance. Ship and checksum the display modules in both the node bootstrap and its retained helper.
- A node summary reports success only after Core connection and provider readiness are confirmed.
- Output from the service account stays plain and passes through the terminal-control sanitizer.

## Node installer

The node installer runs as root and prepares the host for one node per installation.

- It creates or adopts the `oac-node` system user, adds it to the `docker` or `kvm` group (no other group), and installs one root-owned system service per installation that runs the node program as `User=oac-node`. Nodes on a host share that account, so a host serves one Core.
- Docker group membership makes that user, and so the node, root-equivalent on the host; that is inherent to Docker sandboxes. microsandbox needs only `kvm`, user KVM access and the Linux runtime libraries.
- Node configuration and identity live under `~/.oac/nodes/<installation-id>/` in the node account's home (`/var/lib/oac-node`); microsandbox uses a separate short private Runtime home.
- The node service owns its provider processes outside the Core container. `KillMode=process` keeps resident microVM and helper processes across a service restart. The service restarts after failures with no start limit, so a node outlasts a Core outage, and stops restarting when the node program exits 78 because Core answered 401 to its credential (a removed node).
- It never installs Docker, KVM or packages and never changes device permissions. It refuses SELinux-enforcing hosts and changes nothing when a check fails. The enrollment token comes only on standard input, never in arguments or the environment.
- Files the service account owns are read, written and deleted only with that account's credentials. The one exception is root removing the account's home after `userdel`, when no process can still run as that account. That work runs in a child that starts its own session with `/dev/null` as input, joins a new session keyring and dies with its parent; root shows its output only as plain text (terminal controls become `?`). SIGINT, SIGHUP and SIGTERM stop that child and what it started.
- Root never runs a file the service account can write, opens a URL it wrote, or follows a link in its home. Capture the trusted bootstrap bytes before dropping to the service account and pass them through the fork; the service account writes its own retained generation helper. Never open the caller's private download directory to it or let root write into service-owned state.
- The generated bootstrap passes only the six standard HTTP/HTTPS proxy and bypass variables through sudo and gives both spellings the lowercase value when present, even if empty, so curl, urllib and the Go registration command follow the same rules. The installation child keeps just those names beside its fixed environment. Proxy values stay out of arguments, saved configuration, service units and diagnostics; never use broad sudo environment inheritance. This covers installation downloads only, not the node service.
- `--uninstall` removes a node only after Core rejects its credential, except for a node that never registered and with `--force`, which Web offers when the old Core address no longer responds. It never touches sandboxes, volumes or images (the Runtime image and the microsandbox store stay), deletes the account only when the installer created it and no node remains, and otherwise removes only the groups it added.
- Refuse resources of an older product name for the same installation ID; never adopt them or remove another installation's resources.

## Download contract

The distribution manifest is the one download contract for the Core and node installers: flat versioned file names, and the compressed and unpacked size and SHA-256 of the Runtime. The self-hosted counterpart is the native `catalog.json`, which Core serves as one `<platform>.sha256` per installer archive.

- The default installation downloads the Core, Web and PostgreSQL payloads, never the Runtime image or node execution artifacts. Core's image never acquires execution-only payloads. The offline archive stays an explicit option.
- A node obtains bootstrap metadata from the console that generated its command, or from a local offline bundle. Web serves artifacts it has locally and redirects missing declared execution artifacts to the versioned HTTPS release base in the verified manifest. Web never downloads or caches those bytes.
- Only artifact requests may follow HTTPS redirects, and only without credentials or cookies. Metadata and enrollment requests stay on the configured console. The console publishes only fixed non-secret files and declared artifact names.
- Download into private temporary files, verify size and SHA-256 before an atomic rename, resume interrupted transfers, and reuse only verified cache entries or exact image identities. Never select a release other than the pinned one.
- Python zipapps bundle the shared resolver with each remote bootstrap. The node asset is the `oac-node` binary.
- Release downloads are anonymous. Never add repository credentials to installed node or Runtime configuration.
- Manual builds use the `build-<full SHA>` release tag and tag builds the `v*` tag. The manifest's download base must match the release tag; artifact file names and source provenance keep the full source SHA.

## Image identity

The manifest's `images` records each exported image's config digest, and `image_manifest_digests` its OCI manifest or index digest. Derive and verify both from the same archive, including its referenced config and layer bytes, and require the build host's selected image ID to match one of them.

Docker's classic image store identifies images by config digest, and its containerd store by the OCI descriptor. The build therefore takes the digest the local store resolves from BuildKit's build metadata, never the `--iidfile` config digest alone, and disables provenance attestations so each image and archive holds one platform manifest in both stores. For the same reason the default PostgreSQL image is pinned by its linux/amd64 platform manifest digest: a pulled multi-platform tag keeps its whole index in the containerd store, and its export holds every platform.

The Core and node installers share one resolver for these identities. It confirms Linux amd64 and the returned immutable local ID, and service and provider configuration and Runtime launches use that ID. Tags never replace identity verification. The microsandbox `runtime_ref` is independent of Docker's local store identity.

## Native daemon installer

`oac-daemon install` installs the daemon and selected Harnesses on a self-hosted Linux, macOS or Windows machine; the [credential contract](../../contracts/agents-api/environment-executor-credentials.md#installation-grant) covers the grant it claims.

- Interactive selection and CLI-only installation share one options and validation path. There is no installation-options file. The saved installation state and explicitly supplied credential and tool-variable files serve runtime operation, not a second configuration language.
- Each release bundles pinned Node.js and npm, the native Harnesses and their adapter assets. Registration lives in the CLI, and native activation and readiness in each adapter's optional `agent.Installation` descriptor. Core never selects native paths or OS-specific steps.
- Bootstrap scripts only download and extract the current platform's archive, after verifying the checksum Core provides. Installation, startup, connection verification and execution stay common. Native bundles must match Core's source revision and Runtime wire version.
- Neither Core installation nor repair downloads native payloads. The Core installer keeps the catalog and any offline archives in the installation's private `native-installers/` directory, mounts it read-only into container Core and points native Core at the same files. Core serves the local offline archives or redirects to the catalog URL without proxying or caching; it verifies local archives when it starts, and a corrupt local archive stops Core from starting.
- Every mutation holds the installation directory lock. Publish complete, checksum-verified components from staging, then commit the configuration after native readiness passes. A rerun with the same connection settings adds the selected Harnesses and validates existing contents. Never overwrite, upgrade, repair or migrate installed components; missing, modified, wrong-platform or incompatible content is an explicit error. A partial addition keeps the old configuration and reusable complete components and removes nothing.
- Serialize background PID inspection and publication so concurrent starts cannot create two daemons. An installed daemon registers only the adapter kinds its verified installation manifest names; other Harness executables on `PATH` cannot extend it. Direct `connect` refuses an installed Runtime and points to `start`.
- The installer runs as the current user in writable directories and never elevates. Subprocess diagnostics never expose sensitive parameters or environment values. Readiness checks take the installer's cancellation context and reap their processes before returning.
- Report installation, authenticated connection and model configuration as separate results. Starting execution never downloads or installs Harnesses. Stop and reconnect keep capability snapshots and native Session state.
- `scripts/build-native-installer.mjs` validates pins and startup, hashes every component file, accepts only contained regular files and rejects escaping links. Before it packages Claude, the `native-check` workflow reinstalls the frozen `pnpm deploy` export with the hoisted linker, so flattening contained links keeps Node's dependency resolution; the Runtime image's Claude archive is unchanged. The `native-check` workflow builds and tests installation, addition and reuse, missing arguments and the unsupported Windows MiniMax case on Linux, macOS and Windows.

## Validation

`make check-distribution` covers the production proxy, the installation rules, release metadata and native catalog assembly, including bundle manifests larger than Node's default subprocess buffer (catalog assembly reads up to 64 MiB). Live release qualification and its stages are in the `scripts/promote-qualified-release.py` docstring. Diagnostics report observed service health, never fabricated model or environment readiness. Runtime observations belong to Core; do not add monitoring or lifecycle tracking to the installer or the landing site.
