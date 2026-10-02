---
title: "管理你的安装"
source: docs/getting-started/operations.md
source_hash: 83a7f46bfd82d743be230955a7ac2312efaa9ca8d88b243117e66cc19626e2a4
---

安装运维人员负责 Core 主机、存储和可用性。节点主机运行各自的服务；参阅[节点](nodes.md)。设置见[配置参考](../configuration.md)。

## oac 命令 {#the-oac-command}

每个安装目录中都有自己的管理命令，无需发行包或 root：

```sh
~/.oac/core/oac status
```

| 命令 | 功能 |
| --- | --- |
| `oac status` | 展示各服务及健康状态、Core 与 Web 健康状态、公开 URL、API 基础 URL、控制台地址、源码提交、反向代理所需路由、尚未应用的 `config.json` 变更和手动修改的生成文件。服务不可用时以非零状态退出。不调用模型 |
| `oac start` | 使用 `apply` 最近写入的文件启动所有服务；提示尚未应用的变更 |
| `oac stop` | 停止 PostgreSQL、Core 和 Web。保留数据、节点和沙箱，运行中的沙箱工作可能继续 |
| `oac apply` | 应用 `config.json` 并重启发生变化的服务；参阅 [apply 工作方式](../configuration.md#how-oac-apply-works) |
| `oac apply --dry-run` | 展示变更的设置、文件与重启计划，不修改任何内容 |
| `oac apply --discard-edits` | 覆盖手动修改的生成文件，各自保留为 `generated/<file>.edited-<time>` |
| `oac apply --confirm-public-url-change URL` | 无提示地确认公开 URL 变更；必须与新 URL 相同 |
| `oac domain HOSTNAME [--confirm-public-url-change URL]` | 托管入口：将公开 URL 设为 `https://HOSTNAME`，对应 Web 的 **Configure domain and HTTPS**；参阅[配置域名和 HTTPS](install.md#configure-the-domain-and-https) |
| `oac rotate-core-key [--yes]` | 替换 Core 密钥；参阅[轮换 Core 密钥](#rotate-the-core-key) |
| `oac uninstall [--yes]` | 从主机移除安装及全部数据；参阅[卸载](#uninstall) |

第二个安装使用自己的命令，例如 `~/.oac/second/oac status`。

## 服务健康状态 {#service-health}

根据不同问题使用这些观察：

| 观察 | 能证明什么 |
| --- | --- |
| `oac status`、PostgreSQL 健康状态 | 数据库接受就绪检查 |
| Core `/healthz` | Core 进程存活 |
| 经过认证的 API 读取 | 调用者的密钥适用于该资源 |
| Environment 连接 | Runtime 传输已连接 |
| 已完成的 Turn 及结果 | 任务已记录的结果 |

服务健康状态不表示 Harness 或模型可用。执行情况使用 Session、Turn、Items 和 Usage 读取，节点连接、就绪和分配使用 Web 的 **Nodes** 页面。本地诊断使用安装自身的 Compose 文件：

```sh
docker compose -f "$HOME/.oac/core/generated/compose.json" ps --all
docker compose -f "$HOME/.oac/core/generated/compose.json" logs --tail 200 core
```

不要将 `docker compose config`、`docker inspect` 或原始日志粘贴到公开问题报告。

## 停止与重启 {#stop-and-restart}

计划重启前，先等待活动工作结束：

```sh
~/.oac/core/oac stop
~/.oac/core/oac start
```

停止 Core 不会停止节点或沙箱。节点服务、microVM 和 Docker 容器继续运行；停止不是回收计算资源的方法。Core 重启不会透明地继续被中断的原生工具调用。重连后查询同一 Session；不要创建新 Session 来重放结果不确定的工作。Session 事件流仅提供实时事件；通过读取 Session、Turn 和 Items 恢复。

Web 重启（包括 `oac apply` 引起的重启）会让所有控制台用户退出登录。其他情况下，Web 登录持续 12 小时。

## Core 密钥 {#core-key}

每个安装有一个管理员凭据，即 Core 密钥。安装程序在 `<install dir>/secrets/core.key` 生成 64 字符随机密钥，默认位置为 `~/.oac/core/secrets/core.key`。Core 密钥：

- 用于登录 Web。浏览器获得 HttpOnly 会话 cookie，不持有密钥；
- 通过 `Authorization: Bearer <Core key>` 授权 Core API（`/core/v1`）请求；
- 不授权 Agents API（`/v1`）。应用使用 Project API 密钥，后者也不能调用 `/core/v1`。

请保密：`secrets/` 权限为 `0700`，文件为 `0600`。Web，以及托管入口下的 `installation` 服务读取 `core.key`；Core 只读取 `generated/core-key-digests.json` 中的 SHA-256，该文件由 `oac apply` 从密钥派生。Core 密钥至少 32 字符且不含空白。Web 限制失败登录。

### 用脚本调用 Core API {#script-the-core-api}

在 Core 主机上运行脚本，访问 Core 回环端口。此辅助函数从文件读取密钥，使其不进入命令行：

```sh
core() {  # core METHOD PATH [JSON body]
  curl -fsS -X "$1" "http://127.0.0.1:8091/core/v1$2" \
    -H @<(printf 'Authorization: Bearer %s\n' "$(cat ~/.oac/core/secrets/core.key)") \
    -H 'Content-Type: application/json' ${3:+-d "$3"}
}
```

| 任务 | 命令 |
| --- | --- |
| 列出 Project | `core GET /projects` |
| 创建 Project | `core POST /projects '{"name": "billing-bot"}'` |
| 签发 API 密钥（仅显示一次，字段为 `key`） | `core POST /projects/$PROJECT_ID/keys '{"name": "prod"}'` |
| 撤销密钥 | `core DELETE /projects/$PROJECT_ID/keys/$KEY_ID` |
| 归档 Project（撤销全部密钥） | `core POST /projects/$PROJECT_ID/archive` |
| 查看 Harness 及默认模型 | `core GET /harnesses` |
| 设置 Codex 默认模型 | `core PUT /harnesses/codex/model-configuration '{"model": "your-model-id", "model_provider": {"protocol": "responses", "base_url": "https://provider.example/v1", "api_key": "sk-..."}}'` |
| 安装信息，包括 API 基础 URL | `core GET /installation` |

[Core 管理 API](../../../contracts/agents-api/zh/admin-api.md)列出全部路由；错误使用 [Core 错误封装](../../../contracts/agents-api/zh/core-errors.md)。

### 轮换 Core 密钥 {#rotate-the-core-key}

```sh
~/.oac/core/oac rotate-core-key
```

`config.json` 有未应用变更或生成文件被手动修改时，操作被拒绝：先运行 `oac apply`。程序要求确认（`--yes` 跳过），停止 Web，在 `secrets/core.key` 写入新密钥并重新生成摘要文件。安装正在运行时，随后重启 Core、启动 Web，并检查 Core 接受新密钥且拒绝旧密钥；已停止的安装只更新文件，下次 `oac start` 使用新密钥。Core 重启时旧密钥立即失效，所有控制台会话结束：重新登录并更新脚本。命令提前停止时，以 `secrets/core.key` 中的密钥为准；运行 `oac apply` 完成。

## Project 和 API 密钥 {#projects-and-api-keys}

在 Web 的 **Projects and keys**，或通过 [Core API](#script-the-core-api)创建 Project、签发密钥。Project 和密钥行为见 [Project 拥有资产](../concepts.md#projects-own-assets)。

轮换应用密钥：

1. 在同一 Project 签发新密钥。
2. 更新应用以使用它。
3. **Revoke** 旧密钥。

**Archive** 禁用 Project 的所有密钥并保留资产。

Core 记录每次公开资源写入所使用的密钥；历史保留策略为 [`core.write_audit_retention`](../configuration.md#settings)。

## 备份 {#back-up}

一起备份这些内容；恢复时全部需要：

- PostgreSQL 卷 `<project>_database`。其中包含 Project、密钥摘要、节点、默认模型、加密凭据和全部执行历史（含大对象）。逻辑备份：

  ```sh
  docker compose -f "$HOME/.oac/core/generated/compose.json" exec -T database \
    pg_dump -U agents_api agents_api > oac-backup.sql
  ```

- 安装目录：`config.json`、`state.json`（安装 ID）和 `secrets/`。`credential.key` 必须与数据库一起保留，否则无法解密存储的凭据；不要为绕过错误而重新生成它。
- 使用 E2B 时的 `state/e2b/`：Core 清理 E2B 沙箱所需的回执。
- 各节点主机上的状态目录 `/var/lib/oac-node/.oac/nodes/<installation-id>/` 及提供商存储：Docker 卷或 microsandbox 存储。恢复方法见[节点主机故障时](nodes.md#when-a-node-host-fails)。
- 安装所使用的发行包，用于修复同一版本。

不要通过清理 Docker 卷或删除原生 Harness 历史来让重试成功。Session 已删除不证明所有提供商资源已回收。

## 卸载 {#uninstall}

```sh
~/.oac/core/oac uninstall
```

从主机移除安装：Compose 项目及其容器、网络和数据库卷、安装程序加载的镜像，以及安装目录（包含 `secrets/` 和 `oac` 命令本身）。具有其他标签或被其他容器使用的镜像会保留并报告，例如同一版本的其他安装使用的镜像。

全部数据随之删除：Project 和 API 密钥、Session 历史、存储的凭据和 Core 密钥。没有 `secrets/` 的数据库卷无法使用，因此不会单独保留。要保留数据，请用 `oac stop` 停止安装，或先[备份](#back-up)。

命令列出将删除的内容，Core 响应时还列出已注册节点。输入安装目录确认，或传入 `--yes`；无终端执行必须提供该参数。命令持有安装锁，只需要 `state.json`，因此也能移除未完成安装或丢失 `config.json` 的安装。目录最后移除；中途停止时重新执行。

卸载不停止沙箱：节点沙箱在节点继续运行，E2B 沙箱在 E2B 继续运行并计费。Core 仍运行时，归档它们的 Session，或[重置部署](nodes.md#change-the-sandbox-configuration)并等待完成；命令展示 Core 正在使用的沙箱数量。

其他主机上的节点继续运行。按常规方式卸载时，先在 Web 移除，见[移除节点](nodes.md#remove-a-node)。`oac uninstall` 后 Core 已不存在：在各节点主机使用安装时[所用发行包](#installation-version-policy)的 `node-install.pyz`，执行带 `--force` 的节点卸载命令。`oac uninstall` 输出含安装 ID 的命令。

## 安装版本策略 {#installation-version-policy}

安装在整个生命周期使用同一发行版本。不支持原地升级或降级，也不在版本间迁移数据。

迁移到新版本时，安装到全新的空目录，使用独立数据库、Core 密钥和节点，并从新 Web 添加节点。保留旧安装、数据和节点，直到工作完成。节点运行添加它的控制台所提供的程序，不原地升级；Core 仅接受使用自身节点协议的节点。

使用完全相同发行包中的 `./install.sh --install-dir DIR` 修复当前版本；下载程序将包保存在 `~/.oac/releases/`。修复重新加载缺失镜像、恢复 `oac` 命令、应用 `config.json` 并启动服务。它保留身份、设置、密钥和历史，只接受 `--install-dir`，拒绝其他发行版本的包。安装程序从未报告为运行中的安装不会被修复，而会[删除后重新安装](install.md#install)。

安装程序和修改状态的 `oac` 命令共用安装锁 `.oac.lock`，包括修复和中断的 apply 恢复过程。其他命令持有锁时，等待其结束后重试；不要删除或替换 `.oac.lock` 来绕过忙碌安装。重新安装不删除其他安装的文件、数据库、Runtime 资源或 Session 历史。

## 问题排查 {#troubleshooting}

| 症状 | 原因与解决方法 |
| --- | --- |
| `Core installation requires Linux amd64 with Docker access` | 使用 Linux amd64 和有 Docker 访问权限的账号；支持 root 和普通用户 |
| `Installation failed: inspect prerequisites and private deployment files` | 前置条件失败但未单独报告，通常是 Docker：检查此用户能运行 `docker info` 和 `docker compose version` |
| `Docker Compose 2.26.0 or newer is required …` | 更新 Docker Compose 插件 |
| `Port N (…) is already in use on ADDRESS …` | 其他程序占用所需端口。使用输出的 `ss` 命令定位并停止，或选择其他端口：[安装](install-options.md#ports)时传入 `--web-port` 或 `--core-port`，或在 `oac apply` 前修改 `config.json` 端口 |
| `ADDRESS (…) is not an address of this machine …` | 将 `--host` 或 `config.json` 的 `host` 设为机器 IP 地址，或 `0.0.0.0` 等通配地址 |
| `Automatic HTTPS needs ports 80 and 443 …` | 释放提示中的端口，不传 `--public-url` 安装并之后配置域名，或使用 `--ingress external` 和自己的[反向代理](install-options.md#https-and-the-reverse-proxy) |
| `Installation directory is not empty …` | 使用空的 `--install-dir` |
| `This installation is configured by …/config.json …` | 参数只初始化新安装：修改 `config.json` 后运行 `oac apply`。要使用其他参数重新开始，先[卸载](#uninstall) |
| `This installation version is not supported …` | 目标安装状态格式或源码版本与发行包不匹配。保留原安装，在其他空 `--install-dir` 安装（[版本策略](#installation-version-policy)） |
| `generated/<file> was edited by hand` | 将变更移入 `config.json`，再运行 `oac apply --discard-edits` |
| `config.json has changes that are not applied` | 运行 `oac apply` |
| `Core rejects secrets/core.key …` | 运行 `oac apply`，使 Core 使用密钥摘要重启 |
| `config.json not applied: …` | `oac apply` 已在上方输出 Core 启动错误；修复 `config.json` 后重新应用 |
| `The services did not start: …` | 新安装首次启动失败，安装程序[删除了所创建内容](install.md#install)。上方输出 Compose 或 Core 错误；修复后执行同一命令 |
| `Removal did not finish. Left: …` | 清理失败安装的安装程序或 `oac uninstall` 未能删除全部内容。执行输出的命令移除残留，或修复原因后重新执行原命令 |
| `This installation did not finish installing …` | 安装程序在报告服务运行前停止。重新执行安装命令，[清理残留](install.md#install)后重新安装，或[卸载](#uninstall) |
| 域名配置时 `… already in use on this server. Automatic HTTPS cannot run beside another program …` | 其他程序占用 80 或 443。用输出的 `ss` 定位、停止并重试；自动 HTTPS 无法共享[这些端口](install-options.md#ports) |
| 域名配置时 `HTTPS verification failed …` | DNS 指向其他位置、防火墙或 NAT 阻止 80/443 入站，或证书申请失败；参阅[配置域名和 HTTPS](install.md#configure-the-domain-and-https) |
| Web 返回 403 `Forbidden` | 严格使用 `oac status` 输出的控制台地址；反向代理必须传递原始 Host |
| `/v1` 或 `/api/v1` 返回 404 | 路径被发送到 Web；将其路由到 Core（[反向代理](install-options.md#https-and-the-reverse-proxy)） |
| Web 显示 Core 不可用（502） | Core 停止或失败：先 `oac status`，再查看 Core 日志 |
| 创建 Session 返回 400 `model_provider_required` | 缺少模型提供商：为 Harness 设置[默认模型](../configuration.md#default-models)，或显式提供；自托管 Session 始终自带提供商 |
| Add node 不展示命令 | 参阅[添加节点前](nodes.md#before-you-add-a-node) |
| 节点未就绪 | 参阅[节点问题排查](nodes.md#troubleshooting) |

## 对外暴露与网络策略 {#exposure-and-network-policy}

| 监听器 | 托管入口（默认） | 外部入口 |
| --- | --- | --- |
| Web | 仅通过 `gateway` 服务访问，它在 `host`（默认所有 IPv4 接口）发布 `ports.web`（8080），HTTPS 启用后还发布 [80 和 443](install-options.md#ports) | `host:ports.web`（默认回环地址），位于反向代理之后 |
| Core | `127.0.0.1:ports.core`（8091）；网关将 `/v1` 和 `/api/v1` 路由到它 | `host:ports.core`，位于反向代理之后 |
| PostgreSQL | 不发布端口 | 不发布端口 |

Web 使用 Core 密钥让管理员登录，检查每个请求来源，并用保留在服务器上的 Core 密钥将已登录的 `/core/v1` 请求转发到 Core。无论请求携带何种凭据，`/v1` 和 `/api/v1` 均返回 404；Web 仅在 `/node-install/` 提供不含密钥的节点文件，没有 Docker 或 KVM 访问权限。`/api/v1` 机器路由使用独立注册和连接凭据。托管入口中，`installation` 服务通过 Docker 套接字应用域名变更；Web 仅通过私有 Unix 套接字访问它，每次请求都检查 Core 密钥。

沙箱是隔离边界（[Runtime 与外层隔离](../concepts.md#runtime-and-outer-isolation)）。Docker 沙箱共享节点内核，Docker 节点在主机上[等同于 root 权限](nodes.md#what-the-installer-sets-up)；microsandbox 为每个沙箱提供具有显式[网络策略](nodes.md#what-the-installer-sets-up)的 microVM。Core 自身无 Docker 套接字或 KVM 访问权限。
