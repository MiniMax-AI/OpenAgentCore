---
title: "Operate your installation"
---

The installation operator owns the Core host, its storage and its availability. Node hosts run their own services; see [Nodes](./nodes.md). Settings are described in the [configuration reference](../configuration.md).

## The oac command

Each installation has its own management command in its directory. It needs neither the bundle nor root:

```sh
~/.oac/core/oac status
```

| Command | What it does |
| --- | --- |
| `oac status` | Shows each service and its health, Core and Web health, the public URL, API base URL, console address and source commit, the routes a reverse proxy needs, `config.json` changes not applied yet and generated files edited by hand. Exits non-zero when a service is unavailable. It never calls a model |
| `oac start` | Starts every service with the files last written by `apply`; warns about unapplied changes |
| `oac stop` | Stops PostgreSQL, Core and Web. Data, nodes and sandboxes are kept, and running sandbox work may continue |
| `oac apply` | Applies `config.json` and restarts what changed; see [how apply works](../configuration.md#how-oac-apply-works) |
| `oac apply --dry-run` | Shows the changed settings, files and restarts, and changes nothing |
| `oac apply --discard-edits` | Overwrites generated files edited by hand, keeping each as `generated/<file>.edited-<time>` |
| `oac apply --confirm-public-url-change URL` | Confirms a public URL change without a prompt; must equal the new URL |
| `oac domain HOSTNAME [--confirm-public-url-change URL]` | Managed ingress: sets the public URL to `https://HOSTNAME`, as **Configure domain and HTTPS** in Web does; see [Configure the domain and HTTPS](./install.md#configure-the-domain-and-https) |
| `oac rotate-core-key [--yes]` | Replaces the Core key; see [Rotate the Core key](#rotate-the-core-key) |
| `oac uninstall [--yes]` | Removes the installation and all its data from this host; see [Uninstall](#uninstall) |

For a second installation, use its own command, such as `~/.oac/second/oac status`.

## Service health

Use these observations for different questions:

| Observation | What it establishes |
| --- | --- |
| `oac status`, PostgreSQL health | The database accepts its readiness check |
| Core `/healthz` | Core's process is alive |
| An authenticated API read | The caller's key works for that resource |
| Environment connection | The Runtime transport is connected |
| A finished Turn and its results | The task's recorded outcome |

Service health does not show that a harness or a model works. Use Session, Turn, Items and Usage reads for execution, and Web's **Nodes** page for node connection, readiness and placement. For local diagnosis, use the installation's own Compose file:

```sh
docker compose -f "$HOME/.oac/core/generated/compose.json" ps --all
docker compose -f "$HOME/.oac/core/generated/compose.json" logs --tail 200 core
```

Don't paste `docker compose config`, `docker inspect` or raw logs into public issue reports.

## Stop and restart

Let active work settle before a planned restart:

```sh
~/.oac/core/oac stop
~/.oac/core/oac start
```

Stopping Core stops no node and no sandbox. Node services, their microVMs and Docker containers keep running; stopping is not a way to reclaim compute. A Core restart does not continue an interrupted native tool call transparently. After reconnecting, query the same Session; don't create a new Session to replay uncertain work. Session event streams are live only; recover through Session, Turn and Items reads.

A Web restart, including one caused by `oac apply`, signs everyone out of the console. Web sign-ins otherwise last 12 hours.

## Core key

Each installation has one administrator credential, the Core key. The installer generates a 64-character random key in `<install dir>/secrets/core.key`, by default `~/.oac/core/secrets/core.key`. The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes Core API (`/core/v1`) requests sent as `Authorization: Bearer <Core key>`;
- never authorizes the Agents API (`/v1`). Applications use Project API keys, which in turn can't call `/core/v1`.

Keep it private: `secrets/` is mode `0700` and its files `0600`. Web and, with managed ingress, the `installation` service read `core.key`; Core reads only its SHA-256 from `generated/core-key-digests.json`, which `oac apply` derives from the key. A Core key has at least 32 characters and no whitespace. Web limits failed sign-ins.

### Script the Core API

Run scripts on the Core host against Core's loopback port. This helper reads the key from its file, keeping it off the command line:

```sh
core() {  # core METHOD PATH [JSON body]
  curl -fsS -X "$1" "http://127.0.0.1:8091/core/v1$2" \
    -H @<(printf 'Authorization: Bearer %s\n' "$(cat ~/.oac/core/secrets/core.key)") \
    -H 'Content-Type: application/json' ${3:+-d "$3"}
}
```

| Task | Command |
| --- | --- |
| List Projects | `core GET /projects` |
| Create a Project | `core POST /projects '{"name": "billing-bot"}'` |
| Issue an API key (shown once, as `key`) | `core POST /projects/$PROJECT_ID/keys '{"name": "prod"}'` |
| Revoke a key | `core DELETE /projects/$PROJECT_ID/keys/$KEY_ID` |
| Archive a Project (revokes all keys) | `core POST /projects/$PROJECT_ID/archive` |
| See harnesses and their default models | `core GET /harnesses` |
| Set Codex's default model | `core PUT /harnesses/codex/model-configuration '{"model": "your-model-id", "model_provider": {"protocol": "responses", "base_url": "https://provider.example/v1", "api_key": "sk-..."}}'` |
| Installation facts, including the API base URL | `core GET /installation` |

The [Core administration API](../../contracts/agents-api/admin-api.md) lists every route; errors use the [Core error envelope](../../contracts/agents-api/core-errors.md).

### Rotate the Core key

```sh
~/.oac/core/oac rotate-core-key
```

It refuses while `config.json` has changes that are not applied or a generated file was edited by hand: run `oac apply` first. It asks for confirmation (`--yes` skips it), stops Web, writes a new key to `secrets/core.key` and regenerates the digest file. On a running installation it then restarts Core, starts Web and checks that Core accepts the new key and refuses the old one; a stopped installation only gets the new files and uses the new key at the next `oac start`. The old key stops working as soon as Core restarts, and every console session ends: sign in again and update your scripts. If the command stops early, `secrets/core.key` holds the key to use; run `oac apply` to finish.

## Projects and API keys

Create Projects and issue keys in Web, on **Projects and keys**, or through the [Core API](#script-the-core-api). How Projects and keys behave is in [Projects own assets](../concepts.md#projects-own-assets).

To rotate an application key:

1. Issue a new key in the same Project.
2. Update the application to use it.
3. **Revoke** the old key.

**Archive** disables every key of a Project and keeps its assets.

Core records which key made each public resource write; the retention of that history is [`core.write_audit_retention`](../configuration.md#settings).

## Back up

Back up these together; a restore needs all of them:

- the PostgreSQL volume `<project>_database`. It holds Projects, key digests, nodes, default models, encrypted credentials and all execution history, including large objects. A logical dump:

  ```sh
  docker compose -f "$HOME/.oac/core/generated/compose.json" exec -T database \
    pg_dump -U agents_api agents_api > oac-backup.sql
  ```

- the installation directory: `config.json`, `state.json` (installation ID) and `secrets/`. `credential.key` must stay with the database, or stored credentials can't be decrypted; never regenerate it to get past an error.
- `state/e2b/`, when E2B is used: receipts Core needs to clean up E2B sandboxes.
- each node's state directory on its host, `/var/lib/oac-node/.oac/nodes/<installation-id>/`, with its provider storage: Docker volumes or microsandbox's store. See [when a node host fails](./nodes.md#when-a-node-host-fails) for restoring them.
- the bundle you installed from, to repair the same release.

Never prune Docker volumes or delete native harness history to make a retry pass. A deleted Session does not prove that all provider resources were reclaimed.

## Uninstall

```sh
~/.oac/core/oac uninstall
```

It removes the installation from this host: its Compose project with the containers, networks and database volume, the images the installer loaded, and the installation directory, including `secrets/` and the `oac` command itself. It keeps an image that has a tag or that another container uses, such as one of another installation of the same release, and says so.

All data goes with it: Projects and API keys, Session history, stored credentials and the Core key. The database volume is useless without `secrets/`, so it is never kept on its own. To keep the data, stop the installation with `oac stop` instead, or [back it up](#back-up) first.

The command lists what it removes and, when Core answers, the registered nodes. Confirm by typing the installation directory, or pass `--yes`, which a run without a terminal requires. It holds the installation lock and needs only `state.json`, so it also removes an installation that did not finish installing or lost `config.json`. It removes the directory last; if it stops part way, run it again.

Uninstall stops no sandbox: node sandboxes keep running on their nodes, and E2B sandboxes keep running, and billing, at E2B. While Core is still up, archive their Sessions or [reset the deployment](./nodes.md#change-the-sandbox-configuration) and let it complete; the command shows how many sandboxes Core has in use.

Nodes on other hosts keep running. To uninstall them the usual way, remove them in Web first, as in [Remove a node](./nodes.md#remove-a-node). After `oac uninstall` their Core is gone: on each node host, run the node uninstall command with `--force`, using `node-install.pyz` from the [bundle you installed from](#installation-version-policy). `oac uninstall` prints that command with the installation ID.

## Installation version policy

An installation runs one release for its whole life. In-place version upgrades and downgrades are not supported. Nothing migrates data between releases.

To move to a new release, install it into a new, empty directory, with its own database, Core key and nodes, and add nodes from its Web. Keep the old installation, its data and its nodes until their work is finished. Nodes run the program of the console that added them and are never upgraded in place; Core accepts only nodes that speak its own node protocol.

Repair the current release by rerunning `./install.sh --install-dir DIR` from the exact same bundle; the downloader keeps it under `~/.oac/releases/`. Repair reloads missing images, restores the `oac` command, applies `config.json` and starts the services. It preserves identity, settings, secrets and history, accepts only `--install-dir`, and refuses a bundle from another release. An installation the installer never reported as running is not repaired but [removed and installed again](./install.md#install).

The installer and mutating `oac` commands hold the same installation lock, `.oac.lock`, including during repair and interrupted apply recovery. If another command holds it, retry after that command finishes; never remove or replace `.oac.lock` to get past a busy installation. Reinstallation never deletes another installation's files, database, Runtime resources or Session history.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `Core installation requires Linux amd64 with Docker access` | Use Linux amd64 and an account with Docker access; root and ordinary users are supported |
| `Installation failed: inspect prerequisites and private deployment files` | A prerequisite failed without its own message, most often Docker: check that `docker info` and `docker compose version` work for this user |
| `Docker Compose 2.26.0 or newer is required …` | Update the Docker Compose plugin |
| `Port N (…) is already in use on ADDRESS …` | Another program holds a port the installation needs. Find it with the printed `ss` command and stop it, or choose another port: `--web-port` or `--core-port` at [installation](./install-options.md#ports), or the port in `config.json` before `oac apply` |
| `ADDRESS (…) is not an address of this machine …` | Set `--host`, or `host` in `config.json`, to one of the machine's IP addresses or a wildcard such as `0.0.0.0` |
| `Automatic HTTPS needs ports 80 and 443 …` | Free the port the message names, install without `--public-url` and set up the domain later, or install with `--ingress external` and use your own [reverse proxy](./install-options.md#https-and-the-reverse-proxy) |
| `Installation directory is not empty …` | Use an empty `--install-dir` |
| `This installation is configured by …/config.json …` | Flags only seed a new installation: edit `config.json` and run `oac apply`. To start over with other flags, [uninstall](#uninstall) it first |
| `This installation version is not supported …` | The target installation's state format or source revision does not match this release. Keep it, and install into another empty `--install-dir` ([version policy](#installation-version-policy)) |
| `generated/<file> was edited by hand` | Put the change in `config.json`, then `oac apply --discard-edits` |
| `config.json has changes that are not applied` | Run `oac apply` |
| `Core rejects secrets/core.key …` | Run `oac apply`, which restarts Core with the key's digest |
| `config.json not applied: …` | `oac apply` printed Core's startup error above; fix `config.json` and apply again |
| `The services did not start: …` | A new installation's first start failed, and the installer [removed what it created](./install.md#install). Compose's or Core's error is printed above it; fix the cause and run the same command again |
| `Removal did not finish. Left: …` | The installer, cleaning up a failed new installation, or `oac uninstall` could not remove everything. Run the printed commands to remove what is left, or fix the cause and run the same command again |
| `This installation did not finish installing …` | The installer stopped before reporting that the services were running. Rerun the installer command, which [removes what is left](./install.md#install) and installs again, or [uninstall](#uninstall) it |
| `… already in use on this server. Automatic HTTPS cannot run beside another program …` during domain setup | Another program holds port 80 or 443. Stop it, using the printed `ss` command to find it, and retry; automatic HTTPS cannot share [these ports](./install-options.md#ports) |
| `HTTPS verification failed …` during domain setup | DNS points elsewhere, a firewall or NAT blocks inbound ports 80 and 443, or the certificate request failed; see [Configure the domain and HTTPS](./install.md#configure-the-domain-and-https) |
| Web answers 403 `Forbidden` | Open exactly the console address `oac status` prints; a reverse proxy must pass the original Host |
| `/v1` or `/api/v1` answers 404 | Those paths reach Web; route them to Core ([reverse proxy](./install-options.md#https-and-the-reverse-proxy)) |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `oac status`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the harness, or pass one; self-hosted Sessions always pass their own |
| Add node shows no command | See [Before you add a node](./nodes.md#before-you-add-a-node) |
| A node is not ready | See [node troubleshooting](./nodes.md#troubleshooting) |

## Exposure and network policy

| Listener | Managed ingress (default) | External ingress |
| --- | --- | --- |
| Web | Reached only through the `gateway` service, which publishes `ports.web` (8080) on `host` (all IPv4 interfaces by default), plus [80 and 443](./install-options.md#ports) once HTTPS is on | `host:ports.web` (loopback by default), behind your reverse proxy |
| Core | `127.0.0.1:ports.core` (8091); the gateway routes `/v1` and `/api/v1` to it | `host:ports.core`, behind your reverse proxy |
| PostgreSQL | No published port | No published port |

Web signs administrators in with the Core key, checks the origin of every request, and forwards signed-in `/core/v1` requests to Core with the Core key, which stays on the server. It answers 404 on `/v1` and `/api/v1` whatever credential a request carries, serves only the non-secret node payload at `/node-install/`, and has no Docker or KVM access. Machine routes under `/api/v1` use their own enrollment and connection credentials. With managed ingress, the `installation` service applies domain changes through the Docker socket; Web reaches it only over a private Unix socket, and it checks the Core key on every request.

Sandboxes are the isolation boundary ([Runtime and outer isolation](../concepts.md#runtime-and-outer-isolation)). Docker sandboxes share the node's kernel, and a Docker node is [root-equivalent](./nodes.md#what-the-installer-sets-up) on its host; microsandbox gives each sandbox a microVM with an explicit [network policy](./nodes.md#what-the-installer-sets-up). Core itself has no Docker socket or KVM access.
