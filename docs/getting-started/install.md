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
- An account that can run `docker` and write to its home directory. Ordinary users and root both work; the installer never calls sudo.
- Free port 8080 for Web. See [ports](./install-options.md#ports). Docker must be able to publish it; the installer does not change host policy.
- For anything off this machine, the origin in `OAC_PUBLIC_URL` must be the address browsers, nodes and executors use. You can sign in on this machine first.

Sandbox nodes run on Linux amd64. When Core runs on macOS or Windows, connect a Linux node or use E2B.

## Install

Linux and macOS:

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

When binding to all IPv4 addresses, the installer uses the private address of the default route if available; otherwise the console address is `http://localhost:8080`. To choose another reachable origin, pass `--public-url`; if a reverse proxy already serves this host, use its HTTPS address:

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --public-url https://core.example
```

The script downloads that release's Compose files, checks their SHA-256, and:

1. checks Docker is running Linux containers, meets the required versions, and can publish the chosen port;
2. prepares the [installation directory](../configuration.md#installation-directory), `~/.oac/core`, with `.env` and the native `oac` command (`oac.exe` on Windows);
3. starts the services with Docker Compose. Web serves the console on port 8080 and forwards `/v1`, `/api/v1` and `/docs` to Core. Core and PostgreSQL are not published.

The installer prints the console address and Core key. After signing in, configure execution resources and create Projects in Web.

Downloads and configuration checks happen before the installation directory is published. After that, failures preserve the configuration and data and report the failed step; inspect it with `docker compose logs --tail 100` in the installation directory. Fix the reported cause and rerun the same command, or specify the installation directory:

```bash
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --install-dir "$HOME/.oac/core"
```

Use your chosen directory if it differs. A rerun uses the saved Compose file and settings; installation flags only apply to new directories. Existing images and containers are reused; missing images are downloaded. To change settings, edit `.env` and run `oac apply`. A new release needs a new directory; see [version policy](./operations.md#installation-version-policy).

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

Core-hosted Sessions without their own model provider use their harness's default model. On **System**, under **Default model configuration**, find the harness marked **Default** (Codex unless you changed `core.default_harness`) and choose **Set**. Enter the model ID, the protocol, and the provider's base URL and API key. MiniMax Code also needs the context window and max output tokens. See [default models](../configuration.md#default-models).

## Issue a Project API key

1. On **Projects and keys**, choose **Create project**, then **Issue key**. The dialog shows the key once: copy it and keep it safe. Its **How to call** card shows the API base URL and sample requests.
2. Give the key and the API base URL to the application developer. They continue with the [quickstart](./quickstart.md).

Web's **Overview** tracks these steps in a **Getting started** checklist.

## Add sandbox capacity

Sessions need somewhere to run:

- Choose the backend in **System** → **Manage sandbox configuration**, then [add a node](./nodes.md) from **Nodes**. The installer selects none.

Day-to-day operation, backups and upgrades are in [Operations](./operations.md).
