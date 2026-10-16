---
title: "Troubleshooting"
---

Find the symptom, its cause and the fix for installation, access, nodes and Sessions. This page is for operators of a Core installation.

For a failing request or Turn, start with Web's **Monitor → Request log** and **Session log**; see [Request diagnosis](./operations.md#request-diagnosis) and [Service health](./operations.md#service-health).

## Installation

| Symptom | Cause and fix |
| --- | --- |
| `Web port unavailable; choose --web-port` | Another program holds that port. Stop it, or choose another `--web-port`. The installer does not move to a different port. When the message names a Compose project `oac-…`, an earlier installation whose directory is gone still runs: remove it as in [Uninstall](./operations.md#uninstall) |
| `incomplete installation: preserve … and choose another directory` | Preserve the directory and choose another `--install-dir` |
| `configuration check failed; no service was changed` | `.env` has a value Core rejects. The message names the variable and not the value. Fix `.env` and run `oac apply` again |
| `data volume check failed; no service was changed` | Initialization refused the data volume; the message names the cause, such as a volume a newer release upgraded. See [downgrades](./operations.md#downgrades) |
| `This installation runs release …` | The installer or initialization belongs to another release. To move to a newer one, follow [Upgrade](./operations.md#upgrade); downgrades are refused |
| `This account cannot use Docker` | Add the account to the `docker` group and log in again, or use Docker Desktop or rootless Docker. Do not run the installer with `sudo` |
| `This account cannot access …` | An earlier run with `sudo` left files owned by root. Run the `sudo chown` command the message prints, then rerun as your own account |
| `Docker Compose 2.26 or newer is required` | Install or update the Docker Compose plugin |
| `Docker could not pull …` | The Docker daemon cannot reach the registry. Configure its proxy or a registry mirror, or use the [offline archive](./install-options.md#offline-hosts) |
| `Interrupted; rerun the installer to continue.` | Ctrl-C stopped the installer and left the installation ready for a rerun. Run the same command again. On Linux and macOS the launcher keeps an unfinished `oac` download in `${TMPDIR:-/tmp}/oac-install-<uid>` and the rerun resumes it; delete that directory to start the download over |
| `startup failed; data retained, rerun the installer` | The message lists the services that are not healthy and the `docker compose logs --tail 50` command for them. Fix the cause, then [resume installation](./install.md#install); keep the data. After `oac apply`, the same message says to run `oac apply` again |

If an installer step reports insufficient space or quota, free space on the filesystem named by the error. Image-loading failures can also require space in Docker's storage, which may be on a different filesystem.

## Web and API access

| Symptom | Cause and fix |
| --- | --- |
| Web answers 403 `Forbidden` | The browser host is not `OAC_PUBLIC_URL`. Open that origin; a reverse proxy must pass the original Host |
| Web shows that Core is unavailable (502) | Core is stopped or failing: `docker compose ps`, then Core's log |
| Session creation returns 400 `model_provider_required` | No model provider: set a [default model](../configuration.md#default-models) for the Harness, or pass one; self-hosted Sessions always pass their own |

For a request that reached Core, see [Diagnose a failure](../api/public-agent-api.md#diagnose-a-failure) in the Agents API guide. A Session that failed to execute has [Session diagnostics](../../contracts/agents-api/session-diagnostics.md), which name the failure category.

## Nodes

Add node shows no command: see [Before you add a node](./nodes.md#before-you-add-a-node). A node that is online but not ready reports a code; see [Readiness codes](./nodes.md#readiness-codes).

### Sandboxes cannot reach a private endpoint

A microsandbox sandbox cannot reach a model provider or MCP server at a private address, and the Turn fails once the Harness stops retrying. Sandbox DNS queries go to the nameservers listed in the host's `/etc/resolv.conf`, and DNS rebind protection discards any answer that contains a private address. A name those nameservers resolve to a private address therefore does not resolve in the sandbox at all, even for a public service such as a model API behind a cloud VPC resolver.

Installer-added nodes have no setting to change this. Make those nameservers return the service's public addresses. microsandbox does not read the host's `/etc/hosts`, so an entry there helps only when `/etc/resolv.conf` points at a local stub resolver that serves it, such as systemd-resolved at `127.0.0.53`. Otherwise use a Docker node, which does not filter sandbox traffic, or [register the node manually](./nodes.md#register-a-node-manually) with a `network` policy that also allows the private range in a rule without a port, because rebind protection ignores port-scoped rules.

### Connection failures

A registered node that can't connect to Core says why in its [log](./nodes.md#logs): `sandbox node connection to Core failed; retrying` with a `cause`, once for each cause however often it retries, then `sandbox node connected to Core` when it connects. A node service that starts while Core is unreachable logs `cannot confirm approved node capacity:` followed by the cause.

| Cause | Fix |
| --- | --- |
| `HTTP 400 on the WebSocket upgrade` | The reverse proxy in front of Core doesn't pass WebSocket upgrades on `/api/v1`; see [Put Core behind HTTPS](./public-address.md#https-and-the-reverse-proxy) |
| Another status, such as `HTTP 404 on the WebSocket upgrade` or `HTTP 502 …` | The reverse proxy doesn't route `/api/v1` to Core or doesn't preserve Host, or Core is not running |
| `dial tcp: …`, such as `connection refused`, `i/o timeout` or `no such host` | The host can't reach Core at the public URL. Check DNS and firewalls; the node service uses the [proxy](./nodes.md#download-through-a-proxy) the installer ran with, so rerun the command with the right settings if the proxy changed |
| `tls: …`, or `TLS certificate expired or not yet valid` | The host doesn't trust Core's certificate, or the public URL uses `https` on a port that doesn't serve it |
| `Core did not accept the node: …` | Core closed the connection after the node introduced itself. After a Core upgrade, apply the release's [protocol changes](./operations.md#protocol-changes); otherwise read Core's log |

### Installer messages

| Message | Fix |
| --- | --- |
| `The enrollment command expired or was already used` | Generate a new command in Web and run it within an hour |
| `Core rejected the node configuration read (HTTP …)` | Check the Core URL and the reverse proxy in front of it, then rerun |
| `Not enough disk space under …` or `The disk is full` | Free the space it names, then rerun; the download resumes |
| This host's node program differs from the one this console serves | Core was upgraded after the node was added. Choose **Remove node**, uninstall it on the host, then add it again with **Add node** |
| Another node installation is running, or this node is preparing a new sandbox configuration | Retry once **Nodes** no longer shows **Preparing target** |
| A sandbox provider check failed before registering, such as `This host lacks a capability …` | Fix what it names (see [Readiness codes](./nodes.md#readiness-codes)) and rerun; the token was not used |
| Core's public URL changed after this command was generated | Generate a new command in Web and run it |
| This host's node uses `<address>`, but this command uses `<address>` | The node was added under an older public URL. Remove it in Web, uninstall it, then add it again |
| Docker Engine is not installed, or Docker is not running | Install Docker Engine, or `sudo systemctl enable --now docker`, then rerun |
| Docker on this host does not enforce CPU and memory limits | Use cgroup v2, then rerun |
| KVM is unavailable, or `/dev/kvm` must be group-accessible | Enable virtualization; your distribution's KVM package sets `root:kvm 0660` |
| This host has N CPUs and M MiB of memory; each sandbox needs … | Use a larger host, or change the sandbox size |
| SELinux is enforcing on this host | Use a host supported by the installer; it does not change SELinux settings |
| This host already runs a sudo-mode node for another Core | Remove that node and uninstall it first |
| Node installation and removal require root | Run Web's command with sudo, or from a root shell |
| Core still lists this node | Remove it on the **Nodes** page first |
| Core no longer accepts this node | It was removed, or a reset retired it. Uninstall it, then add the host with a new command |
