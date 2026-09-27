# Operate your installation

The installation operator owns the Core host, its storage and its availability. Node
hosts run their own services; see [Nodes](nodes.md). Settings are described in the
[configuration reference](../configuration.md).

## The oac command

Each installation has its own management command in its directory. It needs neither
the bundle nor root:

```sh
~/.oac/core/oac status
```

| Command | What it does |
| --- | --- |
| `oac status` | Shows each service and its health, Core and Web health, the public URL, API base URL, console address and source commit, the reverse-proxy routes to configure, `config.json` changes not applied yet and generated files edited by hand. A Web-only installation also checks that its Core accepts its Core key. Exits non-zero when a service is unavailable. It never calls a model |
| `oac start` | Starts every service with the files last written by `apply`; warns about unapplied changes |
| `oac stop` | Stops PostgreSQL, Core and Web. Data, nodes and sandboxes are kept, and running sandbox work may continue |
| `oac apply` | Applies `config.json` and restarts what changed; see [how apply works](../configuration.md#how-oac-apply-works) |
| `oac apply --dry-run` | Shows the changed settings, files and restarts, and changes nothing |
| `oac apply --discard-edits` | Overwrites generated files edited by hand, keeping each as `generated/<file>.edited-<time>` |
| `oac apply --confirm-public-url-change URL` | Confirms a public URL change without a prompt; must equal the new URL |
| `oac apply --yes` | Web-only: pairs Web with a different Core without asking |
| `oac rotate-core-key [--yes]` | Replaces the Core key; see [Rotate the Core key](#rotate-the-core-key) |

`install.sh --status` and `--stop` are retired; they name the `oac` command instead.
For a second installation, use its own command, such as
`~/.oac/core-console/oac status`.

## Service health

Use these observations for different questions:

| Observation | What it establishes |
| --- | --- |
| `oac status`, PostgreSQL health | The database accepts its readiness check |
| Core `/healthz` | Core's process is alive |
| An authenticated API read | The caller's key works for that resource |
| Environment connection | The Runtime transport is connected |
| A finished Turn and its results | The task's recorded outcome |

Service health does not prove that a native harness or a model works. Use Session,
Turn, Items and Usage reads for execution, and Web's **Nodes** page for node connection,
readiness and placement. For local diagnosis, use the installation's own Compose file:

```sh
docker compose -f "$HOME/.oac/core/generated/compose.json" ps --all
docker compose -f "$HOME/.oac/core/generated/compose.json" logs --tail 200 core
```

Native Core logs to its user unit: `journalctl --user -u <project>-core.service`, with
`project` from `state.json`. Don't paste `docker compose config`, `docker inspect` or
raw logs into public issue reports.

## Stop and restart

Let active work settle before a planned restart:

```sh
~/.oac/core/oac stop
~/.oac/core/oac start
```

Stopping Core stops no node and no sandbox. Node services, their microVMs and Docker
containers keep running; stopping is not a way to reclaim compute. A Core restart does
not continue an interrupted native tool call transparently. After reconnecting, query
the same Session; don't create a new Session to replay uncertain work. Session event
streams are live only; recover through Session, Turn and Items reads.

A Web restart, including one caused by `oac apply`, signs everyone out of the
console.

## Core key

Each installation has one administrator credential, the Core key. The installer
generates a 64-character random key in `<install dir>/secrets/core.key`, by default
`~/.oac/core/secrets/core.key`. The Core key:

- signs in to Web. The browser gets an HttpOnly session cookie, never the key;
- authorizes Core API (`/core/v1`) requests sent as `Authorization: Bearer <Core key>`;
- never authorizes the Agent API (`/v1`). Applications use Project API keys, which in
  turn can't call `/core/v1`.

Keep it private: `secrets/` is mode `0700` and its files `0600`. Of the services, only
Web reads `core.key`; Core reads its SHA-256 from `generated/core-key-digests.json`,
which `oac apply` derives from the key. Both read them only at startup. A Core key
has at least 32 characters and no whitespace. Web limits failed sign-ins.

Scripts run on the Core host, call Core's loopback port and read the key from its file,
which keeps it off the command line:

```sh
curl -fsS -H @<(printf 'Authorization: Bearer %s\n' "$(cat "$HOME/.oac/core/secrets/core.key")") \
  http://127.0.0.1:8091/core/v1/projects
```

### Rotate the Core key

```sh
~/.oac/core/oac rotate-core-key
```

It refuses while `config.json` has changes that are not applied or a generated file was
edited by hand: run `oac apply` first. It asks for confirmation (`--yes` skips it),
stops Web, writes a new key to `secrets/core.key` and regenerates the digest file. On a
running installation it then restarts Core, starts Web and checks that Core accepts the
new key and refuses the old one; a stopped installation only gets the new files and
uses the new key at the next `oac start`. The old key stops working as soon as Core
restarts, and every console session ends: sign in again and update your scripts. If the
command stops early, `secrets/core.key` holds the key to use; run `oac apply` to
finish.

A separate Web-only installation keeps its own copy of the key. After rotating, copy
`secrets/core.key` from the Core host into that installation's `secrets/core.key`
(mode `0600`) and run its `oac apply`. Its `oac status` reports a key that Core
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
  docker compose -f "$HOME/.oac/core/generated/compose.json" exec -T database \
    pg_dump -U agents_api agents_api > oac-backup.sql
  ```

- the installation directory: `config.json`, `state.json` (installation ID) and
  `secrets/`. `credential.key` must stay with the database, or stored credentials can't
  be decrypted; never regenerate it to get past an error.
- `state/e2b/`, when E2B is used: receipts Core needs to clean up E2B sandboxes.
- each node's state directory on its host,
  `/var/lib/oac-node/.oac/nodes/<installation-id>/` (sudo mode) or
  `~/.oac/nodes/<installation-id>/` (no sudo), with its provider storage: Docker
  volumes or microsandbox's store.
- the bundle you installed from, to repair or recover the same release.

A missing node state directory is a recovery incident: restore it with the database
and provider storage rather than registering over existing resources. Never prune
Docker volumes or delete native history to make a retry pass. A deleted Session does
not prove that all provider resources were reclaimed.

### Upgrade an installation

| Installation | How to upgrade |
| --- | --- |
| Before the OpenAgentCore rename: `state.json` format 1 and a `parsar-…` project, or a legacy `installation.json` layout | [Convert it](#convert-an-earlier-installation) with this bundle |
| OpenAgentCore, same release | Rerun `./install.sh --install-dir DIR` from the same bundle to repair it |
| OpenAgentCore, another release | General upgrades are not supported yet; keep the installed release |

There is no downgrade after database migrations. Back up before converting.

### Convert an earlier installation

Use the old Web and old nodes to drain first: enable maintenance, archive hosted
Sessions until allocations and pending work are both zero, remove every node on the
Nodes page, and run each node's uninstall command from the previous release.
Suspended sandboxes, retained snapshots and uncertain cleanup still count. Archived
Sessions retain their history but cannot resume their old sandbox. Self-hosted
executors keep running and are not converted.

Extract the new bundle and run:

```sh
./install.sh --convert
```

Without `--install-dir`, this converts `~/.parsar/core` into `~/.oac/core`, or resumes
an unfinished conversion already moved there. An explicit custom directory converts
in place. `--yes` skips the one confirmation; `--public-url` only settles a legacy
layout's conflicting public addresses. Convert the Core host before Web-only hosts.
The latter rename their directory, project and payload without copying a database.

Preflight reports generated-file edits, invalid configuration, destination conflicts,
insufficient database-copy space, and every available drain blocker. It reads the
old Core with its Core key. When that Core is stopped and static checks pass, it
announces a temporary start using only the existing files. Refusal restores the
services this probe started; a restoration failure is reported explicitly. No
installation conversion happens before confirmation.

The confirmation prints the backup command for the old project and the directory
and secret moves. For a config.json installation, take the backup before confirming:

```sh
docker compose -f "$HOME/.parsar/core/generated/compose.json" exec -T database \
  pg_dump -U agents_api agents_api > oac-backup.sql
```

A legacy installation uses `compose.json` at the directory's top level instead.
Its supported settings move into config.json and secrets move into secrets/ in the
same run, with no intermediate upgrade or second confirmation. Unknown legacy files
are reported and left in place; unknown settings and edited derived values refuse.

Conversion stops the old services, copies the stopped database into
`oac-<same suffix>_database`, removes the old containers and network, and moves the
default directory atomically. It retains `parsar-<suffix>_database` as a backup.
An interrupted copy is restarted only after verifying that its target volume and
copy container belong to this conversion. A foreign same-name resource is refused.

The new state format is 2; config.json's schema format remains 1. Ports, public URL,
secrets, Core key, installation ID and database contents are preserved. The old
`parsar` path becomes a stub naming the new `oac` command. New services use `OAC_*`
settings and `io.oac.inputs` labels. Docker and microsandbox deployments retain
their resources, replace their Runtime with the bundle's release through the normal
maintenance-only API, then resume admission. E2B remains in maintenance: rebuild its
template with this release's `build-template.py`, replace it in Web, then resume.

Rerun `./install.sh --convert` with the **same bundle** after any interruption,
including after the directory move. For a custom directory, repeat `--install-dir`.
The journal rejects a different bundle or installation identity. It verifies Project
identities and service health before recording completion. Check retained history,
then add new nodes through **Nodes → Add node**. Keep the old volume until this
verification is complete; the final output names its exact `docker volume rm`
command. Never remove the new volume or use a Docker-wide cleanup.

Before the directory move, the old command can restart the old services against the
untouched old volume. After the move, finish by rerunning conversion. Once new Core
migrations run, restoring the old release requires the old bundle, old installation
files and the pre-conversion backup or retained old volume; this is not a downgrade
of the new database.

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
| `install.sh --status`, `--stop` | `oac status`, `oac stop` | Installer flags |
| `PARSAR_NODE_ENROLLMENT_TOKEN` | The token on standard input with `--enrollment-token-stdin`, as Web's Add node command passes it | Node installer; it refuses the variable |

The release also renames the following operator identities. Old process settings
are refused even when empty; errors name all applicable replacements without values.
These historical names are inputs only to conversion and retirement diagnostics.

| Before | OpenAgentCore |
| --- | --- |
| `AGENTS_API_*` process settings | `OAC_*`; `AGENTS_API_ENGINE` becomes `OAC_DEFAULT_HARNESS`, `AGENTS_API_SANDBOX_INSTALLATION_ID` becomes `OAC_INSTALLATION_ID`, and `AGENTS_API_RUNTIME_HISTORY_FILE` becomes `OAC_HISTORY_SETTINGS_FILE` |
| `CORE_CONSOLE_*` | `OAC_WEB_*`; `CORE_CONSOLE_UPSTREAM` becomes `OAC_WEB_UPSTREAM` |
| `PARSAR_LOG_*` | `OAC_LOG_*` |
| Runtime `PARSAR_*`, including `PARSAR_HOME` | `OAC_RUNTIME_*`, including `OAC_RUNTIME_HOME`; separate Parsar-product hooks are unchanged |
| Developer/build variables and test database/SDK variables | `OAC_DEV_*` and `OAC_TEST_*`; `OAC_TEST_DATABASE_URL` selects the dedicated test database |
| `~/.parsar/core`, `parsar`, `parsar_cli.py`, `parsar.pyz`, `.parsar.lock` | `~/.oac/core`, `oac`, `oac_cli.py`, `oac.pyz`, `.oac.lock` |
| `parsar-<hex>` project, containers, default network and database volume | `oac-<same hex>`; the old database volume is retained as a backup |
| `parsar-<hex>-core.service`, `PARSAR_INPUTS`, `io.parsar.inputs`, `x-parsar`, `/run/parsar` | `oac-<hex>-core.service`, `OAC_INPUTS`, `io.oac.inputs`, `x-oac`, `/run/oac` |
| `parsar-core-<commit>-linux-amd64` bundle and artifact prefix | `oac-<commit>-linux-amd64` |
| `agents-api`, `agents-api-migrate`, `agents-api-device`, `agents-api-environment-key`, `core-console` | `oac-core`, `oac-core-migrate`, `oac-core-device`, `oac-core-environment-key`, `oac-web` |
| `agents-api-e2b-provider`, `/opt/parsar/e2b` | `oac-e2b-provider`, `/opt/oac/e2b` |
| `parsar-daemon`, Runtime `agents-api-*` helpers | `oac-daemon`, `oac-*` helpers |
| `~/.parsar/parsar-daemon`, `/home/runtime/.parsar`, `/run/parsar/daemon-suspend.json` | `~/.oac/daemon`, `/home/runtime/.oac`, `/run/oac/daemon-suspend.json` |
| `parsar-core-runtime@sha256:…`, `agents-runtime-<hex>` | `oac-runtime@sha256:…`, `oac-runtime-<hex>` |
| Runtime `io.parsar.agents-api.*`, microsandbox `io.parsar.*`, `parsar.runtime.placement` | `io.oac.*`, `io.oac.*`, `io.oac.placement` |
| E2B `parsar_*` metadata, `/opt/parsar-e2b`, `/root/.parsar/e2b`, `/etc/parsar-runtime-env.json` | `oac_*`, `/opt/oac-e2b`, `/root/.oac/e2b`, `/etc/oac-runtime-env.json` |
| Model-visible `parsar_workspace`, `parsar_worker`, `parsar_root`, `parsar/subagents`, Codex provider `parsar` | `oac_workspace`, `oac_worker`, `oac_root`, `oac/subagents`, provider `oac` |

The PostgreSQL database and role remain `agents_api`. Public API routes, Go module
paths, source directory names and npm package names are unchanged. Historical
Session content is never rewritten. Nodes and self-hosted executors require their
own [node](nodes.md) and [self-hosted](self-hosted.md) procedures; the Core converter
does not rename their stores or adopt their resources.

Before converting, rename or remove only the names from before the Core key:
`admin/sandbox-admin.key`, `admin/digests.json`, `AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE`
and the retired `CORE_CONSOLE_*` settings. Leave `AGENTS_API_DAEMON_WS_URL`,
`AGENTS_API_CONFIG_FILE` and `AGENTS_API_EXECUTION_OPTIONS_FILE` to `--convert`, which
maps or reports them. Don't add `AGENTS_API_PUBLIC_URL` by hand to a legacy installation: when it is present,
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
| `This installation is configured by …/config.json …` | Flags only seed a new installation: edit `config.json` and run `oac apply` |
| `This installation predates config.json …` | [Convert it](#convert-an-earlier-installation) |
| `This installation runs another release …` | See [Upgrade an installation](#upgrade-an-installation) |
| `generated/<file> was edited by hand` | Put the change in `config.json`, then `oac apply --discard-edits` |
| `config.json has changes that are not applied` | Run `oac apply` |
| `Core rejects secrets/core.key …` | Run `oac apply`, which restarts Core with the key's digest |
| `config.json not applied: …` | `oac apply` printed Core's startup error above; fix `config.json` and apply again |
| Web answers 403 `Forbidden` | Open exactly the console address `oac status` prints; the reverse proxy must pass the original Host |
| `/v1` or `/api/v1` answers 404 | Those paths reach Web; route them to Core ([reverse proxy](install.md#https-and-the-reverse-proxy)) |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `oac status`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the harness, or pass one; self-hosted Sessions always pass their own |
| Add node says nodes need an HTTPS public URL | Set `public_url` and run `oac apply` |
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
