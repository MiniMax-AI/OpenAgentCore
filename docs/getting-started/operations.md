---
title: "Operate your installation"
---

The installation operator owns the Core host, its storage and its availability. Node hosts run their own services; see [Nodes](./nodes.md). Settings are described in the [configuration reference](../configuration.md).

## The oac command

Each installation has its own native management command in its directory: `oac` on Unix, `oac.exe` on Windows. It needs Docker access and no root privileges:

```sh
docker compose -f ~/.oac/core/compose.yaml ps
```

| Command | What it does |
| --- | --- |
| `docker compose ps` | Shows the services. Run it in the installation directory |
| `docker compose start` | Starts the services |
| `docker compose stop` | Stops the services. Data, nodes and sandboxes are kept |
| `oac apply` | Runs `oac-core check-config`, then `docker compose up -d --wait`. A failed check changes no service |
| `oac core-key [--show]` | Identifies the key location in the data volume, or prints the key with `--show` |
| `oac rotate-core-key` | Replaces the Core key and restarts Core and Web |
| `docker compose down` | Removes the containers. Data is kept; to delete it, [uninstall](#uninstall) |

The examples use the default installation directory. On Windows, invoke the management command with `& "$HOME/.oac/core/oac.exe"` followed by the same arguments. For a custom installation directory, replace the path in each command.

## Service health

Use these observations for different questions:

| Observation | What it establishes |
| --- | --- |
| `docker compose ps` | The database accepts its readiness check |
| Core `/healthz` | Core's process is alive |
| An authenticated API read | The caller's key works for that resource |
| Environment connection | The Runtime transport is connected |
| A finished Turn and its results | The task's recorded outcome |

Service health does not show that a harness or a model works. Use Session, Turn, Items and Usage reads for execution, and Web's **Nodes** page for node connection, readiness and placement. For local diagnosis, use the installation's own Compose file:

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" ps --all
docker compose -f "$HOME/.oac/core/compose.yaml" logs --tail 200 core
docker compose -f "$HOME/.oac/core/compose.yaml" logs --timestamps init
```

The `init` service exits after initialization. Its step logs are described in [Deployment](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation).

Don't paste `docker compose config`, `docker inspect` or raw logs into public issue reports.

## Stop and restart

Let active work settle before a planned restart:

```sh
docker compose -f ~/.oac/core/compose.yaml stop
docker compose -f ~/.oac/core/compose.yaml start
```

Stopping Core stops no node and no sandbox. Node services, their microVMs and Docker containers keep running; stopping is not a way to reclaim compute. A Core restart does not continue an interrupted native tool call transparently. After reconnecting, query the same Session; don't create a new Session to replay uncertain work. Session event streams are live only; recover through Session, Turn and Items reads.

A Web restart, including one caused by `oac apply`, signs everyone out of the console. Web sign-ins otherwise last 12 hours.

## Core key

Each installation has one administrator credential, the Core key. The installer generates a key with the `oac_admin_` prefix followed by 64 random lowercase hexadecimal characters in `secrets/web/core.key`. Read it with `oac core-key --show`; the file is owned by the container user. The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes Core API (`/core/v1`) requests sent as `Authorization: Bearer <Core key>`;
- never authorizes the Agents API (`/v1`). Applications use Project API keys, which in turn can't call `/core/v1`.

Keep it private. Web reads `secrets/web/core.key`. Core reads only its SHA-256 from `secrets/core/core-key-digests.json`. A Core key has at least 32 characters and no whitespace. Web limits failed sign-ins.

### Script the Core API

Core publishes no host port. On the Core host, this helper runs `curl` in Core's network namespace and passes the key on stdin, keeping it off the command line:

```sh
core() (  # core METHOD PATH [JSON body]
  cd ~/.oac/core
  ./oac core-key --show | sed 's/^/Authorization: Bearer /' |
    docker run -i --rm --network "container:$(docker compose ps -q core)" curlimages/curl \
      -fsS -X "$1" "http://127.0.0.1:8091/core/v1$2" -H @- -H 'Content-Type: application/json' ${3:+-d "$3"}
)
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

It runs in the initialization container, updates `secrets/web/core.key` and `secrets/core/core-key-digests.json` in the data volume, and restarts Core and Web. The old key stops working as soon as Core restarts, and every console session ends: sign in again and update your scripts.

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

- the Docker volume `<project>_data`, including its `database/`, `secrets/` and `state/` directories. It holds Projects, key digests, nodes, default models, encrypted credentials and all execution history, including large objects. A logical dump:

  ```sh
  docker compose -f "$HOME/.oac/core/compose.yaml" exec -T database \
    pg_dump -U agents_api agents_api > oac-backup.sql
  ```

- the installation directory containing `.env`, `compose.yaml` and the command. The data volume's `secrets/core/credential.key` must stay with the database, or stored credentials cannot be decrypted.
- each node's state directory on its host, `/var/lib/oac-node/.oac/nodes/<installation-id>/`, with its provider storage: Docker volumes or microsandbox's store. See [when a node host fails](./nodes.md#when-a-node-host-fails) for restoring them.

Stop with `docker compose stop`, export the complete data volume and archive the installation directory, then `docker compose start`. Docker Desktop supports volume export from its **Volumes** view. A SQL dump alone does not include the encryption key or Provider state.

## Uninstall

```sh
cd ~/.oac/core
docker compose down --volumes --remove-orphans --rmi all
cd && rm -rf ~/.oac/core
```

`down --volumes` deletes the installation data volume. Remove the installation directory afterward; on Windows use `Remove-Item -Recurse "$HOME/.oac/core"`.

All data goes with it: Projects and API keys, Session history, stored credentials and the Core key. To keep the data, stop the installation with `docker compose stop` instead, or [back it up](#back-up) first.

Uninstall stops no sandbox: node sandboxes keep running on their nodes, and E2B sandboxes keep running, and billing, at E2B. While Core is still up, archive their Sessions or [reset the deployment](./nodes.md#change-the-sandbox-configuration) and let it complete; the command shows how many sandboxes Core has in use.

Nodes on other hosts keep running. To uninstall them the usual way, remove them in Web first, as in [Remove a node](./nodes.md#remove-a-node). After the installation directory is gone, their Core is gone: on each node host, run the node uninstall command with `--force`, using `node-install.pyz` from the release that installed them. The installation ID is `secrets/core/installation.id` in the data volume.

## Installation version policy

An installation runs one release for its whole life. In-place version upgrades and downgrades are not supported. Nothing migrates data between releases.

To move to a new release, install it into a new, empty directory, with its own database, Core key and nodes, and add nodes from its Web. Keep the old installation, its data and its nodes until their work is finished. Nodes run the program of the console that added them and are never upgraded in place; Core accepts only nodes that speak its own node protocol.

An interrupted installation can [resume with its saved configuration](./install.md#install). An unrelated nonempty directory is refused.

Installation and mutating `oac` commands share the [installation lock](../configuration.md#installation-directory). If another command is running, wait for it to finish before retrying.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `Port N is already in use.` | Another program holds that port. Stop it, or choose another `--web-port`. The installer does not move to a different port |
| `Directory is not a complete Core installation` | Preserve the directory and choose another `--install-dir` |
| `configuration check failed; no service was changed` | `.env` has a value Core rejects. The message names the variable and not the value. Fix `.env` and run `oac apply` again |
| `Docker Compose 2.26 or newer is required` | Update the Docker Compose plugin |
| `Installation and data retained at …` | Read the service logs above it, fix the cause, then [resume installation](./install.md#install); keep the data |
| Web answers 403 `Forbidden` | The browser host is not `OAC_PUBLIC_URL`. Open that origin; a reverse proxy must pass the original Host |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `docker compose ps`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the harness, or pass one |
| Add node shows no command | See [Before you add a node](./nodes.md#before-you-add-a-node) |
| A node is not ready | See [node troubleshooting](./nodes.md#troubleshooting) |

## Exposure and network policy

| Listener | Host installation | Behind a reverse proxy |
| --- | --- | --- |
| Web and the API | Web publishes `OAC_WEB_PORT` (8080) on `OAC_HOST` | Web publishes `OAC_WEB_PORT` on `OAC_HOST`. Your proxy should use `127.0.0.1` |
| Core | No published port. Web forwards `/v1`, `/api/v1` and `/docs` | No published port. Web forwards `/v1`, `/api/v1` and `/docs` |
| PostgreSQL | No published port | No published port |

Web signs administrators in with the Core key, checks the origin of every request, and forwards signed-in `/core/v1` requests to Core with the Core key, which stays on the server. It forwards `/v1` and `/api/v1` to Core unchanged, with the caller's own credential, serves only the non-secret node payload at `/node-install/`, and has no Docker or KVM access. Machine routes under `/api/v1` use their own enrollment and connection credentials. No service receives a Docker socket.

Sandboxes are the isolation boundary ([Runtime and outer isolation](../concepts.md#runtime-and-outer-isolation)). Docker sandboxes share the node's kernel, and a Docker node is [root-equivalent](./nodes.md#what-the-installer-sets-up) on its host; microsandbox gives each sandbox a microVM with an explicit [network policy](./nodes.md#what-the-installer-sets-up). Core itself has no Docker socket or KVM access.
