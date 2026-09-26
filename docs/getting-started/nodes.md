# Add and manage nodes

A node is a Linux machine that runs sandboxes for Core-hosted Sessions when the
sandbox backend is Docker or microsandbox. Core places each new Session on a node with
free capacity; the node creates the sandbox, and the sandbox connects back to Core.
With E2B you need no nodes. Self-hosted executors, which applications run for their
own Sessions, are a different thing; see [Self-hosted executors](self-hosted.md).

You add a node by generating a command in Web and pasting it on the host. The
[operator reference](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md) covers the
node protocol, manual registration, placement and failure handling.

## Before you add a node

- **Core has an HTTPS public URL** that the host and its sandboxes can reach: each
  sandbox calls Core at `public_url`. A loopback installation can't have nodes, and
  Web says so at Add node. See [HTTPS and the reverse proxy](install.md#https-and-the-reverse-proxy).
- **Web holds the node files.** Install from the offline bundle, or add the release's
  node files to the smaller bundle; see [Download a release](install.md#download-a-release).
  Otherwise Add node says that the console has no node files for the provider.
- **A sandbox backend is chosen.** The installer selects Docker unless you chose
  otherwise. After `--sandbox none`, the **Nodes** page first asks you to choose
  **Own machines**, Docker or microsandbox, and a sandbox size, then
  **Save configuration**. Every node of a deployment uses that provider.

## Add a node

1. In Web, open **Nodes** and select **Add node**.
2. Set **Sandboxes at once** (default 2) and **Retained sandboxes** (default 8): how
   many sandboxes Core may run and keep on this node. You can change them later with
   **Edit node**.
3. Select **Generate command** and copy the command. It works once, within 10 minutes;
   Web counts down and offers **Generate new command** when it expires.
4. Run it on the host. Web follows the node from registered to connected to ready.

The command downloads the node installer from your console, checks its SHA-256, and
runs it with a one-time enrollment token. The installer downloads the node files from
the same console and checks each against the release manifest, imports the Runtime
image, registers the node, starts its service and waits until Core reports the node
connected and ready. It never installs software, and it stops with a one-line hint
before changing anything when a prerequisite is missing.

Web's command runs the installer with sudo: it prepares the host itself, creating a
`parsar-node` service user and a system service. On a host where you have no sudo, the
dialog's **No sudo on this host?** section gives the same command without sudo; the
node then runs as a user service of a user that an administrator prepared.

### The command

This is the command Web generates, with this installation's values:

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\n' '<enrollment-token>' | $s python3 "$d/node-install.pyz" --enrollment-token-stdin --source-url 'https://core.example' --core-url 'https://core.example' --provider 'docker' --installation-id '<installation-id>')
```

- It downloads the installer into a private temporary directory, checks its SHA-256,
  and runs it with `sudo`, or directly in a root shell.
- The token reaches the installer on standard input, so it never appears in a process
  argument, an environment variable or sudo's log.
- The leading space keeps the command out of the shell history where `HISTCONTROL`
  ignores such lines (the Debian and Ubuntu default).
- If the command is interrupted or the download stalls, run the same command again:
  the download resumes.

The host needs:

- Linux amd64 with systemd; Python 3.9+, `curl` and `sha256sum`; root or sudo.
- SELinux not enforcing. Sudo mode refuses an enforcing host; use the no-sudo command
  with a prepared user there.
- One Core per host: a host already running a sudo-mode node for another Core is
  refused.
- Docker: rootful Docker Engine running, its socket `/var/run/docker.sock` owned by the
  `docker` group with mode `0660`, enforcing CPU and memory limits (cgroup v2).
- microsandbox: `/dev/kvm` in the `kvm` group (hardware or nested virtualization), and
  the libraries microsandbox links (glibc).
- CPUs and memory for at least one sandbox of the deployment's size, and about 2 GB of
  disk for the Runtime image.
- HTTPS access to the console and Core at the public URL; sandboxes reach Core too.

### Without sudo

The no-sudo command is the same without `sudo`:

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\n' '<enrollment-token>' | python3 "$d/node-install.pyz" --enrollment-token-stdin --source-url 'https://core.example' --core-url 'https://core.example' --provider 'docker' --installation-id '<installation-id>')
```

Run it as the non-root user that will run the node; run by root, it installs the
sudo-mode service instead. An administrator prepares that user once:

- Docker: `sudo usermod -aG docker <user>`, with Docker enforcing CPU and memory limits.
  microsandbox: `sudo usermod -aG kvm <user>` for read and write access to `/dev/kvm`.
- Lingering, after the group change: `sudo loginctl enable-linger <user>`. After a group
  change, sign in again as that user; if the user's systemd manager was already
  running, restart it (`sudo systemctl restart user@$(id -u <user>).service`) or reboot.
- microsandbox only: a home directory of at most 25 bytes, such as `/home/parsar`,
  because microsandbox's socket paths are short.
- The host requirements above, except root, SELinux and one Core per host.

Run the command as that user over SSH, or from a root shell with `su - <user>`. The
node's state then lives in `~/.parsar/nodes/<installation-id>/` of that user. The
Docker group makes this user root-equivalent on the host too.

## What sudo mode sets up

| Item | Detail |
| --- | --- |
| Service user | System user `parsar-node`, home `/var/lib/parsar-node`, no login shell. An existing account with that home and a nologin shell is adopted; any other account named `parsar-node` is refused |
| Group | The group that owns `/var/run/docker.sock` (Docker) or `/dev/kvm` (microsandbox): `docker` or `kvm`, nothing else |
| Service | `/etc/systemd/system/parsar-node-<installation-id>.service`, a root-owned system unit with `User=parsar-node`, enabled at boot. It restarts every 5 seconds while Core is unreachable and stops for good once Core no longer accepts the node |
| Node state | `/var/lib/parsar-node/.parsar/nodes/<installation-id>/`: identity, configuration, node files. microsandbox keeps its images and sandboxes under `/var/lib/parsar-node/.parsar/m/` |
| Records | `/etc/parsar-node/`: what the installer created or changed, used by reruns and uninstall |
| Docker | The Runtime image, imported once, and a network `parsar-node-<installation-id>` |

Root only prepares the account, group, unit and network; everything else runs as
`parsar-node`, in its own session without a terminal. The installer never installs
Docker, KVM or packages, never starts Docker, never changes device permissions,
sudoers, firewall or SELinux settings, and never touches other accounts.

**Docker mode is root-equivalent.** Membership in the `docker` group lets
`parsar-node`, and so anything that controls the node, act as root on the host. This
is inherent to running sandboxes on Docker and equally true without sudo. Add Docker
nodes only on hosts dedicated to sandboxes. microsandbox nodes need only the `kvm`
group.

**One Core per host.** All sudo-mode nodes share the `parsar-node` account, so a host
serves one Core in sudo mode; a command from a second Core is refused. A host also
can't run the same installation's node both with and without sudo.

**The token.** It is single-use, expires after 10 minutes and only registers the node.
A sudoers policy with `log_input` records standard input, and so the token.

## Rerun, expiry and slow links

- Rerunning the same command is safe. Once the node is registered, a rerun uses the
  node's own credential, changes nothing that already matches and needs no token.
- If the command expires during a slow download, the downloaded files are kept:
  generate a new command in Web and run it.
- Downloads resume where they stopped. A download that brings less than 64 KiB in a
  minute stops, keeping what it has; run the command again.
- The Docker Runtime image is about 500 MB. On a slow link, load it first: copy the
  release's `parsar-core-<commit>-linux-amd64-runtime.tar.gz` asset to the host and run
  `sudo docker load -i` on it. The installer then finds the exact image and skips the
  download.
- Interrupting the installer, or closing its terminal, stops it; run the command again
  to continue.

## Logs

After you run the command, the Add node dialog shows the node's log command:

| Node installed | Command |
| --- | --- |
| With sudo, or by root | `sudo journalctl -u parsar-node-<installation-id>.service` |
| Without sudo, as the node's user | `journalctl --user -u parsar-node-<installation-id>.service` |

The installation ID is in the command (`--installation-id`) and on the **System** page.

## Remove a node

1. In Web, open **Nodes**, select the node and choose **Remove node**. Core refuses
   while the node still holds sandboxes, snapshots or pending cleanup; let them finish,
   or [archive their Sessions](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance)
   through the Core API. Removal is permanent: the host can come back only as a new node.
2. Web then shows **Clean up the host** with the uninstall command. Run it on the host:

   ```sh
    (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
   printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
   $s python3 "$d/node-install.pyz" --uninstall --installation-id '<installation-id>')
   ```

   For a node installed without sudo, **Installed without sudo?** gives the same command
   without `sudo`; run it as that user.

Uninstall first asks Core, at the address the node enrolled with, whether the node was
removed, and refuses while Core still lists it. If that address no longer responds, for
example after the public URL changed, **Old Core address gone?** gives the command with
`--force`, which skips the check; remove the node on the Nodes page first. Uninstall
stops and removes the service, the node state, the records and the Docker network. It
deletes the `parsar-node` account only if the installer created it and no node remains;
an adopted account only loses the groups the installer added.

It never deletes sandboxes, volumes or images. It keeps the Runtime image and prints
the `docker image rm` command. For microsandbox it keeps the store under
`/var/lib/parsar-node/.parsar/m/`, prints how to delete it
(`sudo -u parsar-node rm -rf <store>`), and keeps a created account until the store is
gone; rerun uninstall afterwards. With `--force`, microVMs may still use the store, so
check `pgrep -u parsar-node` first. Uninstall can be rerun until it completes.

## Troubleshooting

### Readiness codes

When a node is online but its provider is not ready, **Nodes** shows one of these codes
(the `diagnostic` field of `GET /core/v1/sandbox/nodes`). A node reports only its first
failed check, and the next heartbeat, about ten seconds after a fix, clears or replaces
the code. The node's log has the local error behind the code.

| Code | Cause | Fix |
| --- | --- | --- |
| `docker_unavailable` | The Docker socket is unreachable or not accessible, or Docker fails its info or image request | Start Docker and give the node's user access to `/var/run/docker.sock` |
| `docker_limits_unsupported` | Docker reports no CPU quota or memory limit support | Use a host whose cgroups enforce CPU and memory limits (cgroup v2) |
| `runtime_image_unavailable` | Docker does not have the pinned Runtime image | Rerun the add command, or load the image from the matching release |
| `kvm_unavailable` | The node can't open `/dev/kvm` for reading and writing | Enable hardware virtualization and give the node's user KVM access, through the `kvm` group |
| `microsandbox_artifacts_unavailable` | The Runtime or firmware is missing or fails its SHA-256 check, or the helper is missing | Rerun the add command |
| `capacity_insufficient` | The host has fewer CPUs or less memory than one sandbox | Use a larger host, or change the sandbox size |
| `provider_unavailable` | Any other failure, and every failure an older node reports | Read the node's log |

A new group membership applies only to a new process: restart the service with
`sudo systemctl restart parsar-node-<installation-id>.service`, or
`systemctl --user restart parsar-node-<installation-id>.service` without sudo. A node
that is registered but never connects usually can't reach Core at the public URL, or
its `/api/v1` WebSocket doesn't pass the reverse proxy.

### Installer messages

| Message | Fix |
| --- | --- |
| The enrollment command expired or was already used | Generate a new command in Web and run it |
| Core's public URL changed after this command was generated | Generate a new command in Web and run it |
| This host's node uses `<address>`, but this command uses `<address>` | The node was added under an older public URL. Remove it in Web, uninstall it, then add it again |
| Docker Engine is not installed, or Docker is not running | Install Docker Engine, or `sudo systemctl enable --now docker`, then rerun |
| Docker on this host does not enforce CPU and memory limits | Use cgroup v2, then rerun |
| KVM is unavailable, or `/dev/kvm` must be group-accessible | Enable virtualization; your distribution's KVM package sets `root:kvm 0660` |
| This host has N CPUs and M MiB of memory; each sandbox needs … | Use a larger host, or change the sandbox size |
| SELinux is enforcing on this host | Use the no-sudo command as a prepared user |
| This host already runs a sudo-mode node for another Core | One host serves one Core in sudo mode. Remove that node and uninstall it first |
| Ask the host administrator to enable user lingering, or No systemd user manager is running | No-sudo mode: `sudo loginctl enable-linger <user>`, or use sudo |
| Core still lists this node | Remove it on the Nodes page first |

## Change the sandbox backend or size

Changing Docker, microsandbox or E2B, the sandbox size or the Runtime release applies to
the whole deployment. On **Nodes**, choose **Enter maintenance to change provider**, which
pauses new hosted sandboxes; let the existing ones finish, or archive their Sessions
through the Core API, until nothing is retained; save the new configuration; then choose
**Resume hosted placement**. A change retires every node: remove and uninstall them, and
add the hosts again with new commands. See
[maintenance](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-maintenance)
for the exact steps and API.
