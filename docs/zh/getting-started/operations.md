---
title: "运维"
source: docs/getting-started/operations.md
source_hash: 54c6d2de28e483497c00a3f2bfc310b4d8b7ff22519607778e7340ec851f252e
---

Core 安装运维人员的日常任务：`oac` 命令、服务健康状态、Core 密钥、备份、升级和卸载。节点有[专门的指南](./nodes.md)，[故障排查](./troubleshooting.md)列出了安装程序和访问方面的提示信息。设置见[配置参考](../configuration.md)。

## oac 命令 {#the-oac-command}

每个安装目录都有自己的 `oac` 命令（Windows 为 `oac.exe`）。以安装 Core 的账号运行它；它需要 Docker 访问权限，不需要 root。示例使用默认安装目录 `~/.oac/core`；Windows 上使用 `& "$HOME/.oac/core/oac.exe"` 调用该命令，后接相同参数。使用自定义目录时，替换路径。

| 命令 | 功能 |
| --- | --- |
| `oac install` | 安装、继续或[升级](#upgrade)安装；安装脚本会运行它 |
| `oac apply` | 检查数据卷和 `.env`，然后启动服务，并重新创建配置有变化的服务；参见 [oac apply 的工作方式](../configuration.md#how-oac-apply-works)。校验失败时不改动任何服务。成功时打印 `Services are running with the current settings.` |
| `oac core-key [--show]` | 指出 Core 密钥在数据卷中的位置；加上 `--show` 时打印密钥本身 |
| `oac rotate-core-key` | 替换 Core 密钥并重启 Core 和 Web，然后说明如何读取新密钥 |
| `oac uninstall` | 确认后删除安装及其数据；参见[卸载](#uninstall) |

Docker Compose 命令管理服务；针对该安装的 Compose 文件运行它们。`stop` 和 `start` 见[停止与重启](#stop-and-restart)。

| 命令 | 功能 |
| --- | --- |
| `docker compose -f ~/.oac/core/compose.yaml ps` | 展示服务 |
| `docker compose -f ~/.oac/core/compose.yaml logs --tail 200 core` | 展示服务的日志 |
| `docker compose -f ~/.oac/core/compose.yaml down` | 移除容器。数据保留 |

## 服务健康状态 {#service-health}

根据不同问题使用这些观察：

| 观察 | 能证明什么 |
| --- | --- |
| `docker compose ps` | 数据库接受就绪检查，Core 和 Web 各自响应自己的 `/healthz` |
| Core `/healthz` | Core 进程存活 |
| 经过认证的 API 读取 | 调用者的密钥适用于该资源 |
| Environment 连接 | Runtime 传输已连接 |
| 已完成的 Turn 及结果 | 任务已记录的结果 |

只有 Web 是监听器；参阅[配置公开地址](./public-address.md#listeners-and-access)。服务健康状态不表示 Harness 或模型可用。执行情况使用 Session、Turn、Items 和 Usage 读取，节点连接、就绪和分配使用 Web 的 **节点** 页面。本地诊断使用安装自身的 Compose 文件：

```sh
docker compose -f "$HOME/.oac/core/compose.yaml" ps --all
docker compose -f "$HOME/.oac/core/compose.yaml" logs --tail 200 core
docker compose -f "$HOME/.oac/core/compose.yaml" logs --timestamps init
```

`init` 服务在初始化完成后退出。其步骤日志见[部署说明](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation)。

不要将 `docker compose config`、`docker inspect` 或原始日志粘贴到公开问题报告。

## 请求诊断 {#request-diagnosis}

打开 **监控 → 请求日志** 查看近期请求，并复制请求 ID 与 Core 服务日志关联。[请求日志](../../../contracts/agents-api/zh/admin-api.md#request-log)定义范围、隐私及有限的内存保留方式。请求接受了异步 Turn 时，请到 **Session 日志** 查看执行结果；HTTP 接受不等于任务成功。跨重启或 Core 实例的留存使用已有日志采集系统。

## 停止与重启 {#stop-and-restart}

计划重启前，先等待活动工作结束：

```sh
docker compose -f ~/.oac/core/compose.yaml stop
docker compose -f ~/.oac/core/compose.yaml start
```

停止 Core 不会停止节点或沙箱。节点服务、microVM 和 Docker 容器继续运行；停止不是回收计算资源的方法。Core 重启不会透明地继续被中断的原生工具调用。重连后查询同一 Session；不要创建新 Session 来重放结果不确定的工作。Session 事件流仅提供实时事件；通过读取 Session、Turn 和 Items 恢复。

Web 重启（包括 `oac apply` 引起的重启）会让所有控制台用户退出登录；参阅[登录](../web/console-server.md#sign-in)。

## Core 密钥 {#core-key}

每个安装有一个管理员凭据，即 Core 密钥。安装程序在 `secrets/web/core.key` 生成以 `oac_admin_` 为前缀、后接 64 个随机小写十六进制字符的密钥。用 `oac core-key --show` 读取；该文件属于容器用户。Core 密钥：

- 用于登录 Web。浏览器获得 HttpOnly 会话 cookie，不持有密钥；
- 通过 `Authorization: Bearer <Core key>` 授权 Core API（`/core/v1`）请求；
- 不授权 Agents API（`/v1`）。应用使用项目 API 密钥，后者也不能调用 `/core/v1`。

请保密。Web 读取 `secrets/web/core.key`。Core 只读取 `secrets/core/core-key-digests.json` 中的 SHA-256。Core 密钥至少 32 字符且不含空白。Web 限制失败登录。

### 用脚本调用 Core API {#script-the-core-api}

在 Core 主机上，此辅助函数在 Core 的网络命名空间中运行 `curl`，并通过 stdin 传入密钥，使其不进入命令行：

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
| 列出项目 | `core GET /projects` |
| 创建项目 | `core POST /projects '{"name": "billing-bot"}'` |
| 签发 API 密钥（仅显示一次，字段为 `key`） | `core POST /projects/$PROJECT_ID/keys '{"name": "prod"}'` |
| 撤销密钥 | `core DELETE /projects/$PROJECT_ID/keys/$KEY_ID` |
| 归档项目（撤销全部密钥） | `core POST /projects/$PROJECT_ID/archive` |
| 查看 Harness 及默认模型 | `core GET /harnesses` |
| 设置 Codex 默认模型 | `core PUT /harnesses/codex/model-configuration '{"model": "your-model-id", "model_provider": {"protocol": "responses", "base_url": "https://provider.example/v1", "api_key": "sk-..."}}'` |
| 安装信息，包括 API 基础 URL | `core GET /installation` |

[Core 管理 API](../../../contracts/agents-api/zh/admin-api.md)列出全部路由；错误使用 [Core 错误封装](../../../contracts/agents-api/zh/core-errors.md)。

### 轮换 Core 密钥 {#rotate-the-core-key}

```sh
~/.oac/core/oac rotate-core-key
```

它在初始化容器中更新数据卷内的 `secrets/web/core.key` 和 `secrets/core/core-key-digests.json`，然后重启 Core 与 Web。Core 重启后旧密钥立即失效，控制台会话也会结束；请重新登录并更新脚本。

## 项目和 API 密钥 {#projects-and-api-keys}

在 Web 的 **项目与密钥**，或通过 [Core API](#script-the-core-api)创建项目、签发密钥。项目和密钥行为见 [项目拥有资产](../concepts.md#projects-own-assets)。

轮换应用密钥：

1. 在同一项目签发新密钥。
2. 更新应用以使用它。
3. **撤销** 旧密钥。

**归档** 禁用项目的所有密钥并保留资产。

Core 记录每次公开资源写入所使用的密钥；历史保留策略为 [`OAC_WRITE_AUDIT_RETENTION`](../configuration.md#settings)。

## 备份 {#back-up}

可恢复的备份必须是一组在停止写入期间取得的一致数据。仅数据库转储或 Core 卷导出不包含独立工作区存储及节点计算状态。恢复数据卷时，同时恢复同一份备份中的安装目录，其中保存着写入该数据的版本；之后可以用更新的版本[升级](#upgrade)恢复后的安装。

### 单机备份与恢复 {#single-host-backup}

如果安装的全部数据都在其 Docker 数据卷中，没有独立工作区存储，也没有需要保留的节点状态，那么数据卷加安装目录就是完整备份。以下流程使用 Unix shell，以及该安装已拉取的 PostgreSQL 镜像。

```sh
cd ~/.oac/core
docker compose stop
project=$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' .env)
image=$(docker compose images --quiet database)
mkdir -p ~/oac-backup
docker run --rm -v "${project}_data:/data:ro" -v ~/oac-backup:/backup --entrypoint tar "$image" czf /backup/data.tar.gz -C /data .
cp -a ~/.oac/core ~/oac-backup/installation
docker compose start
```

要在本机或另一台主机上恢复，先[卸载](#uninstall)仍在使用该项目的安装，然后：

```sh
cp -a ~/oac-backup/installation ~/.oac/core
cd ~/.oac/core
project=$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' .env)
docker compose pull
docker compose up --no-start
image=$(docker compose images --quiet database)
docker run --rm -v "${project}_data:/data" -v ~/oac-backup:/backup:ro --entrypoint tar "$image" xzf /backup/data.tar.gz -C /data
./oac apply
```

带有独立工作区存储或节点的安装需要下面的完整流程。

### 备份内容 {#backup-contents}

- Docker 卷 `<project>_data`，包括 `database/`、`secrets/` 和 `state/`。其中保存项目、密钥摘要、节点、默认模型、加密凭据和执行历史（含大对象）。`secrets/core/credential.key` 必须与匹配的数据库一起保留，否则存储的凭据无法解密。
- 安装目录，包括 `.env`、`compose.yaml`、Compose 覆盖文件和管理命令。
- 当前或保留对象所使用的每个独立工作区命名空间，即使其位于安装卷之外。备份整个根目录，包括 `.oac-storage-root`、对象标识、staging、live 数据、trash 和删除标记。保留数值所有者、权限、链接及扩展属性，包括全部 `user.*` 属性。仅复制 `live/data` 会丢失生命周期证据，并可能使已删除标识复活。[工作区存储](../workspace-provider.md#kernel-nfs-adapter)负责定义布局与所有权要求。
- 对于[配额模式](../workspace-provider.md#quota-mode)命名空间，将承载导出的整个 XFS 文件系统作为一份块级副本备份：存储或 LVM 快照，或已冻结或已卸载设备的镜像。只有这样的副本能保持 project ID、配额限制、inode 标识、文件系统 UUID 和 `oac-workspace-quota` 日志库的一致。文件级复制、`tar`、`rsync` 或 `xfsdump` 会丢失这些信息；此时该命令会拒绝恢复后的对象，而不是在无上限状态下提供它们。
- 各节点状态目录 `/var/lib/oac-node/.oac/nodes/<installation-id>/` 及其提供商存储：Docker 卷或 microsandbox 存储。包含数据库仍然引用的全部计算资源和快照。参见[节点主机故障时](./nodes.md#when-a-node-host-fails)。

- 每个私有 `checkpoint_root` 的全部内容，包括 `.oac-checkpoint-store`、归档、operation journal、lock 文件与 tombstone，并与匹配的 Core 数据库一起保留。它独立于原生 SDK store 和工作区 namespace。保留其身份和私有所有权；[检查点转移契约](../sandbox-provider.md#checkpoint-transfer)定义其生命周期。

### 建立停止写入窗口 {#establish-a-stopped-write-window}

1. 在安装入口阻止新应用 input、实时文件访问和管理变更。等待活动执行、初始化、文件操作和原生清理完成。停止其他能够写入同一文件系统的应用或主机进程。
2. 保持 Core 和节点可用，确认每个所属写入方均已停止，且所有 Create、Kill、快照清理及文件系统变更均已结算。节点离线、超时、实例缺失或服务停止均不足以证明这一点。确认挂起会停止源计算资源，但保留的检查点也必须纳入备份。最简单的冷恢复基线是等待符合条件的保留 Session 完成检查点到期清理、allocation 已释放，再按[冷替换契约](../sandbox-provider.md#cold-replacement-after-checkpoint-retention)保留文件系统对象及原生历史。
3. 停止 Core 和 Web，再停止节点控制服务，以防止新的生命周期操作。重新核实精确的原生资源，保持所有写入方停止，直到备份各部分全部完成。**停止 Core 或节点服务不会停止其沙箱。** 节点服务使用 `KillMode=process`；microVM 及其他由提供商管理的计算资源可能继续独立运行。
4. 使用存储平台支持的流程将已停止写入的文件系统中的待写数据落盘，并创建其快照或导出。对于配额模式，在复制其设备期间使用 `xfs_freeze -f` 冻结 XFS 文件系统或将其卸载，完成后解冻。在同一窗口内导出 Core 数据卷、安装目录及所需的节点状态和提供商存储。复制 PostgreSQL 的 `database/` 文件前必须停止 PostgreSQL。一起记录版本、备份时间、组成清单和校验值，并将备份保存在被备份安装之外。
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

启动 Core 前，恢复相匹配的 secrets 和数据库状态、完整的独立文件系统命名空间，以及任何所需的节点标识和提供商存储。保留数据库中的配置和命名空间 UUID、对象标识及删除标记；不要对恢复后的存储执行首次命名空间初始化。恢复配额模式文件系统时，将其块级副本写入后备设备，并以 `prjquota` 挂载到配置的挂载点；绝不要对其运行 `oac-workspace-quota init`，也不要将副本与原文件系统同时挂载，二者具有相同的文件系统 UUID。重新建立配置中的挂载路径和服务 UID，然后从每个参与服务的上下文验证所挂载文件系统、所有权及 xattr。[设置工作区存储](./workspace-storage.md)负责定义部署设置。

已释放计算资源的冷恢复基线仅在新计算资源准入后续接具备资格的原生历史。包含保留检查点的备份还依赖匹配的原生 Runtime、节点标识、提供商存储和外部文件系统标识。文件级恢复可能改变 inode 标识，即使文件字节一致，也可能阻止严格检查点恢复。此流程不承诺可移植内存快照，也不承诺恢复到任意替代节点；应保留未解决的归属，而不是替换为新沙箱。

恢复依赖全部完整后才启动 database、Core 和节点服务；检查期间保持入口受限。在隔离演练中，验证保留 Session 的文件和具备资格时在新计算资源上的原生续接，确认已删除对象无法重新打开，并检查旧写入方均无法访问恢复后的命名空间。在依赖备份进行恢复前记录结果。

## 升级 {#upgrade}

v0.0.10 及更高版本的安装可以原地升级到更新的发布版本，并保留数据库、数据卷、`.env`、密钥、Core 密钥、安装 ID 以及你在 `compose.override.yaml` 中添加的条目。更早的安装无法升级：请将新版本安装到新目录。降级会被拒绝。

> [!IMPORTANT]
> 升级之前，阅读目标版本的发布说明，了解[协议变更](#protocol-changes)及其要求的操作，并[备份](#single-host-backup)安装。升级不会备份；升级之后，备份是回到先前版本的唯一途径。

1. 使用目标版本的安装程序，带上 `--upgrade` 和安装目录运行：

   ```sh
   curl -fsSL https://openagentcore.dev/install.sh | bash -s -- --upgrade --install-dir "$HOME/.oac/core"
   ```

   `--version TAG` 选择最新版本以外的发布版本。Windows 上运行 `& ([scriptblock]::Create((irm https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download/install.ps1))) --upgrade --install-dir "$HOME\.oac\core"`。对于从[源码包](../development.md#install-a-checkout-without-publishing-a-release)安装的实例，运行新源码包的安装程序：`python3 <bundle>/install.py --install-dir <dir> --upgrade`。
2. 准备好后，[应用该版本的 Runtime](#runtime-after-an-upgrade)。

不带 `--upgrade` 时，其他版本的安装程序不做任何更改，并以 `This installation runs release <old>; this installer is <new>. Back up the installation (Upgrade in the operations guide), then rerun with --upgrade.` 停止（`install.py` 的提示为 `this bundle is`）。无论是否带该参数，已安装版本的安装程序都会继续该安装。安装程序和修改状态的 `oac` 命令共用[安装锁](../configuration.md#installation-directory)；其他命令正在运行时，等待其结束后重试。

### 升级会做什么 {#what-the-upgrade-does}

安装程序下载该版本的 `compose.yaml` 并拉取缺失的镜像。替换任何文件之前，它使用安装的 `.env` 和 Compose 覆盖文件运行新版本的配置检查和初始化。初始化会升级数据卷，或拒绝降级。随后安装程序将新的 `compose.yaml` 和 `oac` 放到位并启动服务，重新创建有变化的服务；Core 启动时迁移数据库。[部署](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md#installation)说明了每个步骤。这次重启对工作的影响与任何一次 [Core 重启](#stop-and-restart)相同；节点和沙箱继续运行并重新连接。

成功时，安装程序打印控制台地址、用 `oac core-key --show` 代替密钥，以及一行 `Upgraded from <old> to <new>. Settings → Sandbox and scheduling offers this release's Runtime; sandboxes keep their current Runtime until you apply it.`

如果某项检查失败，安装程序会打印 `Upgrade stopped; the running services are unchanged` 及 Docker 的输出，先前版本继续提供服务。修复原因后重新运行命令。初始化升级数据卷后，先前版本便无法再在该数据卷上启动：如果后续步骤失败（例如出现 `startup failed; data retained, rerun the installer`），重新运行同一命令，它会安全地重复每个步骤；也可以[回滚](#roll-back)。

### 升级后的 Runtime {#runtime-after-an-upgrade}

升级不会改变沙箱配置：节点继续服务已保存的 Runtime，新 Session 也使用它。要切换到该版本的 Runtime，打开 **设置** → **沙箱与调度**，在 **Runtime** 行选择 **使用本安装的 Runtime**，确认更改后选择 **保存配置**。节点随后准备新代次，现有沙箱保留原代次，直到被释放；参阅[修改沙箱配置](nodes.md#change-the-sandbox-configuration)。E2B 部署没有 **Runtime** 行；除非该版本更改了 Core–Runtime 协议，其模板继续可用。

### 协议变更 {#protocol-changes}

以下三种连接要求两端的协议版本相同：Core 与节点、节点与 microsandbox 辅助程序、Core 与 Runtime。升级不会改动任何节点主机或正在运行的沙箱。更改其中某个协议的版本会在发布说明中注明，并指出需要以下哪些操作：

- 重新安装每个节点：[移除节点](nodes.md#remove-a-node)，清理主机，再通过 **添加节点** 重新添加该主机。Core 拒绝移除仍持有沙箱的节点，所以升级前先归档这些 Session；
- 升级后立即应用该版本的 Runtime；仍在较早代次上的沙箱将停止工作；
- [构建新的 E2B 模板](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md#build-a-template)，并在[沙箱配置](nodes.md#change-the-sandbox-configuration)中选择它；
- 重新连接[自托管机器](self-hosted.md#reconnect-an-existing-session)。

### 降级 {#downgrades}

初始化会拒绝不比已安装版本更新的版本，并提示 `This installation runs release <installed> (<date>); this release, <own> (<date>), is not newer. Downgrades are not supported: run that release or a newer one, or restore the backup taken before the upgrade.` 安装程序将其报告为已停止的升级，安装继续运行。Core 会拒绝由更新版本迁移过的数据库，并提示 `Agents API database schema version N is newer than this Core supports (M). Downgrades are not supported: run the release that upgraded this database or a newer one, or restore the backup taken before the upgrade.` 要回到较早的版本，请[回滚](#roll-back)。

### 回滚 {#roll-back}

回滚即恢复升级前的备份：停止服务，[恢复](#restore-and-rehearse)数据卷和安装目录，然后启动服务。尽量在应用该版本的 Runtime 之前恢复：节点准备好它之后，恢复的数据库并不知道节点的新代次，因此还需[移除每个节点](nodes.md#remove-a-node)并重新添加。备份之后创建的 Session 和数据会丢失。

## 卸载 {#uninstall}

先移除节点，参见[移除节点](./nodes.md#remove-a-node)。

```sh
~/.oac/core/oac uninstall
```

Windows 上运行 `& "$HOME/.oac/core/oac.exe" uninstall`。命令会展示 Compose 项目、数据卷、安装目录和安装 ID，在你输入目录名或 `yes` 之前不会删除任何内容。随后它删除容器、网络和数据卷、安装目录以及[安装锁](../configuration.md#installation-directory)（`~/.oac/core.lock`）。Windows 上正在运行的 `oac.exe` 无法删除自身：命令会打印删除其余内容的 `Remove-Item`，在同一目录重新安装之前先运行它。它保留 Docker 镜像，重新安装时不必再次下载，并打印删除镜像的 `docker image rm` 命令。

全部数据随之删除：项目和 API 密钥、Session 历史、存储的凭据和 Core 密钥。要保留数据，请用 `docker compose stop` 停止安装，或先[备份](#back-up)。

卸载不停止沙箱：节点沙箱在节点继续运行，E2B 沙箱在 E2B 继续运行并计费。Core 仍运行时，归档它们的 Session，或[重置部署](nodes.md#change-the-sandbox-configuration)并等待完成。

如果 Core 已不存在，按那里的说明在各节点主机上带 `--force` 运行节点卸载命令，使用 `oac uninstall` 打印的安装 ID，或节点单元名 `oac-node-<installation-id>.service` 中的安装 ID。

如果安装目录被手动删除，其容器会继续运行并占用 Web 端口。不依赖任何安装文件即可找到并移除该项目：

```sh
docker compose ls --all
docker compose -p <project> down --volumes --remove-orphans
```

项目是列表中的 `oac-…` 名称。之后如果 `~/.oac/core.lock` 仍然存在（Windows 上还有 `~/.oac/.core.lock.runtime.lock`），将其删除。
