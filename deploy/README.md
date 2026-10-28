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

Preparation happens in `<install-dir>.staging`. On entry, the installer removes an empty stage or one with the empty ownership directory `.oac-installer-<hash of the installation path>`; it preserves unrecognized directories. Ordinary failures remove owned staging immediately. The ownership marker is created atomically before downloading files. A killed process leaves staging for the next attempt. The release's `compose.yaml` is verified against its published checksum in the stage and must name the installer's own release in `OAC_REVISION`. Publication happens only after downloads, checks and pulls succeed. Later failures preserve saved configuration, containers and the Docker data volume. `<install-dir>.lock` is a persistent lock directory shared by installation and mutating operator commands; the `runtimefs` platform adapter owns locking.

An existing directory runs the release named by `OAC_REVISION` in its `compose.yaml`. When that is the installer's release, the installer resumes: it keeps the saved settings, pulls only missing images and starts the services the way `oac apply` does. The port check applies only to new directories. Another release is refused.

`install.py` prepares a new installation in a `.staging-*` directory inside the installation directory: the node payload, `.env`, `oac` and both Compose files. After `docker compose config` accepts them, it moves them into place, `compose.yaml` last. An earlier failure removes the stage, and the directory too when the run created it, so the same command can be rerun; the next run also removes a stage left by a killed run ([Offline hosts](../docs/getting-started/install-options.md#offline-hosts)). A directory with other contents but no `compose.yaml` is refused.

The host `oac` command also implements `apply`, `core-key`, `rotate-core-key` and `uninstall`. Reading the key executes `oac-web core-key` in Web. The initialization image verifies and copies bundled node metadata without network access. Its receipt authenticates immutable metadata and credentials; the operator-rotatable Core key and its digest remain mutable. No service receives a Docker socket. Initialization emits structured step logs without credential values; view them with `docker compose logs --timestamps init`.

Web is the only published service; [Web server](../docs/web/console-server.md#forwarding-to-core) defines what it serves and forwards. HTTPS is terminated by the operator's reverse proxy or hosting platform, which routes to `web:8080`; `OAC_PUBLIC_URL` records that origin.

## Native daemon installer

`oac-daemon install` installs the daemon and selected Harnesses on a self-hosted machine. The [credential contract](../contracts/agents-api/environment-executor-credentials.md#installation-grant) covers the grant it claims. The release catalog is in the Core image at `/opt/oac/native-installers`; Core serves it from there. Node installation is separate and stays in `node-install.pyz`.
