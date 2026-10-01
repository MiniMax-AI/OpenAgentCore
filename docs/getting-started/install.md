---
title: "Install Core and Web"
---

One command installs Core, the Web console and PostgreSQL on a Linux host. Web is the administrator console: you sign in with the Core key, give the installation a domain, set a default model and issue Project API keys. Applications then call Core's API with those keys, and their Sessions run in sandboxes on nodes you add, or on E2B.

1. [Check the prerequisites](#prerequisites).
2. [Run the installer](#install).
3. [Sign in to Web](#sign-in-to-web).
4. [Configure the domain and HTTPS](#configure-the-domain-and-https).
5. [Set a default model](#set-a-default-model).
6. [Issue a Project API key](#issue-a-project-api-key).
7. [Add sandbox capacity](#add-sandbox-capacity).

This page follows the default path. Every flag, existing reverse proxies, split and native deployments and offline hosts are in [installation options](./install-options.md).

## Prerequisites

- Linux amd64 with Python 3.9 or newer, and curl. No GitHub account or CLI is needed.
- Docker Engine with Docker Compose 2.26.0 or newer (`docker compose version`).
- An account that can run `docker` and write to its home directory. Ordinary users and root both work; the installer never calls sudo.
- A free port each for initial Web access (8080) and Core (8091, on loopback), and free ports 80 and 443 once you turn on HTTPS; see [ports](./install-options.md#ports). Docker must be able to publish them; the installer does not change host policy.
- A DNS hostname that points to this host, before you connect applications, nodes, E2B or self-hosted machines. You can install and sign in first.

The Core host needs no KVM; nodes that run microsandbox do.

## Install

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

If DNS already points to this host, pass the address to set up HTTPS during installation instead of in step 4:

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --public-url https://core.example
```

The script picks the latest stable release, verifies its checksum and runs the bundled installer, which:

1. checks its settings and the ports it needs, then the host, and loads the Core, Web, PostgreSQL and HTTPS gateway images;
2. creates the [installation directory](../configuration.md#installation-directory), `~/.oac/core`, with the Core key, `config.json` and the `oac` management command;
3. starts the services with Docker Compose. The gateway serves Web on all IPv4 interfaces, at the port the installer prints; Core stays on loopback and PostgreSQL stays private;
4. selects the microsandbox sandbox backend at the Standard size. It adds no node.

It creates no Project or key and makes no model request. It ends by printing the console address, the API base URL and the next steps.

Downloads retry temporary network failures automatically. The terminal shows download progress and activity during long steps.

If installation fails or is interrupted before the services first become healthy, fix the reported cause and rerun the same command. The installer removes its temporary download, new service project, volumes and installation files; loaded Docker images remain reusable. A rerun first clears an incomplete installation or download left by a forced exit or power loss. It never clears another active installation process or an unrelated directory. Once the services have started successfully, failures preserve the installation and its data; use [same-release repair](./operations.md#installation-version-policy).

For insufficient space or quota, free space on the filesystem named by the error. Image-loading failures can also require space in Docker's storage, which may be on a different filesystem.

## Sign in to Web

1. Open the console address the installer printed, such as `http://SERVER_IP:8080`, or your public URL if you passed one. Behind NAT, use the IP address your browser reaches. Until a domain is set, Web accepts IP addresses only, not host names.
2. Sign in with the [Core key](./operations.md#core-key), the installation's administrator credential. Web has no user accounts.

   ```sh
   cat ~/.oac/core/secrets/core.key
   ```

## Configure the domain and HTTPS

Applications, nodes and sandboxes reach Core at one HTTPS address, the public URL. The initial HTTP address serves only Web.

1. Point the hostname's A/AAAA records to this host, allow inbound ports 80 and 443 from the internet, and keep other programs off [those ports](./install-options.md#ports).
2. In Web, open **System**, choose **Configure domain and HTTPS**, enter the hostname, such as `core.example.com`, and choose **Apply**.

The installation checks DNS and the ports, requests a certificate and checks that the HTTPS address reaches this installation before switching Core and Web to it. Then open the HTTPS address and sign in again; the initial HTTP address redirects there. Certificates renew automatically. If DNS or the certificate fails, the previous address stays in use: correct the reported problem and retry. Retry an interrupted switch with the same hostname, or check it with `oac status` and finish it with `oac apply`.

The same operation from a terminal:

```sh
~/.oac/core/oac domain core.example.com
```

To change the address later, see [changing the public URL](../configuration.md#changing-the-public-url).

## Set a default model

Core-hosted Sessions without their own model provider use their harness's default model. On **System**, under **Default model configuration**, find the harness marked **Default** (Codex unless you changed `core.default_harness`) and choose **Set**. Enter the model ID, the protocol, and the provider's base URL and API key. MiniMax Code also needs the context window and max output tokens. See [default models](../configuration.md#default-models).

## Issue a Project API key

1. On **Projects and keys**, choose **Create project**, then **Issue key**. The dialog shows the key once: copy it and keep it safe. Its **How to call** card shows the API base URL and sample requests.
2. Give the key and the API base URL to the application developer. They continue with the [quickstart](./quickstart.md).

Web's **Overview** tracks these steps in a **Getting started** checklist.

## Add sandbox capacity

Sessions need somewhere to run:

- **Nodes** run the microsandbox backend the installer selected: [add a node](./nodes.md) from Web's **Nodes** page. Docker nodes need `--sandbox docker` at installation, or a [reset](./nodes.md#change-the-sandbox-configuration) to change the backend.
- **E2B**, which needs no nodes: [change the sandbox configuration](./nodes.md#change-the-sandbox-configuration) in Web, or [choose it during installation](./install-options.md#sandbox-backend).

Day-to-day operation, backups and upgrades are in [Operations](./operations.md).
