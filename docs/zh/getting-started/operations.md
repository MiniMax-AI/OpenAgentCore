---
title: "管理你的安装"
source: docs/getting-started/operations.md
source_hash: e60a6e96cd61692b6adc7664874ba0ed2ce489982e7da2fe2347631ee2a94c0a
---

安装运维人员负责 Core 主机、存储和可用性。节点主机运行各自的服务；参阅[节点](nodes.md)。设置见[配置参考](../configuration.md)。

## oac 命令 {#the-oac-command}

每个安装目录中都有自己的管理命令，无需发行包或 root：

```sh
docker compose -f ~/.oac/core/compose.yaml ps
```

| 命令 | 功能 |
| --- | --- |
| `docker compose ps` | 在安装目录中展示服务 |
| `docker compose start` | 启动服务 |
| `docker compose stop` | 停止服务。保留数据、节点和沙箱 |
| `oac apply` | 先运行 `oac-core check-config`，再执行 `docker compose up -d --wait`。校验失败时不改动任何服务 |
| `oac core-key [--show]` | 打印 Core 密钥路径；加上 `--show` 时打印密钥本身 |
| `oac rotate-core-key` | 替换 Core 密钥并重启 Core 和 Web |
| `docker compose down` | 移除容器。数据保留；要删除数据，请[卸载](#uninstall) |

第二个安装使用自己的目录，例如 `~/.oac/second`。

## 服务健康状态 {#service-health}

根据不同问题使用这些观察：

| 观察 | 能证明什么 |
| --- | --- |
| `docker compose ps` | 数据库接受就绪检查 |
| Core `/healthz` | Core 进程存活 |
| 经过认证的 API 读取 | 调用者的密钥适用于该资源 |
| Environment 连接 | Runtime 传输已连接 |
| 已完成的 Turn 及结果 | 任务已记录的结果 |

服务健康状态不表示 Harness 或模型可用。执行情况使用 Session、Turn、Items 和 Usage 读取，节点连接、就绪和分配使用 Web 的 **Nodes** 页面。本地诊断使用安装自身的 Compose 文件：

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" ps --all
docker compose -f "$HOME/.oac/core/compose.yaml" logs --tail 200 core
```

不要将 `docker compose config`、`docker inspect` 或原始日志粘贴到公开问题报告。

## 停止与重启 {#stop-and-restart}

计划重启前，先等待活动工作结束：

```sh
docker compose -f ~/.oac/core/compose.yaml stop
docker compose -f ~/.oac/core/compose.yaml start
```

停止 Core 不会停止节点或沙箱。节点服务、microVM 和 Docker 容器继续运行；停止不是回收计算资源的方法。Core 重启不会透明地继续被中断的原生工具调用。重连后查询同一 Session；不要创建新 Session 来重放结果不确定的工作。Session 事件流仅提供实时事件；通过读取 Session、Turn 和 Items 恢复。

Web 重启（包括 `oac apply` 引起的重启）会让所有控制台用户退出登录。其他情况下，Web 登录持续 12 小时。

## Core 密钥 {#core-key}

每个安装有一个管理员凭据，即 Core 密钥。安装程序在 `data/secrets/web/core.key` 生成以 `oac_admin_` 为前缀、后接 64 个随机小写十六进制字符的密钥。用 `oac core-key --show` 读取；该文件属于容器用户。Core 密钥：

- 用于登录 Web。浏览器获得 HttpOnly 会话 cookie，不持有密钥；
- 通过 `Authorization: Bearer <Core key>` 授权 Core API（`/core/v1`）请求；
- 不授权 Agents API（`/v1`）。应用使用 Project API 密钥，后者也不能调用 `/core/v1`。

请保密。Web 读取 `data/secrets/web/core.key`。Core 只读取 `data/secrets/core/core-key-digests.json` 中的 SHA-256。Core 密钥至少 32 字符且不含空白。Web 限制失败登录。

### 用脚本调用 Core API {#script-the-core-api}

Core 不发布主机端口。在 Core 主机上，此辅助函数在 Core 的网络命名空间中运行 `curl`，并通过 stdin 传入密钥，使其不进入命令行：

```sh
core() (  # core METHOD PATH [JSON body]
  cd ~/.oac/core
  ./oac core-key --show | sed 's/^/Authorization: Bearer /' |
    docker run -i --rm --network "container:$(docker compose ps -q core)" curlimages/curl \
      -fsS -X "$1" "http://127.0.0.1:8091/core/v1$2" -H @- -H 'Content-Type: application/json' ${3:+-d "$3"}
)
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

它把新密钥写入 `data/secrets/web/core.key`，重新生成 `data/secrets/core/core-key-digests.json`，并重启 Core 和 Web。Core 重启后旧密钥立即失效，所有控制台会话结束：重新登录并更新脚本。

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
  docker compose -f "$HOME/.oac/core/compose.yaml" exec -T database \
    pg_dump -U agents_api agents_api > oac-backup.sql
  ```

- 安装目录，尤其是 `data/`。`data/secrets/core/credential.key` 必须与数据库一起保留，否则无法解密存储的凭据。

先 `docker compose stop`，打包安装目录，再 `docker compose start`。
- 各节点主机上的状态目录 `/var/lib/oac-node/.oac/nodes/<installation-id>/` 及提供商存储：Docker 卷或 microsandbox 存储。恢复方法见[节点主机故障时](nodes.md#when-a-node-host-fails)。
- 安装所使用的发行包，用于修复同一版本。

不要通过清理 Docker 卷或删除原生 Harness 历史来让重试成功。Session 已删除不证明所有提供商资源已回收。

## 卸载 {#uninstall}

```sh
cd ~/.oac/core
docker compose down --remove-orphans
docker compose run --rm --no-deps --entrypoint find init /data -mindepth 1 -delete
docker compose down --rmi all
cd && rm -rf ~/.oac/core
```

`data/` 归容器所有，因此由 `init` 镜像删除其内容；随后 `down --rmi all` 移除镜像，`rm` 删除安装目录。只有确定要删数据时才执行这些命令。

全部数据随之删除：Project 和 API 密钥、Session 历史、存储的凭据和 Core 密钥。要保留数据，请用 `docker compose stop` 停止安装，或先[备份](#back-up)。

卸载不停止沙箱：节点沙箱在节点继续运行，E2B 沙箱在 E2B 继续运行并计费。Core 仍运行时，归档它们的 Session，或[重置部署](nodes.md#change-the-sandbox-configuration)并等待完成；命令展示 Core 正在使用的沙箱数量。

其他主机上的节点继续运行。按常规方式卸载时，先在 Web 移除，见[移除节点](nodes.md#remove-a-node)。安装目录删除后，它们的 Core 已不存在：在各节点主机使用当时发布版的 `node-install.pyz`，执行带 `--force` 的节点卸载命令。安装 ID 在 `data/secrets/core/installation.id`。

## 安装版本策略 {#installation-version-policy}

安装在整个生命周期使用同一发行版本。不支持原地升级或降级，也不在版本间迁移数据。

迁移到新版本时，安装到全新的空目录，使用独立数据库、Core 密钥和节点，并从新 Web 添加节点。保留旧安装、数据和节点，直到工作完成。节点运行添加它的控制台所提供的程序，不原地升级；Core 仅接受使用自身节点协议的节点。

`install.sh` 拒绝非空目录。首次启动失败时，它删除自己创建的目录，同一命令可以再运行。已经启动的安装会保留。

会修改状态的 `oac` 命令持有 `.oac.lock`。其他命令持有锁时，等待其结束后重试。不要删除 `.oac.lock` 来绕过忙碌安装。

## 问题排查 {#troubleshooting}

| 症状 | 原因与解决方法 |
| --- | --- |
| `Core installation requires Linux amd64 with Docker access` | 使用 Linux amd64 和有 Docker 访问权限的账号；支持 root 和普通用户 |
| `Installation failed: inspect prerequisites and private deployment files` | 前置条件失败但未单独报告，通常是 Docker：检查此用户能运行 `docker info` 和 `docker compose version` |
| `Docker Compose 2.26.0 or newer is required …` | 更新 Docker Compose 插件 |
| `Port N is already in use.` | 其他程序占用该端口。停止它，或另选 `--web-port`。安装程序不会改用其他端口 |
| `Installation directory is not empty` | 使用空的 `--install-dir`，或先[卸载](#uninstall)现有安装 |
| `configuration check failed; no service was changed` | `.env` 中有 Core 拒绝的值。消息只包含变量名。修正 `.env` 后再次运行 `oac apply` |
| `Docker Compose 2.26 or newer is required` | 更新 Docker Compose 插件 |
| `The services did not start: …` | 新安装首次启动失败，安装程序[删除了所创建内容](install.md#install)。上方输出 Compose 或 Core 错误；修复后执行同一命令 |
| `Removal did not finish. Left: …` | 首次启动失败后没能删干净。执行输出的命令移除残留，或修复原因后重新执行原命令 |
| `This installation did not finish installing …` | 安装程序在报告服务运行前停止。重新执行安装命令，[清理残留](install.md#install)后重新安装，或[卸载](#uninstall) |
| Web 返回 403 `Forbidden` | 浏览器主机名不是 `OAC_PUBLIC_URL`。打开该源地址；反向代理必须传递原始 Host |
| Web 显示 Core 不可用（502） | Core 停止或失败：先 `docker compose ps`，再查看 Core 日志 |
| 创建 Session 返回 400 `model_provider_required` | 缺少模型提供商：为 Harness 设置[默认模型](../configuration.md#default-models)，或显式提供；自托管 Session 始终自带提供商 |
| Add node 不展示命令 | 参阅[添加节点前](nodes.md#before-you-add-a-node) |
| 节点未就绪 | 参阅[节点问题排查](nodes.md#troubleshooting) |

## 对外暴露与网络策略 {#exposure-and-network-policy}

| 监听器 | 主机安装 | 反向代理之后 |
| --- | --- | --- |
| Web 和 API | Web 在 `OAC_HOST` 上发布 `OAC_WEB_PORT`（8080） | Web 在 `OAC_HOST` 上发布 `OAC_WEB_PORT`。反向代理应使用 `127.0.0.1` |
| Core | 不发布端口。Web 转发 `/v1`、`/api/v1` 和 `/docs` | 不发布端口。Web 转发 `/v1`、`/api/v1` 和 `/docs` |
| PostgreSQL | 不发布端口 | 不发布端口 |

Web 使用 Core 密钥让管理员登录，检查每个请求来源，并用保留在服务器上的 Core 密钥将已登录的 `/core/v1` 请求转发到 Core。它把 `/v1` 和 `/api/v1` 原样转发给 Core，使用调用方自己的凭据；Web 仅在 `/node-install/` 提供不含密钥的节点文件，没有 Docker 或 KVM 访问权限。`/api/v1` 机器路由使用独立注册和连接凭据。没有服务持有 Docker 套接字。

沙箱是隔离边界（[Runtime 与外层隔离](../concepts.md#runtime-and-outer-isolation)）。Docker 沙箱共享节点内核，Docker 节点在主机上[等同于 root 权限](nodes.md#what-the-installer-sets-up)；microsandbox 为每个沙箱提供具有显式[网络策略](nodes.md#what-the-installer-sets-up)的 microVM。Core 自身无 Docker 套接字或 KVM 访问权限。
