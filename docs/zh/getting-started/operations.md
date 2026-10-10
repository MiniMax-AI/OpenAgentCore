---
title: "运维"
source: docs/getting-started/operations.md
source_hash: e92596b9cf193beef1f7f7382c27df4fbbf55c52bdb16a303bf5d7e04e15693b
---

安装运维人员负责 Core 主机、存储和可用性。节点主机运行各自的服务；参阅[节点](nodes.md)。设置见[配置参考](../configuration.md)。

## oac 命令 {#the-oac-command}

每个安装目录都有自己的原生管理命令：Unix 使用 `oac`，Windows 使用 `oac.exe`。命令需要 Docker 访问权限，不需要 root：

```sh
docker compose -f ~/.oac/core/compose.yaml ps
```

| 命令 | 功能 |
| --- | --- |
| `docker compose ps` | 在安装目录中展示服务 |
| `docker compose start` | 启动服务 |
| `docker compose stop` | 停止服务。保留数据、节点和沙箱 |
| `oac apply` | 先运行 `oac-core check-config`，再执行 `docker compose up -d --wait`。校验失败时不改动任何服务 |
| `oac core-key [--show]` | 指出 Core 密钥在数据卷中的位置；加上 `--show` 时打印密钥本身 |
| `oac rotate-core-key` | 替换 Core 密钥并重启 Core 和 Web |
| `docker compose down` | 移除容器。数据保留；要删除数据，请[卸载](#uninstall) |

示例使用默认安装目录。Windows 上使用 `& "$HOME/.oac/core/oac.exe"` 调用管理命令，后接相同参数。使用自定义安装目录时，替换各命令中的路径。

本地凭据和注册解析完成后，Runtime 的 Harness 发现与已认证的引导 HTTP 请求并发执行。两者都成功后才建立连接并发布能力。失败会取消另一项操作并等待其清理。执行器仍由连接生命周期持有。Runtime 启动变更需要重新构建并验证 Runtime 模板。


## Runtime 启动延迟 {#runtime-startup-latency}

daemon 通过 `executor preparation stage` 记录 `stage=workspace`、executor 和 Session ID、毫秒耗时及 `success`。Codex 通过 `codex preparation stage` 记录 `session_plan`、`model_catalog`、`process_spawn`、`rpc_initialize` 和 `verification`，携带所属请求的 trace、耗时及 `success`。这些记录不包含原生错误文本、凭据、配置、模型目录内容或命令输出。未执行的条件阶段表示未观测，不能按零计算。`session_plan` 包含 `model_catalog`；executor 就绪耗时包含工作区准备、adapter 各阶段和传输开销，不应重复相加。失败的 `rpc_initialize` 包含必要的子进程清理。

`model_catalog` 内，`model_catalog_command` 包含原生命令执行、退出和管道排空；只有进程已启动才输出 `model_catalog_cleanup`，它仅测量既有尽力发送的进程组信号。其 `success` 是信号调用的返回结果，进程组已退出也可能为 false；不证明后代已回收，也不改变命令结果。`model_catalog_validation` 包含 JSON、模型、verbosity 及托管 home 校验；`model_catalog_snapshot` 包含快照创建、写入、关闭及绑定 Session plan。失败保持原有拒绝和清理行为。

`rpc_initialize_write`、`rpc_initialize_response` 从同一请求起点累计计时，不能相加，也不是 Core 网络 RTT。仅在匹配响应 ID 时、原生结果解码前记录响应时刻；其 `success` 只表示帧中无原生错误，不代表握手有效。原有 `rpc_initialize` 父阶段记录完整解码握手及失败所需清理。没有匹配响应时不输出响应项。读取协程只捕获时间，不写日志；请求结束后才导出记录，因此日志时间是导出时刻。initialize 的 10 秒期限及 Session 持有的进程生命周期保持不变。

app-server 初始化前仍会校验并固定模型目录。阶段计时用于区分目录准备与原生进程初始化，本身不证明提速。应在相同 Runtime 模板、模型和 Provider 下对比全新及复用 Session，并同时验证持久化回复、用量和首字延迟。Runtime 启动逻辑变更需要重新构建和验收 Runtime 模板；仅替换 Core 不会更新既有沙箱。

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
docker compose -f "$HOME/.oac/core/compose.yaml" logs --timestamps init
```

`init` 服务在初始化完成后退出。其步骤日志见[部署说明](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation)。

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

每个安装有一个管理员凭据，即 Core 密钥。安装程序在 `secrets/web/core.key` 生成以 `oac_admin_` 为前缀、后接 64 个随机小写十六进制字符的密钥。用 `oac core-key --show` 读取；该文件属于容器用户。Core 密钥：

- 用于登录 Web。浏览器获得 HttpOnly 会话 cookie，不持有密钥；
- 通过 `Authorization: Bearer <Core key>` 授权 Core API（`/core/v1`）请求；
- 不授权 Agents API（`/v1`）。应用使用 Project API 密钥，后者也不能调用 `/core/v1`。

请保密。Web 读取 `secrets/web/core.key`。Core 只读取 `secrets/core/core-key-digests.json` 中的 SHA-256。Core 密钥至少 32 字符且不含空白。Web 限制失败登录。

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

它在初始化容器中更新数据卷内的 `secrets/web/core.key` 和 `secrets/core/core-key-digests.json`，然后重启 Core 与 Web。Core 重启后旧密钥立即失效，控制台会话也会结束；请重新登录并更新脚本。

## Project 和 API 密钥 {#projects-and-api-keys}

在 Web 的 **Projects and keys**，或通过 [Core API](#script-the-core-api)创建 Project、签发密钥。Project 和密钥行为见 [Project 拥有资产](../concepts.md#projects-own-assets)。

轮换应用密钥：

1. 在同一 Project 签发新密钥。
2. 更新应用以使用它。
3. **Revoke** 旧密钥。

**Archive** 禁用 Project 的所有密钥并保留资产。

Core 记录每次公开资源写入所使用的密钥；历史保留策略为 [`core.write_audit_retention`](../configuration.md#settings)。

## 备份 {#back-up}

可恢复的备份必须是一组在停止写入期间取得的一致数据。仅数据库转储或 Core 卷导出不包含独立工作区存储及节点计算状态。恢复时使用相同版本；参见[安装版本策略](#installation-version-policy)。

### 备份内容 {#backup-contents}

- Docker 卷 `<project>_data`，包括 `database/`、`secrets/` 和 `state/`。其中保存 Project、密钥摘要、节点、默认模型、加密凭据和执行历史（含大对象）。`secrets/core/credential.key` 必须与匹配的数据库一起保留，否则存储的凭据无法解密。
- 安装目录，包括 `.env`、`compose.yaml`、Compose 覆盖文件和管理命令。
- 当前或保留对象所使用的每个独立工作区命名空间，即使其位于安装卷之外。备份整个根目录，包括 `.oac-storage-root`、对象标识、staging、live 数据、trash 和删除标记。保留数值所有者、权限、链接及扩展属性，包括全部 `user.*` 属性。仅复制 `live/data` 会丢失生命周期证据，并可能使已删除标识复活。[工作区存储](../workspace-provider.md#kernel-nfs-adapter)负责定义布局与所有权要求。
- 各节点状态目录 `/var/lib/oac-node/.oac/nodes/<installation-id>/` 及其 Provider 存储：Docker 卷或 microsandbox 存储。包含数据库仍然引用的全部计算资源和快照。参见[节点主机故障时](./nodes.md#when-a-node-host-fails)。

### 建立停止写入窗口 {#establish-a-stopped-write-window}

1. 在安装入口阻止新应用 input、实时文件访问和管理变更。等待活动执行、初始化、文件操作和原生清理完成。停止其他能够写入同一文件系统的应用或主机进程。
2. 保持 Core 和节点可用，确认每个所属写入方均已停止，且所有 Create、Kill、快照清理及文件系统变更均已结算。节点离线、超时、实例缺失或服务停止均不足以证明这一点。确认挂起会停止源计算资源，但保留的检查点也必须纳入备份。最简单的冷恢复基线是等待符合条件的保留 Session 完成检查点到期清理、allocation 已释放，再按[冷替换契约](../sandbox-provider.md#cold-replacement-after-checkpoint-retention)保留文件系统对象及原生历史。
3. 停止 Core 和 Web，再停止节点控制服务，以防止新的生命周期操作。重新核实精确的原生资源，保持所有写入方停止，直到备份各部分全部完成。**停止 Core 或节点服务不会停止其沙箱。** 节点服务使用 `KillMode=process`；microVM 及其他由 Provider 管理的计算资源可能继续独立运行。
4. 使用存储平台支持的流程将已停止写入的文件系统中的待写数据落盘，并创建其快照或导出。在同一窗口内导出 Core 数据卷、安装目录及所需的节点状态和 Provider 存储。复制 PostgreSQL 的 `database/` 文件前必须停止 PostgreSQL。一起记录版本、备份时间、组成清单和校验值，并将备份保存在被备份安装之外。
5. 所有复制完成后才恢复服务。先恢复存储，再启动 Core 和节点，验证就绪后开放入口。不要通过重放不确定的原生操作来让备份通过。

当前没有一条命令即可完成且无损的维护排空流程。归档和部署重置会改变 Session 生命周期状态，不能替代保持续接能力的备份暂停。任何写入方或变更仍然未知时，应将备份保留为未完成状态，先解决相应归属。

若在停止写入窗口中进行数据库逻辑复制，仅保持 database 服务运行，执行：

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" exec -T database \
  pg_dump -U agents_api agents_api > oac-backup.sql
```

逻辑转储仍然需要匹配的 secrets、state、独立文件系统及所需节点资源。Docker Desktop 的 **Volumes** 导出可用于复制已停止的数据卷，但不会协调这些其他部分。

### 恢复与演练 {#restore-and-rehearse}

恢复期间保持应用入口及 Core/节点服务关闭。暴露恢复后的文件系统前，必须隔离或关闭每一个旧写入方；旧主机不可达不代表已隔离。演练必须使用隔离的安装和存储副本，不能连接原节点，也不能共享其可写命名空间。

启动 Core 前，恢复相匹配的 secrets 和数据库状态、完整的独立文件系统命名空间，以及任何所需的节点标识和 Provider 存储。保留数据库中的配置和命名空间 UUID、对象标识及删除标记；不要对恢复后的存储执行首次命名空间初始化。重新建立配置中的挂载路径和服务 UID，然后从每个参与服务的上下文验证所挂载文件系统、所有权及 xattr。[独立工作区存储](../configuration.md#independent-workspace-storage)负责定义部署设置。

已释放计算资源的冷恢复基线仅在新计算资源准入后续接具备资格的原生历史。包含保留检查点的备份还依赖匹配的原生 Runtime、节点标识、Provider 存储和外部文件系统标识。文件级恢复可能改变 inode 标识，即使文件字节一致，也可能阻止严格检查点恢复。此流程不承诺可移植内存快照，也不承诺恢复到任意替代节点；应保留未解决的归属，而不是替换为新沙箱。

恢复依赖全部完整后才启动 database、Core 和节点服务；检查期间保持入口受限。在隔离演练中，验证保留 Session 的文件和具备资格时在新计算资源上的原生续接，确认已删除对象无法重新打开，并检查旧写入方均无法访问恢复后的命名空间。在依赖备份进行恢复前记录结果。

## 卸载 {#uninstall}

```sh
cd ~/.oac/core
docker compose down --volumes --remove-orphans --rmi all
cd && rm -rf ~/.oac/core
```

`down --volumes` 会删除安装数据卷。之后删除安装目录；Windows 使用 `Remove-Item -Recurse "$HOME/.oac/core"`。

全部数据随之删除：Project 和 API 密钥、Session 历史、存储的凭据和 Core 密钥。要保留数据，请用 `docker compose stop` 停止安装，或先[备份](#back-up)。

卸载不停止沙箱：节点沙箱在节点继续运行，E2B 沙箱在 E2B 继续运行并计费。Core 仍运行时，归档它们的 Session，或[重置部署](nodes.md#change-the-sandbox-configuration)并等待完成；命令展示 Core 正在使用的沙箱数量。

其他主机上的节点继续运行。按常规方式卸载时，先在 Web 移除，见[移除节点](nodes.md#remove-a-node)。安装目录删除后，它们的 Core 已不存在：在各节点主机使用当时发布版的 `node-install.pyz`，执行带 `--force` 的节点卸载命令。安装 ID 在数据卷的 `secrets/core/installation.id` 中。

## 安装版本策略 {#installation-version-policy}

安装在整个生命周期使用同一发行版本。不支持原地升级或降级，也不在版本间迁移数据。

迁移到新版本时，安装到全新的空目录，使用独立数据库、Core 密钥和节点，并从新 Web 添加节点。保留旧安装、数据和节点，直到工作完成。节点运行添加它的控制台所提供的程序，不原地升级；Core 仅接受使用自身节点协议的节点。

中断的安装可以[沿用已保存配置继续](install.md#install)。与本安装无关的非空目录会被拒绝。

安装程序和修改状态的 `oac` 命令共用[安装锁](../configuration.md#installation-directory)。其他命令正在运行时，等待其结束后重试。

## 问题排查 {#troubleshooting}

| 症状 | 原因与解决方法 |
| --- | --- |
| `Port N is already in use.` | 其他程序占用该端口。停止它，或另选 `--web-port`。安装程序不会改用其他端口 |
| `Directory is not a complete Core installation` | 保留目录内容，选择另一个 `--install-dir` |
| `configuration check failed; no service was changed` | `.env` 中有 Core 拒绝的值。消息只包含变量名。修正 `.env` 后再次运行 `oac apply` |
| `Docker Compose 2.26 or newer is required` | 更新 Docker Compose 插件 |
| `Installation and data retained at …` | 查看上方服务日志，修复原因，再[继续安装](install.md#install)；不要删除数据 |
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

## 执行延迟 {#execution-latency}

通过 `environment input reserved` 日志将提交输入的 HTTP Trace 与 `reservation_id`、`execution_trace_id` 关联。worker 从持久化预留标识派生执行诊断 Trace，观察连接断开或 owner 重启都不依赖进程内 Trace 映射。这是 HTTP Trace 与执行 Trace 之间的明确关联，不是原 HTTP span 的延续。沿 `environment input selected`、`execution preparation requested`、`environment input admitted` 将预留与 Session、Environment、device、准备请求、Executor、Turn 关联。`preparation_request_id` 标识控制准备请求，与 HTTP `request_id` 不同。创建 Session 时附带输入的情况，通过相同 `session_id` 和 `is_initial=true` 将 `session initial input origin` 与 worker 的预留关联；普通和流式创建都支持，不增加 store 查询。

`execution stage` 记录 `stage`、`duration_ms` 与 `status`（`ok`、`error`、`cancelled`、`timeout`）。阶段覆盖输入验证/ownership、预留和等待 admission、执行配置与 admission 提升、生命周期 gate 等待、Runtime 观察和供给、provider create/resume、Environment 初始化。Runtime 连接日志记录已确认观察到的连接变化，不是 socket 建立的精确时间。通过 Environment、allocation、node ID 将后台 Runtime 记录与输入关联；缺少输入 Trace 的后台记录不能视为连续的请求 span。阶段耗时使用本进程单调时钟。`queue_age_ms` 则比较选择时间与数据库预留创建时间，可能受到时钟偏差影响，不能当作单独测量的调度等待。

`control_ready_ms` 从调度、Runtime 就绪和配置组装完成后开始。`start_control_ms` 描述控制确认。`input_to_first_text_ms` 从执行投递前开始，包含 start-control 时间，不是纯模型 TTFT。admission、control、首字区间存在重叠，不要当作独立耗时相加。provider 调用成功表示该操作成功，不等于 daemon 连接完成或 Turn 完成。除非原生适配器提供相应观测，模型请求开始、响应 headers、模型首字、重试仍为不可观测。日志仅记录关联 ID 与有限状态分类，不记录输入内容、凭据或 provider 原始错误。

`runtime transport registered` 记录认证后的 WebSocket 注册，`runtime capability snapshot observed` 记录合法能力声明变化及设备 ID、能力数量。连接注册本身不代表执行就绪。包含可用 Harness 的能力变化会发出合并后的调度提示；Worker 仍检查所有权、容量、生命周期和能力要求，轮询负责兜底。

Daemon 启动日志记录 `runtime process starting` 和构建版本，`runtime startup stage` 记录各 Harness 探测、bootstrap 和每次连接尝试的本地单调时钟耗时，不输出凭据或原始错误。探测发生在连接注册之前。结合这些边界与已观察到的持久化连接时间，区分启动、周期观察和调度等待。Daemon 观测要求 Runtime 使用带有这些埋点的构建；仅升级 Core 不会升级现有 Runtime 模板。托管启动回执只确认进程已启动，不确认连接或能力就绪。

Codex CLI 可用性探测输出 `process_spawn`、`process_wait` 的 `runtime version probe` 记录。`first_stdout` 从启动尝试开始，计时到 Go 复制协程观察到第一块非空 stdout；`stdout_to_completion` 从该时刻计时到 `Wait` 返回，包含剩余执行和管道排空。无 stdout 时不输出这两项。记录在完成后导出，不允许提前成功：仍以进程退出和输出校验决定可用性。`runtime version probe resources` 使用 `ProcessState` 返回的 OS 进程统计，输出 `user_cpu_ms`、`system_cpu_ms`；平台可能包含已回收后代，无法取得进程统计时省略。缺页和存储字节计数未采集，缺失表示不可观测，不能按零计算，也不能据此认定存储或网络根因。这些区间均不是模型执行。
