---
title: "Installation options and advanced deployments"
---

The [default installation](./install.md) needs no options. Use this page to run behind an existing reverse proxy or install without internet access.

Pass options to the downloaded script:

```sh
./install.sh --public-url https://core.example
```

With the one-line command, append them after `bash -s --`. The release downloader also accepts `--version TAG` to select a published release; otherwise it selects the latest stable release. It verifies the bundle's SHA-256 before extracting it and keeps the verified bundle for [repair](./operations.md#installation-version-policy).

The installer prints each stage, then a summary of addresses, sign-in details and next steps. Set `NO_COLOR=1` to disable colors. A failed step stops installation without a success message.

## Docker Compose and hosting platforms

Use the self-contained [Compose template](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/compose.yaml) with Docker Compose 2.26 or newer on Linux amd64. It pulls the existing, digest-pinned v0.0.3 images and starts PostgreSQL, Core, Web and an HTTP gateway. The one-time initialization service generates random secrets in persistent volumes and prepares the node installer; the migration service initializes the database before Core starts. [Compose configuration](../configuration.md#compose-installations) owns the settings and volumes.

For a local trial, download `compose.yaml` and the [local port override](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/local.yaml) into one directory, then run:

```sh
docker compose -f compose.yaml -f local.yaml up -d --wait --wait-timeout 900
docker compose -f compose.yaml run --rm credentials
```

The `credentials` command prints the generated Core key to your terminal without storing it in container logs. Open `http://localhost:8080` and use that key to sign in. All installation secrets are generated automatically; keep the same Compose project and its volumes when restarting.

The first initialization downloads and verifies the release's approximately 385 MB control archive, retaining only the small node installation metadata. Later starts verify the saved files without downloading again. Image downloads are additional. An interrupted first initialization can be rerun; an existing database with missing installation secrets is refused.

You can deploy before choosing a domain: leave `OAC_PUBLIC_URL` unset or empty, then follow [Compose configuration](../configuration.md#compose-installations) to set it and redeploy once the platform's domain is ready. The initial localhost origin allows services to start; Web accepts the configured host only, so platform-domain access becomes available after that redeployment.

### Dokploy

Create a Docker Compose application and paste `compose.yaml`. Set `OAC_PUBLIC_URL` to the public HTTPS origin, enable isolated deployment, and add a domain for service `gateway`, port `8080`. Enable **HTTPS** and select a certificate provider such as **Let's Encrypt** for that domain before deploying. Deploy without `local.yaml`; internal services publish no host ports. The [template metadata](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/dokploy.toml) supplies the generated domain and environment when packaging this Compose file for Dokploy's template catalog; HTTPS and its certificate provider still need to be enabled after import.

### Coolify

Create a **Docker Compose Empty** service and paste `compose.yaml`. Set `OAC_PUBLIC_URL` to the public HTTPS origin and assign that domain to `gateway` on port `8080`. Add Coolify's `exclude_from_hc: true` to the `init`, `migrate` and `credentials` service definitions so completed initialization and optional tooling do not affect its overall health. Save and deploy without `local.yaml`; Coolify supplies HTTPS.

On either platform, open its server terminal and run `docker compose ls` to find the deployed project name and Compose file. Using those exact values and the deployment's `OAC_PUBLIC_URL`, run `docker compose -p <project-name> -f <compose-file> run --rm credentials`, then sign in at the configured origin. The [Dokploy domain guide](https://docs.dokploy.com/docs/core/docker-compose/domains) and [Coolify Compose guide](https://coolify.io/docs/services/configuration/docker-compose) describe their domain and service controls. These are importable deployment files; no hosted marketplace listing is published by this repository.

After signing in, choose the sandbox backend and add nodes using [Nodes](./nodes.md). The Compose stack deploys the control plane; execution machines remain separate.

Stop with `docker compose stop` using the same files and environment. Back up all [installation volumes](../configuration.md#compose-installations) together while the services are stopped. Follow the [installation version policy](./operations.md#installation-version-policy): a different release needs a new Compose project and fresh volumes.

## Process settings

These flags seed the installation's `config.json` once. Their defaults, valid values and restart behavior are defined in the [configuration reference](../configuration.md#settings). After installation, edit that file and run `oac apply`; rerunning the installer only [repairs](./operations.md#installation-version-policy) the installation.

[//]: # (BEGIN install-flags: generated by scripts/config-reference.py)
| Flag | config.json field |
| --- | --- |
| `--public-url` | `public_url` |
| `--host` | `host` |
| `--core-port` | `ports.core` |
| `--web-port` | `ports.web` |
| `--ingress` | `ingress` |
[//]: # (END install-flags)

`--config FILE` seeds `config.json` from a JSON file instead of these setting flags; they cannot be combined. A `--config` document follows the schema defaults, so set `ingress: "managed"` and `host: "0.0.0.0"` in it for managed HTTPS.

## Installation actions

These options choose an installation location or perform initial setup; they are not saved in `config.json`.

| Option | Purpose |
| --- | --- |
| `--install-dir DIR` | Absolute installation directory; defaults to `~/.oac/core`. A new installation requires an empty or missing directory, or one holding an [installation that never started](./install.md#install) |

Several installations can share a machine when they use distinct installation directories and ports. Only one installation with managed HTTPS can hold [ports 80 and 443](#ports) on an IP address; use distinct IP addresses or an external shared proxy for more. Each installation has its own database, Core key and nodes.

## Sandbox backend

After the services are healthy, the installer saves microsandbox at the Standard size in Web's [`standard-sizes.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/src/features/sandbox/standard-sizes.json). The choice is stored in Core's database, not in `config.json`, and a repair does not change it. If Core refuses the choice, the installer prints Core's message and exits; the services keep running and you choose the backend in Web.

To use Docker or E2B, or another size, open **System** → **Manage sandbox configuration** and [reset the deployment](./nodes.md#change-the-sandbox-configuration). Docker shares each node's kernel with its sandboxes, and its node service account is [root-equivalent](./nodes.md#what-the-installer-sets-up). E2B needs a public HTTPS URL that is not loopback, because E2B's sandboxes call Core from E2B's cloud. Prepare an E2B template with the [E2B guide](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md).

## Listeners and access

The default installation selects `--ingress managed` and `--host 0.0.0.0`. Its gateway publishes Web on `--web-port` (8080 by default), and [ports 80 and 443](#ports) once HTTPS is on. Core's `--core-port` stays on loopback and PostgreSQL stays private. `--host` accepts IPv4 or IPv6, without a port, scheme or zone. Use a concrete server IP in the browser, not a wildcard. Managed ingress needs a local Docker Unix socket.

`--ingress external` uses your own reverse proxy instead. Core and Web then listen on `--host`, loopback by default. External non-loopback listeners require an HTTPS `public_url` and a [reverse proxy](#https-and-the-reverse-proxy), and Web's domain setup is unavailable: set `public_url` in `config.json` and run `oac apply`.

`--public-url` seeds a DNS-based HTTPS origin for unattended setup; with managed ingress, the certificate and connectivity checks must pass. The ingress mode is fixed for an installation.

### Ports

Before it verifies the bundle or loads images, the installer checks `--host` and every port the installation will listen on: Web's, Core's, and 80 and 443 with managed ingress and `--public-url`.

- `--host` must be an address of this machine, or a wildcard such as `0.0.0.0`.
- A port set with `--web-port`, `--core-port` or in the `--config` file must be free, and so must a port that a loopback `--public-url` names, such as 8080 in `http://localhost:8080`. Otherwise the installer stops, names the port and prints the `ss` command that finds the program holding it.
- A Web or Core port you leave out moves to the first free port above its default, at most 20 above, and never to another port of the same installation. The installer writes the chosen port to `config.json` and names it in the summary, for example `Port 8080 was in use; Web uses 8081.`
- Managed ingress uses ports 80 and 443 only for HTTPS and never moves them. The gateway publishes them once `public_url` is set, from `--public-url` or [domain setup in Web](./install.md#configure-the-domain-and-https), and no other program on the host may use them. If either is in use at installation, free it, install without `--public-url` and set up the domain later, or install with `--ingress external` and use your own [reverse proxy](#https-and-the-reverse-proxy).

After installation, `oac apply` [checks the ports](../configuration.md#how-oac-apply-works) of a changed `host` or port, and 80 and 443 when `public_url` turns HTTPS on. Domain setup in Web and `oac domain` check, before they start, that the hostname resolves and that no other program holds port 80 or 443, and name the port that is in use.

## HTTPS and the reverse proxy

With external ingress, Core and Web share one public origin. Your reverse proxy terminates TLS and routes by path:

| Path | Goes to | Callers |
| --- | --- | --- |
| `/v1`, `/v1/*` | Core, `127.0.0.1:8091` by default | Applications, with a Project API key |
| `/api/v1/*` | Core, `127.0.0.1:8091` | Nodes, sandboxes and self-hosted machines. Uses WebSockets |
| Everything else | Web, `127.0.0.1:8080` by default | Browsers, and node installers at `/node-install/*` |

The proxy must:

- **Preserve Host.** Web accepts only the host of its public URL.
- **Pass WebSocket upgrades** on `/api/v1`.
- **Not buffer or time out streams.** `/v1` streams Session events.
- **Accept large uploads.** Source files may reach 512 MiB; Core enforces the limits.

Run the proxy on the Core host while Core and Web listen on loopback, the default. `oac status` prints these routes with your addresses and ports.

**Caddy** obtains the certificate itself and passes Host and WebSockets by default:

```caddyfile
core.example {
	@core path /v1 /v1/* /api/v1/*
	handle @core {
		reverse_proxy 127.0.0.1:8091
	}
	handle {
		reverse_proxy 127.0.0.1:8080
	}
}
```

**nginx**, for example in `/etc/nginx/conf.d/oac.conf` inside the `http` block:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name core.example;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name core.example;
    ssl_certificate     /etc/letsencrypt/live/core.example/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/core.example/privkey.pem;

    client_max_body_size 0;          # Core enforces its own upload limits
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_buffering off;             # server-sent events on /v1
    proxy_request_buffering off;
    proxy_read_timeout 1h;           # long-lived WebSockets and streams
    proxy_send_timeout 1h;

    location = /v1    { proxy_pass http://127.0.0.1:8091; }
    location /v1/     { proxy_pass http://127.0.0.1:8091; }
    location /api/v1/ { proxy_pass http://127.0.0.1:8091; }
    location /        { proxy_pass http://127.0.0.1:8080; }
}
```

Then set `public_url` in `~/.oac/core/config.json` and run `~/.oac/core/oac apply`. Check the routing:

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
	@core path /v1 /v1/* /api/v1/*
	handle @core {
		reverse_proxy 127.0.0.1:8091
	}
	handle {
		reverse_proxy 127.0.0.1:8080
	}
}
```

1. Start the proxy: `caddy run --config Caddyfile`.
2. Start the tunnel: `cloudflared tunnel --url http://127.0.0.1:8443`. It prints an address such as `https://random-words.trycloudflare.com`.
3. Set that address as `public_url` in `~/.oac/core/config.json` and run `~/.oac/core/oac apply`.

The address changes whenever `cloudflared` restarts; nodes bound to the old address must then be added again. Throughput is low, so a node's first Runtime download (about 500 MB) can be slow; see [slow links](./nodes.md#rerun-expiry-and-slow-links).

## Offline hosts

Transfer the release's `*-linux-amd64-offline.tar.gz` and its `.sha256` file, verify and extract them, then run the bundled `./install.sh`. The offline bundle also carries the node and Runtime files, so Web serves them to nodes without release access.
