---
title: "安装 Core 和 Web"
source: docs/getting-started/install.md
source_hash: b588dc9d68ba909fe9fadc440ee3f6fda9b79ebe0f14f6011d8bab603e17c710
---

一条命令即可在 Linux 主机上安装 Core、Web 控制台和 PostgreSQL。用 Core 密钥登录 Web，设置默认模型并签发 Project API 密钥。应用使用这些密钥调用 Core。Session 在你添加的节点上的沙箱中运行，也可以在 E2B 上运行。

1. [检查前置条件](#prerequisites)。
2. [运行安装程序](#install)。
3. [登录 Web](#sign-in-to-web)。
4. [配置公开地址](#configure-the-domain-and-https)。
5. [设置默认模型](#set-a-default-model)。
6. [签发 Project API 密钥](#issue-a-project-api-key)。
7. [添加沙箱容量](#add-sandbox-capacity)。

本页介绍默认流程。完整参数、已有反向代理和离线主机请参阅[安装选项](install-options.md)。

## 前置条件 {#prerequisites}

- Linux amd64 和 curl。
- Docker Engine 和 Docker Compose 2.26.0 或更高版本（`docker compose version`）。
- 能运行 `docker` 并向自己的主目录写入文件的账号。普通用户和 root 均可；安装程序不会调用 sudo。
- Web 的 8080 端口空闲。参阅[端口](install-options.md#ports)。Docker 必须能发布该端口；安装程序不会修改主机策略。
- 本机以外的访问要求 `OAC_PUBLIC_URL` 就是浏览器、节点和执行器使用的地址。可以先在本机登录。

Core 主机不需要 KVM；运行 microsandbox 的节点需要。

## 安装 {#install}

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash
```

如果主机默认路由的源地址是私有网络地址，安装程序把公开 URL 设为 `http://<that address>:8080`，同一网络的机器即可打开 Web 并添加节点；否则只有本机可以访问。反向代理已经提供这台主机时，传入它的 HTTPS 地址：

```sh
curl -fsSL https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.sh | bash -s -- --public-url https://core.example
```

脚本下载该发布版的 Compose 文件，校验 SHA-256，然后：

1. 检查 Linux amd64、Docker Compose 2.26 或更高版本，以及将要发布的端口是否空闲；
2. 创建[安装目录](../configuration.md#installation-directory) `~/.oac/core`，写入 `.env`，并从 Core 镜像复制 `oac` 命令；
3. 用 Docker Compose 启动服务。Web 在 8080 端口提供控制台，并把 `/v1`、`/api/v1` 和 `/docs` 转发到 Core。Core 和 PostgreSQL 不发布端口。

安装程序不保存沙箱后端，不添加节点，不创建 Project 或密钥，也不发起模型请求。完成后输出控制台地址和 Core 密钥。

如果服务进入健康状态之前安装失败，安装程序会删除它创建的目录。修复报告的问题后，重新运行同一命令。服务已经启动之后，后续失败会保留安装和数据。新发布版使用新目录；见[版本策略](operations.md#installation-version-policy)。

空间或配额不足时，请释放错误信息所指文件系统的空间。加载镜像失败还可能需要释放 Docker 存储空间，该存储可能位于另一个文件系统。

## 登录 Web {#sign-in-to-web}

1. 打开安装程序输出的控制台地址，即公开 URL。Web 只接受这个主机名。
2. 使用 [Core 密钥](operations.md#core-key)登录，这是安装的管理员凭据。Web 没有用户账号。

   ```sh
   ~/.oac/core/oac core-key --show
   ```

## 配置公开地址 {#configure-the-domain-and-https}

应用、节点和沙箱通过同一个地址访问 Core，即公开 URL。局域网上用 HTTP 即可。对外暴露时，在前面放反向代理，并把公开 URL 设为它提供的 HTTPS 源地址。E2B 客户机从互联网访问 Core，因此需要非回环的公开 URL。

1. 把反向代理指向 Web。
2. 把 `OAC_PUBLIC_URL` 设为反向代理提供的 HTTPS 源地址，然后运行 `oac apply`。见[修改公开 URL](../configuration.md#changing-the-public-url)。

## 设置默认模型 {#set-a-default-model}

没有自带模型提供商的 Core 托管 Session 使用其 Harness 的默认模型。在 **System** 的 **Default model configuration** 下，找到标记为 **Default** 的 Harness（除非修改了 `core.default_harness`，否则为 Codex），选择 **Set**。输入模型 ID、协议以及提供商的基础 URL 和 API 密钥。MiniMax Code 还需要上下文窗口和最大输出 token 数。参阅[默认模型](../configuration.md#default-models)。

## 签发 Project API 密钥 {#issue-a-project-api-key}

1. 在 **Projects and keys** 中选择 **Create project**，然后选择 **Issue key**。对话框只显示一次密钥：复制并妥善保存。**How to call** 卡片展示 API 基础 URL 和示例请求。
2. 将密钥和 API 基础 URL 交给应用开发者，他们可以继续阅读[快速开始](quickstart.md)。

Web 的 **Overview** 通过 **Getting started** 清单跟踪这些步骤。

## 添加沙箱容量 {#add-sandbox-capacity}

Session 需要执行位置：

- 在 **System** → **Manage sandbox configuration** 选择后端，然后在 **Nodes** 页面[添加节点](nodes.md)。安装程序不选择后端。

日常操作、备份和升级见[运维](operations.md)。
