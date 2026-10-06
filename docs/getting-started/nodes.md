---
title: "Add and manage nodes"
---

A node is a Linux host that runs sandboxes for Core-hosted Sessions when the sandbox backend is Docker or microsandbox. Core places each new Session on a node with free capacity; the node creates the sandbox, and the sandbox connects back to Core. E2B needs no nodes. Machines that applications connect for their own Sessions are [self-hosted executors](./self-hosted.md), not nodes.

You add a node by generating a command in Web and running it on the host. The [sandbox deployment contract](../../contracts/agents-api/sandbox-deployment.md) defines the sandbox settings and their change rules, and the [node protocol](../../contracts/agents-api/node-generation-protocol.md) defines how nodes prepare and keep Runtime generations.

## Before you add a node

- **Core has a public URL** that the host and its sandboxes can reach. Nodes download from Core's console and connect to Core at `public_url`. Until it is set, Add node says *Set a public address other machines can reach before adding nodes*; see [Configure the public address](./install.md#configure-the-domain-and-https).
- **The sandbox configuration is saved.** Open **System** → **Manage sandbox configuration**, choose **Own machines**, the backend and a sandbox size, and **Save configuration**. To change a saved configuration, choose **Reset deployment** first. Every node of an installation uses that backend.
- **The console can serve the node files.** Nodes download their Runtime and provider files from the console, which redirects to the release for files it does not hold, and check each file's size and SHA-256 against the release manifest. Node hosts therefore need access to the release. Without the files, Add node says *This console has no node files for …*.

The Core host joins like any other host: to run sandboxes on it, add it as a node.

## Add a node

1. In Web, open **Nodes** and choose **Add node**.
2. Set **Sandboxes at once** and, with microsandbox, **Retained sandboxes**: the node's [capacity](../configuration.md#node-capacity). You can change them later with **Edit node**.
3. Choose **Generate command** and copy the command. It registers one node, once, and only if it runs within 10 minutes; Web counts down and offers **Generate new command** when it expires.
4. Run it on the host. Web follows the node from registered to connected to ready.

The command downloads the node installer from your console, checks its SHA-256 and runs it with a one-time enrollment token. The installer downloads the node files and checks each against the release manifest, imports the Runtime image, registers the node, starts its service and waits until Core reports the node connected and ready. It never installs software, and it stops with a one-line hint before changing anything when a prerequisite is missing.

Node installation and removal require root. Web's command uses `sudo` unless the shell is already root. The installer creates an `oac-node` service user and a system service; the node runs as that user, not as root. Running the installer as an ordinary user fails before it reads the enrollment token or changes the host.

### The command

