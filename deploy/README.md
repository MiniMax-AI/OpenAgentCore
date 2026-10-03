# Deployment

| Path | Contents |
| --- | --- |
| `install.sh` | Host installer published with each release |
| `compose/` | Compose template and its tests |
| `distribution/` | Image Dockerfiles |
| `node/` | [Node installer](node/README.md), packaged as `node-install.pyz` |

## Installation

`install.sh` downloads its release's `compose.yaml`, checks it against `compose-sha256sums.txt`, writes `.env`, and starts Compose. Core applies database migrations when it starts. The host needs Linux amd64 and Docker Compose 2.26 or newer. [Configuration](../docs/configuration.md) owns the installation layout and settings.

`oac` is a Go command (`services/core/cmd/oac`) in the Core image and the ingress image. The host copy implements `apply`, `core-key` and `rotate-core-key`; `core-key --show` runs `oac-web core-key` in the Web container. Start, stop, logs and removal are `docker compose`. `apply` runs `oac-core check-config` before recreating services. The ingress image runs data initialization as `oac init`, verifies and copies its bundled node metadata without network access, and contains no Python. No service receives a Docker socket.

Web serves the console and forwards `/v1` and `/api/v1` to Core, so it is the only published service. HTTPS is terminated by the operator's reverse proxy or hosting platform, which routes to `web:8080`; `OAC_PUBLIC_URL` records that origin.

## Native daemon installer

`oac-daemon install` installs the daemon and selected Harnesses on a self-hosted machine. The [credential contract](../contracts/agents-api/environment-executor-credentials.md#installation-grant) covers the grant it claims. The release catalog is in the Core image at `/opt/oac/native-installers`; Core serves it from there. Node installation is separate and stays in `node-install.pyz`.
