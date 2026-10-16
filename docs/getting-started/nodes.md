---
title: "Add and manage nodes"
---

A node is a Linux host that runs sandboxes for Core-hosted Sessions. This guide is for operators who add, change and remove nodes when the sandbox backend (see [Choose a sandbox backend](./sandbox-backends.md)) is microsandbox or Docker; E2B needs no nodes.

Core places each new Session on a node with free capacity; the node creates the sandbox, and the sandbox connects back to Core. Machines that applications connect for their own Sessions are [self-hosted executors](./self-hosted.md), not nodes. You add a node by generating a command in Web and running it on the host. The [sandbox deployment contract](../../contracts/agents-api/sandbox-deployment.md) defines the sandbox settings and their change rules, and the [node protocol](../../contracts/agents-api/node-generation-protocol.md) defines how nodes prepare and keep Runtime generations.

## Before you add a node

- **The sandbox configuration is saved.** Open **Settings** → **Sandbox and scheduling**, choose **Own machines**, the backend and a sandbox size, and **Save configuration**; see [Choose a sandbox backend](./sandbox-backends.md). To change the backend, choose **Reset deployment** first. Every node of an installation uses that backend.
- **Core has a public URL** that the host and its sandboxes can reach. Nodes download from Core's console and connect to Core at `public_url`. Until it is set, Add node says *Set a public address other machines can reach before adding nodes*; see [Configure the public address](./public-address.md).
- **The console can serve the node files.** Nodes download their Runtime and provider files from the console, which redirects to the release for files it does not hold, and check each file's size and SHA-256 against the release manifest. Node hosts therefore need access to the release. Without the files, Add node says *This console has no node files for …*.
- **The host meets the requirements below.**
- **For independent workspace storage,** the NFS mount and service account are ready on this host; see [Set up workspace storage](./workspace-storage.md). The node receives the selected immutable filesystem configuration with each binding; do not author a separate node storage setting.
- **For checkpoint recovery across microsandbox nodes,** see [Checkpoint recovery across nodes](#checkpoint-recovery-across-nodes).

The Core host joins like any other host: to run sandboxes on it, add it as a node.

### Host requirements

- Linux amd64 with systemd; Python 3.9+, `curl`, `sha256sum` and `flock`; root or sudo.
- SELinux not enforcing. The installer does not support hosts with enforcing SELinux.
- Docker: rootful Docker Engine running, its socket `/var/run/docker.sock` owned by the `docker` group with mode `0660`, enforcing CPU and memory limits (cgroup v2).
- microsandbox: `/dev/kvm` in the `kvm` group (hardware or nested virtualization), and the libraries microsandbox links (glibc).
- CPUs and memory for at least one sandbox of the installation's size, and about 3 GB of free disk while the Runtime image installs: its download and unpacked copy sit under `/var/lib/oac-node` until the import finishes, and the image takes about 1.5 GB in Docker's storage or the microsandbox store. The installer checks the space before it downloads and deletes the archives after the import, so the node keeps the image and under 0.2 GB of programs.
- Access to the console and Core at the public URL; sandboxes reach Core too.

## Add a node

1. In Web, open **Nodes** and choose **Add node**.
2. Set **Sandboxes at once** and, with microsandbox, **Retained sandboxes**: the node's [capacity](../configuration.md#node-capacity). You can change them later with **Edit node**.
3. Choose **Generate command** and copy the command. It registers one node, once, and only if it runs within an hour; Web counts down and offers **Generate new command** when it expires.
4. Run it on the host. Web follows the node from registered to connected to ready.

> [!NOTE]
> The enrollment token is single-use, expires after an hour and only registers the node. The installer takes it only on standard input and refuses it in the environment, where `sudo VAR=… python3` would record it in sudo's log. A sudoers policy with `log_input` records standard input, and so the token.

The command downloads the node installer from your console, checks its SHA-256 and runs it with a one-time enrollment token. The installer downloads the node files and checks each against the release manifest, imports the Runtime image, registers the node, starts its service and waits until Core reports the node connected and ready. It never installs software, and it stops with a one-line hint before changing anything when a prerequisite is missing.

Node installation and removal require root. Web's command uses `sudo` unless the shell is already root. The installer creates an `oac-node` service user and a system service; the node runs as that user, not as root. Running the installer as an ordinary user fails before it reads the enrollment token or changes the host.

### The command

Copy the current command from **Nodes → Add node**. It includes this installation's address, checksum and enrollment token. Before downloading, it checks Linux amd64, Python 3.9+ and the required tools. Temporary download failures retry up to three attempts. If checksum verification fails, copy a fresh command from Web and retry.

- It downloads into the invoking account's private `~/.oac/node-bootstrap` directory, checks SHA-256, and runs with `sudo`, or directly in a root shell. Each retry clears the unfinished download while holding a lock; verified `<sha256>.pyz` files remain available to installers.
- The token reaches the installer on standard input, so it never appears in a process argument, an environment variable or sudo's log.
- The leading space keeps the command out of the shell history where `HISTCONTROL` ignores such lines (the Debian and Ubuntu default).

The installer shows each phase as it runs and, once Core confirms the node, a summary with status and log commands. `export NO_COLOR=1` disables colors; the command passes this through sudo as `--no-color`. It never prints tokens.

### Download through a proxy

Export `http_proxy`, `https_proxy` and `no_proxy` before running the generated command. Uppercase `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` work too; lowercase takes precedence when both are set, even when empty. Use an HTTP proxy URL, optionally with URL-encoded credentials, and comma-separated host exclusions in `no_proxy`. A proxy for HTTPS downloads must support CONNECT.

The command asks sudo to preserve only these six names, and the installer keeps them when it drops to `oac-node`. If sudo policy refuses, ask the host administrator to permit those names, or run from a root shell with the variables exported there. An outer `sudo` you add yourself can discard them. Don't put credentials in command arguments or share shell traces.

The node service keeps the proxy settings of the latest run, so it reaches Core through the proxy it was installed with. They are stored in `/etc/oac-node/<installation-id>.env`, readable only by root, never in the unit file. Loopback addresses always stay direct. Rerunning the command with different settings replaces them and restarts the service; a rerun without a proxy removes them.

## What the installer sets up

| Item | Detail |
| --- | --- |
| Service user | System user `oac-node`, home `/var/lib/oac-node`, no login shell. An existing account with that home and a nologin shell is adopted; any other account named `oac-node` is refused |
| Group | The group that owns `/var/run/docker.sock` (Docker) or `/dev/kvm` (microsandbox): `docker` or `kvm`, nothing else |
| Service | `/etc/systemd/system/oac-node-<installation-id>.service`, a root-owned system unit with `User=oac-node`, enabled at boot. It restarts every 5 seconds while Core is unreachable and stops for good once Core no longer accepts the node |
| Node state | `/var/lib/oac-node/.oac/nodes/<installation-id>/`: identity, configuration, node files. microsandbox keeps its images and sandboxes under `/var/lib/oac-node/.oac/m/` |
| Records | `/etc/oac-node/`: what the installer created or changed, used by reruns and uninstall, and the service's proxy settings |
| Docker | The Runtime image, imported once, and a network `oac-node-<installation-id>` |
| microsandbox network policy | Sandboxes may reach Core, DNS on the host and public addresses, and nothing else: no inbound connections and no private networks |

Root only prepares the account, the group and the unit; everything else, the Docker network included, runs as `oac-node`, in its own session without a terminal. The installer never installs Docker, KVM or packages, never starts Docker, never changes device permissions, sudoers, firewall or SELinux settings, and never touches other accounts.

> [!CAUTION]
> Docker mode is root-equivalent. Membership in the `docker` group lets `oac-node`, and so anything that controls the node, act as root on the host, and containers share the host kernel. Add Docker nodes only on hosts dedicated to sandboxes. microsandbox nodes need only the `kvm` group and run each sandbox in its own microVM.

> [!WARNING]
> microsandbox sandboxes cannot reach private addresses; see [Sandboxes cannot reach a private endpoint](./troubleshooting.md#sandboxes-cannot-reach-a-private-endpoint).

**One Core per host.** Nodes share the `oac-node` account, so a host serves one Core; a command from a second Core is refused.

**Node software.** A node keeps the host program of the console that added it; an upgrade of Core doesn't replace it. A release that changes the node protocol lists the actions it requires; see [protocol changes](./operations.md#protocol-changes).

## Rerun, expiry and slow links

- Rerunning the same command is safe. Once the node is registered, a rerun uses the node's own credential, changes nothing that already matches and needs no token. It changes nothing and says why when the node's retained identity belongs to another Core address or installation, when the sandbox configuration has changed since the node registered (the node prepares it itself; see [Change the sandbox configuration](#change-the-sandbox-configuration)), or when Core was upgraded and serves a newer node program: choose **Remove node** on **Nodes**, uninstall the node on the host, then add it again with **Add node**.
- A run that fails before the node registers leaves nothing that Core knows. The next command, even after a configuration change or a Core upgrade, replaces the earlier attempt's configuration and any downloaded file that no longer matches, keeps the files that match and continues.
- A command that already expired, or was used on another host, fails at once with `The enrollment command expired or was already used`: generate a new command and run it within an hour.
- If the command expires during a slow download, registration fails with `The enrollment command expired or was already used`. The downloaded files are kept: generate a new command in Web and run it, and the download continues where it stopped.
- Every file shows its progress. Failed or interrupted downloads keep what arrived, and a rerun resumes each partial file where it stopped (`Resuming …`), reuses verified complete files and downloads the rest. If the server cannot resume, the file starts again from the beginning. A transfer that brings less than 64 KiB in a minute stops; check the network and rerun the command.
- The Docker Runtime image is about 400 MB and the microsandbox files about 500 MB. On a slow link with Docker, load the image first: copy the release's `oac-<commit>-linux-amd64-runtime.tar.gz` asset to the host and run `sudo docker load -i` on it. The installer then finds the exact image and skips the download.
- Interrupting the installer, or closing its terminal, stops it; run the command again to continue.

## Logs

The installer prints the node's log command (`Logs: …`) when it finishes. The Add node dialog shows it too when something needs attention: when the node reports a problem, or when it hasn't become connected and ready about a minute after registering.

Run `sudo journalctl -u oac-node-<installation-id>.service`. In a root shell, omit `sudo`. The installation ID is in the command (`--installation-id`) and under **Installation details** on the **Settings → Service and deployment** page.

## Change the sandbox configuration

Sandbox settings apply to the whole installation; nodes follow them.

- **Size, Runtime release, E2B key or template build.** Open **Settings** → **Sandbox and scheduling** → **Change resources**, edit and save. Existing sandboxes keep their configuration. Each node prepares the new one while it keeps serving the old one, and **Nodes** shows its progress: **Preparing target**, **Ready for target** or **Preparation failed** with the [reason](#readiness-codes). Core places new Sessions on nodes ready for the new configuration first, and on nodes still serving an older one when those have no room. E2B changes apply at once.
- **The installation's Runtime.** When the saved Runtime differs from the one the installation serves, **Settings** → **Sandbox and scheduling** shows a **Runtime** row with both source commits. **Use this installation's Runtime** opens **Change resources** on its review with that Runtime, and **Save configuration** starts the same node preparation. Other changes keep the saved Runtime. When the console serves no Runtime manifest, enter the release under **Advanced settings**.
- **Capacity and idle suspension.** On **Nodes**, use **Edit node** for a node's active and retained limits. In **Settings** → **Sandbox and scheduling**, use **Automatic suspension** → **Idle before suspension (seconds)** → **Save** for the deployment-wide [idle policy](../../contracts/agents-api/sandbox-deployment.md#suspension-policy).
- **Backend.** On the same page, choose **Reset deployment**. Auto reset archives idle hosted Sessions at once and lets running work finish until the deadline you set; force cancels it now. Offline nodes must come back so Core can confirm their cleanup. When the reset completes, Core has retired every node and unused command: set up the new backend, then add nodes again. **Cancel reset** stops the remaining work; archived Sessions stay archived.

The [reset contract](../../contracts/agents-api/sandbox-deployment.md#generation-ownership-and-rollout) describes what reset archives and keeps. To archive a single hosted Session, use the [Core API](../../contracts/agents-api/admin-api.md#session-archive).

## Checkpoint recovery across nodes

For microsandbox checkpoint recovery across nodes, add `--checkpoint-root /absolute/private/shared-store` to the installer command on each participating node. Mount the same private store at those paths before installation and grant only the node service account access. The default is a private node-local store and supports recovery on that node. The checkpoint store is separate from workspace storage and must never be guest-accessible; the [checkpoint transfer contract](../sandbox-provider.md#checkpoint-transfer) owns compatibility and retention requirements.

microsandbox includes the host kernel and CPU profile in its checkpoint execution compatibility. An OS or CPU-profile change can therefore make retained checkpoints incompatible even on their original node. Before maintenance, keep a node with the matching execution class and the same ready generation available for those checkpoints; otherwise their requests wait under the [checkpoint retention policy](../sandbox-provider.md#checkpoint-transfer).

## Remove a node

1. In Web, open **Nodes** and choose **Remove node** on the node's page, or **Remove** in its list row, then **Confirm removal**. Core refuses while the node still holds sandboxes, snapshots or pending cleanup; let them finish, or archive their Sessions. Removal is permanent: the host can come back only as a new node. Removing every node keeps the sandbox configuration.
2. Web then shows **Clean up the host** with the uninstall command. Run it on the host. **Clean up a host** on **Nodes** shows the same command at any time, for a host you removed earlier, after a deployment reset retired its node, or for a host that never registered.

Uninstall first asks Core, at the address the node enrolled with, whether the node was removed, and refuses while Core still lists it. When the node enrolled with an address other than the current public URL, the dialog also shows **Old Core address gone?**: if that address no longer responds, it gives the command with `--force`, which skips the check; remove the node on the Nodes page first. If Core is gone, so is Web: run `sudo python3 ~/.oac/node-bootstrap/<sha256>.pyz --uninstall --installation-id <installation-id> --force` on the host, with the installer the original command left in that directory. The installation ID is in the unit name `oac-node-<installation-id>.service` and in `/etc/oac-node/<installation-id>.json`; Web shows it under **Settings** → **Service and deployment** → **Installation details**. Uninstall stops and removes the service, the node state, the records, the proxy settings and the Docker network. It deletes the `oac-node` account only if the installer created it and no node remains; an adopted account only loses the groups the installer added. The output lists what it kept: an adopted account with its home `/var/lib/oac-node`, and the downloaded installers in `~/.oac/node-bootstrap`, which `rm -f ~/.oac/node-bootstrap/*.pyz` removes.

It never deletes Docker images or volumes. It keeps the Runtime image and prints the `sudo docker image rm` command. For microsandbox, once Core has confirmed the removal, it also deletes the node's store under `/var/lib/oac-node/.oac/m/` (about 1.5 GB), and with it the created account. With `--force` Core isn't asked, so the store is kept and the output prints the command that deletes it (`sudo -u oac-node rm -rf <store>`); microVMs may still use it, so check `pgrep -u oac-node` first, then rerun uninstall to finish removing the account. The output lists what was deleted and what was kept. Uninstall can be rerun until it completes.

## Register a node manually

Use manual registration when you manage the node's files and service yourself instead of running Web's command. A manually registered node serves only the configuration it registered with: after a size or Runtime change, **Nodes** shows it as **Node software incompatible**, and it keeps serving the old configuration until you remove it and register the host again.

1. Take `oac-node` from the same release as Core.
2. Get an enrollment token: the token in a command from **Add node**, or `POST /core/v1/sandbox/enrollment-tokens` with the Core key. It is single-use and carries the node's approved capacity; the response's `expires_at` says when it expires. Save it in a `0600` file on the host.
3. Read the node configuration with the token, which does not consume it: `GET /api/v1/sandbox-node/configuration` with `Authorization: Bearer <token>`.
4. Write a private provider file. Copy `provider`, `installation_id`, `core_url`, `generation` and `specification` from the response, and add a `native` object with the host settings of that provider. The adapter reads sandbox size, the Runtime image and artifact hashes from `specification`:
   - Docker: the [Docker node configuration](../configuration.md#docker-node-configuration) fields, with `host` an explicit Unix socket, `image` the local ID of the imported Runtime image and `seccomp_file` absolute.
   - microsandbox: absolute `helper_path`, `runtime_path` and `firmware_path`, a `network` policy, an explicit `checkpoint_root` for private checkpoint archives, and `runtime_home`: a private directory, which the helper creates with mode `0700` when it is missing. microsandbox places Unix sockets under it, so keep its path within 48 bytes; the installer refuses a longer one for its own nodes.
   Before the first microsandbox registration, initialize the empty `checkpoint_root` as the node service account using the same release's source checkout: `PYTHONPATH=deploy/node python3 -c 'from node_install import prepare_checkpoint_root; prepare_checkpoint_root("/absolute/private/checkpoints")'`. This reuses the installer's exclusive UUID-marker initialization and ownership checks. Initialize a shared store once; other nodes use the existing marker. Never replace the marker or initialize over restored or nonempty unowned storage.
5. Register, then run the node under the host's service supervisor, with real absolute paths:

   ```sh
   oac-node register \
     --config /var/lib/oac/provider.json \
     --state-dir /var/lib/oac/node \
     --core-url https://core.example \
     --name worker-1 \
     --enrollment-token-file /var/lib/oac/enrollment-token
   oac-node run \
     --config /var/lib/oac/provider.json \
     --state-dir /var/lib/oac/node
   ```

`oac-node run` exits with status 78 once Core rejects its credential, after the node is removed; configure the supervisor not to restart it then (systemd: `RestartPreventExitStatus=78`).

The node connects out to Core; Core needs no SSH or Docker TCP access to the host. Registration writes the node's identity to the state directory before contacting Core, so a lost response can be retried under the same identity. Keep the state directory on persistent storage, private to the node's account and used by one process at a time. Never copy it to another directory or host: Core refuses a second connection for a node while the first is open. The provider file can't change the node's capacity or sandbox configuration. Core compares its digest at registration and on every connection, and a node whose file differs takes no work until the approved configuration is restored.

## When a node host fails

A restarted node service keeps its identity and finds its existing sandboxes again. Core does not take over an active or unconfirmed writer merely because its node is unreachable or its sandbox is missing: those resources show as **Node disconnected** or **Sandbox resource missing** while ownership remains unresolved. Restart a crashed or hung host rather than removing its node: after the node reconnects, a running sandbox that the node confirms gone is released under the [confirmed compute loss](../sandbox-provider.md#confirmed-compute-loss) rules, and the Session's next input continues on new compute; any other missing sandbox keeps **Sandbox resource missing**. A host that never returns keeps its Sessions' compute ownership. A settled retained checkpoint can follow the [checkpoint transfer contract](../sandbox-provider.md#checkpoint-transfer), and confirmed release can permit [cold replacement](../sandbox-provider.md#cold-replacement-after-checkpoint-retention). A lost node state directory is a recovery incident: restore it from its [backup](./operations.md#back-up) together with the database and the provider storage, rather than registering the host again over existing resources.

## Troubleshooting

Connection failures and installer messages are in [Troubleshooting](./troubleshooting.md#nodes).

### Readiness codes

When a node is online but its sandbox provider is not ready, **Nodes** and **Overview** show it as **Provider not ready**. Where the reason appears depends on how the node was added:

- **Nodes added with Web's command** report a failed check of the current sandbox configuration as **Preparation failed** in the node's target status, with the reason in a help tip (`rollout.diagnostic` in `GET /core/v1/sandbox/nodes`). The tip beside **Provider not ready** only says *Sandbox provider unavailable*.
- **Manually registered nodes** show the reason in the help tip beside **Provider not ready** (`diagnostic`).

Each code is a Provider-neutral class; the local error behind it stays on the node. A manually registered node logs it. For a node added with Web's command, rerun the command on the host: the installer checks the host requirements first and names what to fix ([Installer messages](./troubleshooting.md#installer-messages)).

A node reports only its first failed check, in this order: the Docker daemon or KVM, Docker's limit support, host capacity, then the installed Runtime files. An unreachable Docker daemon therefore hides a missing image. The next heartbeat, about ten seconds after a fix, clears or replaces the code. An offline node keeps its last code, which Web hides until the node reconnects.

| Code | Help tip | Cause | Fix |
| --- | --- | --- | --- |
| `provider_unavailable` | Sandbox provider unavailable | The provider's service is unreachable or fails a request, such as a stopped Docker daemon, or a failure without a class | Rerun the node's command, or read a manually registered node's log |
| `host_unsupported` | Host unsupported | The host lacks a capability the provider requires, such as Docker CPU and memory limits (cgroup v2) or read-write access to `/dev/kvm` | Rerun the node's command, or read a manually registered node's log |
| `capacity_insufficient` | Host too small | The host has fewer CPUs or less memory than one sandbox | Use a larger host, or change the sandbox size |
| `runtime_image_unavailable` | Runtime image missing | The provider does not have the pinned Runtime image | A node added with Web's command downloads it again by itself; otherwise load the image from the matching release |
| `artifacts_unavailable` | Provider files missing | A pinned provider file, such as the microsandbox Runtime, firmware or helper, is missing or fails its SHA-256 check | A node added with Web's command downloads the missing files by itself; otherwise restore them from the matching release |
| `runtime_download_failed` | Runtime download failed | While preparing a new configuration, the node could not download or verify the Runtime files | Check the node's HTTPS access to the console and the release. The node retries with growing delays, up to 30 minutes apart |

A new group membership applies only to a new process. Restart the node service: `sudo systemctl restart oac-node-<installation-id>.service`.
