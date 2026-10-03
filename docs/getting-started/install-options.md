---
title: "Installation options and advanced deployments"
---

The [default installation](./install.md) needs no options. Use this page to run behind an existing reverse proxy or install without internet access.

Pass options to the downloaded script:

```sh
./install.sh --public-url https://core.example
```

With the one-line command, append them after `bash -s --`. `--version TAG` selects a published release; otherwise the script selects the latest stable release and verifies each Compose file's SHA-256. A failed step stops installation without a success message.

## Docker Compose and hosting platforms

Use the `compose.yaml` from a release with Docker Compose 2.26 or newer on Linux amd64. The release pins its initialization image and source revision in the [Compose template](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/compose.yaml). Core and Web use the `latest` images, and PostgreSQL uses `postgres:16-alpine`. It starts PostgreSQL, Core and Web. Web forwards `/v1` and `/api/v1` to Core. Data is bind-mounted from a directory. The one-time initialization service generates random secrets there and prepares the node installer; Core applies database migrations when it starts. [Compose configuration](../configuration.md#compose-installations) owns the settings and the data directory.

For a local trial, download `compose.yaml` from a release into an empty directory, then run:

```sh
docker compose up -d --wait --wait-timeout 900
docker compose exec web oac-web core-key
```

`oac-web core-key` prints the generated Core key to your terminal without writing it to container logs. Open `http://localhost:8080` and use that key to sign in. All installation secrets are generated automatically; keep the same Compose project and its data directory when restarting.

The initialization image contains only the small node installation metadata alongside the initialization command. First startup verifies and copies that metadata without downloading the control archive or requiring access to GitHub Releases. Later starts verify the saved files. Container images still need to be pulled. An interrupted first initialization can be rerun; an existing database with missing installation secrets is refused.

You can deploy before choosing a domain: leave `OAC_PUBLIC_URL` unset or empty, then follow [Compose configuration](../configuration.md#compose-installations) to set it and redeploy once the platform's domain is ready. The initial localhost origin allows services to start; Web accepts the configured host only, so platform-domain access becomes available after that redeployment.

### Dokploy

Create a Docker Compose application and paste `compose.yaml`. Set `OAC_PUBLIC_URL` to the public HTTPS origin, enable isolated deployment, and add a domain for service `web`, port `8080`. Enable **HTTPS** and select a certificate provider such as **Let's Encrypt** for that domain before deploying. Remove the `ports` block from service `web` before deploying so Dokploy routes to its container port without publishing a host port. The [template metadata](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/dokploy.toml) supplies the generated domain and environment when packaging this Compose file for Dokploy's template catalog; HTTPS and its certificate provider still need to be enabled after import.

### Coolify

Create a **Docker Compose Empty** service and paste `compose.yaml`. Set `OAC_PUBLIC_URL` to the public HTTPS origin and assign that domain to `web` on port `8080`. Add Coolify's `exclude_from_hc: true` to the `init` service definition so completed initialization does not affect its overall health. Remove the `ports` block from service `web`, then save and deploy; Coolify routes to its container port and supplies HTTPS.

On either platform, open its server terminal and run `docker compose ls` to find the deployed project name and Compose file. Using those exact values and the deployment's `OAC_PUBLIC_URL`, run `docker compose -p <project-name> -f <compose-file> exec web oac-web core-key`, then sign in at the configured origin. The [Dokploy domain guide](https://docs.dokploy.com/docs/core/docker-compose/domains) and [Coolify Compose guide](https://coolify.io/docs/services/configuration/docker-compose) describe their domain and service controls. These are importable deployment files; no hosted marketplace listing is published by this repository.

After signing in, choose the sandbox backend and add nodes using [Nodes](./nodes.md). The Compose stack deploys the control plane; execution machines remain separate.

Stop with `docker compose stop` using the same files and environment. Back up the data directory together while the services are stopped. Follow the [installation version policy](./operations.md#installation-version-policy): a different release needs a new Compose project and a fresh data directory.

## Process settings

These flags are written to `.env` once. After installation, edit that file and run `oac apply`. Rerunning the installer does not change an installation that has already started.

| Flag | `.env` variable |
| --- | --- |
| `--public-url` | `OAC_PUBLIC_URL` |
| `--host` | `OAC_HOST` |
| `--web-port` | `OAC_WEB_PORT` |


## Installation actions

`--install-dir` chooses where the installation is created. It is not a process setting.

| Option | Purpose |
| --- | --- |
| `--install-dir DIR` | Absolute installation directory; defaults to `~/.oac/core`. A new installation requires an empty or missing directory, or one holding an [installation that never started](./install.md#install) |

Several installations can share a machine when they use distinct installation directories and ports. Use distinct IP addresses or a shared reverse proxy for more. Each installation has its own database, Core key and nodes.

## Sandbox backend

The installer saves no sandbox backend. After signing in, open **System** → **Manage sandbox configuration** and choose Docker, microsandbox or E2B; Web proposes the Standard size in [`standard-sizes.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/src/features/sandbox/standard-sizes.json). The choice is stored in Core's database. To change it later, [reset the deployment](./nodes.md#change-the-sandbox-configuration). Docker shares each node's kernel with its sandboxes, and its node service account is [root-equivalent](./nodes.md#what-the-installer-sets-up). E2B needs a public HTTPS URL that is not loopback, because E2B's sandboxes call Core from E2B's cloud. Prepare an E2B template with the [E2B guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md).

## Listeners and access

The default installation publishes Web on `--web-port` (8080) at `--host 0.0.0.0`. Core and PostgreSQL stay private. `--host` is an IPv4 or IPv6 address, without a port, scheme or zone. Use a concrete server IP in the browser, not a wildcard.

`--public-url` sets `OAC_PUBLIC_URL`, the origin applications, nodes and executors use. Behind a reverse proxy, set it to the HTTPS origin the proxy serves. Without it, the installer uses `http://<address>:<web port>` when `--host` is `0.0.0.0` and the host's default route has a private-network address, and `http://localhost:<web port>` otherwise. [Changing the public URL](../configuration.md#changing-the-public-url) lists the accepted origins.

### Ports

The installer checks `--web-port` before it downloads images.

- `--host` is the address that port binds. `0.0.0.0` publishes Web on every IPv4 interface. `127.0.0.1` keeps Web on this machine.
- A busy port stops installation. It does not move to another port.

`oac apply` recreates Web when `OAC_HOST` or `OAC_WEB_PORT` changes.

## HTTPS and the reverse proxy

Install with `--host 127.0.0.1` and point your reverse proxy at Web, `127.0.0.1:8080` by default. Web routes `/v1`, `/api/v1` and `/docs` to Core and serves everything else itself.

The proxy must:

- **Preserve Host.** Web accepts only the host of `OAC_PUBLIC_URL`.
- **Pass WebSocket upgrades** on `/api/v1`.
- **Not buffer or time out streams.** `/v1` streams Session events.
- **Accept large uploads.** Source files may reach 512 MiB; Core enforces the limits.

**Caddy** obtains the certificate itself and passes Host and WebSockets by default:

```caddyfile
core.example {
	reverse_proxy 127.0.0.1:8080
}
```

Then set `OAC_PUBLIC_URL=https://core.example` in `~/.oac/core/.env` and run `~/.oac/core/oac apply`. Check the routing:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'OpenAI-Beta: agents=v1' https://core.example/v1/agents
```

`401` means `/v1` reached Core, which asks for a key. `404` means it reached Web: fix the proxy, or application calls and every node connection will fail.

TLS verification stays on everywhere. With a private certificate authority, node hosts, self-hosted machines and the Runtime image must trust it.

### Try it locally with a quick tunnel

A [Cloudflare quick tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/) gives a trial installation with external ingress a temporary public HTTPS address. It forwards to one port, so put a local proxy with the same routes in front:

```caddyfile
http://:8443 {
	bind 127.0.0.1
	reverse_proxy 127.0.0.1:8080
}
```

1. Start the proxy: `caddy run --config Caddyfile`.
2. Start the tunnel: `cloudflared tunnel --url http://127.0.0.1:8443`. It prints an address such as `https://random-words.trycloudflare.com`.
3. Set that address as `OAC_PUBLIC_URL` in `~/.oac/core/.env` and run `~/.oac/core/oac apply`.

The address changes whenever `cloudflared` restarts; nodes bound to the old address must then be added again. Throughput is low, so a node's first Runtime download (about 500 MB) can be slow; see [slow links](./nodes.md#rerun-expiry-and-slow-links).

## Offline hosts

This installer does not install from an offline bundle. It downloads Compose files and container images from the release.