This is the command Web generates, with this installation's values:

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
printf '\n==> Downloading node installer...\n' &&
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '==> Verifying node installer...\n' &&
printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\n' '<enrollment-token>' | $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --enrollment-token-stdin --source-url 'https://core.example' --core-url 'https://core.example' --provider 'docker' --installation-id '<installation-id>')
```

- It downloads the installer into a private temporary directory, checks its SHA-256, and runs it with `sudo`, or directly in a root shell.
- The token reaches the installer on standard input, so it never appears in a process argument, an environment variable or sudo's log.
- The leading space keeps the command out of the shell history where `HISTCONTROL` ignores such lines (the Debian and Ubuntu default).

The installer shows each phase as it runs and, once Core confirms the node, a summary with status and log commands. `export NO_COLOR=1` disables colors; the command passes this through sudo as `--no-color`. It never prints tokens.

### Host requirements

- Linux amd64 with systemd; Python 3.9+, `curl` and `sha256sum`; root or sudo.
- SELinux not enforcing. The installer does not support hosts with enforcing SELinux.
- Docker: rootful Docker Engine running, its socket `/var/run/docker.sock` owned by the `docker` group with mode `0660`, enforcing CPU and memory limits (cgroup v2).
- microsandbox: `/dev/kvm` in the `kvm` group (hardware or nested virtualization), and the libraries microsandbox links (glibc).
- CPUs and memory for at least one sandbox of the installation's size, and about 2 GB of disk for the Runtime image.
- Access to the console and Core at the public URL; sandboxes reach Core too.

### Download through a proxy

Export `http_proxy`, `https_proxy` and `no_proxy` before running the generated command. Uppercase `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` work too; lowercase takes precedence when both are set, even when empty. Use an HTTP proxy URL, optionally with URL-encoded credentials, and comma-separated host exclusions in `no_proxy`. A proxy for HTTPS downloads must support CONNECT.

The command asks sudo to preserve only these six names, and the installer keeps them when it drops to `oac-node`. If sudo policy refuses, ask the host administrator to permit those names, or run from a root shell with the variables exported there. An outer `sudo` you add yourself can discard them. Don't put credentials in command arguments or share shell traces.

The proxy applies only to installation downloads. It is not saved in the node's configuration or service, so the running node needs its own network access to Core.

## What the installer sets up

| Item | Detail |
| --- | --- |
| Service user | System user `oac-node`, home `/var/lib/oac-node`, no login shell. An existing account with that home and a nologin shell is adopted; any other account named `oac-node` is refused |
| Group | The group that owns `/var/run/docker.sock` (Docker) or `/dev/kvm` (microsandbox): `docker` or `kvm`, nothing else |
| Service | `/etc/systemd/system/oac-node-<installation-id>.service`, a root-owned system unit with `User=oac-node`, enabled at boot. It restarts every 5 seconds while Core is unreachable and stops for good once Core no longer accepts the node |
| Node state | `/var/lib/oac-node/.oac/nodes/<installation-id>/`: identity, configuration, node files. microsandbox keeps its images and sandboxes under `/var/lib/oac-node/.oac/m/` |
| Records | `/etc/oac-node/`: what the installer created or changed, used by reruns and uninstall |
| Docker | The Runtime image, imported once, and a network `oac-node-<installation-id>` |
| microsandbox network policy | Sandboxes may reach Core, DNS on the host and public addresses, and nothing else: no inbound connections and no private networks. A private model or MCP endpoint needs a policy change on the node |

Root only prepares the account, the group and the unit; everything else, the Docker network included, runs as `oac-node`, in its own session without a terminal. The installer never installs Docker, KVM or packages, never starts Docker, never changes device permissions, sudoers, firewall or SELinux settings, and never touches other accounts.

**Docker mode is root-equivalent.** Membership in the `docker` group lets `oac-node`, and so anything that controls the node, act as root on the host. Containers also share the host kernel, so a container escape reaches the host. Add Docker nodes only on hosts dedicated to sandboxes. microsandbox nodes need only the `kvm` group and run each sandbox in its own microVM.

**One Core per host.** Nodes share the `oac-node` account, so a host serves one Core; a command from a second Core is refused.

**One release.** A node runs the program of the console that added it and is never upgraded in place; see the [installation version policy](./operations.md#installation-version-policy).

**The token.** It is single-use, expires after 10 minutes and only registers the node. The installer takes it only on standard input and refuses it in the environment, where `sudo VAR=… python3` would record it in sudo's log. A sudoers policy with `log_input` records standard input, and so the token.

## Rerun, expiry and slow links

- Rerunning the same command is safe. Once the node is registered, a rerun uses the node's own credential, changes nothing that already matches and needs no token. It refuses, without changing anything, when the node's retained identity belongs to another Core address or installation.
- A command that already expired, or was used on another host, fails at once with `Core rejected the node configuration read (HTTP 401)`: generate a new command and run it within 10 minutes.
- If the command expires during a slow download, registration fails with `The enrollment command expired or was already used`. The downloaded files are kept: generate a new command in Web and run it.
- Downloads resume where they stopped. A download that brings less than 64 KiB in a minute stops, keeping what it has; run the command again.
- The Docker Runtime image is about 500 MB. On a slow link, load it first: copy the release's `oac-<commit>-linux-amd64-runtime.tar.gz` asset to the host and run `sudo docker load -i` on it. The installer then finds the exact image and skips the download.
- Interrupting the installer, or closing its terminal, stops it; run the command again to continue.

## Logs

The installer prints the node's log command (`Logs: …`) when it finishes. The Add node dialog shows it too when something needs attention: when the node reports a problem, or when it hasn't become connected and ready about a minute after registering.

Run `sudo journalctl -u oac-node-<installation-id>.service`. In a root shell, omit `sudo`. The installation ID is in the command (`--installation-id`) and on the **System** page.

## Change the sandbox configuration

Sandbox settings apply to the whole installation; nodes follow them.

- **Size, Runtime release, E2B key or template build.** Open **System** → **Manage sandbox configuration** → **Change resources**, edit and save. Existing sandboxes keep their configuration. Each node prepares the new one while it keeps serving the old one, and **Nodes** shows its progress: **Preparing target**, **Ready for target** or **Preparation failed** with the [reason](#readiness-codes). Core places new Sessions on nodes ready for the new configuration first, and on nodes still serving an older one when those have no room. E2B changes apply at once.
- **Backend.** On the same page, choose **Reset deployment**. Auto reset archives idle hosted Sessions at once and lets running work finish until the deadline you set; force cancels it now. Offline nodes must come back so Core can confirm their cleanup. When the reset completes, Core has retired every node and unused command: set up the new backend, then add nodes again. **Cancel reset** stops the remaining work; archived Sessions stay archived.

The [reset contract](../../contracts/agents-api/sandbox-deployment.md#generation-ownership-and-rollout) describes what reset archives and keeps. To archive a single hosted Session, use the [Core API](../../contracts/agents-api/admin-api.md#session-archive).

## Remove a node

1. In Web, open **Nodes** and choose **Remove node** on the node's page, or **Remove** in its list row, then **Confirm removal**. Core refuses while the node still holds sandboxes, snapshots or pending cleanup; let them finish, or archive their Sessions. Removal is permanent: the host can come back only as a new node. Removing every node keeps the sandbox configuration.
2. Web then shows **Clean up the host** with the uninstall command. Run it on the host:

   ```sh
    (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
   printf '\n==> Downloading node installer...\n' &&
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
   printf '==> Verifying node installer...\n' &&
   printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
   $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --uninstall --installation-id '<installation-id>')
   ```

Uninstall first asks Core, at the address the node enrolled with, whether the node was removed, and refuses while Core still lists it. When the node enrolled with an address other than the current public URL, the dialog also shows **Old Core address gone?**: if that address no longer responds, it gives the command with `--force`, which skips the check; remove the node on the Nodes page first. Without the dialog, take the installer's SHA-256 from the `node-install.pyz` line of `https://core.example/node-install/SHA256SUMS`. Uninstall stops and removes the service, the node state, the records and the Docker network. It deletes the `oac-node` account only if the installer created it and no node remains; an adopted account only loses the groups the installer added.

