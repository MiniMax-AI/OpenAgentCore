# Operate your installation

The installation operator owns the Core host, its storage and its availability. Node
hosts run their own services; see [Nodes](nodes.md). Settings are described in the
[configuration reference](../configuration.md).

## The parsar command

Each installation has its own management command in its directory. It needs neither
the bundle nor root:

```sh
~/.parsar/core/parsar status
```

| Command | What it does |
| --- | --- |
| `parsar status` | Shows each service and its health, Core and Web health, the public URL, API base URL, console address and source commit, the reverse-proxy routes to configure, `config.json` changes not applied yet and generated files edited by hand. A Web-only installation also checks that its Core accepts its Core key. Exits non-zero when a service is unavailable. It never calls a model |
| `parsar start` | Starts every service with the files last written by `apply`; warns about unapplied changes |
| `parsar stop` | Stops PostgreSQL, Core and Web. Data, nodes and sandboxes are kept, and running sandbox work may continue |
| `parsar apply` | Applies `config.json` and restarts what changed; see [how apply works](../configuration.md#how-parsar-apply-works) |
| `parsar apply --dry-run` | Shows the changed settings, files and restarts, and changes nothing |
| `parsar apply --discard-edits` | Overwrites generated files edited by hand, keeping each as `generated/<file>.edited-<time>` |
| `parsar apply --confirm-public-url-change URL` | Confirms a public URL change without a prompt; must equal the new URL |
| `parsar apply --yes` | Web-only: pairs Web with a different Core without asking |
| `parsar rotate-core-key [--yes]` | Replaces the Core key; see [Rotate the Core key](#rotate-the-core-key) |

`install.sh --status` and `--stop` are retired; they name the `parsar` command instead.
For a second installation, use its own command, such as
`~/.parsar/core-console/parsar status`.

## Service health

Use these observations for different questions:

| Observation | What it establishes |
| --- | --- |
| `parsar status`, PostgreSQL health | The database accepts its readiness check |
| Core `/healthz` | Core's process is alive |
| An authenticated API read | The caller's key works for that resource |
| Environment connection | The Runtime transport is connected |
| A finished Turn and its results | The task's recorded outcome |

Service health does not prove that a native harness or a model works. Use Session,
Turn, Items and Usage reads for execution, and Web's **Nodes** page for node connection,
readiness and placement. For local diagnosis, use the installation's own Compose file:

```sh
docker compose -f "$HOME/.parsar/core/generated/compose.json" ps --all
docker compose -f "$HOME/.parsar/core/generated/compose.json" logs --tail 200 core
```

Native Core logs to its user unit: `journalctl --user -u <project>-core.service`, with
`project` from `state.json`. Don't paste `docker compose config`, `docker inspect` or
raw logs into public issue reports.

## Stop and restart

Let active work settle before a planned restart:

```sh
~/.parsar/core/parsar stop
~/.parsar/core/parsar start
```

Stopping Core stops no node and no sandbox. Node services, their microVMs and Docker
containers keep running; stopping is not a way to reclaim compute. A Core restart does
not continue an interrupted native tool call transparently. After reconnecting, query
the same Session; don't create a new Session to replay uncertain work. Session event
streams are live only; recover through Session, Turn and Items reads.

A Web restart, including one caused by `parsar apply`, signs everyone out of the
console.

## Core key

Each installation has one administrator credential, the Core key. The installer
generates a 64-character random key in `<install dir>/secrets/core.key`, by default
`~/.parsar/core/secrets/core.key`. The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes Core API (`/core/v1`) requests sent as `Authorization: Bearer <Core key>`;
- never authorizes the Agent API (`/v1`). Applications use Project API keys, which in
  turn can't call `/core/v1`.

Keep it private: `secrets/` is mode `0700` and its files `0600`. Of the services, only
Web reads `core.key`; Core reads its SHA-256 from `generated/core-key-digests.json`,
which `parsar apply` derives from the key. Both read them only at startup. A Core key
has at least 32 characters and no whitespace. Web limits failed sign-ins.

Scripts run on the Core host, call Core's loopback port and read the key from its file,
which keeps it off the command line:

```sh
curl -fsS -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.parsar/core/secrets/core.key")") \
  http://127.0.0.1:8091/core/v1/projects
```

### Rotate the Core key

```sh
~/.parsar/core/parsar rotate-core-key
```

It refuses while `config.json` has changes that are not applied or a generated file was
edited by hand: run `parsar apply` first. It asks for confirmation (`--yes` skips it),
stops Web, writes a new key to `secrets/core.key` and regenerates the digest file. On a
running installation it then restarts Core, starts Web and checks that Core accepts the
new key and refuses the old one; a stopped installation only gets the new files and
uses the new key at the next `parsar start`. The old key stops working as soon as Core
restarts, and every console session ends: sign in again and update your scripts. If the
command stops early, `secrets/core.key` holds the key to use; run `parsar apply` to
finish.

A separate Web-only installation keeps its own copy of the key. After rotating, copy
`secrets/core.key` from the Core host into that installation's `secrets/core.key`
(mode `0600`) and run its `parsar apply`. Its `parsar status` reports a key that Core
refuses. `rotate-core-key` refuses to run on a Web-only installation.

## Projects and API keys

Create Projects and issue their keys in Web, on **Projects and keys**, or with the
[Core API](../../contracts/agents-api/admin-api.md) under `/core/v1/projects`. A key's
plaintext is shown once; Core stores only its digest. All keys of a Project share its
assets and execution principal, and each write records the key that made it.

To rotate an application key, issue another key in the same Project, update the
application, then **Revoke** the old key. **Archive** disables all of a Project's keys
and keeps its assets and accepted work. Administrators can inspect and delete retained
resources but can't execute with the Core key.

## Data and upgrades

### Back up

Back up these together; a restore needs all of them:

- the PostgreSQL volume `<project>_database`. It holds Projects, key digests, nodes,
  default models, encrypted credentials and all execution history, including large
  objects. A logical dump:

  ```sh
  docker compose -f "$HOME/.parsar/core/generated/compose.json" exec -T database \
    pg_dump -U agents_api agents_api > parsar-backup.sql
  ```

- the installation directory: `config.json`, `state.json` (installation ID) and
  `secrets/`. `credential.key` must stay with the database, or stored credentials can't
  be decrypted; never regenerate it to get past an error.
- `state/e2b/`, when E2B is used: receipts Core needs to clean up E2B sandboxes.
- each node's state directory on its host,
  `/var/lib/parsar-node/.parsar/nodes/<installation-id>/` (sudo mode) or
  `~/.parsar/nodes/<installation-id>/` (no sudo), with its provider storage: Docker
  volumes or microsandbox's store.
- the bundle you installed from, to repair or recover the same release.

A missing node state directory is a recovery incident: restore it with the database
and provider storage rather than registering over existing resources. Never prune
Docker volumes or delete native history to make a retry pass. A deleted Session does
not prove that all provider resources were reclaimed.

### Upgrade an installation

| Installation | How to upgrade |
| --- | --- |
| Made before `config.json` (it has `installation.json`) | [Convert it](#convert-an-earlier-installation) with the new bundle |
| Made with `config.json`, same release | Rerun `./install.sh` from the same bundle to repair it |
| Made with `config.json`, other release | Not supported yet. The installer refuses a bundle from another release and says upgrades arrive with `parsar upgrade`, which doesn't exist yet. Keep running the installed release |

There is no downgrade: database migrations can't be undone, so back up before any
upgrade or conversion.

### Convert an earlier installation

Installations made before `config.json` have `installation.json`, `config/core.env` and
`admin/`: those of every installer since the Core key was introduced. Plain
`./install.sh` refuses them. Convert one with the new release's bundle:

```sh
./install.sh --convert --install-dir "$HOME/.parsar/core"
```

Conversion is also an upgrade to that release, and its database migrations can't be
undone. Back up the database first. An installation made before `config.json` keeps its
Compose file at the top of the installation directory:

```sh
docker compose -f "$HOME/.parsar/core/compose.json" exec -T database \
  pg_dump -U agents_api agents_api > parsar-backup.sql
```

Conversion reads the old files without changing anything, shows the resulting settings
and the same backup command, and asks for confirmation. `--yes` skips the prompt and
runs the migrations at once, so use it only after backing up. It then writes
`config.json` and `state.json`, moves the secrets into `secrets/` without copying
them, removes the old generated files, and starts the new release with the same
Compose project, database and installation ID. Settings set by hand, such as
`AGENTS_API_EXECUTION_CONCURRENCY`, `PARSAR_LOG_*` or a Runtime history file, move
into `config.json`.

It stops before changing anything, listing each reason, when something can't be
converted: an edited `compose.json`, an unknown or edited generated value in
`core.env`, an external database, or an installation public URL that differs from the
sandbox deployment's Core URL. For that last case, rerun with `--public-url` naming one
of the two; choosing the installation's URL means the deployment's nodes must be added
again. Unknown files in `config/` and `admin/` are reported and left in place.

If conversion is interrupted, or the new release fails to start, fix the cause and rerun
`./install.sh --convert` with the same bundle; `--public-url` may be repeated but not
changed. In a split deployment, convert the Core host first, then each Web-only host,
and run the Web host's `parsar apply` afterwards.

### Upgrade notes

These names are retired. Core and Web refuse to start while a retired setting is
present, and the installers reject retired flags and variables; each error names the
replacement:

| Retired | Replacement | Where |
| --- | --- | --- |
| `AGENTS_API_EXECUTION_OPTIONS_FILE` | Default models, set per harness in Web (**System**, **Default model**) or with `PUT /core/v1/harnesses/{harness}/model-provider` | `config/core.env`. `--convert` does not carry it over; it leaves the file, which may hold model keys, and reports it. Delete the file once the defaults are set |
| `AGENTS_API_DAEMON_WS_URL` (a `wss://…/api/v1/agent-daemon/ws` URL) | `OAC_PUBLIC_URL` (the origin, such as `https://core.example`), or `public_url` in `config.json` | `config/core.env` |
| `AGENTS_API_CONFIG_FILE` | None; delete the line | `config/core.env` |
| `AGENTS_API_MANAGED_RUNTIMES_FILE` | The sandbox deployment in the database, set in Web | `config/core.env`; see [older file-managed installations](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#older-file-managed-installations) |
| `admin/sandbox-admin.key`, `admin/digests.json` | `admin/core.key`, `admin/core-key-digests.json` (conversion then moves them to `secrets/` and `generated/`) | Files and their `compose.json` mounts |
| `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE` | `OAC_CORE_KEY_DIGESTS_FILE` | `config/core.env` |
| `CORE_CONSOLE_ADMIN_TOKEN_FILE`, `install.sh --admin-token-file` | `OAC_WEB_CORE_KEY_FILE`, `--core-key-file` | Web environment; installer flag |
| `CORE_CONSOLE_AUTH_MODE`, `CORE_CONSOLE_STATE_DIR`, `CORE_CONSOLE_PASSWORD_FILE` | None: Web has no accounts or passwords; sign in with the Core key | Web environment, with their `state/console` and `config/console.password` mounts |
| `install.sh --sandbox-provider`, `--provider` | `install.sh --sandbox`; add the Core host as a node with Add node | Installer flags |
| `install.sh --status`, `--stop` | `parsar status`, `parsar stop` | Installer flags |
| `PARSAR_NODE_ENROLLMENT_TOKEN` | The token on standard input with `--enrollment-token-stdin`, as Web's Add node command passes it | Node installer; it refuses the variable |

Before converting, rename or remove only the names from before the Core key:
`admin/sandbox-admin.key`, `admin/digests.json`, `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`
and the retired `CORE_CONSOLE_*` settings. Leave `AGENTS_API_DAEMON_WS_URL`,
`AGENTS_API_CONFIG_FILE` and `AGENTS_API_EXECUTION_OPTIONS_FILE` to `--convert`, which
maps or reports them. Don't add `OAC_PUBLIC_URL` by hand: when it is present,
conversion takes it as the address Core uses and skips the check against the sandbox
deployment's Core address that keeps existing nodes bound. Hosted and self-hosted
Sessions that relied on the retired options file
have no model provider of their own and can't start new work after the upgrade. Count
them before converting, from the new bundle:

```sh
python3 model_provider_sessions.py --install-dir "$HOME/.parsar/core"
```

Recreate them with `x_agents_core.model_provider` or an Agent that has one saved.

The deployment no longer stores a Core address; nodes keep the one they enrolled with.
For a Core you run without the installer, read `GET /core/v1/sandbox/deployment` with
the Core key before upgrading and set `OAC_PUBLIC_URL` to its `core_url`; with
any other value, every existing node counts as bound to another address and must be
added again.

#### Node connections at /api/v1

Core and its nodes must come from the same distribution; the node installer refuses a
mismatched release. Current releases serve every machine connection at `/api/v1`, and
Web returns 404 there. When the new Core and Web go live, route `/api/v1/*`, including
WebSocket upgrades, to Core instead of Web
([HTTPS and the reverse proxy](install.md#https-and-the-reverse-proxy)); with the old
routing, node enrollment and every Runtime connection fail, for Docker, microsandbox,
E2B and self-hosted executors alike.

Nodes from releases that used the removed `/core/v1/sandbox/enroll` and
`/core/v1/sandbox/node/*` paths can't connect to a current Core. For a Docker or
microsandbox deployment:

1. Drain with the previous release while its nodes are connected: enter maintenance,
   archive retained hosted Sessions and wait until nothing is retained (the
   [maintenance procedure](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance)).
2. Upgrade Core and Web, and change the routing as above.
3. Save the new release's Runtime, resume, and add the nodes again. On each node host,
   stop the old node service and move its state directory aside as a backup first.

A Web-selected Docker or microsandbox deployment saved before deployment specifications
existed has an empty specification after migration. Draining it needs a Core that has
the pre-specification drain mode (pull request #114) but still serves the old node
paths. No such release was published: build a distribution from main commit
`7b66be236a627246c85658722314285e6b39d9b8`, or any commit that contains #114 but not the
move to `/api/v1/sandbox-node`. That Core loads the deployment only to drain; fresh
sandboxes, node configuration reads and enrollment are refused. Back up, replace Core
and Web with that build, drain as in step 1, then upgrade to the current release and
continue with steps 2 and 3. Node IDs change; Session history and persisted Files and
Artifacts remain. E2B deployments from that period are not covered.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `Run as a non-root user on Linux amd64 with Docker access` | Run `install.sh` as a normal user, on Linux amd64 |
| `Installation failed; inspect prerequisites and private deployment files` | A prerequisite failed without its own message, most often Docker: check that `docker info` and `docker compose version` work for this user |
| `Docker Compose 2.26.0 or newer is required …` | Update the Docker Compose plugin |
| `Port N is already in use; select another port` | Free the port, or install with `--core-port`/`--web-port` |
| `Installation directory is not empty …` | Use an empty `--install-dir` |
| `This installation is configured by …/config.json …` | Flags only seed a new installation: edit `config.json` and run `parsar apply` |
| `This installation predates config.json …` | [Convert it](#convert-an-earlier-installation) |
| `This installation runs another release …` | See [Upgrade an installation](#upgrade-an-installation) |
| `generated/<file> was edited by hand` | Put the change in `config.json`, then `parsar apply --discard-edits` |
| `config.json has changes that are not applied` | Run `parsar apply` |
| `Core rejects secrets/core.key …` | Run `parsar apply`, which restarts Core with the key's digest |
| `config.json not applied: …` | `parsar apply` printed Core's startup error above; fix `config.json` and apply again |
| Web answers 403 `Forbidden` | Open exactly the console address `parsar status` prints; the reverse proxy must pass the original Host |
| `/v1` or `/api/v1` answers 404 | Those paths reach Web; route them to Core ([reverse proxy](install.md#https-and-the-reverse-proxy)) |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `parsar status`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the harness, or pass one; self-hosted Sessions always pass their own |
| Add node says nodes need an HTTPS public URL | Set `public_url` and run `parsar apply` |
| Add node says the console has no node files | Install from the offline bundle, or add the node files and rerun `./install.sh` |
| A node is not ready | See [node troubleshooting](nodes.md#troubleshooting) |

## Exposure and network policy

Core and Web listen on host loopback; PostgreSQL has no published port unless Core is
native. Your TLS reverse proxy routes `/v1` and `/api/v1` to Core and everything else to
Web. Web signs administrators in with the Core key, checks the origin of every request,
and forwards signed-in `/core/v1` requests to Core with the Core key, which stays on
the server. It answers 404 on `/v1` and `/api/v1` whatever credential a request carries,
serves only the non-secret node payload at `/node-install/`, and has no Docker or KVM
access. Machine routes keep their own enrollment and connection credentials. Web
sign-in sessions live in Web's memory and last 12 hours.

microsandbox uses an explicit network policy: public egress, the Core and DNS ports the
Runtime needs, and no inbound or private-network access. Private model or MCP endpoints
need an explicit policy change. A Session's own network policy is separate.

The Docker node uses the nested-sandbox Runtime policy. Its service account needs the
host's Docker daemon, and Core has no Docker socket. Install nodes on trusted hosts and
don't share their Docker access with untrusted users.

A Core restart leaves node services and resident microVMs running. A host reboot, the
end of a user manager or the loss of a running microVM is not a Core restart and is not
a qualified recovery workflow. Idle snapshots keep their recovery contract.

### API-key write history

Core records which key made each public resource write, for the console. Set
`core.write_audit_retention` in `config.json` (Go duration, at least `1h`, default
`2160h`) to control how long non-creation history is kept; creation ownership is kept
for good. Removing keys or resources does not delete these records. See the
[query contract](../../contracts/agents-api/write-audit.md); administrator mutations
have a separate
[audit log](../../contracts/agents-api/admin-api.md#monitoring-and-audit). Neither
logs bodies or secrets.
