---
title: "安装选项与高级部署"
source: docs/getting-started/install-options.md
source_hash: 4b847976f3b3bc4fe4afe7b7946c5815df9fecf75dc39648621814e793288f9e
---

[默认安装](install.md)无需任何选项。使用本页可以在现有反向代理后运行，或者在无法访问互联网时进行安装。

向下载的脚本传递选项：

```sh
./install.sh --public-url https://core.example
```

使用单行命令时，请将选项追加在 `bash -s --` 之后。发布包下载器还接受 `--version TAG` 来选择已发布的版本；否则会选择最新的稳定版本。它会在解压前验证捆绑包的 SHA-256，并保留已验证的捆绑包以供[修复](operations.md#installation-version-policy)。

安装程序会打印每个阶段，然后打印地址、登录信息和后续步骤的摘要。设置 `NO_COLOR=1` 可禁用彩色输出。任一步骤失败都会停止安装，并且不会显示成功消息。

## Docker Compose 与托管平台 {#docker-compose-and-hosting-platforms}

在 Linux amd64 上使用发行版中的 `compose.yaml` 和 Docker Compose 2.26 或更高版本。发行流程会把节点元数据渲染进 [Compose 模板](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/compose.yaml)。Core 和 Web 使用 `latest` 镜像，PostgreSQL 使用 `postgres:16-alpine`。它会启动 PostgreSQL、Core 和 Web。Web 把 `/v1` 和 `/api/v1` 转发到 Core。数据通过目录 bind mount 挂载。一次性初始化服务会在该目录中生成随机机密信息并准备节点安装程序；Core 启动时执行数据库迁移。[Compose 配置](../configuration.md#compose-installations)负责管理各项设置和数据目录。

进行本地试用时，请将同一发行版的 `compose.yaml` 和 `ports.yaml` 下载到同一个目录，然后运行：

```sh
docker compose -f compose.yaml -f ports.yaml up -d --wait --wait-timeout 900
docker compose -f compose.yaml exec web oac-web core-key
```

`oac-web core-key` 会将生成的 Core 密钥打印到终端，而不会将其写入容器日志。打开 `http://localhost:8080` 并使用该密钥登录。所有安装机密信息都会自动生成；重启时请保留同一个 Compose 项目及其数据目录。

首次初始化会下载并验证该发布版本中约 385 MB 的控制归档文件，仅保留较小的节点安装元数据。后续启动会验证已保存的文件，而不会再次下载。镜像需要额外下载。首次初始化中断后可以重新运行；如果现有数据库缺少安装机密信息，初始化会被拒绝。

你可以在选择域名前进行部署：将 `OAC_PUBLIC_URL` 保持未设置或留空，然后在平台的域名准备好后，按照 [Compose 配置](../configuration.md#compose-installations)进行设置并重新部署。初始的 localhost 源地址允许服务启动；Web 仅接受已配置的主机，因此重新部署后即可通过平台域名访问。

### Dokploy {#dokploy}

创建一个 Docker Compose 应用并粘贴 `compose.yaml`。将 `OAC_PUBLIC_URL` 设置为公共 HTTPS 源地址，启用隔离部署，并为 `web` 服务添加域名和端口 `8080`。部署前，为此域名启用 **HTTPS**，并选择 **Let's Encrypt** 等证书提供程序。部署时不要使用 `ports.yaml`；内部服务不会发布主机端口。将此 Compose 文件打包到 Dokploy 模板目录时，[模板元数据](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/dokploy.toml)会提供生成的域名和环境；导入后仍需启用 HTTPS 及其证书提供程序。

### Coolify {#coolify}

创建一个 **Docker Compose Empty** 服务并粘贴 `compose.yaml`。将 `OAC_PUBLIC_URL` 设置为公共 HTTPS 源地址，并将该域名分配给 `web` 服务的端口 `8080`。将 Coolify 的 `exclude_from_hc: true` 添加到 `init` 的服务定义中，使已完成的初始化不会影响其总体健康状态。保存并在不包含 `ports.yaml` 的情况下部署；HTTPS 由 Coolify 提供。

在这两个平台上，打开服务器终端并运行 `docker compose ls`，查找已部署的项目名称和 Compose 文件。使用这些完全一致的值以及该部署的 `OAC_PUBLIC_URL`，运行 `docker compose -p <project-name> -f <compose-file> exec web oac-web core-key`，然后在已配置的源地址登录。[Dokploy 域名指南](https://docs.dokploy.com/docs/core/docker-compose/domains)和[Coolify Compose 指南](https://coolify.io/docs/services/configuration/docker-compose)介绍了各自的域和服务控制项。这些都是可导入的部署文件；本仓库不发布托管市场条目。

登录后，使用 [Nodes](nodes.md)选择沙箱后端并添加节点。Compose 堆栈部署控制平面；执行机器仍需单独部署。

使用相同的文件和环境运行 `docker compose stop` 以停止服务。停止服务后，备份数据目录。请遵循[安装版本策略](operations.md#installation-version-policy)：使用不同发布版本时，需要创建新的 Compose 项目并使用全新的数据目录。

## 进程设置 {#process-settings}

这些标志只会一次性写入 `.env`。安装完成后，编辑该文件并运行 `oac apply`。安装程序不会更改已经启动的安装。

以下表格是由脚本自动生成的参考信息，因此保留为英文原文：
| Flag | `.env` variable |
| --- | --- |
| `--public-url` | `OAC_PUBLIC_URL` |
| `--host` | `OAC_HOST` |
| `--web-port` | `OAC_WEB_PORT` |

## 安装操作 {#installation-actions}

`--install-dir` 选择安装目录。它不是进程设置。

| 选项 | 用途 |
| --- | --- |
| `--install-dir DIR` | 绝对安装目录；默认为 `~/.oac/core`。新安装要求目录为空或不存在，或者包含一个[从未启动过的安装](install.md#install) |

只要使用不同的安装目录和端口，多个安装就可以共用一台机器。需要更多安装时，请使用不同的 IP 地址或共享反向代理。每个安装都有自己的数据库、Core 密钥和节点。

## 沙箱后端 {#sandbox-backend}

安装程序不保存沙箱后端。登录后，打开 **System** → **Manage sandbox configuration**，选择 Docker、microsandbox 或 E2B；Web 会按 [`standard-sizes.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/src/features/sandbox/standard-sizes.json) 推荐 Standard 尺寸。该选择保存在 Core 的数据库中。以后要更改，请[重置部署](nodes.md#change-the-sandbox-configuration)。Docker 沙箱与每个节点共用该节点的内核，其节点服务账户[等效于 root](nodes.md#what-the-installer-sets-up)。E2B 需要一个非回环的公共 HTTPS URL，因为 E2B 沙箱会从 E2B 云端调用 Core。按照 [E2B 指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md)准备模板。

## 监听器与访问 {#listeners-and-access}

默认安装在 `--host 0.0.0.0` 的 `--web-port`（8080）上发布 Web。Core 和 PostgreSQL 保持私有。`--host` 是不含端口、协议或区域的 IPv4 或 IPv6 地址。请在浏览器中使用服务器的具体 IP，而不是通配地址。

`--public-url` 设置 `OAC_PUBLIC_URL`，即应用、节点和执行器使用的源地址。使用反向代理时，把它设为代理提供的 HTTPS 源地址。未传入时，如果 `--host` 为 `0.0.0.0` 且主机默认路由的源地址是私有网络地址，安装程序使用 `http://<address>:<web port>`，否则使用 `http://localhost:<web port>`。可接受的源地址见[修改公开 URL](../configuration.md#changing-the-public-url)。

### 端口 {#ports}

安装程序在下载镜像之前检查 `--web-port`。

- `--host` 是该端口绑定的地址。`0.0.0.0` 在所有 IPv4 接口发布 Web。`127.0.0.1` 只在本机发布 Web。
- 端口被占用时安装停止，不会改用其他端口。

`OAC_HOST` 或 `OAC_WEB_PORT` 变化时，`oac apply` 会重新创建 Web。

## HTTPS 与反向代理 {#https-and-the-reverse-proxy}

请用 `--host 127.0.0.1` 安装，并把反向代理指向 Web，默认是 `127.0.0.1:8080`。Web 把 `/v1`、`/api/v1` 和 `/docs` 转到 Core，其余由自己提供。

反向代理必须：

- **保留 Host。** Web 仅接受 `OAC_PUBLIC_URL` 中的主机。
- **传递 WebSocket 升级请求**，包括 `/api/v1` 上的升级请求。
- **不对流进行缓冲或设置超时。** `/v1` 会流式传输 Session 事件。
- **接受大文件上传。** 源文件最大可达 512 MiB；具体限制由 Core 执行。

按照默认配置，让网关在回环地址上监听，并在 Core 主机上运行反向代理。

**Caddy** 会自行获取证书，默认传递 Host 并支持 WebSockets：

```caddyfile
core.example {
	reverse_proxy 127.0.0.1:8080
}
```

然后在 `~/.oac/core/.env` 中设置 `OAC_PUBLIC_URL=https://core.example`，并运行 `~/.oac/core/oac apply`。检查路由：

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'OpenAI-Beta: agents=v1' https://core.example/v1/agents
```

`401` 表示 `/v1` 已到达 Core，Core 正在要求提供密钥。`404` 表示请求已到达 Web：请修复反向代理，否则应用调用和每个节点连接都会失败。

所有位置都必须保持启用 TLS 验证。使用私有证书颁发机构时，节点主机、自托管机器和 Runtime 镜像必须信任该机构。

### 使用快速隧道进行本地试用 {#try-it-locally-with-a-quick-tunnel}

[Cloudflare 快速隧道](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/)会为使用外部入口的试用安装提供一个临时公共 HTTPS 地址。它只会转发到一个端口，因此需要在前面放置一个具有相同路由的本地代理：

```caddyfile
http://:8443 {
	bind 127.0.0.1
	reverse_proxy 127.0.0.1:8080
}
```

1. 启动代理：`caddy run --config Caddyfile`。
2. 启动隧道：`cloudflared tunnel --url http://127.0.0.1:8443`。它会打印一个地址，例如 `https://random-words.trycloudflare.com`。
3. 在 `~/.oac/core/.env` 中将该地址设置为 `OAC_PUBLIC_URL`，并运行 `~/.oac/core/oac apply`。

每当 `cloudflared` 重启时，该地址都会改变；随后必须重新添加绑定到旧地址的节点。吞吐量较低，因此节点首次下载 Runtime（约 500 MB）时可能很慢；请参阅[慢速链接](nodes.md#rerun-expiry-and-slow-links)。

## 离线主机 {#offline-hosts}

本安装程序不支持从离线捆绑包安装。它从发布版本下载 Compose 文件和容器镜像。
