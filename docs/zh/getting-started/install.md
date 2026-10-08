---
title: "安装 Core 和 Web"
source: docs/getting-started/install.md
source_hash: 0753c0a4b761a6cb16181413a6567245be03cb4805469a65e0aebf2bea344a9f
---

一条命令即可在 Linux amd64 Docker 引擎上安装 Core、Web 控制台、agent host 和 PostgreSQL。操作员启动器也可在 macOS 和 Windows 上运行。用 Core 密钥登录 Web，设置默认模型并签发 Project API 密钥。应用使用这些密钥调用 Core。Session 在你添加的节点上的沙箱中运行，也可以在 E2B 上运行。

1. [检查前置条件](#prerequisites)。
2. [运行安装程序](#install)。
3. [登录 Web](#sign-in-to-web)。
4. [配置公开地址](#configure-the-domain-and-https)。
5. [设置默认模型](#set-a-default-model)。
6. [签发 Project API 密钥](#issue-a-project-api-key)。
7. [添加沙箱容量](#add-sandbox-capacity)。

本页介绍默认流程。完整参数、已有反向代理和离线主机请参阅[安装选项](install-options.md)。

## 前置条件 {#prerequisites}

- 操作端需要 curl（Linux 或 macOS）或 PowerShell（Windows x64）；其 Docker 引擎必须满足下面的执行平台要求。
- 运行 Linux amd64 容器的 Docker Engine 26 或更高版本，以及 Docker Compose 2.26.0 或更高版本。不支持 ARM64 Docker 引擎；安装器不会启用模拟。
- 能运行 `docker` 并向自己的主目录写入文件的账号。普通用户和 root 均可；安装程序不会调用 sudo。
- Web 的 8080 端口空闲。参阅[端口](install-options.md#ports)。Docker 必须能发布该端口；安装程序不会修改主机策略。
- 本机以外的访问要求 `OAC_PUBLIC_URL` 就是浏览器、节点和执行器使用的地址。可以先在本机登录。

沙箱节点运行在 Linux amd64 上。操作员启动器在 macOS 或 Windows 上运行时，其 Docker 引擎仍必须是 Linux amd64；请连接 Linux 沙箱节点或使用 E2B。

## 安装 {#install}

Linux 和 macOS：

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

Windows PowerShell：

```powershell
irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1 | iex
```

绑定所有 IPv4 地址时，安装器会使用默认路由的私网地址；如果没有可用私网地址，则使用 `http://localhost:8080`。也可以通过 `--public-url` 指定可访问的源地址；如果已有反向代理，就使用它的 HTTPS 地址：

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --public-url https://core.example
```

脚本下载该发布版的 Compose 文件，校验 SHA-256，然后：

1. 检查 Docker 使用 Linux 容器、符合版本要求，并确认所选端口空闲；
2. 准备[安装目录](../configuration.md#installation-directory) `~/.oac/core`，写入 `.env` 和原生 `oac` 命令（Windows 为 `oac.exe`）；
3. 用 Docker Compose 启动服务。Web 在 8080 端口提供控制台，并把 `/v1`、`/api/v1` 和 `/docs` 转发到 Core。Core 和 PostgreSQL 不发布端口。

安装器会打印控制台地址和 Core 密钥。登录后，在 Web 中配置执行资源并创建 Project。

安装目录就位前，先完成下载和配置检查。之后的失败会保留配置与数据，并报告失败步骤；在安装目录运行 `docker compose logs --tail 100` 查看日志。修复报错后，重新执行同一命令，或指定安装目录即可继续：

```bash
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --install-dir "$HOME/.oac/core"
```

如果使用了其他目录，请替换路径。重新执行时沿用已保存的 Compose 文件和设置；安装参数只对新目录生效。已有镜像和容器直接复用，缺失镜像会重新下载。修改设置请编辑 `.env` 并运行 `oac apply`。新发布版使用新目录；见[版本策略](operations.md#installation-version-policy)。

空间或配额不足时，请释放错误信息所指文件系统的空间。加载镜像失败还可能需要释放 Docker 存储空间，该存储可能位于另一个文件系统。

## 登录 Web {#sign-in-to-web}

1. 打开安装程序输出的控制台地址，即公开 URL。Web 只接受这个主机名。
2. 使用 [Core 密钥](operations.md#core-key)登录，这是安装的管理员凭据。Web 没有用户账号。

   ```sh
   ~/.oac/core/oac core-key --show
   ```

   Windows 使用 `& "$HOME/.oac/core/oac.exe" core-key --show`。各平台的命令参数相同。

## 配置公开地址 {#configure-the-domain-and-https}

应用、节点和沙箱通过同一个地址访问 Core，即公开 URL。局域网上的应用和节点用 HTTP 即可。托管沙箱需要非回环主机上的 HTTPS 公开 URL：在前面放反向代理，并把公开 URL 设为它提供的 HTTPS 源地址。

1. 把反向代理指向 Web。
2. 把 `OAC_PUBLIC_URL` 设为反向代理提供的 HTTPS 源地址，然后运行 `oac apply`。见[修改公开 URL](../configuration.md#changing-the-public-url)。

## 设置默认模型 {#set-a-default-model}

没有自带模型提供商的 Session 使用其 Harness 的默认模型。在 **System** 的 **Default model configuration** 下，找到标记为 **Default** 的 Harness（除非修改了 `core.default_harness`，否则为 Codex），选择 **Set**。输入模型 ID、协议以及提供商的基础 URL 和 API 密钥。MiniMax Code 还需要上下文窗口和最大输出 token 数。参阅[默认模型](../configuration.md#default-models)。

## 签发 Project API 密钥 {#issue-a-project-api-key}

1. 在 **Projects and keys** 中选择 **Create project**，然后选择 **Issue key**。对话框只显示一次密钥：复制并妥善保存。**How to call** 卡片展示 API 基础 URL 和示例请求。
2. 将密钥和 API 基础 URL 交给应用开发者，他们可以继续阅读[快速开始](quickstart.md)。

Web 的 **Overview** 通过 **Getting started** 清单跟踪这些步骤。

## 添加沙箱容量 {#add-sandbox-capacity}

Session 需要执行位置：

- 在 **System** → **Manage sandbox configuration** 选择后端，然后在 **Nodes** 页面[添加节点](nodes.md)。安装程序不选择后端。

日常操作、备份和升级见[运维](operations.md)。
