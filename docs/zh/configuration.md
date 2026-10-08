---
title: "配置参考"
source: docs/configuration.md
source_hash: 879929d69336b8212a8421d604b18076325afcad5ff336e34aa0aed8a0d1fb8c
---

Core 安装的每项设置都恰好只有一个归属位置，分属以下三类：

| 类别 | 示例 | 归属位置 | 修改方式 | 生效方式 |
| --- | --- | --- | --- | --- |
| [进程设置](#process-settings) | 公共 URL、端口、日志、Harness、执行并发度、审计保留期、OAuth 来源、Runtime 历史记录 | 安装目录中的 `.env`（默认 `~/.oac/core`） | 编辑 `.env`，然后运行 `oac apply` | `oac apply` 会重新创建读取了这些已更改设置的服务 |
| [机密信息](#compose-installations) | 数据库密码、凭据加密密钥、安装 ID、agent-host 身份、Core 密钥及由其派生的 Core 密钥摘要 | Compose 数据卷中的 `secrets/`，每项一份 | 初始化时一次性生成；`oac rotate-core-key` 替换 Core 密钥及其摘要 | `oac rotate-core-key` 会重启 Core 和 Web |
| [运行时设置](#runtime-settings-web) | 沙箱后端和大小、节点、项目和密钥、默认模型、执行器凭据 | Core 的 PostgreSQL 数据库 | 在 Web 中修改，或使用 Core 密钥调用 Core API（`/core/v1`） | 保存时无需重启 Core；节点会异步准备 Runtime 变更 |

Web 的 **System** 页面显示该安装的地址、默认模型和沙箱配置，并在 **Startup settings** 下以只读方式显示 Core 加载的进程设置。没有任何配置文件定义项目或 API 密钥。

## 进程设置 {#process-settings}

[安装选项](getting-started/install-options.md)中的安装标志只会一次性写入 `.env`。要更改设置，请编辑 `.env` 并应用：

```sh
~/.oac/core/oac apply
```

### oac apply 的工作方式 {#how-oac-apply-works}

1. 它用你改过的 `.env` 运行 `oac-core check-config`。值无效时什么都不改。
2. 它运行 `docker compose up -d --wait`。Compose 只重新创建配置有变化的服务。

用 `docker compose ps` 检查服务。重启会中断哪些操作，见[停止和重启](getting-started/operations.md#stop-and-restart)。

### 更改公共 URL {#changing-the-public-url}

`OAC_PUBLIC_URL` 是应用、节点、沙箱和自托管执行器使用的唯一源地址。Core 从中派生守护进程 WebSocket URL、沙箱 Link URL、自托管 `remote_url` 和每个沙箱的连接地址。它是 http 或 https 源地址，也就是浏览器和节点使用的地址。安装通过 `OAC_WEB_PORT` 以 HTTP 提供 Web；前面有反向代理或托管平台时，由它们终止 HTTPS。

源地址为 https 时，沙箱和 agent host 通过 `wss://<origin>/api/v1/sandbox-link` 连接[沙箱 Link](./sandbox-link-protocol.md)。`localhost` 或回环地址上的 http 源地址得到 `ws://<origin>/api/v1/sandbox-link`，只有 Core 自身网络命名空间内的 peer（例如同机的 agent host）能访问。其他主机上的 http 源地址没有 Link URL。托管沙箱运行在 Core 的网络命名空间之外，因此只有源地址是非回环主机上的 https 地址时，Core 才会选择和准入托管沙箱。自托管机器从自己的主机连接 Link，因此只有源地址是 https 时，Core 才会[登记](../../contracts/agents-api/zh/machine-api.md#enroll-a-self-hosted-daemon)这类机器。

要更改它，先把反向代理指向新地址，然后编辑 `OAC_PUBLIC_URL` 并运行 `oac apply`。之后：

- 使用旧地址的节点不会再获得新沙箱：请在 Web 中移除这些节点，然后重新添加。
- 只有在旧地址仍可访问此 Core 时，现有沙箱和执行器才会继续工作。
- 自托管执行器必须使用新的 `remote_url` 重启，并且其安装程序会拒绝为旧地址进行的安装：请创建新的自托管 Session，然后重新连接其主机。

### 设置 {#settings}

模型提供商不属于进程设置；请参阅[默认模型](#default-models)。

以下配置参考表保留英文原文。

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | 必填；`compose.yaml` 设为 `http://localhost:8080` | 应用、节点、沙箱和自托管执行器使用的源地址。参阅[更改公共 URL](#changing-the-public-url) |
| `OAC_HOST` | `127.0.0.1` | `compose.yaml` 发布的 Web 绑定地址。安装器设置为 `0.0.0.0` |
| `OAC_WEB_PORT` | `8080` | Host port of Web |
| `OAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `OAC_LOG_FORMAT` | `auto` | `auto`, `text` or `json` |
| `OAC_LOG_ADD_SOURCE` | unset | `1` adds source locations |
| `OAC_EXECUTION_CONCURRENCY` | `4` | Concurrent execution work, from 1 to 1024 |
| `OAC_DEFAULT_HARNESS` | `codex` | Harness used when a request does not name one |
| `OAC_HARNESSES` | Every registered Harness | Comma-separated Harnesses to enable besides the default one. Unknown names stop startup |
| `OAC_WRITE_AUDIT_RETENTION` | `2160h` | Minimum `1h` |
| `OAC_OAUTH_TRUSTED_ORIGINS` | unset | Comma-separated HTTPS origins |
| `OAC_HISTORY_SETTINGS_FILE` | unset | 可选的 [Runtime 历史文件](#runtime-history-file)。敏感；Core 只报告它是否已配置 |

未设置或为空的值使用默认值。编辑 `.env`，然后运行 `oac apply`。Core 在启动时一次性读取所有进程设置及设置指向的文件，并在 `GET /core/v1/installation` 报告加载的结果。`oac-core check-config` 会在不启动 Core 的情况下加载并校验同样的设置和文件。`OAC_PROVIDER_ROOT` 下的原生安装程序目录清单不属于设置，Core 只在启动时检查它。错误信息只指明变量名，绝不包含其值。敏感设置只报告是否已配置。

### Runtime 历史文件 {#runtime-history-file}

`OAC_HISTORY_SETTINGS_FILE` 指向一个 JSON 文件，用于调整[保留的历史记录](../../contracts/agents-api/zh/runtime-observability.md#retained-history-and-optional-export)，并可添加 OTLP 导出。没有此文件时，Core 按下表默认值把历史记录保存在数据库中。在 Compose 安装中，把该文件放在数据卷的 `secrets/core/` 目录中，属主为 UID 65532，权限为 `0600`，并设置 `OAC_HISTORY_SETTINGS_FILE=/run/oac/<file name>`：Core 以只读方式把该目录挂载到 `/run/oac`。headers 中可以包含导出凭据，它们绝不会出现在 `oac` 输出或安装报告中。未知字段会被拒绝。

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `sample_interval_seconds` | `30` | 定期采样间隔，范围为 5 到 300 |
| `queue_capacity` | `256` | 每个导出器排队的记录数，最大 4096 |
| `timeout_seconds` | `2` | 导出和历史查询超时，最大 30 |
| `endpoint` | 未设置 | 绝对 OTLP/HTTP 指标 URL，例如 `https://collector.example/v1/metrics`。未设置时 Core 不导出，其他导出字段也必须未设置 |
| `transport` | 未设置 | `otlp_http`；设置 `endpoint` 时必填 |
| `insecure` | `false` | `http` 端点必须设为 `true`，`https` 端点不允许设为 `true` |
| `headers` | 无 | 发往端点的请求标头。`Host`、`Content-Length`、`Content-Type` 和 `Content-Encoding` 为保留标头 |

## 运行时设置：Web {#runtime-settings-web}

运行时设置存储在 Core 的数据库中。请在 Web 中修改；脚本使用同一个 Core API 和 Core 密钥。

| 设置 | Web 中的位置 | Core API | 注意事项 |
| --- | --- | --- | --- |
| 沙箱后端：Docker、microsandbox 或 E2B | **System** → **Manage sandbox configuration**：设置向导，最后点击 **Save configuration** | `/core/v1/sandbox/deployment` | 每个安装只能使用一个后端，在首次登录后选择。要改用其他后端，必须先执行 **Reset deployment**；请参阅[更改沙箱配置](getting-started/nodes.md#change-the-sandbox-configuration) |
| 沙箱大小、Runtime 发行版、E2B 密钥和模板构建 | **System** → **Manage sandbox configuration** → **Change resources** | `/core/v1/sandbox/deployment` | Web 会推荐 [Provider 声明的默认大小](sandbox-provider.md#register-the-provider-kind)。现有沙箱会保留其大小和发行版。E2B 密钥仅可写入，并且已加密 |
| 节点及其容量 | **Nodes**：**Add node**；在节点页面上使用 **Edit node** 和 **Remove node** | `/core/v1/sandbox/enrollment-tokens`、`/core/v1/sandbox/nodes` | 请参阅[节点容量](#node-capacity)和[节点指南](getting-started/nodes.md) |
| 项目和 API 密钥 | **Projects and keys**：**Create project**、**Rename**、**Issue key**、**Revoke**、**Archive** | `/core/v1/projects` | 密钥只显示一次；Core 存储其摘要 |
| 每个 Harness 的默认模型 | **System** → **Default model configuration**：**Set** | `/core/v1/harnesses/{harness}/model-configuration` | 请参阅[默认模型](#default-models) |
| 自托管 Session 的执行器凭据 | **Session log**，然后进入 **Session** 页面：**Executor credentials** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | 请参阅[自托管执行器](getting-started/self-hosted.md) |

哪些 Harness 已启用以及默认 Harness 属于进程设置（`core.harnesses`、`core.default_harness`）；System 会以只读方式显示它们。[Core 管理 API](../../contracts/agents-api/zh/admin-api.md) 列出了所有 Core API 路由，[部署契约](../../contracts/agents-api/zh/sandbox-deployment.md) 定义了沙箱字段、限制和更改规则。

### 节点容量 {#node-capacity}

生成 **Add node** 命令时，Core 会批准节点容量：**Sandboxes at once**（`max_active`，默认值为 2），并且仅对 microsandbox 还会批准 **Retained sandboxes**（`max_retained`，默认值为 8），其中 `max_retained >= max_active >= 1`。Docker 从不暂停沙箱，因此 Web 不会询问此项，并且 Core 会使 `max_retained` 保持等于 `max_active`。之后可使用 **Edit node** 修改这些值。预留和尚未确认的清理操作都会占用容量；调低限制不会停止任何正在运行的沙箱。节点自身的文件无法更改其容量、大小或 Runtime。

`core.execution_concurrency` 与此无关：它限制 Core 中并发执行的工作量。

### 默认模型 {#default-models}

在 **System** → **Default model configuration** 中设置默认值，或使用 `PUT /core/v1/harnesses/{harness}/model-configuration`。Core 使用 `secrets/core/credential.key` 加密提供商密钥，并且绝不返回这些密钥。[模型执行](../../contracts/agents-api/zh/model-execution.md#deployment-defaults) 定义了请求字段和替换规则，[优先级](../../contracts/agents-api/zh/model-execution.md#saved-defaults-and-precedence)说明了哪些 Session 使用默认值。

## Compose 安装 {#compose-installations}

发行版中的[独立 Compose 文件](getting-started/install-options.md#docker-compose-and-hosting-platforms)从平台环境读取进程设置。把 [`OAC_PUBLIC_URL`](#settings) 设成准确的公共源地址，不要带尾部斜杠，并在添加节点或执行器之前重新创建 Core 和 Web。平台终止 TLS，并把流量转到 `web:8080`。

初始化服务首次生成机密信息和安装 ID，随后在后续部署中验证它们。每项机密信息都只有一个持久来源；Core 的密钥摘要派生自 Web 的登录密钥。对于现有安装，初始化绝不会替换缺失或已更改的机密信息。Core 从 `.env` 读取进程环境。

| 数据目录路径 | 内容 | 读取方 |
| --- | --- | --- |
| `database/` | PostgreSQL 数据 | PostgreSQL；初始化会检查它是否为空 |
| `secrets/database/` | 生成的数据库密码 | PostgreSQL 和 Core |
| `secrets/core/` | 凭据加密密钥、安装 ID 和 Core 密钥摘要 | Core |
| `secrets/web/` | 生成的 Core 登录密钥 | Web |
| `secrets/agent-host/` | `identity.json`，即 [agent host 的身份](#agent-host-container) | Core 和 agent host |
| `state/` | 私有 Provider 状态，在 Core 中挂载到 `/state`。每个适配器拥有一个子目录；E2B 使用 `e2b/`，不允许组或其他用户访问 | Core |
| `agent-host/` | [agent host 的状态目录](#agent-host-container) | agent host；初始化时检查它是否为空 |
| `node-payload/` | 已验证的节点安装元数据 | Web |

初始化会准备该目录；应用服务以只读方式接收各自的机密目录。`docker compose exec web oac-web core-key` 把 Core 密钥打印到运维人员终端，不写入容器日志。数据库密码和凭据加密密钥绝不打印。

上述路径位于 Docker 命名卷 `<project>_data` 中。所有宿主机平台都由 Docker 管理 Linux 文件权限；每个服务只挂载需要的子目录。数据卷必须和项目定义、公共 URL 一同保留。仅删除机密目录不会重置安装；数据库已存在时初始化会拒绝重建。Core 也会校验安装 ID 与数据库的绑定。运行时设置仍保存在 [Core 数据库](#runtime-settings-web)中。

## Docker 节点配置 {#docker-node-configuration}

节点安装程序会将 Docker 的主机设置写入节点配置文件的 `native` 对象，只有 Docker 适配器读取它。部署资源、沙箱发行版本和容量仍存储在 [Core 的数据库](#runtime-settings-web)中。

| 字段 | 安装程序设置的值 | 含义 |
| --- | --- | --- |
| `host` | `unix:///var/run/docker.sock` | 显式 Docker Engine 套接字 |
| `image` | 加载后沙箱镜像的本地 ID | 发行版本的 `image_id` 或 `image_manifest_digest`。主机的镜像存储决定由哪个 digest 指代已加载的镜像，因此该值属于节点本地；适配器只接受这两个值 |
| `network` | `oac-node-<installation-id>` | 沙箱容器网络 |
| `seccomp_file` | `<node-root>/runtime/seccomp.json` | 所匹配发行版的 seccomp 配置文件 |

[Docker 适配器](sandbox-provider.md#docker-adapter)负责容器隔离、卷布局和生命周期行为。

## Agent-host 容器 {#agent-host-container}

agent host 在沙箱之外、在每个 Session 自己的视图中运行该 Session 的 Harness（见[在 agent-host 视图中运行](../../contracts/agents-api/zh/harness-onboarding.md#run-in-an-agent-host-view)）。它的容器以 root 运行 [agent-host 镜像](maintainers.md#runtime-images-and-helpers)，需要 Linux 5.14 或更高版本以及 cgroup v2：

| 要求 | Docker 参数 | 用途 |
| --- | --- | --- |
| 私有 cgroup 命名空间 | `--cgroupns=private` | 使容器自己的 cgroup 成为容器内 cgroup v2 挂载的根 |
| `CAP_SYS_ADMIN` | `--cap-add SYS_ADMIN` | 该 cgroup v2 挂载、视图的挂载命名空间及其 FUSE world |
| `CAP_NET_ADMIN` | `--cap-add NET_ADMIN` | 每个视图仅含 loopback 的网络 |
| `/dev/fuse` | `--device /dev/fuse` | 视图的 world，即通过 File access 协议提供的沙箱文件 |
| 不使用 AppArmor 配置文件 | `--security-opt apparmor=unconfined` | Docker 默认的 AppArmor 配置文件会拒绝这些挂载 |

保留 Docker 默认的 seccomp 配置文件：有了 `CAP_SYS_ADMIN`，它允许 `clone3`、`mount` 和 `unshare`。容器不获得 Docker 套接字，也不发布端口。

agent host 需要一个委派给它的 cgroup v2 目录。它在该目录中为每个视图启动一个独立的 cgroup，用 `cgroup.kill` 结束视图并删除该 cgroup。启动时，它会结束并删除该目录中的每个 cgroup，因为其中每个 cgroup 都视为视图的 cgroup，所以其他任何东西都不得使用该目录。该目录必须可写，且不能包含 agent host 自己的进程。Docker 以只读方式挂载容器的 cgroup。使用上述参数时，在容器内再次挂载的 cgroup v2 就是容器自己的 cgroup 且可写：agent host 启动时把它挂载到 `/run/oac/cgroup`，并把目录 `/run/oac/cgroup/views` 委派给它的视图。没有这项委派时 agent host 不会启动：它以 `agenthost.ErrUnsupported` 失败，且没有任何回退。

容器运行 `oac-daemon agent-host --identity-file <path> --core-url <origin>`，该命令不接受其他参数：

- `--identity-file` 是 agent host 的身份，一个包含 `runtime_id` 和 `credential` 的 JSON 对象。agent host 以该 Runtime 的身份连接 Core，并按原样向 Runtime gateway 和 [Link](./sandbox-link-protocol.md) 出示该凭据。
- `--core-url` 是 Core 的源地址。agent host 按 Core 从 `OAC_PUBLIC_URL` 推导的方式，从它推导 Runtime gateway 和 Link 的 URL，并忽略 Core 返回的公开地址，因此它必须是 https 源地址，或回环主机上的 http 源地址，后者只有 Core 网络命名空间内的 peer 才能访问。

agent host 为镜像的 `/opt/oac/harnesses.json` 所安装、且声明了视图的每个 Harness 提供服务；没有任何 Harness 时它也会启动并连接。它把每个 Session 的 home（含 Harness 的原生历史）保存在 `/var/lib/oac/agent-host`，该目录必须比容器存活更久，Session 才能在重启后继续。连接断开后它按退避策略重新拨号；当 Core 连续两分钟不可达时，agent host 以非零状态退出，由其监管程序重启。

Compose 安装把 agent host 作为 `agent-host` 服务运行在 Core 的网络命名空间中，并使用 `--core-url http://127.0.0.1:8091`。它从以只读方式挂载的[数据卷](#compose-installations) `secrets/agent-host/` 读取身份，并把状态目录保存在数据卷的 `agent-host/` 中。

绝不要为 agent host 设置 `GODEBUG=http2debug`。设置后，Go 的 HTTP/2 实现会记录它编码的每个请求头，包括 agent host 添加的模型和 MCP 凭据。

## 安装目录 {#installation-directory}

安装目录默认为 `~/.oac/core`（Windows 为 `$HOME/.oac/core`）。其中保存进程设置和原生管理命令；服务持久数据位于 [Compose 数据卷](#compose-installations)。

| 路径 | 内容 | 修改者 |
| --- | --- | --- |
| `.env` | 进程设置和固定的 Compose 项目名 | 用户修改后运行 `oac apply` |
| `compose.yaml`、`compose-sha256sums.txt` | 已校验的发行版服务定义 | 发行流程 |
| `oac`（Windows 为 `oac.exe`） | 原生管理命令 | 安装程序 |

同级 `<install-dir>.lock` 目录用于同步操作并一直保留；`<install-dir>.staging` 保存尚未就位的安装文件。两者都不保存服务数据。Unix 上安装程序以 `0700` 创建私有目录，以 `0600` 创建配置文件。

Compose 项目名为 `oac-<10 hex digits>`，服务包括 `init`、`database`、`core`、`agent-host` 和 `web`。Core 启动时执行数据库迁移。Web 提供控制台并把 `/v1`、`/api/v1` 转发到 Core，是唯一发布端口（`OAC_WEB_PORT`）的服务。没有服务持有 Docker 套接字。

## 附录：没有安装程序时的 Core 环境 {#appendix-core-environment-without-the-installer}

Core 读取进程环境。Compose 将 `.env` 插值到环境中，并把机密文件挂载到下表中的容器路径。直接运行 Core 时，将文件变量设为 Core 进程可读取的绝对路径；见[服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md)。

| 变量 | 设置来源 |
| --- | --- |
| `OAC_PUBLIC_URL` | 必填。[公共 URL](#settings)。Core 只校验一次，并从中派生 Agents API 基地址、守护进程 WebSocket URL、[沙箱 Link URL](#changing-the-public-url)、自托管 `remote_url`、安装程序下载地址、托管沙箱地址和部署的只读 `core_url`，绝不从请求标头派生 |
| `OAC_ADDR` | 安装程序在容器中设置为 `:8091`。独立启动的 Core 在未设置或为空时，默认使用 `127.0.0.1:8091` |
| `OAC_DATABASE_URL` | 必填。不含密码的 PostgreSQL URL |
| `OAC_DATABASE_PASSWORD_FILE` | `/run/database/password`。此时 URL 不得包含密码 |
| `OAC_CREDENTIAL_KEY_FILE` | 必填。`/run/oac/credential.key`：Base64 编码的 32 字节随机密钥。Core 用它加密存储的凭据 |
| `OAC_CORE_KEY_DIGESTS_FILE` | 必填。`/run/oac/core-key-digests.json`：一个包含 Core 密钥 SHA-256 的 JSON 数组 |
| `OAC_INSTALLATION_ID_FILE` | 必填。`/run/oac/installation.id`：安装 ID，采用规范 UUID 格式。如果 ID 与数据库记录的 ID 不一致，Core 会拒绝它 |
| `OAC_AGENT_HOST_IDENTITY_FILE` | 必填。`/run/agent-host/identity.json`：[agent host 的身份](#agent-host-container)，其 `runtime_id` 为规范 UUID。Core 启动时用该 ID 和凭据注册 agent host；新凭据会隔离旧凭据认证过的 Link，已吊销的 agent host 保持吊销 |
| `OAC_EXECUTION_CONCURRENCY`、`OAC_DEFAULT_HARNESS`、`OAC_HARNESSES`、`OAC_WRITE_AUDIT_RETENTION`、`OAC_OAUTH_TRUSTED_ORIGINS`、`OAC_HISTORY_SETTINGS_FILE`、`OAC_LOG_LEVEL`、`OAC_LOG_FORMAT`、`OAC_LOG_ADD_SOURCE` | 对应的[进程设置](#settings)。Web 也读取三个日志设置 |
| `OAC_PROVIDER_ROOT` | 适配器构件的绝对根目录。Core 镜像设置为 `/opt/oac`。每个适配器都拥有此根目录下的辅助路径。当其中的 `native-installers/` 目录包含 `catalog.json` 时，Core 在核对该目录清单与自身发行版后提供自托管守护进程安装程序。适配器状态位于 `/state`，即数据卷的 [`state/`](#compose-installations) |

Core 会记录所加载的历史文件路径，但绝不记录环境变量的值或文件内容。

显式 OAuth 受信任源无效时，Core 会停止启动。条目必须是不含凭据、查询参数和非根路径的 HTTPS 源地址。[Vaults](../../contracts/agents-api/zh/vaults.md) 负责刷新和网络策略。私有颁发者还需要受信任的 CA：独立管理的 Unix Core 可以使用 Go 的 `SSL_CERT_FILE` PEM CA-bundle 覆盖机制，从而保留证书验证。托管安装没有自定义 CA 设置。

## 附录：没有安装程序时的 Web 环境 {#appendix-web-environment-without-the-installer}

Compose 为 Web 设置这些变量。仅在不使用 Compose 运行控制台时才自行设置。Compose 把数据卷的 `secrets/web/` 挂载到 `/run/oac`，并设置 `OAC_WEB_CORE_KEY_FILE=/run/oac/core.key`。

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | 监听地址。主机部分为空或未指定时，健康检查在 `127.0.0.1` 上探测它 |
| `OAC_PUBLIC_URL` | 必填 | [公共 URL](#settings)：面向浏览器的准确源地址，可以使用 HTTP 或 HTTPS，且不得包含路径。Host 和源地址检查使用此值；HTTPS 会使 Session Cookie 具备 `Secure` 属性 |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core 的源地址，可以使用 HTTP 或 HTTPS，且不得包含凭据、查询参数或路径。健康检查探测其 `/healthz` |
| `OAC_WEB_CORE_KEY_FILE` | 必填 | 常规文件的绝对路径，该文件没有组或其他用户权限，并保存 Core 密钥：至少 32 个字符、不含空白字符、最大 4 KiB |
| `OAC_WEB_DIST` | `/www` | 已构建控制台的绝对目录；必须包含 `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | 未设置 | 所匹配发行版的节点载荷（即安装程序的 `node-payload/`）的绝对路径。未设置时，不提供 `/node-install/*`，且 Add node 不可用 |

未设置或为空的变量使用其默认值。无效值会阻止控制台启动，并显示一条指明变量名的消息。控制台还会读取三个日志[进程设置](#settings)，并拒绝 Core 拒绝的值。对于不在同一台计算机上的任何浏览器，请使用 HTTPS。
