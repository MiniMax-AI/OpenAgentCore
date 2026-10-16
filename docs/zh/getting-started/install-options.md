---
title: "安装选项"
source: docs/getting-started/install-options.md
source_hash: de599bae1dc932f9f1c25f06c2b67bee8b90d2287ebd9e45157141128ac9fec1
---

本页是安装程序参数的参考，并说明如何用默认方式以外的方式安装：在已有反向代理之后、在托管平台上使用 Docker Compose，或在无法访问互联网的主机上。[默认安装](./install.md)无需任何选项。

## 传入参数 {#pass-flags}

::: code-group

```sh [macOS / Linux]
curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --public-url https://core.example
```

```powershell [Windows]
& ([scriptblock]::Create((irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1))) --public-url https://core.example
```

:::

Windows 上 `irm … | iex` 无法传参，默认执行策略也会拦截下载的 `.ps1` 文件，因此以脚本块运行脚本。除非传入 `--version`，脚本会选择最新稳定版，并校验原生命令和 Compose 文件的 SHA-256。

## 参数 {#flags}

| 参数 | 默认值 | 写入 `.env` 的变量 | 作用 |
| --- | --- | --- | --- |
| `--public-url` | 由主机的私网地址推导，或 `http://localhost:<web port>`；参阅[选择公开 URL](./public-address.md#choose-the-public-url) | `OAC_PUBLIC_URL` | 应用、节点和执行器使用的源地址 |
| `--host` | `0.0.0.0` | `OAC_HOST` | Web 绑定的地址；参阅[监听器与访问](./public-address.md#listeners-and-access) |
| `--web-port` | `8080` | `OAC_WEB_PORT` | Web 的主机端口；参阅[端口](./public-address.md#ports) |
| `--install-dir` | `~/.oac/core` | | 绝对安装目录。新安装使用空目录或不存在的目录；[重试](./install.md#install)或升级时使用已有安装目录 |
| `--version` | `latest` | | 已发布的版本标签 |
| `--upgrade` | | | 将 `--install-dir` 中的已有安装升级到安装程序所属的版本；参阅[升级](./operations.md#upgrade) |

映射到 `.env` 的参数只会写入一次。安装完成后，编辑该文件并运行 `oac apply`。对已有安装重新运行安装程序时，这些参数会被忽略，安装程序会指出与已保存设置不同的参数，并以已保存的设置启动。

只要使用不同的安装目录和端口，多个安装就可以共用一台机器。需要更多安装时，请使用不同的 IP 地址或共享反向代理。每个安装都有自己的数据库、Core 密钥和节点。

## 反向代理与 HTTPS {#reverse-proxy-and-https}

请用 `--host 127.0.0.1` 安装，把反向代理指向 Web，并把 `--public-url` 设为它的 HTTPS 源地址。代理要求、Caddy 和 nginx 示例、路由检查，以及 `--host`、`--web-port` 和端口检查，见[配置公开地址](./public-address.md#https-and-the-reverse-proxy)。

## Docker Compose 与托管平台 {#docker-compose-and-hosting-platforms}

在任一[支持的 Core 主机](./install.md#prerequisites)上使用发行版中的 `compose.yaml`。发行流程会固定初始化、Core、Web 和 PostgreSQL 镜像及源码版本。PostgreSQL 16 使用官方镜像，按摘要固定，并与其他镜像一起镜像到 GHCR。它会启动 PostgreSQL、Core 和 Web。数据保存在 Docker 命名卷中。一次性初始化服务会在该卷中生成随机机密信息并准备节点安装程序；Core 启动时执行数据库迁移。[Compose 配置](../configuration.md#compose-installations)负责管理各项设置和数据目录。

进行本地试用时，请将发行版的 `compose.yaml` 下载到一个空目录，然后运行：

```sh
docker compose up -d --wait --wait-timeout 900
docker compose exec web oac-web core-key
```

`oac-web core-key` 会将生成的 Core 密钥打印到终端，而不会将其写入容器日志。打开 `http://localhost:8080` 并使用该密钥登录。所有安装机密信息都会自动生成；重启时请保留同一个 Compose 项目及其数据卷。

初始化镜像除初始化命令外，仅包含较小的节点安装元数据。首次启动会验证并复制这些元数据，无需下载控制归档或访问 GitHub Releases。后续启动会验证已保存的文件。容器镜像仍需拉取。首次初始化中断后可以重新运行；如果现有数据库缺少安装机密信息，初始化会被拒绝。

你可以在选择域名前进行部署：将 `OAC_PUBLIC_URL` 保持未设置或留空，然后在平台的域名准备好后，按照 [Compose 配置](../configuration.md#compose-installations)进行设置并重新部署。初始的 localhost 源地址允许服务启动；Web 仅接受已配置的主机，因此重新部署后即可通过平台域名访问。

### Dokploy {#dokploy}

1. 创建一个 Docker Compose 应用并粘贴 `compose.yaml`。
2. 将 `OAC_PUBLIC_URL` 设置为公共 HTTPS 源地址，并启用隔离部署。
3. 为 `web` 服务添加域名，端口 `8080`。为该域名启用 **HTTPS**，并选择 **Let's Encrypt** 等证书提供程序。
4. 移除 `web` 服务的 `ports` 块，让 Dokploy 路由到容器端口，而不发布主机端口。
5. 部署。

将此 Compose 文件打包到 Dokploy 模板目录时，[模板元数据](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/compose/dokploy.toml)会提供生成的域名和环境；导入后仍需启用 HTTPS 及其证书提供程序。

### Coolify {#coolify}

1. 创建一个 **Docker Compose Empty** 服务并粘贴 `compose.yaml`。
2. 将 `OAC_PUBLIC_URL` 设置为公共 HTTPS 源地址，并将该域名分配给 `web` 服务的端口 `8080`。
3. 将 Coolify 的 `exclude_from_hc: true` 添加到 `init` 的服务定义中，使已完成的初始化不会影响其总体健康状态。
4. 移除 `web` 服务的 `ports` 块，再保存并部署。Coolify 路由到容器端口并提供 HTTPS。

在这两个平台上，打开服务器终端并运行 `docker compose ls`，查找已部署的项目名称和 Compose 文件。使用这些完全一致的值以及该部署的 `OAC_PUBLIC_URL`，运行 `docker compose -p <project-name> -f <compose-file> exec web oac-web core-key`，然后在已配置的源地址登录。[Dokploy 域名指南](https://docs.dokploy.com/docs/core/docker-compose/domains)和 [Coolify Compose 指南](https://coolify.io/docs/services/configuration/docker-compose)介绍了各自的域名和服务控制项。

登录后，[选择沙箱提供商](./sandbox-backends.md)，并按[添加和管理节点](./nodes.md)添加节点。Compose 堆栈部署控制平面；执行机器仍需单独部署。

使用相同的文件和环境运行 `docker compose stop` 以停止服务。停止服务后，一并备份数据卷和配置。升级时先备份，再使用相同的项目、环境和数据卷部署新版本的 `compose.yaml`：初始化会升级数据卷，Core 会迁移数据库。[升级](./operations.md#upgrade)说明会保留哪些内容，以及为何拒绝较旧的版本。

## 离线主机 {#offline-hosts}

安装程序使用当前 shell 的网络设置（包括 `HTTPS_PROXY`）从 GitHub Releases 下载发行文件。Docker 通过其守护进程拉取镜像，守护进程不读取 shell 的代理变量。所有镜像（包括 PostgreSQL）都来自 GHCR（`ghcr.io`），不需要 Docker Hub。如果只能经代理或镜像加速器访问 GHCR，请配置[守护进程代理](https://docs.docker.com/engine/daemon/proxy/)或[镜像加速器](https://docs.docker.com/docker-hub/image-library/mirror/)，然后重新运行安装程序。

无法访问这些地址的 Linux amd64 主机可使用发行资产 `oac-<commit>-linux-amd64-offline.tar.gz` 安装，其中包含所有镜像和节点制品。将其复制到该主机并解压，然后用 Python 3.9 或更高版本和 Docker 运行其中的 `install.py`。该主机上不需要检出代码、Go、Node 或 pnpm。安装完成前请保留解压后的安装包；之后已安装的服务和节点下载不再依赖它原来的位置。[开发 OpenAgentCore](../development.md#install-a-checkout-without-publishing-a-release)从检出代码构建的就是同一个归档。

### `install.py` 选项 {#install-py-options}

```sh
python3 oac-<commit>-linux-amd64/install.py --install-dir "$HOME/.oac/local" --host 0.0.0.0 --public-url http://YOUR_CORE_HOST:8080
```

| 选项 | 默认值 | 作用 |
| --- | --- | --- |
| `--install-dir` | `~/.oac/local` | 绝对安装目录 |
| `--host` | `127.0.0.1` | Web 绑定的地址。与 `oac install` 不同，`install.py` 默认使用回环地址：需要从其他机器访问时传入 `0.0.0.0` |
| `--web-port` | `8080` | Web 的主机端口 |
| `--public-url` | | 浏览器和节点使用的源地址。新安装必须提供 |
| `--upgrade` | | 将已有安装升级到此安装包所属的版本；参阅[升级](./operations.md#upgrade) |

将 `YOUR_CORE_HOST` 替换为浏览器和沙箱节点都能访问的地址，或在反向代理之后使用你的 HTTPS 源地址。新安装会打印 Core 密钥；重新运行则打印显示它的命令。该安装是标准的 Compose 安装，外加一个只读的 `node-payload` 目录，其中保存安装包的公开节点制品。请为解压后的安装包和已安装的副本都预留磁盘空间。

在其安装目录上重新运行同一安装包的 `install.py`，会以已保存的设置继续安装。来自其他提交的安装包会拒绝已有安装，除非传入 `--upgrade`。如果新安装在启动服务之前失败，它会保持安装目录原样；修复原因后重新运行同一命令。

启动失败或安装中断时，使用已保存的配置，而不是重新安装：

```sh
cd "$HOME/.oac/local"
docker compose logs --tail 100
# Retry without downloading release images or replacing saved configuration.
docker compose up -d --wait --pull never
./oac core-key --show
```