It never deletes sandboxes, volumes or images. It keeps the Runtime image and prints the `docker image rm` command. For microsandbox it keeps the store under `/var/lib/oac-node/.oac/m/`, prints how to delete it (`sudo -u oac-node rm -rf <store>`), and keeps a created account until the store is gone; rerun uninstall afterwards. With `--force`, microVMs may still use the store, so check `pgrep -u oac-node` first. Uninstall can be rerun until it completes.

## Register a node manually

Use manual registration when you manage the node's files and service yourself instead of running Web's command. A manually registered node serves only the configuration it registered with: after a size or Runtime change, **Nodes** shows it as **Node software incompatible**, and it keeps serving the old configuration until you remove it and register the host again.

1. Take `oac-node` from the same release as Core.
2. Get an enrollment token: the token in a command from **Add node**, or `POST /core/v1/sandbox/enrollment-tokens` with the Core key. It is single-use and carries the node's approved capacity; the response's `expires_at` says when it expires. Save it in a `0600` file on the host.
3. Read the node configuration with the token, which does not consume it: `GET /api/v1/sandbox-node/configuration` with `Authorization: Bearer <token>`.
4. Write a private provider file. Copy `provider`, `installation_id`, `core_url`, `generation` and `specification` from the response, and add one adapter object for the host:
   - `docker`: `host` (an explicit Unix socket), `image` (the locally imported Runtime image of the approved release), `network`, `extra_hosts`, an absolute `seccomp_file` and `nested_sandbox`.
   - `microsandbox`: absolute `helper_path`, `runtime_path` and `firmware_path` with their `runtime_sha256` and `firmware_sha256`, `image`, the sandbox `cpus`, `memory_mib`, `root_disk_mib` and `environment_disk_mib`, a `network` policy, and `runtime_home`: a private directory, which the helper creates with mode `0700` when it is missing. microsandbox places Unix sockets under it, so keep its path within 48 bytes; the installer refuses a longer one for its own nodes.
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

