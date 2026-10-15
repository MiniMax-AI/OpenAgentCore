---
title: "Operations"
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
| `oac apply` | Checks the data volume and `.env`, then starts the services and recreates those whose configuration changed; see [how oac apply works](../configuration.md#how-oac-apply-works). A failed check changes no service |
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

Service health does not show that a Harness or a model works. Use Session, Turn, Items and Usage reads for execution, and Web's **Nodes** page for node connection, readiness and placement. For local diagnosis, use the installation's own Compose file:

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" ps --all
docker compose -f "$HOME/.oac/core/compose.yaml" logs --tail 200 core
docker compose -f "$HOME/.oac/core/compose.yaml" logs --timestamps init
```

The `init` service exits after initialization. Its step logs are described in [Deployment](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation).

Don't paste `docker compose config`, `docker inspect` or raw logs into public issue reports.

## Request diagnosis

Open **Monitor → Request log** to inspect recent requests and copy their request IDs for correlation with Core's service log. [Request log](../../contracts/agents-api/admin-api.md#request-log) defines its scope, privacy and limited in-memory retention. For a request that accepted an asynchronous Turn, inspect **Session log** for the execution result; HTTP acceptance alone is not task success. Use your existing log collector for retention across restarts or Core instances.

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
| See Harnesses and their default models | `core GET /harnesses` |
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

Core records which key made each public resource write; the retention of that history is [`OAC_WRITE_AUDIT_RETENTION`](../configuration.md#settings).

## Back up

A recoverable backup contains one consistent, stopped-write set. A database dump or Core volume export alone does not include independent workspace storage or node compute state. Restore the data volume together with the installation directory from the same backup, which holds the release that wrote it; a newer release can [upgrade](#upgrade) the restored installation afterward.

### Backup contents

- The Docker volume `<project>_data`, including `database/`, `secrets/` and `state/`. It holds Projects, key digests, nodes, default models, encrypted credentials and execution history, including large objects. Keep `secrets/core/credential.key` with its matching database or stored credentials cannot be decrypted.
- The installation directory, including `.env`, `compose.yaml`, Compose overrides and the management command.
- Every independent workspace namespace selected by current or retained objects, even when it is outside the installation volume. Back up its entire root, including `.oac-storage-root`, object identities, staging, live data, trash and deletion markers. Preserve numeric ownership, modes, links and extended attributes, including every `user.*` attribute. Copying only `live/data` loses lifecycle evidence and can resurrect deleted identities. [Workspace storage](../workspace-provider.md#kernel-nfs-adapter) owns the layout and ownership requirements.
- For a [quota mode](../workspace-provider.md#quota-mode) namespace, the entire XFS filesystem holding the export as one block-level copy: a storage or LVM snapshot, or an image of the frozen or unmounted device. Only such a copy keeps project IDs, quota limits, inode identities, the filesystem UUID and the `oac-workspace-quota` journal consistent. A file-level copy, `tar`, `rsync` or `xfsdump` loses them; the command then refuses the restored objects instead of serving them unbounded.
- Each node's state directory, `/var/lib/oac-node/.oac/nodes/<installation-id>/`, and its provider storage: Docker volumes or microsandbox's store. Include all compute and snapshot resources still referenced by the database. See [when a node host fails](./nodes.md#when-a-node-host-fails).

- Each private `checkpoint_root` in its entirety, including `.oac-checkpoint-store`, archives, operation journals, lock files and tombstones, together with the matching Core database. It is separate from the native SDK store and independent workspace namespace. Preserve its identity and private ownership; the [checkpoint transfer contract](../sandbox-provider.md#checkpoint-transfer) owns its lifecycle.

### Establish a stopped-write window

1. Block new application input, live file access and administrative mutations at the installation's ingress. Let active execution, initialization, file operations and native cleanup finish. Stop other applications or host processes that can write to the same filesystem.
2. While Core and nodes remain available, confirm that every owned writer has stopped and all Create, Kill, snapshot cleanup and filesystem mutations have settled. An offline node, timeout, missing instance or stopped service is insufficient. A confirmed suspension stops its source compute but retains a checkpoint that must be included in the backup. For the simplest cold recovery baseline, wait until eligible retained Sessions have completed checkpoint expiry cleanup and their allocations are released; retain their filesystem objects and native history under the [cold replacement contract](../sandbox-provider.md#cold-replacement-after-checkpoint-retention).
3. Stop Core and Web, then stop the node control services to prevent new lifecycle work. Recheck the exact native resources and keep all writers stopped until every part of the backup is complete. **Stopping Core or a node service does not stop its sandboxes.** The node service uses `KillMode=process`; microVMs and other provider-managed compute may continue to run independently.
4. Flush the stopped filesystem's pending writes using the storage platform's supported procedure and take its snapshot or export. For quota mode, freeze the XFS filesystem with `xfs_freeze -f` or unmount it while copying its device, then thaw it. Export the Core data volume and installation directory, plus required node state and provider stores, within the same window. Stop PostgreSQL before copying its `database/` files. Record the release, backup time, component inventory and checksums together, and keep the backup outside the installation being backed up.
5. Restore service availability only after all copies finish. Bring up storage before Core and nodes, verify readiness, then reopen ingress. Do not replay uncertain native work to make a backup pass.

There is no one-command, lossless maintenance drain. Archive and deployment reset change Session lifecycle state; they are not substitutes for a backup pause that preserves resumability. If any writer or mutation remains unknown, keep the backup incomplete and resolve that ownership first.

For a logical database copy during the stopped-write window, leave only the database service running and use:

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" exec -T database \
  pg_dump -U agents_api agents_api > oac-backup.sql
```

A logical dump still needs the matching secrets, state, independent filesystems and required node resources. Docker Desktop's **Volumes** export is a way to copy a stopped data volume; it does not coordinate these other components.

### Restore and rehearse

Keep application ingress and Core/node services stopped while restoring. Fence or shut down every old writer before exposing the restored filesystem; an unreachable old host is not a fence. A rehearsal must use an isolated installation and storage copy that cannot connect to the original nodes or share their writable namespace.

Restore matching secrets and database state, the complete independent filesystem namespaces and any required node identities/provider stores before starting Core. Preserve the database's configuration and namespace UUIDs, object identities and deletion markers; do not run first-time namespace initialization over restored storage. Restore a quota mode filesystem by writing its block-level copy to the backing device and mounting it with `prjquota` at the configured mount point; never run `oac-workspace-quota init` over it, and do not mount the copy beside the original, which has the same filesystem UUID. Reestablish the configured mount paths and service UIDs, then validate the mounted filesystem, ownership and xattrs from each participating service context. [Independent workspace storage](../configuration.md#independent-workspace-storage) owns deployment setup.

The cold released baseline resumes only qualified native history after new compute admission. A backup containing retained checkpoints also depends on the matching native runtime, node identity, provider store and external filesystem identities. File-level restore can change inode identity and prevent strict checkpoint restore even when file bytes match. This procedure does not promise portable memory snapshots or restoration onto an arbitrary replacement node; preserve unresolved ownership rather than substituting a new sandbox.

Start the database, Core and node services only after their restored dependencies are complete; keep ingress restricted while checking them. In an isolated rehearsal, verify a retained Session's files and native continuation on fresh compute where qualified, confirm that deleted objects cannot be reopened, and check that no old writer can access the restored namespace. Record the results before relying on the backup for recovery.

## Upgrade {#upgrade}

An installation of v0.0.10 or later upgrades in place to a newer release. It keeps its database, data volume, `.env`, secrets, Core key, installation ID and the entries you added to `compose.override.yaml`. An older installation can't upgrade: install the new release into a new directory. Downgrades are refused.

1. Read the target release's notes for [protocol changes](#protocol-changes) and the actions they require.
2. [Back up](#back-up) the installation. The upgrade takes no backup, and after it the backup is the only way back to the previous release.
3. Run the target release's installer with `--upgrade` and the installation directory:

   ```sh
   curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --upgrade --install-dir "$HOME/.oac/core"
   ```

   `--version TAG` selects a release other than the latest. On Windows, pass the same flags to the downloaded `install.ps1`. For an installation from a [source bundle](../development.md#install-a-checkout-without-publishing-a-release), run the new bundle's installer: `python3 <bundle>/install.py --install-dir <dir> --upgrade`.
4. When you are ready, [apply the release's Runtime](#runtime-after-an-upgrade).

Without `--upgrade`, another release's installer changes nothing and stops with `This installation runs release <old>; this installer is <new>. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade.` (`install.py` says `this bundle is`). The installed release's installer resumes the installation, with or without the flag. Installation and mutating `oac` commands share the [installation lock](../configuration.md#installation-directory); if another command is running, wait for it to finish before retrying.

### What the upgrade does

The installer downloads the release's `compose.yaml` and pulls missing images. Before it replaces any file, it runs the new release's configuration check and initialization with the installation's `.env` and Compose overrides. Initialization upgrades the data volume, or refuses a downgrade. The installer then puts the new `compose.yaml` and `oac` in place and starts the services, recreating those that changed; Core migrates the database as it starts. [Deployment](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation) describes each step. The restart interrupts work as any [Core restart](#stop-and-restart) does; nodes and sandboxes keep running and reconnect.

On success the installer prints the Core key and the line `Upgraded from <old> to <new>. Settings → Sandbox and scheduling offers this release's Runtime; sandboxes keep their current Runtime until you apply it.`

If a check fails, the installer prints `Upgrade stopped; the running services are unchanged` with Docker's output, and the previous release keeps serving. Fix the cause and rerun the command. Once initialization has upgraded the data volume, the previous release can no longer start on it: if a later step fails, for example with `startup failed; data retained, rerun the installer`, rerun the same command, which repeats every step safely, or [roll back](#roll-back).

### Runtime after an upgrade

The upgrade leaves the sandbox configuration unchanged: nodes keep serving the saved Runtime, and new Sessions use it. To move to the release's Runtime, open **Settings** → **Sandbox and scheduling**, choose **Use this installation's Runtime** in the **Runtime** row, review the change and choose **Save configuration**. Nodes then prepare the new generation, and existing sandboxes keep theirs until they are released; see [Change the sandbox configuration](./nodes.md#change-the-sandbox-configuration). An E2B deployment has no **Runtime** row; its template keeps working unless the release changes the Core–Runtime protocol.

### Protocol changes

Three connections require the same protocol version on both sides: Core and its nodes, a node and its microsandbox helper, and Core and the Runtime. The upgrade changes no node host and no running sandbox. A release that changes one of these protocols says so in its release notes and names which of these actions it requires:

- reinstall each node: [remove it](./nodes.md#remove-a-node), clean up the host, then add the host again with **Add node**;
- apply the release's Runtime right after the upgrade; sandboxes on earlier generations stop working;
- [build a new E2B template](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md#build-a-template) and select it in [the sandbox configuration](./nodes.md#change-the-sandbox-configuration);
- reconnect [self-hosted machines](./self-hosted.md#reconnect-an-existing-session).

### Downgrades

Initialization refuses a release that is not newer than the installed one, with `This installation runs release <installed> (<date>); this release, <own> (<date>), is not newer. Downgrades are not supported: run that release or a newer one, or restore the backup taken before the upgrade.` The installer reports it as a stopped upgrade, and the installation keeps running. Core refuses a database that a newer release migrated, with `Agents API database schema version N is newer than this Core supports (M). Downgrades are not supported: run the release that upgraded this database or a newer one, or restore the backup taken before the upgrade.` To return to an earlier release, [roll back](#roll-back).

### Roll back

A rollback restores the backup taken before the upgrade: stop the services, [restore](#restore-and-rehearse) the data volume and the installation directory, then start them. Restore before you apply the release's Runtime if you can: once nodes have prepared it, the restored database doesn't know their new generation, so also [remove every node](./nodes.md#remove-a-node) and add it again. Sessions and data created after the backup are lost.

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

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `Web port unavailable; choose --web-port` | Another program holds that port. Stop it, or choose another `--web-port`. The installer does not move to a different port |
| `incomplete installation: preserve … and choose another directory` | Preserve the directory and choose another `--install-dir` |
| `configuration check failed; no service was changed` | `.env` has a value Core rejects. The message names the variable and not the value. Fix `.env` and run `oac apply` again |
| `data volume check failed; no service was changed` | Initialization refused the data volume; the message names the cause, such as a volume a newer release upgraded. See [downgrades](#downgrades) |
| `This installation runs release …` | The installer or initialization belongs to another release. To move to a newer one, follow [Upgrade](#upgrade); downgrades are refused |
| `This account cannot use Docker` | Add the account to the `docker` group and log in again, or use Docker Desktop or rootless Docker. Do not run the installer with `sudo` |
| `This account cannot access …` | An earlier run with `sudo` left files owned by root. Run the `sudo chown` command the message prints, then rerun as your own account |
| `Docker Compose 2.26 or newer is required` | Install or update the Docker Compose plugin |
| `Docker could not pull …` | The Docker daemon cannot reach the registry. Configure its proxy or a registry mirror, or use the [offline archive](./install-options.md#offline-hosts) |
| `startup failed; data retained, rerun the installer` | Read the service logs above it, fix the cause, then [resume installation](./install.md#install); keep the data |
| Web answers 403 `Forbidden` | The browser host is not `OAC_PUBLIC_URL`. Open that origin; a reverse proxy must pass the original Host |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `docker compose ps`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the Harness, or pass one; self-hosted Sessions always pass their own |
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
