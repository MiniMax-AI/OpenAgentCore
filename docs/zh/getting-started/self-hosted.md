---
title: "自托管执行器"
source: docs/getting-started/self-hosted.md
source_hash: fc22cdc9db23a43d4c90921a2bb06b16c41d6e7586c3533284aeca2f903a850a
---

`self_hosted` Session 在应用拥有的机器上运行：工作站、虚拟机或你管理的沙箱。应用通过 `/v1` 创建 Session，并获得安装 `oac-daemon`、启动它并连接 Core 的命令。Web 在 Session 页面展示同一命令；Web 是可选的。Core 不创建、停止或回收这台机器。

**守护进程不是沙箱。** 工具以启动守护进程的账号权限运行，能访问该账号可访问的所有资源。需要隔离时，请使用容器或虚拟机；参阅 [Runtime 与外层隔离](../concepts.md#runtime-and-outer-isolation)。守护进程不限制网络访问，因此要求网络策略的 Template 会被自托管 Session 拒绝。

Session 的 Harness 在部署的 agent host 上运行，通过这台机器的 [Sandbox I/O 服务](../sandbox-bootstrap.md)读取文件和运行工具，因此模型密钥不会到达这台机器。机器获得的执行器凭据仅适用于这一个 Environment。

## 平台 {#platforms}

自托管安装仅支持 Linux amd64。在其他平台（包括 macOS、Windows 和 Linux arm64）上，`oac-daemon install` 和 `oac-daemon start` 返回 `UnsupportedPlatformError`；安装在领取凭据之前拒绝执行。

安装程序包含 `oac-daemon` 启动器、`oac-sandbox-io` 及其版本和平台元数据。Harness 及其依赖打包在 agent host 上，自托管机器不安装它们。

机器需要：

- 通过 HTTPS 访问 Core，以及访问发布下载主机；如果 Core 已有安装程序的离线副本，则无需后者。Core 的[公共 URL](../configuration.md#changing-the-public-url) 必须是 https 地址，否则 Core 拒绝该机器的注册；
- 用于环境设置和 MiniMax Code 工具的 Bash；
- Session 的软件包需要时，安装 Python 和 pip；
- 环境设置所需的系统软件包。守护进程不运行 apt、sudo 或其他提权命令，请通过主机的常规管理方式安装。

不需要 Docker。安装程序创建 Session 工作区、`/environment/{workspace,initialization,packages}` 和 `/home/runtime`，并验证当前账号能写入这些目录。如果该账号不能在 `/environment` 和 `/home` 下创建所需目录，管理员必须在安装前准备好这些目录并设置合适的所有权；安装程序不会提权。下载命令还使用 `curl`、`tar`、`gzip`、SHA-256 工具和 `flock`。Runtime 主目录必须允许执行文件。如果挂载点设为 `noexec`，请先用 `OAC_RUNTIME_HOME` 指定另一个允许执行的绝对目录，再运行命令。

## 连接机器 {#connect-a-machine}

1. 在目标机器上选择绝对工作区路径。使用该路径和应用的 Project API 密钥创建 Session：

   ```python
   import os
   from openai import OpenAI

   client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
   session = client.beta.agents.sessions.create(
       environment={
           "type": "self_hosted",
           "workspace_directory": os.environ["EXECUTOR_WORKSPACE"],
       },
       extra_body={
           "agent": {"model": os.environ["MODEL_NAME"], "x_agents_core": {"harness": "codex"}},
           "x_agents_core": {"model_provider": {
               "protocol": "responses",
               "base_url": os.environ["MODEL_BASE_URL"],
               "api_key": os.environ["MODEL_API_KEY"],
           }},
       },
   )
   installation = session.model_dump()["x_agents_core"]["installation"]
   print(installation["commands"]["posix"])
   ```

2. 在目标机器上，以应运行工具的账号执行命令。命令下载与 Core 匹配的安装程序、验证校验和、询问安装目录、安装启动器和 Sandbox I/O 服务、准备所需目录、启动并检查连接。
3. 发送一个 Turn。机器已连接只证明认证成功；第一个 Turn 才会检查 Harness 和模型。

在 Web 中打开 Session，复制 **Connect a host** 下的命令。

请保密该命令：它包含短期[安装授权](../../../contracts/agents-api/zh/environment-executor-credentials.md#installation-grant)，用于领取机器凭据。过期后重新读取 Session，或从 Web 复制新命令。

安装程序报告三个结果：

| 结果 | 含义 |
| --- | --- |
| **Installation** | 启动器和 Sandbox I/O 服务已安装，所需目录可写 |
| **Host connection** | Core 已确认这台机器通过[沙箱 Link](../sandbox-link-protocol.md) 为此 Environment 提供服务 |
| **Model configuration** | 未检查；第一个 Turn 使用 Session 的模型提供商 |

临时网络故障会重试下载，最多尝试三次，并在终端展示进度。下载、解压和复制组件前会检查磁盘空间。如果下载或安装被中断，重新执行命令：它清理未完成的临时副本，同时保留已完成的组件、凭据和工作区。下载暂存位于 Runtime 主目录的 `native-download` 中；小型 `download.lock` 文件保留用于并发控制。其他运行不会清理仍在进行的下载或安装。命令过期时，从 Session 复制新命令。

如果 45 秒内未确认连接，安装程序输出守护进程日志路径。守护进程继续重连。修复原因后，以同一安装目录重新执行安装命令；如果命令已过期，从 Web 复制新的。已完成的组件和凭据会保留，已运行的守护进程会复用。不要删除工作区或 Session 来重试。

### 自动化选项 {#options-for-automation}

在命令后追加这些选项：

| 选项 | 效果 |
| --- | --- |
| `--non-interactive` | 不提示；缺少输入时失败 |
| `--install-dir ABS` | 安装目录。默认是 `~/.oac` 下的 `environments/<environment-id>`；设置了 `OAC_RUNTIME_HOME` 时则在该目录下 |

工作区在创建 Session 时固定。使用不同工作区时，创建另一个 Session。

## 本地能力目录 {#local-capability-directories}

自托管 Session 可以在工作区之外提供 `capability_directories`：

```python
environment = {
    "type": "self_hosted",
    "workspace_directory": os.environ["EXECUTOR_WORKSPACE"],
    "capability_directories": [os.environ["EXECUTOR_CAPABILITIES"]],
}
```

路径必须是绝对路径。在机器连接前，先在机器上准备好这些目录。agent host 上的 Environment owner 通过沙箱 Link 验证并读取它们；指定目录不会挂载它或创建沙箱。

使用 `x_agents_core.environment` 提供与托管 Session 相同的 Project 所属 Skills、Plugin 归档、文件、软件包、设置命令或 Template：

```python
session = client.beta.agents.sessions.create(
    agent_id=agent_id,
    environment=environment,
    extra_body={"x_agents_core": {
        "model_provider": model_provider,
        "environment": {"environment_template_id": template_id},
    }},
)
```

同一扩展也支持 `environment={"type": "openai_hosted"}`。不要在 `environment` 和扩展中重复指定同一个字段。设置过程使用守护进程账号权限。

第一个 Turn 之前，Environment owner 通过沙箱 Link 准备能力快照。即使源内容已修改，重连仍复用快照；新 Session 会创建新快照。[准备契约](../../../contracts/agents-api/zh/environments.md#runtime-capability-preparation)列出字段、合并规则、快照行为和失败结果。

## 管理安装 {#operate-the-installation}

安装的 `bin/oac-daemon` 能定位自己的安装目录。使用它执行：

| 命令 | 效果 |
| --- | --- |
| `oac-daemon start` | 注册机器并在后台启动守护进程。守护进程运行 [Sandbox I/O 服务](../sandbox-bootstrap.md)，服务退出时重新注册并重启它 |
| `oac-daemon logs -n 100`、`oac-daemon logs -f` | 输出或持续跟踪守护进程日志 |
| `oac-daemon stop` | 停止守护进程 |

设置了 `OAC_RUNTIME_HOME` 时，每个命令都使用同一值。在 Web 的 Session 页面 **Host connection** 下检查连接，或使用[连接状态](../../../contracts/agents-api/zh/environment-executor-credentials.md#connection-status)。

停止守护进程、取消 Turn 或删除 Session 不会删除机器的工作区。Harness 原生历史属于 agent host。安装程序拒绝其他守护进程版本的安装，以及文件已被修改的安装。程序不升级、修复或迁移它们；请安装到另一个目录。

## 轮换或撤销 {#rotate-or-revoke}

在 Web 中，Session 的 **Executor credentials** 列出机器凭据：

| 操作 | 效果 |
| --- | --- |
| **Rotate** | 凭据获得新密钥；旧密钥立即失效。已撤销凭据的操作为 **Restore** |
| **Revoke** | 凭据立即失效 |

轮换后重新连接时，通过 `oac-daemon stop` 停止守护进程，用新凭据替换已配置的凭据文件路径中的 JSON，然后运行 `oac-daemon start`。不要再次对已有安装运行 `install`；轮换现有凭据，而非签发第二个凭据，后者[无法连接](../../../contracts/agents-api/zh/environment-executor-credentials.md#revoked-or-rotated-credential)。

已归档 Project 无法签发或轮换凭据，仍可撤销。运维人员可以使用 Core 密钥管理凭据；参阅[凭据协议](../../../contracts/agents-api/zh/environment-executor-credentials.md#core-key-routes)。

## 从已解压的发行包安装 {#install-from-an-extracted-distribution}

同一安装程序也接受已解压的发行包和运维人员签发的凭据文件，无需安装命令：

```sh
./oac-daemon install --non-interactive \
  --install-dir "$HOME/.oac/my-runtime" \
  --remote 'wss://core.example/api/v1/agent-daemon/ws' \
  --environment-id '11111111-2222-4333-8444-555555555555' \
  --workspace "$HOME/workspace" \
  --credential-file "$HOME/executor-credential.json"
"$HOME/.oac/my-runtime/bin/oac-daemon" start
```

使用 Session 的 `remote_url`、Environment ID 和工作区。`--workspace` 在安装时准备该目录；启动器不持久化第二份工作区设置。此模式在运行 `start` 前不会启动。