A restarted node service keeps its identity and finds its existing sandboxes again. Core never replaces a missing sandbox by itself, and never moves a Session to another node: the Session's resources show as **Node disconnected** or **Sandbox resource missing** until the original host and its storage are back, or you archive the Session. A lost node state directory is a recovery incident: restore it from its [backup](./operations.md#back-up) together with the database and the provider storage, rather than registering the host again over existing resources.

## Troubleshooting

### Readiness codes

When a node is online but its sandbox provider is not ready, **Nodes** and **Overview** show it as **Provider not ready**. Where the reason appears depends on how the node was added:

- **Nodes added with Web's command** report a failed check of the current sandbox configuration as **Preparation failed** in the node's target status, with the reason in a help tip (`rollout.diagnostic` in `GET /core/v1/sandbox/nodes`). The tip beside **Provider not ready** only says *Sandbox provider unavailable*.
- **Manually registered nodes** show the reason in the help tip beside **Provider not ready** (`diagnostic`).

The node's log has the local error behind the code.

A node reports only its first failed check, in this order: the Docker daemon or KVM, Docker's limit support, host capacity, then the installed Runtime files. An unreachable Docker daemon therefore hides a missing image. The next heartbeat, about ten seconds after a fix, clears or replaces the code. An offline node keeps its last code, which Web hides until the node reconnects.

| Code | Help tip | Cause | Fix |
| --- | --- | --- | --- |
| `docker_unavailable` | Docker unavailable | The Docker socket is unreachable or not accessible, or Docker fails its info or image request | Start Docker and give the node's user access to `/var/run/docker.sock` |
| `docker_limits_unsupported` | Docker limits unsupported | Docker reports no CPU quota or memory limit support | Use a host whose cgroups enforce CPU and memory limits (cgroup v2) |
| `capacity_insufficient` | Host too small | The host has fewer CPUs or less memory than one sandbox | Use a larger host, or change the sandbox size |
| `runtime_image_unavailable` | Runtime image missing | Docker does not have the pinned Runtime image | A node added with Web's command downloads it again by itself; otherwise load the image from the matching release |
| `kvm_unavailable` | KVM unavailable | The node can't open `/dev/kvm` for reading and writing | Enable hardware virtualization and give the node's user KVM access, through the `kvm` group |
| `microsandbox_artifacts_unavailable` | microsandbox components missing | The Runtime or firmware is missing or fails its SHA-256 check, or the helper is missing | A node added with Web's command downloads the missing files by itself; otherwise restore them from the matching release |
| `runtime_download_failed` | Runtime download failed | While preparing a new configuration, the node could not download or verify the Runtime files | Check the node's HTTPS access to the console and the release. The node retries with growing delays, up to 30 minutes apart |
| `provider_unavailable` | Sandbox provider unavailable | Any other failure | Read the node's log |

A new group membership applies only to a new process. Restart the node service: `sudo systemctl restart oac-node-<installation-id>.service`. A node that is registered but never connects usually can't reach Core at the public URL, or its `/api/v1` WebSocket doesn't pass the reverse proxy.

### Installer messages

| Message | Fix |
| --- | --- |
| `Core rejected the node configuration read (HTTP 401)` before anything downloads, or `The enrollment command expired or was already used` | Generate a new command in Web and run it within 10 minutes |
| Core's public URL changed after this command was generated | Generate a new command in Web and run it |
| This host's node uses `<address>`, but this command uses `<address>` | The node was added under an older public URL. Remove it in Web, uninstall it, then add it again |
| Docker Engine is not installed, or Docker is not running | Install Docker Engine, or `sudo systemctl enable --now docker`, then rerun |
| Docker on this host does not enforce CPU and memory limits | Use cgroup v2, then rerun |
| KVM is unavailable, or `/dev/kvm` must be group-accessible | Enable virtualization; your distribution's KVM package sets `root:kvm 0660` |
| This host has N CPUs and M MiB of memory; each sandbox needs … | Use a larger host, or change the sandbox size |
| SELinux is enforcing on this host | Use a host supported by the installer; it does not change SELinux settings |
| This host already runs a sudo-mode node for another Core | Remove that node and uninstall it first |
| Node installation and removal require root | Run Web's command with sudo, or from a root shell |
| Core still lists this node | Remove it on the Nodes page first |
| Core no longer accepts this node | It was removed, or a reset retired it. Uninstall it, then add the host with a new command |
