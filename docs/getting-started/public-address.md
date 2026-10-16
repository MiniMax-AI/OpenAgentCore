---
title: "Configure the public address"
---

Set the one origin that browsers, nodes, sandboxes and applications use to reach Core, and put a reverse proxy with HTTPS in front of it. This guide is for operators who expose Core beyond the machine it runs on.

## Choose the public URL

`OAC_PUBLIC_URL` is the origin applications, nodes, sandboxes and self-hosted executors use. Web accepts only its host. Core derives the daemon WebSocket URL, the self-hosted `remote_url` and each sandbox's connection address from it. It is an http or https origin without a path.

| Situation | Public URL |
| --- | --- |
| Trial on this machine | `http://localhost:8080`, the default when `--host` is not `0.0.0.0` or the host has no private address |
| Local network | `http://<private address>:8080`, which the installer picks when `--host` is `0.0.0.0` and the default route has a private-network address |
| Beyond the local network | The HTTPS origin your reverse proxy serves, for example `https://core.example` |
| E2B backend | An HTTPS origin reachable from the internet and not loopback, because E2B's sandboxes call Core from E2B's cloud |

Pass the origin as `--public-url` when you install, or change it later as described in [Change the public URL](#changing-the-public-url). To install with a flag, see [Installation options](./install-options.md#pass-flags).

## Listeners and access {#listeners-and-access}

The default installation publishes Web on `--web-port` (8080) at `--host` `0.0.0.0`. Core and PostgreSQL publish no port. `--host` is an IPv4 or IPv6 address, without a port, scheme or zone. Use a concrete server IP in the browser, not a wildcard.

| Service | Reachable from outside |
| --- | --- |
| Web | `OAC_WEB_PORT` on `OAC_HOST`. Behind a reverse proxy, install with `--host 127.0.0.1` |
| Core | Only through Web. `/v1`, `/api/v1` and `/docs` pass through unchanged; signed-in console requests to `/core/v1` carry the Core key. Web serves everything else itself; see [Request boundary](../web/console-server.md#request-boundary) |
| PostgreSQL | No |

Sandboxes are the isolation boundary; see [Runtime and outer isolation](../concepts.md#runtime-and-outer-isolation).

### Ports

The installer checks `--web-port` before it downloads images.

- `--host` is the address that port binds. `0.0.0.0` publishes Web on every IPv4 interface. `127.0.0.1` keeps Web on this machine.
- A busy port stops installation. It does not move to another port.

`oac apply` recreates Web when `OAC_HOST` or `OAC_WEB_PORT` changes.

## Put Core behind HTTPS {#https-and-the-reverse-proxy}

Install with `--host 127.0.0.1` and point your reverse proxy at Web, `127.0.0.1:8080` by default. The proxy must:

- **Preserve Host.** Web accepts only the host of `OAC_PUBLIC_URL`.
- **Pass WebSocket upgrades** on `/api/v1`. Nodes, sandbox Runtimes (E2B included) and self-hosted executors connect there.
- **Not buffer or time out streams.** `/v1` streams Session events.
- **Accept large uploads.** Source files may reach 512 MiB plus multipart overhead; Core enforces the limits.

::: code-group

```caddyfile [Caddy]
core.example {
	reverse_proxy 127.0.0.1:8080
}
```

```nginx [nginx]
map $http_upgrade $connection_upgrade {
	default upgrade;
	''      close;
}

server {
	listen 443 ssl;
	server_name core.example;
	ssl_certificate     /etc/ssl/core.example/fullchain.pem;
	ssl_certificate_key /etc/ssl/core.example/privkey.pem;

	client_max_body_size 513m;

	location / {
		proxy_pass http://127.0.0.1:8080;
		proxy_http_version 1.1;
		proxy_set_header Host $http_host;
		proxy_set_header Upgrade $http_upgrade;
		proxy_set_header Connection $connection_upgrade;
		proxy_buffering off;
		proxy_read_timeout 1h;
		proxy_send_timeout 1h;
	}
}
```

:::

Caddy obtains the certificate itself and passes Host and WebSockets by default. The nginx server block goes in the `http` context and needs a certificate you provide.

Then set `OAC_PUBLIC_URL=https://core.example` in `~/.oac/core/.env` and run `~/.oac/core/oac apply`.

### Verify the routing

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'OpenAI-Beta: agents=v1' https://core.example/v1/agents
```

`401` means `/v1` reached Core, which asks for a key. `404` means it reached Web: fix the proxy, or application calls and every node connection will fail. A node that logs `HTTP 400 on the WebSocket upgrade` sits behind a proxy that does not pass upgrades on `/api/v1`; see [Troubleshooting](./troubleshooting.md#nodes).

TLS verification stays on everywhere. With a private certificate authority, node hosts, self-hosted machines and the Runtime image must trust it.

### Try it locally with a quick tunnel {#try-it-locally-with-a-quick-tunnel}

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

## Change the public URL {#changing-the-public-url}

Point the reverse proxy at the new address first, then edit `OAC_PUBLIC_URL` in `~/.oac/core/.env` and run `oac apply`. Afterwards:

- Nodes on the old address get no new sandboxes: remove them in Web and add them again.
- Existing sandboxes and executors keep working only while the old address still reaches this Core.
- Self-hosted executors must restart with the new `remote_url`, and their installer refuses an installation made for the old address: create new self-hosted Sessions and connect their hosts again.

The setting is listed in the [Configuration reference](../configuration.md#settings).
