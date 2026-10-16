---
title: "Install Core and Web"
---

This guide takes an operator from an empty host to a working installation: Core, the Web console and PostgreSQL running under Docker, a sandbox backend, a default model and a first Project API key. Web's **Overview** tracks the same steps in its **Getting started** checklist.

1. [Check the prerequisites](#prerequisites).
2. [Run the installer](#install).
3. [Sign in to Web](#sign-in-to-web).
4. [Add sandbox capacity](#add-sandbox-capacity).
5. [Set a default model](#set-a-default-model).
6. [Issue a Project API key](#issue-a-project-api-key).

Every installer flag, existing reverse proxies and offline hosts are in [Installation options](./install-options.md).

## Prerequisites

| | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Host | amd64 or arm64, with curl | Intel or Apple Silicon, with curl | x64, with PowerShell |
| Docker | Docker Engine, running | Docker Desktop, running | Docker Desktop, running Linux containers |
| Account | A member of the `docker` group, or rootless Docker | Your user | Your user |

- Docker Engine 26 or newer and Docker Compose 2.26.0 or newer, on every platform. Docker Desktop must be recent enough to include them.
- The Docker daemon can reach the container registries. It does not read this shell's proxy variables; see [offline hosts](./install-options.md#offline-hosts). Release downloads also require access to GitHub.
- Port 8080 is free, and Docker can publish it; see [ports](./public-address.md#ports). The installer does not change host policy.

> [!WARNING]
> Run the installer and `oac` as that account, never with `sudo`. A `sudo` run leaves files in `~/.oac` owned by root, and later runs fail.

## Install

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash
```

```powershell [Windows]
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

:::

The installer checks Docker and the port, writes the [installation directory](../configuration.md#installation-directory) `~/.oac/core` with its `.env` and the `oac` command (`oac.exe` on Windows), and starts the services. Web serves the console on port 8080 and [forwards API requests to Core](./public-address.md#listeners-and-access). Downloads and configuration checks finish before the installation directory is created, so a failed new installation leaves nothing behind. It prints the console address and, on a fresh installation, the Core key; a rerun or upgrade prints `oac core-key --show` instead.

The console address defaults to this host's private IP on the default route, or `http://localhost:8080` when there is none. Browsers must open exactly that address. To choose it yourself, for example the HTTPS address of an existing reverse proxy, pass `--public-url`. To pass flags on Windows, see [Pass flags](./install-options.md#pass-flags).

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --public-url https://core.example
```

```powershell [Windows]
& ([scriptblock]::Create((irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1))) --public-url https://core.example
```

:::

> [!TIP]
> Rerunning is safe. If the installer stops on a failed check, on a service that is not healthy within three minutes, or on Ctrl-C, fix what the message names and run the same command again. A rerun keeps the configuration and data and reuses existing images. `--public-url`, `--host` and `--web-port` apply only to a new installation; to change them later, edit `.env` and run `oac apply`. On Linux and macOS the launcher also resumes an unfinished download. [Troubleshooting](./troubleshooting.md#installation) lists the messages.

To move to a newer release, follow [Upgrade](./operations.md#upgrade).

## Sign in to Web

1. Open the console address the installer printed, the public URL. Web accepts only that host.
2. Sign in with the [Core key](./operations.md#core-key), the installation's administrator credential. Web has no user accounts.

   ```sh
   ~/.oac/core/oac core-key --show
   ```

   On Windows, use `& "$HOME/.oac/core/oac.exe" core-key --show`. The same command arguments work on every platform.

## Add sandbox capacity

The installer selects no sandbox backend. [Choose a sandbox backend](./sandbox-backends.md): microsandbox on your own Linux nodes with workspace storage from [Set up workspace storage](./workspace-storage.md) is the recommended self-managed deployment, and E2B needs no nodes. Applications can also run [self-hosted Sessions](./self-hosted.md) on their own machines.

## Set a default model

Core-hosted Sessions that bring no model provider use their Harness's default model. See [default models](../configuration.md#default-models).

1. Open **Settings → Models and credentials**. Under **Default model configuration**, the Harness marked **Default** shows **Model provider not set**. It is Codex unless you changed `OAC_DEFAULT_HARNESS`; see [settings](../configuration.md#settings).
2. Choose **Set** and enter the model ID, the protocol, and the provider's base URL and API key. MiniMax Code also needs the context window and the maximum output tokens.
3. Save. The Harness shows **Ready**.

## Issue a Project API key

1. On **Projects and keys**, choose **Create project**, then **Issue key**. The dialog shows the key once: copy it and keep it safe. Its **How to call** card shows the API base URL and sample requests.
2. Give the key and the API base URL to the application developer. They continue with [Run your first Session](./quickstart.md).

## Reach Core from other machines {#configure-the-domain-and-https}

This step is optional for a trial on one machine. Applications, nodes and sandboxes reach Core at one address, the public URL. HTTP is enough on the local network; beyond it, put a reverse proxy in front and set the public URL to the HTTPS origin it serves. [Configure the public address](./public-address.md) covers the reverse proxy, ports and changing the address later.

## Next steps

| Next | Where |
| --- | --- |
| Make an API call | [Run your first Session](./quickstart.md) |
| Add Linux nodes | [Add and manage nodes](./nodes.md) |
| Back up, upgrade and uninstall | [Operations](./operations.md) |
| Flags, Compose and offline hosts | [Installation options](./install-options.md) |
