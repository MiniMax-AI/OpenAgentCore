---
title: "Install Core and Web"
---

One command installs Core, the Web console and PostgreSQL on Linux, macOS or Windows. Sign in to Web with the Core key, set a default model and issue Project API keys. Applications call Core with those keys. Sessions run in sandboxes on nodes you add, or on E2B.

1. [Check the prerequisites](#prerequisites).
2. [Run the installer](#install).
3. [Sign in to Web](#sign-in-to-web).
4. [Configure the public address](#configure-the-domain-and-https).
5. [Set a default model](#set-a-default-model).
6. [Issue a Project API key](#issue-a-project-api-key).
7. [Add sandbox capacity](#add-sandbox-capacity).

This page follows the default path. Every flag, existing reverse proxies and offline hosts are in [installation options](./install-options.md).

## Prerequisites

- Linux amd64/arm64 or macOS Intel/Apple Silicon with curl; Windows x64 with PowerShell.
- Docker Engine 26 or newer and Docker Compose 2.26.0 or newer. On macOS and Windows, install and start Docker Desktop using Linux containers.
- An account that can run `docker` and write to its home directory. On Linux, add it to the `docker` group and log in again, or use rootless Docker. Run the installer and `oac` as that account, never with `sudo`: a `sudo` run leaves files in `~/.oac` that belong to root and stop later runs.
- Access to the container registries from the Docker daemon, which does not use this shell's proxy variables. See [offline hosts](./install-options.md#offline-hosts).
- Free port 8080 for Web. See [ports](./install-options.md#ports). Docker must be able to publish it; the installer does not change host policy.
- For anything off this machine, the origin in `OAC_PUBLIC_URL` must be the address browsers, nodes and executors use. You can sign in on this machine first.

Sandbox nodes run on Linux amd64. When Core runs on macOS or Windows, connect a Linux node or use E2B.

## Install

Linux and macOS:

```sh
curl -fsSL https://openagentcore.dev/install.sh | bash
```

The domain entry downloads and runs the latest GitHub Release installer, forwarding all arguments. Release downloads still require access to GitHub.

Windows PowerShell:

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

To pass flags on Windows, see [Install options](./install-options.md).

When binding to all IPv4 addresses, the installer uses the private address of the default route if available; otherwise the console address is `http://localhost:8080`. To choose another reachable origin, pass `--public-url`; if a reverse proxy already serves this host, use its HTTPS address:

```sh
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --public-url https://core.example
```

The script downloads that release's Compose files, checks their SHA-256, and:

1. checks Docker is running Linux containers, meets the required versions, and can publish the chosen port;
2. prepares the [installation directory](../configuration.md#installation-directory), `~/.oac/core`, with `.env` and the native `oac` command (`oac.exe` on Windows);
3. starts the services with Docker Compose. Web serves the console on port 8080 and forwards `/v1`, `/api/v1` and `/docs` to Core. Core and PostgreSQL are not published.

The installer prints the console address and, on a fresh install only, the Core key; a rerun or upgrade prints `oac core-key --show` instead. When the installer chose the console address itself, it adds that browsers must open exactly that address and how to change it with `OAC_PUBLIC_URL`. After signing in, configure execution resources and create Projects in Web.

Downloads and configuration checks happen before the installation directory is published. After that, failures preserve the configuration and data. A rejected setting or data volume is reported by itself: fix it as the message says. A service that does not become healthy within three minutes is named together with the `docker compose logs --tail 50` command for it; fix the cause and rerun the same command. Ctrl-C prints `Interrupted; rerun the installer to continue.` and leaves the installation ready for that rerun. On Linux and macOS the launcher keeps an unfinished `oac` download in `${TMPDIR:-/tmp}/oac-install-<uid>` and a rerun resumes it; if curl prints an error and nothing installs, rerun the same command. Rerun the same command, or specify the installation directory:

```bash
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --install-dir "$HOME/.oac/core"
```

Use your chosen directory if it differs. A rerun uses the saved Compose file and settings; installation flags only apply to new directories, and the installer names the ones it ignored. Existing images and containers are reused; missing images are downloaded. To change settings, edit `.env` and run `oac apply`. To move to a newer release, follow [Upgrade](./operations.md#upgrade).

For insufficient space or quota, free space on the filesystem named by the error. Image-loading failures can also require space in Docker's storage, which may be on a different filesystem.

## Sign in to Web

1. Open the console address the installer printed, the public URL. Web accepts only that host.
2. Sign in with the [Core key](./operations.md#core-key), the installation's administrator credential. Web has no user accounts.

   ```sh
   ~/.oac/core/oac core-key --show
   ```

   On Windows, use `& "$HOME/.oac/core/oac.exe" core-key --show`. The same command arguments work on every platform.

## Configure the public address {#configure-the-domain-and-https}

Applications, nodes and sandboxes reach Core at one address, the public URL. HTTP is enough on the local network. When you expose Core beyond it, put a reverse proxy in front and set the public URL to the HTTPS origin it serves. E2B guests reach Core from the internet, so they need a public URL that is not loopback.

1. Point your reverse proxy at Web.
2. Set `OAC_PUBLIC_URL` to the HTTPS origin it serves, then run `oac apply`. See [changing the public URL](../configuration.md#changing-the-public-url).

## Set a default model

Core-hosted Sessions without their own model provider use their Harness's default model. In **Settings → Models and credentials**, under **Default model configuration**, find the Harness marked **Default** (Codex unless you changed `OAC_DEFAULT_HARNESS`); it shows **Model provider not set**. Choose **Set** and enter the model ID, the protocol, and the provider's base URL and API key. MiniMax Code also needs the context window and max output tokens. The Harness then shows **Ready**. See [default models](../configuration.md#default-models).

## Issue a Project API key

1. On **Projects and keys**, choose **Create project**, then **Issue key**. The dialog shows the key once: copy it and keep it safe. Its **How to call** card shows the API base URL and sample requests.
2. Give the key and the API base URL to the application developer. They continue with the [quickstart](./quickstart.md).

Web's **Overview** tracks these steps in a **Getting started** checklist.

## Add sandbox capacity

The installer selects no sandbox backend. Choose the deployment that fits your infrastructure:

- **Recommended: microsandbox with independent workspace storage.** Prepare the [NFS filesystem and service accounts](../configuration.md#independent-workspace-storage) first, then choose **Own machines** and **microsandbox** in **Settings** → **Sandbox and scheduling**, and [add Linux nodes](./nodes.md) from **Nodes**. The [nodes guide](./nodes.md#before-you-add-a-node) also covers the private checkpoint store needed for memory recovery across compatible nodes.
- **Managed compute: E2B.** Select E2B and configure its credential and template through the same sandbox configuration flow. No execution nodes are needed; see [deployment setup](../../contracts/agents-api/sandbox-deployment.md#initial-setup-and-same-provider-changes).
- **Other supported placements.** Docker nodes remain available. Applications can also connect [self-hosted machines](./self-hosted.md). The [provider comparison](../../contracts/agents-api/sandbox-deployment.md#what-each-field-means-per-sandbox-provider) distinguishes filesystem and suspension support.

Day-to-day operation, backups and upgrades are in [Operations](./operations.md).
