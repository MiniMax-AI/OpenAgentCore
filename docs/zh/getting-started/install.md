---
title: "安装 Core 和 Web"
source: docs/getting-started/install.md
source_hash: 6bde0267753b797a6ada321509d9ac4ab2270021069308f4f07bb33c9d3e03cd
---

本指南带运维人员从一台空主机到可用的安装：在 Docker 下运行的 Core、Web 控制台和 PostgreSQL，一个沙箱提供商，一个默认模型和第一个项目 API 密钥。Web 的 **概览** 通过 **新手引导** 清单跟踪同样的步骤。

1. [检查前置条件](#prerequisites)。
2. [运行安装程序](#install)。
3. [登录 Web](#sign-in-to-web)。
4. [添加沙箱容量](#add-sandbox-capacity)。
5. [设置默认模型](#set-a-default-model)。
6. [签发项目 API 密钥](#issue-a-project-api-key)。

全部安装程序参数、已有反向代理和离线主机见[安装选项](./install-options.md)。

## 前置条件 {#prerequisites}

| | Linux | macOS | Windows |
| --- | --- | --- | --- |
| 主机 | amd64 或 arm64，需安装 curl | Intel 或 Apple Silicon，需安装 curl | x64，需 PowerShell |
| Docker | Docker Engine，已启动 | Docker Desktop，已启动 | Docker Desktop，运行 Linux 容器 |
| 账号 | `docker` 组成员，或使用 rootless Docker | 你的用户 | 你的用户 |

- 所有平台都需要 Docker Engine 26 或更高版本，以及 Docker Compose 2.26.0 或更高版本。Docker Desktop 的版本需足够新，已包含它们。
- Docker 守护进程能访问容器注册表。它不使用当前 shell 的代理变量，参阅[离线主机](./install-options.md#offline-hosts)。下载发行制品还需要能访问 GitHub。
- 8080 端口空闲，且 Docker 能发布该端口，参阅[端口](./public-address.md#ports)。安装程序不会修改主机策略。

> [!WARNING]
> 以该账号运行安装程序和 `oac`，不要使用 `sudo`。`sudo` 运行会在 `~/.oac` 中留下属于 root 的文件，导致之后的运行失败。

## 安装 {#install}

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash
```

```powershell [Windows]
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

:::

安装程序检查 Docker 和端口，写入[安装目录](../configuration.md#installation-directory) `~/.oac/core`，其中包含 `.env` 和 `oac` 命令（Windows 为 `oac.exe`），然后启动服务。Web 在 8080 端口提供控制台，并[把 API 请求转发到 Core](./public-address.md#listeners-and-access)。下载和配置检查在创建安装目录之前完成，因此全新安装失败时不会留下任何文件。安装程序会打印控制台地址；仅在全新安装时打印 Core 密钥，重新运行或升级则打印 `oac core-key --show`。

控制台地址默认是本机默认路由上的私网 IP；没有时为 `http://localhost:8080`。浏览器必须打开这个确切的地址。要自行指定，例如已有反向代理的 HTTPS 地址，请传入 `--public-url`。Windows 上传参的方式见[传入参数](./install-options.md#pass-flags)。

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --public-url https://core.example
```

```powershell [Windows]
& ([scriptblock]::Create((irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1))) --public-url https://core.example
```

:::

> [!TIP]
> 可以放心重新运行。如果安装程序因检查失败、服务三分钟内未变为健康或按 Ctrl-C 而停止，修复提示信息所指的问题后重新执行同一命令。重新运行会保留配置和数据，并复用已有镜像。`--public-url`、`--host` 和 `--web-port` 只对全新安装生效；之后要修改它们，请编辑 `.env` 并运行 `oac apply`。在 Linux 和 macOS 上，启动脚本还会继续未完成的下载。[故障排查](./troubleshooting.md#installation)列出了这些提示信息。

要换到更新的发布版本，请按[升级](./operations.md#upgrade)操作。

## 登录 Web {#sign-in-to-web}

1. 打开安装程序输出的控制台地址，即公开 URL。Web 只接受这个主机名。
2. 使用 [Core 密钥](./operations.md#core-key)登录，这是安装的管理员凭据。Web 没有用户账号。

   ```sh
   ~/.oac/core/oac core-key --show
   ```

   Windows 使用 `& "$HOME/.oac/core/oac.exe" core-key --show`。各平台的命令参数相同。

## 添加沙箱容量 {#add-sandbox-capacity}

安装程序不选择沙箱提供商。请[选择沙箱提供商](./sandbox-backends.md)：在自有 Linux 节点上运行 microsandbox，并使用[设置工作区存储](./workspace-storage.md)中的工作区存储，是推荐的自管部署；E2B 不需要节点。应用也可以在自己的机器上运行[自托管 Session](./self-hosted.md)。

## 设置默认模型 {#set-a-default-model}

没有自带模型提供商的 Core 托管 Session 使用其 Harness 的默认模型。参阅[默认模型](../configuration.md#default-models)。

1. 打开 **设置 → 模型与凭据**。在 **默认模型配置** 下，标记为 **默认** 的 Harness 显示 **未设置模型提供商**。除非修改了 `OAC_DEFAULT_HARNESS`，否则为 Codex，参阅[设置](../configuration.md#settings)。
2. 选择 **设置**，输入模型 ID、协议以及提供商的基础 URL 和 API 密钥。MiniMax Code 还需要上下文窗口和最大输出 token 数。
3. 保存。该 Harness 显示 **就绪**。

## 签发项目 API 密钥 {#issue-a-project-api-key}

1. 在 **项目与密钥** 中选择 **创建项目**，然后选择 **签发密钥**。对话框只显示一次密钥：复制并妥善保存。**如何调用** 卡片展示 API 基础 URL 和示例请求。
2. 将密钥和 API 基础 URL 交给应用开发者，他们可以继续阅读[运行第一个 Session](./quickstart.md)。

## 从其他机器访问 Core {#configure-the-domain-and-https}

在一台机器上试用时，此步骤可选。应用、节点和沙箱通过同一个地址访问 Core，即公开 URL。局域网上用 HTTP 即可；超出局域网时，在前面放反向代理，并把公开 URL 设为它提供的 HTTPS 源地址。反向代理、端口以及之后如何修改地址，见[配置公开地址](./public-address.md)。

## 后续步骤 {#next-steps}

| 下一步 | 位置 |
| --- | --- |
| 发起 API 调用 | [运行第一个 Session](./quickstart.md) |
| 添加 Linux 节点 | [添加和管理节点](./nodes.md) |
| 备份、升级和卸载 | [运维](./operations.md) |
| 参数、Compose 和离线主机 | [安装选项](./install-options.md) |
