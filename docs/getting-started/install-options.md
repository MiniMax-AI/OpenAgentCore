---
title: "Installation options"
---

Reference for the installer's flags, and how to install differently from the default: behind an existing reverse proxy, with Docker Compose on a hosting platform, or on a host without internet access. The [default installation](./install.md) needs no options.

## Pass flags {#pass-flags}

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --public-url https://core.example
```

```powershell [Windows]
& ([scriptblock]::Create((irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1))) --public-url https://core.example
```

:::

On Windows, `irm … | iex` cannot pass flags and the default execution policy blocks a downloaded `.ps1` file, so run the script as a script block. The script selects the latest stable release unless you pass `--version`, and verifies the native command and Compose files against their SHA-256 checksums.

## Flags

| Flag | Default | Written to `.env` as | Effect |
| --- | --- | --- | --- |
| `--public-url` | Derived from the host's private address, or `http://localhost:<web port>`; see [Choose the public URL](./public-address.md#choose-the-public-url) | `OAC_PUBLIC_URL` | The origin applications, nodes and executors use |
| `--host` | `0.0.0.0` | `OAC_HOST` | The address Web binds; see [Listeners and access](./public-address.md#listeners-and-access) |
| `--web-port` | `8080` | `OAC_WEB_PORT` | The host port of Web; see [Ports](./public-address.md#ports) |
| `--install-dir` | `~/.oac/core` | | Absolute installation directory. Use an empty or missing directory for a new installation, or an existing one to [retry](./install.md#install) or upgrade |
| `--version` | `latest` | | A published release tag |
| `--upgrade` | | | Upgrade the existing installation in `--install-dir` to the installer's release; see [Upgrade](./operations.md#upgrade) |

The flags that map to `.env` are written once. After installation, edit that file and run `oac apply`. Rerunning the installer on an existing installation ignores them, names those that differ from the saved settings, and starts it with the saved settings.

Several installations can share a machine when they use distinct installation directories and ports. Use distinct IP addresses or a shared reverse proxy for more. Each installation has its own database, Core key and nodes.

## Reverse proxy and HTTPS

Install with `--host 127.0.0.1`, point your reverse proxy at Web and set `--public-url` to its HTTPS origin. [Configure the public address](./public-address.md#https-and-the-reverse-proxy) has the proxy requirements, Caddy and nginx examples and the routing check, and covers `--host`, `--web-port` and the port check.

## Docker Compose and hosting platforms

Use the `compose.yaml` from a release on any [supported Core host](./install.md#prerequisites). The release pins its initialization, Core, Web and PostgreSQL images and source revision. PostgreSQL 16 is the official image, pinned by digest and mirrored to GHCR with the other images. It starts PostgreSQL, Core and Web. Data lives in a named Docker volume. The one-time initialization service generates random secrets there and prepares the node installer; Core applies database migrations when it starts. [Compose configuration](../configuration.md#compose-installations) owns the settings and the data directory.

For a local trial, download `compose.yaml` from a release into an empty directory, then run:

```sh
docker compose up -d --wait --wait-timeout 900
docker compose exec web oac-web core-key
```

`oac-web core-key` prints the generated Core key to your terminal without writing it to container logs. Open `http://localhost:8080` and use that key to sign in. All installation secrets are generated automatically; keep the same Compose project and its data volume when restarting.

The initialization image contains only the small node installation metadata alongside the initialization command. First startup verifies and copies that metadata without downloading the control archive or requiring access to GitHub Releases. Later starts verify the saved files. Container images still need to be pulled. An interrupted first initialization can be rerun; an existing database with missing installation secrets is refused.

You can deploy before choosing a domain: leave `OAC_PUBLIC_URL` unset or empty, then follow [Compose configuration](../configuration.md#compose-installations) to set it and redeploy once the platform's domain is ready. The initial localhost origin allows services to start; Web accepts the configured host only, so platform-domain access becomes available after that redeployment.

### Dokploy

1. Create a Docker Compose application and paste `compose.yaml`.
2. Set `OAC_PUBLIC_URL` to the public HTTPS origin and enable isolated deployment.
3. Add a domain for service `web`, port `8080`. Enable **HTTPS** and select a certificate provider such as **Let's Encrypt** for that domain.
4. Remove the `ports` block from service `web`, so Dokploy routes to its container port without publishing a host port.
5. Deploy.

The [template metadata](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/dokploy.toml) supplies the generated domain and environment when packaging this Compose file for Dokploy's template catalog; HTTPS and its certificate provider still need to be enabled after import.

### Coolify

1. Create a **Docker Compose Empty** service and paste `compose.yaml`.
2. Set `OAC_PUBLIC_URL` to the public HTTPS origin and assign that domain to `web` on port `8080`.
3. Add Coolify's `exclude_from_hc: true` to the `init` service definition so completed initialization does not affect its overall health.
4. Remove the `ports` block from service `web`, then save and deploy. Coolify routes to its container port and supplies HTTPS.

On either platform, open its server terminal and run `docker compose ls` to find the deployed project name and Compose file. Using those exact values and the deployment's `OAC_PUBLIC_URL`, run `docker compose -p <project-name> -f <compose-file> exec web oac-web core-key`, then sign in at the configured origin. The [Dokploy domain guide](https://docs.dokploy.com/docs/core/docker-compose/domains) and [Coolify Compose guide](https://coolify.io/docs/services/configuration/docker-compose) describe their domain and service controls.

After signing in, [choose a sandbox backend](./sandbox-backends.md) and add nodes using [Add and manage nodes](./nodes.md). The Compose stack deploys the control plane; execution machines remain separate.

Stop with `docker compose stop` using the same files and environment. Back up the data volume and configuration together while the services are stopped. To upgrade, back up first, then deploy the new release's `compose.yaml` with the same project, environment and data volume: initialization upgrades the volume and Core migrates the database. [Upgrade](./operations.md#upgrade) describes what is kept and why an older release is refused.

## Offline hosts

The installer downloads release files from GitHub Releases with this shell's network settings, including `HTTPS_PROXY`. Docker pulls the images through its daemon, which does not read the shell's proxy variables. Every image, PostgreSQL included, comes from GHCR (`ghcr.io`); Docker Hub is not needed. When GHCR is reachable only through a proxy or mirror, configure [the daemon's proxy](https://docs.docker.com/engine/daemon/proxy/) or a [registry mirror](https://docs.docker.com/docker-hub/image-library/mirror/), then rerun the installer.

A Linux amd64 host without that access installs from the release asset `oac-<commit>-linux-amd64-offline.tar.gz`, which contains every image and node artifact. Copy it to the host, extract it and run its `install.py` with Python 3.9 or newer and Docker. No checkout, Go, Node or pnpm is needed on that host. Keep the extracted bundle until installation completes; the installed services and node downloads do not depend on its original location afterward. The same archive is what [Develop OpenAgentCore](../development.md#install-a-checkout-without-publishing-a-release) builds from a checkout.

### `install.py` options

```sh
python3 oac-<commit>-linux-amd64/install.py --install-dir "$HOME/.oac/local" --host 0.0.0.0 --public-url http://YOUR_CORE_HOST:8080
```

| Option | Default | Effect |
| --- | --- | --- |
| `--install-dir` | `~/.oac/local` | Absolute installation directory |
| `--host` | `127.0.0.1` | The address Web binds. Unlike `oac install`, `install.py` defaults to loopback: pass `0.0.0.0` for access from other machines |
| `--web-port` | `8080` | The host port of Web |
| `--public-url` | | The origin browsers and nodes use. Required for a new installation |
| `--upgrade` | | Upgrade an existing installation to this bundle's release; see [Upgrade](./operations.md#upgrade) |

Replace `YOUR_CORE_HOST` with an address your browser and sandbox nodes can reach, or use your HTTPS origin behind a reverse proxy. A new installation prints the Core key; a rerun prints the command that shows it. The installation is the standard Compose installation plus a read-only `node-payload` directory holding the bundle's public node artifacts. Allow disk space for both the extracted bundle and the installed copy.

Rerunning the same bundle's `install.py` on its installation directory resumes the installation with the saved settings. A bundle from another commit refuses an existing installation unless you pass `--upgrade`. If a new installation fails before it starts the services, it leaves the installation directory as it found it; fix the cause and rerun the same command.

For a startup failure or a stopped installation, use the saved configuration instead of reinstalling:

```sh
cd "$HOME/.oac/local"
docker compose logs --tail 100
# Retry without downloading release images or replacing saved configuration.
docker compose up -d --wait --pull never
./oac core-key --show
```
