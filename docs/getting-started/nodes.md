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
  Web says so at Add node. See
  [HTTPS and the reverse proxy](install.md#https-and-the-reverse-proxy).
- **Nodes can download their execution files.** Web supplies the release metadata.
  With the default online installation, nodes follow HTTPS redirects to download
  their matching Runtime and provider files directly from the release, verifying
  sizes and SHA-256 checksums before use. Core and Web do not download these files.
  For nodes without release access, use the [offline bundle](install.md#installer-options)
  on the Web host so it serves the files locally. Verified local files are reused.
- **A sandbox backend is chosen.** The installer selects microsandbox unless you chose
  otherwise. After `--sandbox none`, the **Nodes** page first asks you to choose
  **Own machines**, then microsandbox (preselected as recommended) or Docker, which it
  asks you to confirm, and a sandbox size, then **Save configuration**. Every node of a
  deployment uses that provider.

## Add a node

1. In Web, open **Nodes** and select **Add node**.
2. Set **Sandboxes at once** (default 2): how many sandboxes Core may run on this node.
   With microsandbox, also set **Retained sandboxes** (default 8): how many it may keep,
   suspended ones included. With Docker, Core keeps retained equal to at once. You can
   change them later with **Edit node**.
3. Select **Generate command** and copy the command. It registers one node, once, and
   only if it runs within 10 minutes; Web counts down and offers
   **Generate new command** when it expires.
4. Run it on the host. Web follows the node from registered to connected to ready.

The command downloads the node installer from your console, checks its SHA-256, and
runs it with a one-time enrollment token. The installer downloads the node files from
the same console and checks each against the release manifest, imports the Runtime
image, registers the node, starts its service and waits until Core reports the node
connected and ready. It never installs software, and it stops with a one-line hint
before changing anything when a prerequisite is missing.

Node installation and removal require root. Web's command uses `sudo` unless the
shell is already root. The installer creates an `oac-node` service user and a
system service; the node runs as that service user, not as root. Direct execution
as an ordinary user is rejected before reading the enrollment token or changing
the host. This requirement applies to Sandbox Provider nodes, not the native
self-hosted daemon installer.

### The command

The command shows each installation phase as it runs. After Core confirms the
connection and the Sandbox Provider is ready, it prints a grouped summary with
status and log commands. Terminal colors are optional (`export NO_COLOR=1` disables them; the generated
command passes this preference through sudo with `--no-color`);
redirected output stays plain. Registration tokens are never printed.

This is the command Web generates, with this installation's values:

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
printf '\n==> Downloading node installer...\n' &&
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '==> Verifying node installer...\n' &&
printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\n' '<enrollment-token>' | $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --enrollment-token-stdin --source-url 'https://core.example' --core-url 'https://core.example' --provider 'docker' --installation-id '<installation-id>')
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
- SELinux not enforcing. Hosts with enforcing SELinux are unsupported by this installer.
- One Core per host: a host already running a sudo-mode node for another Core is
  refused.
- Docker: rootful Docker Engine running, its socket `/var/run/docker.sock` owned by the
  `docker` group with mode `0660`, enforcing CPU and memory limits (cgroup v2).
- microsandbox: `/dev/kvm` in the `kvm` group (hardware or nested virtualization), and
  the libraries microsandbox links (glibc).
- CPUs and memory for at least one sandbox of the deployment's size, and about 2 GB of
  disk for the Runtime image.
- HTTPS access to the console and Core at the public URL; sandboxes reach Core too.

## What the installer sets up

| Item | Detail |
| --- | --- |
| Service user | System user `oac-node`, home `/var/lib/oac-node`, no login shell. An existing account with that home and a nologin shell is adopted; any other account named `oac-node` is refused |
| Group | The group that owns `/var/run/docker.sock` (Docker) or `/dev/kvm` (microsandbox): `docker` or `kvm`, nothing else |
| Service | `/etc/systemd/system/oac-node-<installation-id>.service`, a root-owned system unit with `User=oac-node`, enabled at boot. It restarts every 5 seconds while Core is unreachable and stops for good once Core no longer accepts the node |
| Node state | `/var/lib/oac-node/.oac/nodes/<installation-id>/`: identity, configuration, node files. microsandbox keeps its images and sandboxes under `/var/lib/oac-node/.oac/m/` |
| Records | `/etc/oac-node/`: what the installer created or changed, used by reruns and uninstall |
| Docker | The Runtime image, imported once, and a network `oac-node-<installation-id>` |

Root only prepares the account, the group and the unit; everything else, the Docker
network included, runs as `oac-node`, in its own session without a terminal. The
installer never installs Docker, KVM or packages, never starts Docker, never changes
device permissions, sudoers, firewall or SELinux settings, and never touches other
accounts.

**Docker mode is root-equivalent.** Membership in the `docker` group lets
`oac-node`, and so anything that controls the node, act as root on the host. This
is inherent to running sandboxes on Docker. Add Docker
nodes only on hosts dedicated to sandboxes. microsandbox nodes need only the `kvm`
group.

**One Core per host.** Nodes share the `oac-node` account, so a host serves one
Core; a command from a second Core is refused.

**The token.** It is single-use, expires after 10 minutes and only registers the node.
The installer takes it only on standard input and refuses it in the environment,
where `sudo VAR=… python3` would record it in sudo's log. A sudoers policy with
`log_input` records standard input, and so the token.

## Rerun, expiry and slow links

- Rerunning the same command is safe. Once the node is registered, a rerun uses the
  node's own credential, changes nothing that already matches and needs no token.
- A command that already expired, or was used on another host, fails at once with
  `Core rejected the node configuration read (HTTP 401)`: generate a new command and
  run it within 10 minutes.
- If the command expires during a slow download, registration fails with
  `The enrollment command expired or was already used`. The downloaded files are kept:
  generate a new command in Web and run it.
- Downloads resume where they stopped. A download that brings less than 64 KiB in a
  minute stops, keeping what it has; run the command again.
- The Docker Runtime image is about 500 MB. On a slow link, load it first: copy the
  release's `oac-<commit>-linux-amd64-runtime.tar.gz` asset to the host and run
  `sudo docker load -i` on it. The installer then finds the exact image and skips the
  download.
- Interrupting the installer, or closing its terminal, stops it; run the command again
  to continue.

## Logs

The installer prints the node's log command (`Logs: …`) when it finishes. The Add node
dialog shows it too when something needs attention: when the node reports a problem, or
when it hasn't become connected and ready about a minute after registering.

Run `sudo journalctl -u oac-node-<installation-id>.service`. In a root shell, omit
`sudo`; the installer's summary already does this.

The installation ID is in the command (`--installation-id`) and on the **System** page.

## Remove a node

1. In Web, open **Nodes** and choose **Remove node** on the node's page, or
   **Remove** in its list row, then **Confirm removal**. Core refuses while the node
   still holds sandboxes, snapshots or pending cleanup; let them finish, or
   [archive their Sessions](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-reset)
   through the Core API. Removal is permanent: the host can come back only as a new
   node.
2. Web then shows **Clean up the host** with the uninstall command. Run it on the host:

   ```sh
    (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
   printf '\n==> Downloading node installer...\n' &&
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
   printf '==> Verifying node installer...\n' &&
   printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
   $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --uninstall --installation-id '<installation-id>')
   ```

Uninstall first asks Core, at the address the node enrolled with, whether the node was
removed, and refuses while Core still lists it. When the node enrolled with an address
other than the current public URL, the dialog also shows **Old Core address gone?**:
if that address no longer responds, it gives the command with `--force`, which skips
the check; remove the node on the Nodes page first. Without the dialog, take the
installer's SHA-256 from the `node-install.pyz` line of
`https://core.example/node-install/SHA256SUMS`. Uninstall
stops and removes the service, the node state, the records and the Docker network. It
deletes the `oac-node` account only if the installer created it and no node remains;
an adopted account only loses the groups the installer added.

It never deletes sandboxes, volumes or images. It keeps the Runtime image and prints
the `docker image rm` command. For microsandbox it keeps the store under
`/var/lib/oac-node/.oac/m/`, prints how to delete it
(`sudo -u oac-node rm -rf <store>`), and keeps a created account until the store is
gone; rerun uninstall afterwards. With `--force`, microVMs may still use the store, so
check `pgrep -u oac-node` first. Uninstall can be rerun until it completes.

## Troubleshooting

### Readiness codes

When a node is online but its provider is not ready, **Nodes** shows its status as
**Provider unavailable**, with the reason in a help tip; **Overview** counts it as a
**Provider issue**. The API reports the reason as a fixed code, the `diagnostic` field
of `GET /core/v1/sandbox/nodes`. A node reports only its first failed check, and the
next heartbeat, about ten seconds after a fix, clears or replaces it. The node's log has
the local error behind the code.

| Code | Web shows | Cause | Fix |
| --- | --- | --- | --- |
| `docker_unavailable` | Docker unavailable | The Docker socket is unreachable or not accessible, or Docker fails its info or image request | Start Docker and give the node's user access to `/var/run/docker.sock` |
| `docker_limits_unsupported` | Docker limits unsupported | Docker reports no CPU quota or memory limit support | Use a host whose cgroups enforce CPU and memory limits (cgroup v2) |
| `runtime_image_unavailable` | Runtime image missing | Docker does not have the pinned Runtime image | Rerun the add command, or load the image from the matching release |
| `kvm_unavailable` | KVM unavailable | The node can't open `/dev/kvm` for reading and writing | Enable hardware virtualization and give the node's user KVM access, through the `kvm` group |
| `microsandbox_artifacts_unavailable` | microsandbox components missing | The Runtime or firmware is missing or fails its SHA-256 check, or the helper is missing | Rerun the add command |
| `capacity_insufficient` | Host too small | The host has fewer CPUs or less memory than one sandbox | Use a larger host, or change the sandbox size |
| `provider_unavailable` | Sandbox provider unavailable | Any other failure, and every failure an older node reports | Read the node's log |

A new group membership applies only to a new process. Restart the node service:
`sudo systemctl restart oac-node-<installation-id>.service`. A node that is registered
but never connects usually can't reach Core at the public URL, or its `/api/v1`
WebSocket doesn't pass the reverse proxy.

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
| This host already runs a sudo-mode node for another Core | One host serves one Core in sudo mode. Remove that node and uninstall it first |
| Node installation and removal require root | Run Web's command with sudo, or from a root shell |
| Core still lists this node | Remove it on the Nodes page first |

## Change the sandbox backend or size

Change the backend or sandbox size in Web under **System** → **Sandbox backend**.
What each change does, and when it needs a reset, is in
[Sandbox deployment](../configuration.md#sandbox-deployment). This section covers
only what happens on the nodes.

Node program version updates are not supported. `--update` refuses without changing
node state. Preserve an older installation and its Runtime data; provision a fresh
node separately through **Add node**. Current Runtime generation operations below
are independent of program version upgrades.

Fresh v2 preparation keeps a private recovery plan until the actual provider image
identity is resolved and its final configuration is published. Restart does not
serve a pending or partly collected generation. Generation lock identity is durable:
if a lease or its identity record is missing or replaced, preserve the installation
for inspection. Do not delete records, recreate lock files or re-enroll to bypass
that refusal; a process restart does not prove that older helpers have stopped.

Automatic Docker generation GC removes only installation-owned generation/release
files. Shared daemon images remain; a host administrator may remove them only after
confirming no installation still needs them. Microsandbox image cleanup remains
scoped to the installation's private native store and verified ownership receipts.

The [operator reference](../../services/agents-api/HOSTED-SANDBOX-MANAGER.md#removal-and-reset)
describes reset and API behavior. Same-team E2B edits have a separate online Core
API path and do not involve node installation.


## Installation scope

Nodes and Core must come from the same distribution. Provision nodes through
**Add node**; the installer never adopts or removes another installation's
resources. See the [installation version policy](operations.md#installation-version-policy).
