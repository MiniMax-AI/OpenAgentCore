# Deployment

| Path | Contents |
| --- | --- |
| `install.sh`, `install.ps1` | Native command launchers published with each release |
| `compose/` | Compose template and its tests |
| `distribution/` | Image Dockerfiles |
| `node/` | [Node installer](node/README.md), packaged as `node-install.pyz` |

## Installation

`install.sh` and `install.ps1` select a native `oac` release binary, verify its checksum and invoke `oac install`. That Go command owns the same installation lifecycle on Linux, macOS and Windows: validate Docker, verify release configuration, prepare `.env` and the host command, pull images, publish the installation directory, initialize the data volume and start Compose. [Installation](../docs/getting-started/install.md#prerequisites) owns platform prerequisites; [Configuration](../docs/configuration.md) owns settings and storage.

Preparation happens in `<install-dir>.staging`. On entry, the installer removes an empty stage or one with the empty ownership directory `.oac-installer-<hash of the installation path>`; it preserves unrecognized directories. Ordinary failures remove owned staging immediately. The ownership marker is created atomically before downloading files. A killed process leaves staging for the next attempt. Publication happens only after downloads, checks and pulls succeed. Later failures preserve saved configuration, containers and the Docker data volume. Retries reuse those settings and cached images, pull missing images and start services with `--pull never --no-recreate`. `<install-dir>.lock` is a persistent lock directory shared by installation and mutating operator commands; the existing `runtimefs` platform adapter owns locking.

The host `oac` command also implements `apply`, `core-key` and `rotate-core-key`. `apply` checks Core configuration before recreating services. Reading the key executes `oac-web core-key` in Web. Rotation runs in the initialization container, where Linux file ownership is identical on every host, then restarts Core and Web. Start, stop, logs and removal use `docker compose`. The initialization image verifies and copies bundled node metadata without network access. Its receipt authenticates immutable metadata and credentials; the operator-rotatable Core key and its digest remain mutable. No service receives a Docker socket. Initialization emits structured step logs without credential values; view them with `docker compose logs --timestamps init`.

Web serves the console and forwards `/v1` and `/api/v1` to Core, so it is the only published service. HTTPS is terminated by the operator's reverse proxy or hosting platform, which routes to `web:8080`; `OAC_PUBLIC_URL` records that origin.

## Native daemon installer

`oac-daemon install` installs the daemon and selected Harnesses on a self-hosted machine. The [credential contract](../contracts/agents-api/environment-executor-credentials.md#installation-grant) covers the grant it claims. The release catalog is in the Core image at `/opt/oac/native-installers`; Core serves it from there. Node installation is separate and stays in `node-install.pyz`.
