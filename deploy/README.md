# Deployment

| Path | Contents |
| --- | --- |
| `install.sh`, `install.ps1` | Native command launchers published with each release |
| `install-source.py` | Offline source-bundle installation; see [source installation](../docs/development.md#install-a-checkout-without-publishing-a-release) |
| `compose/` | Compose template and its tests |
| `distribution/` | Image Dockerfiles |
| `node/` | [Node installer](node/README.md), packaged as `node-install.pyz` |

## Installation

`install.sh` and `install.ps1` select a native `oac` release binary, verify its checksum and invoke `oac install`. That Go command owns the same installation lifecycle on Linux, macOS and Windows: validate Docker, verify release configuration, prepare `.env` and the host command, pull images, publish the installation directory, initialize the data volume and start Compose. [Installation](../docs/getting-started/install.md#prerequisites) owns platform prerequisites; [Configuration](../docs/configuration.md) owns settings and storage.

Preparation happens in `<install-dir>.staging`. On entry, the installer removes an empty stage or one with the empty ownership directory `.oac-installer-<hash of the installation path>`; it preserves unrecognized directories. Ordinary failures remove owned staging immediately. The ownership marker is created atomically before downloading files. A killed process leaves staging for the next attempt. The release's `compose.yaml` is verified against its published checksum in the stage and must name the installer's own release in `OAC_REVISION`. Publication happens only after downloads, checks and pulls succeed. Later failures preserve saved configuration, containers and the Docker data volume. `<install-dir>.lock` is a persistent lock directory shared by installation and mutating operator commands; the existing `runtimefs` platform adapter owns locking.

An existing directory runs the release named by `OAC_REVISION` in its `compose.yaml`. When that is the installer's release, the installer resumes: it keeps the saved settings, pulls only missing images and starts the services the way `oac apply` does. The port check applies only to new directories. Another release is refused unless `--upgrade` is given. An upgrade stages the new `compose.yaml` and `oac`, pulls the new images, then runs `oac-core check-config` and init against the staged `compose.yaml` together with the directory's `.env` and `compose.override.yaml`. Init upgrades the data volume or refuses, for example a downgrade; until it succeeds, the running installation is unchanged. The installer then renames `oac` and `compose.yaml` into place and starts the services with `--pull never --remove-orphans`, recreating every changed container. Rerunning the same command retries an interrupted upgrade. The upgrade leaves the deployment's Runtime unchanged; the final message names the console step that applies the new one.

`install.py --upgrade` upgrades a source-bundle installation the same way, under the same lock. It publishes the bundle's node payload beside earlier releases, renders the new `compose.yaml` and rewrites only its own `compose.override.yaml` entries, the four service images and Web's `/node-payload` bind, keeping every other entry. After the same two checks it moves the payload's `active.json`, renames `oac`, `compose.override.yaml` and `compose.yaml` into place and starts the services. On its own release it resumes like `oac install`; another release is refused without `--upgrade`, and `--public-url` is required only for a new directory.

`install.py` prepares a new installation in a `.staging-*` directory inside the installation directory: the node payload, `.env`, `oac` and both Compose files. After `docker compose config` accepts them, it moves them into place, `compose.yaml` last. An earlier failure removes the stage, and the directory too when the run created it, so the same command can be rerun; the next run also removes a stage left by a killed run. A directory with other contents but no `compose.yaml` is refused.

The host `oac` command also implements `apply`, `core-key`, `rotate-core-key` and `uninstall`. `apply` runs init, which finishes a first start that stopped before it and refuses a data volume committed to another release, then checks Core configuration before starting services and recreating those that changed. Reading the key executes `oac-web core-key` in Web. Rotation runs in the initialization container, where Linux file ownership is identical on every host, then restarts Core and Web. Start, stop and logs use `docker compose`; `oac uninstall` removes the installation, its data volume, the lock and the sidecar. The initialization image verifies and copies bundled node metadata without network access. Its receipt authenticates immutable metadata and credentials; the operator-rotatable Core key and its digest remain mutable. No service receives a Docker socket. Initialization emits structured step logs without credential values; view them with `docker compose logs --timestamps init`.

Web serves the console and forwards `/v1` and `/api/v1` to Core, so it is the only published service. HTTPS is terminated by the operator's reverse proxy or hosting platform, which routes to `web:8080`; `OAC_PUBLIC_URL` records that origin.

## Native daemon installer

`oac-daemon install` installs the daemon and selected Harnesses on a self-hosted machine. The [credential contract](../contracts/agents-api/environment-executor-credentials.md#installation-grant) covers the grant it claims. The release catalog is in the Core image at `/opt/oac/native-installers`; Core serves it from there. Node installation is separate and stays in `node-install.pyz`.
