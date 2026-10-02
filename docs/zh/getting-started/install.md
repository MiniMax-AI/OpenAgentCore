---
title: "安装 Core 和 Web"
source: docs/getting-started/install.md
source_hash: 7b15a2e8fd3365e0d4e246d9d5a288bd3648a3f79b3fa11ccfc6e3e702d4c77d
---

一条命令即可在 Linux 主机上安装 Core、Web 控制台和 PostgreSQL。Web 是管理员控制台：使用 Core 密钥登录，为安装配置域名、设置默认模型并签发 Project API 密钥。应用随后使用这些密钥调用 Core API，Session 在你添加的节点上的沙箱中运行，也可以在 E2B 上运行。

1. [检查前置条件](#prerequisites)。
2. [运行安装程序](#install)。
3. [登录 Web](#sign-in-to-web)。
4. [配置域名和 HTTPS](#configure-the-domain-and-https)。
5. [设置默认模型](#set-a-default-model)。
6. [签发 Project API 密钥](#issue-a-project-api-key)。
7. [添加沙箱容量](#add-sandbox-capacity)。

本页介绍默认流程。完整参数、已有反向代理和离线主机请参阅[安装选项](install-options.md)。

## 前置条件 {#prerequisites}

- Linux amd64，Python 3.9 或更高版本，以及 curl。无需 GitHub 账号或 CLI。
- Docker Engine 和 Docker Compose 2.26.0 或更高版本（`docker compose version`）。
- 能运行 `docker` 并向自己的主目录写入文件的账号。普通用户和 root 均可；安装程序不会调用 sudo。
- 初次访问 Web 的端口（8080）和 Core 的端口（8091，仅回环地址）各自空闲，启用 HTTPS 后还需要空闲的 80 和 443 端口；参阅[端口](install-options.md#ports)。Docker 必须能发布这些端口；安装程序不会修改主机策略。
- 连接应用、节点、E2B 或自托管机器之前，需要一个指向这台主机的 DNS 主机名。可以先安装并登录。

Core 主机不需要 KVM；运行 microsandbox 的节点需要。

## 安装 {#install}

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

如果 DNS 已指向这台主机，可以传入地址，在安装期间配置 HTTPS，而无需执行第 4 步：

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --public-url https://core.example
```

脚本选择最新稳定版、验证校验和，并运行捆绑的安装程序。安装程序会：

1. 检查设置、所需端口和主机，然后加载 Core、Web、PostgreSQL 和 HTTPS 网关镜像；
2. 创建[安装目录](../configuration.md#installation-directory) `~/.oac/core`，其中包含 Core 密钥、`config.json` 和管理命令 `oac`；
3. 使用 Docker Compose 启动服务。网关在安装程序输出的端口上监听所有 IPv4 接口并提供 Web；Core 仅监听回环地址，PostgreSQL 保持私有；
4. 选择 Standard 规格的 microsandbox 沙箱后端，不添加节点。

安装程序不创建 Project 或密钥，也不发起模型请求。完成后输出控制台地址、API 基础 URL 和后续步骤。

临时网络故障会自动重试下载。长时间执行的步骤会在终端显示下载进度和活动状态。

如果安装失败，或在服务首次进入健康状态前被中断，请修复报告的问题并重新运行同一命令。安装程序会删除临时下载、新服务项目、卷和安装文件；已加载的 Docker 镜像可以继续复用。重新运行时，程序先清理强制退出或断电留下的不完整安装或下载，不会清理其他仍在运行的安装进程或无关目录。服务成功启动之后，失败不会删除安装和数据；请使用[同版本修复](operations.md#installation-version-policy)。

空间或配额不足时，请释放错误信息所指文件系统的空间。加载镜像失败还可能需要释放 Docker 存储空间，该存储可能位于另一个文件系统。

## 登录 Web {#sign-in-to-web}

1. 打开安装程序输出的控制台地址，例如 `http://SERVER_IP:8080`；如果传入了公开 URL，则打开该 URL。在 NAT 后方时，使用浏览器能访问的 IP 地址。设置域名之前，Web 只接受 IP 地址，不接受主机名。
2. 使用 [Core 密钥](operations.md#core-key)登录，这是安装的管理员凭据。Web 没有用户账号。

   ```sh
   cat ~/.oac/core/secrets/core.key
   ```

## 配置域名和 HTTPS {#configure-the-domain-and-https}

应用、节点和沙箱通过同一个 HTTPS 地址访问 Core，即公开 URL。初始 HTTP 地址仅提供 Web。

1. 将主机名的 A/AAAA 记录指向这台主机，允许来自互联网的 80 和 443 端口入站流量，并确保其他程序不占用[这些端口](install-options.md#ports)。
2. 在 Web 中打开 **System**，选择 **Configure domain and HTTPS**，输入主机名，例如 `core.example.com`，然后选择 **Apply**。

安装检查 DNS 和端口、申请证书，并验证 HTTPS 地址能访问本安装，然后将 Core 和 Web 切换到该地址。随后打开 HTTPS 地址并重新登录；初始 HTTP 地址会重定向到它。证书自动续期。如果 DNS 或证书处理失败，继续使用原地址：修复报告的问题后重试。若切换被中断，使用同一主机名重试，或用 `oac status` 检查并通过 `oac apply` 完成。

终端中的对应操作：

```sh
~/.oac/core/oac domain core.example.com
```

之后修改地址的方法见[修改公开 URL](../configuration.md#changing-the-public-url)。

## 设置默认模型 {#set-a-default-model}

没有自带模型提供商的 Core 托管 Session 使用其 Harness 的默认模型。在 **System** 的 **Default model configuration** 下，找到标记为 **Default** 的 Harness（除非修改了 `core.default_harness`，否则为 Codex），选择 **Set**。输入模型 ID、协议以及提供商的基础 URL 和 API 密钥。MiniMax Code 还需要上下文窗口和最大输出 token 数。参阅[默认模型](../configuration.md#default-models)。

## 签发 Project API 密钥 {#issue-a-project-api-key}

1. 在 **Projects and keys** 中选择 **Create project**，然后选择 **Issue key**。对话框只显示一次密钥：复制并妥善保存。**How to call** 卡片展示 API 基础 URL 和示例请求。
2. 将密钥和 API 基础 URL 交给应用开发者，他们可以继续阅读[快速开始](quickstart.md)。

Web 的 **Overview** 通过 **Getting started** 清单跟踪这些步骤。

## 添加沙箱容量 {#add-sandbox-capacity}

Session 需要执行位置：

- **Nodes** 运行安装程序选择的 microsandbox 后端：在 Web 的 **Nodes** 页面[添加节点](nodes.md)。Docker 和 E2B 以后通过 [Reset deployment](nodes.md#change-the-sandbox-configuration) 选择。

日常操作、备份和升级见[运维](operations.md)。
