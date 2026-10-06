---
title: "配置参考"
source: docs/configuration.md
source_hash: e697e320a2df0c130cb004f5e8bbda50deff6515ef7727bea066fc0d4f688b7d
---

Core 安装的每项设置都恰好只有一个归属位置。共有两类：

| 类型 | 示例 | 归属位置 | 修改方式 | 生效方式 |
| --- | --- | --- | --- | --- |
| [进程设置](#process-settings-configjson) | 公共 URL、端口、日志、Harness、执行并发度、审计保留期、OAuth 来源、Runtime 历史记录导出 | 安装目录中的 `.env`（默认 `~/.oac/core`） | 编辑 `.env`，然后运行 `oac apply` | `oac apply` 会重新创建读取了这些已更改设置的服务 |
| [运行时设置](#runtime-settings-web) | 沙箱后端和大小、节点、项目和密钥、默认模型、执行器凭据 | Core 的 PostgreSQL 数据库 | 在 Web 中修改，或使用 Core 密钥调用 Core API（`/core/v1`） | 保存时无需重启 Core；节点会异步准备 Runtime 变更 |

Web 的 **System** 页面显示该安装的地址、默认模型和沙箱配置，并在 **Startup settings** 下以只读方式显示 Core 加载的进程设置。机密信息存放在 [`data/secrets/`](#installation-directory) 中，每项仅保存一份。没有任何配置文件定义项目或 API 密钥。

## 进程设置 {#process-settings-configjson}

[安装选项](getting-started/install-options.md)中的安装标志只会一次性写入 `.env`。要更改设置，请编辑 `.env` 并应用：

```sh
~/.oac/core/oac apply
```

### oac apply 的工作方式 {#how-oac-apply-works}

1. 它用你改过的 `.env` 运行 `oac-core check-config`。值无效时什么都不改。
2. 它运行 `docker compose up -d --wait`。Compose 只重新创建配置有变化的服务。
3. 校验失败时，不会重新创建任何容器。重启会中断哪些操作，见[停止和重启](getting-started/operations.md#stop-and-restart)。

`docker compose ps` 展示服务。域名状态在 `data/domain/status.json`。

### 更改公共 URL {#changing-the-public-url}

`OAC_PUBLIC_URL` 是应用、节点、沙箱和自托管执行器使用的唯一源地址。Core 从中派生守护进程 WebSocket URL、自托管 `remote_url` 和每个沙箱的连接地址。它是 http 或 https 源地址，也就是浏览器和节点使用的地址。安装通过 `OAC_WEB_PORT` 以 HTTP 提供 Web；前面有反向代理或托管平台时，由它们终止 HTTPS。

要更改它，先把反向代理指向新地址，然后编辑 `OAC_PUBLIC_URL` 并运行 `oac apply`。之后：

- 使用旧地址的节点不会再获得新沙箱：请在 Web 中移除这些节点，然后重新添加。
- 只有在旧地址仍可访问此 Core 时，现有沙箱和执行器才会继续工作。
- 自托管执行器必须使用新的 `remote_url` 重启，并且其安装程序会拒绝为旧地址进行的安装：请创建新的自托管 Session，然后重新连接其主机。

### 设置 {#settings}

`OAC_HISTORY_SETTINGS_FILE` 可以指向一个文件，其 headers 中含有导出凭据。该文件权限为 `0600`，这些 headers 绝不会出现在 `oac` 输出或安装报告中。模型提供商不属于进程设置；请参阅[默认模型](#default-models)。

以下配置参考表保留英文原文。

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | `http://localhost:8080` | 应用、节点、沙箱和自托管执行器使用的源地址。参阅[修改公开 URL](#changing-the-public-url) |
| `OAC_HOST` | `127.0.0.1` | `compose.yaml` 发布的 Web 绑定地址。`install.sh` 设置为 `0.0.0.0` |
| `OAC_WEB_PORT` | `8080` | Host port of Web |
| `OAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `OAC_LOG_FORMAT` | `auto` | `auto`, `text` or `json` |
| `OAC_LOG_ADD_SOURCE` | unset | `1` adds source locations |
| `OAC_EXECUTION_CONCURRENCY` | `4` | Concurrent execution work, from 1 to 1024 |
| `OAC_DEFAULT_HARNESS` | `codex` | Harness used when a request does not name one |
| `OAC_HARNESSES` | Every registered Harness | Comma-separated Harnesses to enable besides the default one. Unknown names stop startup |
| `OAC_WRITE_AUDIT_RETENTION` | `2160h` | Minimum `1h` |
| `OAC_OAUTH_TRUSTED_ORIGINS` | unset | Comma-separated HTTPS origins |
| `OAC_HISTORY_SETTINGS_FILE` | unset | Optional Runtime history file. Sensitive; Core reports only whether it is configured |

未设置或为空的值使用默认值。编辑 `.env`，然后运行 `oac apply`。Core 会在 `GET /core/v1/installation` 报告它加载的进程设置。`oac-core check-config` 会在不启动 Core 的情况下校验同一组环境变量。敏感设置只报告是否已配置。有关 Core 如何收集和保留 Runtime 历史记录，请参阅[保留的历史记录](../../contracts/agents-api/zh/runtime-observability.md#retained-history-and-optional-export)。

## 运行时设置：Web {#runtime-settings-web}

运行时设置存储在 Core 的数据库中。请在 Web 中修改；脚本使用同一个 Core API 和 Core 密钥。

| 设置 | Web 中的位置 | Core API | 注意事项 |
| --- | --- | --- | --- |
| 沙箱后端：Docker、microsandbox 或 E2B | **System** → **Manage sandbox configuration**：设置向导，最后点击 **Save configuration** | `/core/v1/sandbox/deployment` | 每个安装只能使用一个后端，在首次登录后选择。要改用其他后端，必须先执行 **Reset deployment**；请参阅[更改沙箱配置](getting-started/nodes.md#change-the-sandbox-configuration) |
| 沙箱大小、Runtime 发行版、E2B 密钥和模板构建 | **System** → **Manage sandbox configuration** → **Change resources** | `/core/v1/sandbox/deployment` | Web 会在 [`standard-sizes.json`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/src/features/sandbox/standard-sizes.json) 中推荐可用大小。现有沙箱会保留其大小和发行版。E2B 密钥仅可写入，并且已加密 |
| 节点及其容量 | **Nodes**：**Add node**；在节点页面上使用 **Edit node** 和 **Remove node** | `/core/v1/sandbox/enrollment-tokens`、`/core/v1/sandbox/nodes` | 请参阅[节点容量](#node-capacity)和[节点指南](getting-started/nodes.md) |
| 项目和 API 密钥 | **Projects and keys**：**Create project**、**Rename**、**Issue key**、**Revoke**、**Archive** | `/core/v1/projects` | 密钥只显示一次；Core 存储其摘要 |
| 每个 Harness 的默认模型 | **System** → **Default model configuration**：**Set** | `/core/v1/harnesses/{harness}/model-configuration` | 请参阅[默认模型](#default-models) |
| 自托管 Session 的执行器凭据 | **Session log**，然后进入 **Session** 页面：**Executor credentials** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | 请参阅[自托管执行器](getting-started/self-hosted.md) |

哪些 Harness 已启用以及默认 Harness 属于进程设置（`core.harnesses`、`core.default_harness`）；System 会以只读方式显示它们。[Core 管理 API](../../contracts/agents-api/zh/admin-api.md) 列出了所有 Core API 路由，[部署契约](../../contracts/agents-api/zh/sandbox-deployment.md) 定义了沙箱字段、限制和更改规则。

### 节点容量 {#node-capacity}

生成 **Add node** 命令时，Core 会批准节点容量：**Sandboxes at once**（`max_active`，默认值为 2），并且仅对 microsandbox 还会批准 **Retained sandboxes**（`max_retained`，默认值为 8），其中 `max_retained >= max_active >= 1`。Docker 从不暂停沙箱，因此 Web 不会询问此项，并且 Core 会使 `max_retained` 保持等于 `max_active`。之后可使用 **Edit node** 修改这些值。预留和尚未确认的清理操作都会占用容量；调低限制不会停止任何正在运行的沙箱。节点自身的文件无法更改其容量、大小或 Runtime。

`core.execution_concurrency` 与此无关：它限制 Core 中并发执行的工作量。

### 默认模型 {#default-models}

在 **System** → **Default model configuration** 中设置默认值，或使用 `PUT /core/v1/harnesses/{harness}/model-configuration`。Core 使用 `secrets/credential.key` 加密提供商密钥，并且绝不返回这些密钥。[模型执行](../../contracts/agents-api/zh/model-execution.md#deployment-defaults) 定义了请求字段和替换规则，[优先级](../../contracts/agents-api/zh/model-execution.md#saved-defaults-and-precedence)说明了哪些 Session 使用默认值。

## Compose 安装 {#compose-installations}

发行版中的[独立 Compose 文件](getting-started/install-options.md#docker-compose-and-hosting-platforms)从平台环境读取进程设置。`OAC_PUBLIC_URL` 为空时选用 `http://localhost:8080`。把它设成准确的公共源地址，不要带尾部斜杠，并在添加节点或执行器之前重新创建 Core 和 Web。平台终止 TLS，并把流量转到 `web:8080`。

初始化服务首次生成机密信息和安装 ID，随后在后续部署中验证它们。每项机密信息都只有一个持久来源；Core 的密钥摘要派生自 Web 的登录密钥。对于现有安装，初始化绝不会替换缺失或已更改的机密信息。Core 从 `.env` 读取进程环境。

| 数据目录路径 | 内容 | 读取方 |
| --- | --- | --- |
| `database/` | PostgreSQL 数据 | PostgreSQL；初始化会检查它是否为空 |
| `secrets/database/` | 生成的数据库密码 | PostgreSQL 和 Core |
| `secrets/core/` | 凭据加密密钥、安装 ID 和 Core 密钥摘要 | Core |
| `secrets/web/` | 生成的 Core 登录密钥 | Web |
| `state/` | 私有 Provider 状态 | Core |
| `node-payload/` | 已验证的节点安装元数据 | Web |

初始化会准备该目录；应用服务以只读方式接收各自的机密目录。`docker compose exec web oac-web core-key` 把 Core 密钥打印到运维人员终端，不写入容器日志。数据库密码和凭据加密密钥绝不打印。

`OAC_DATA_DIR` 选择该目录，默认是 Compose 文件旁的 `./data`。必须将该项目的定义和公共 URL 与该目录一同保留。仅删除机密目录不会重置安装；如果数据库已经存在，初始化会拒绝重新开始。Core 还会将安装 ID 与其数据库绑定。运行时设置仍存储在 [Core 的数据库](#runtime-settings-web)中。

## Docker 节点配置 {#docker-node-configuration}

节点安装程序会将 Docker 的提供商配置写入节点的配置文件。部署资源、Runtime 镜像和容量仍存储在 [Core 的数据库](#runtime-settings-web)中。

| 字段 | 安装程序设置的值 | 含义 |
| --- | --- | --- |
| `host` | `unix:///var/run/docker.sock` | 显式 Docker Engine 套接字 |
| `network` | `oac-node-<installation-id>` | Runtime 容器网络 |
| `seccomp_file` | `<node-root>/runtime/seccomp.json` | 所匹配发行版的 seccomp 配置文件 |
| `nested_sandbox` | `true` | 启用 Docker 适配器的 init 进程和 proc-mask 配置 |
| `extra_hosts` | 可选 | 额外的容器主机映射 |

[Docker 适配器](sandbox-provider.md#docker-adapter)负责容器隔离、卷布局和生命周期行为。

## 安装目录 {#installation-directory}

安装程序会创建安装目录，默认路径为 `~/.oac/core`，权限模式为 `0700`。机密文件为 `0600`。

| 路径 | 内容 | 修改者 |
| --- | --- | --- |
| `.env` | [进程设置](#process-settings-configjson)。由你编辑的文件 | 你，然后运行 `oac apply`；托管域名设置写入 `OAC_PUBLIC_URL` |
| `compose.yaml` | 发行版的服务定义。不要编辑 | 发行版 |
| `oac` | [管理命令](getting-started/operations.md#the-oac-command)，从 Core 镜像复制 | 安装程序 |
| `data/secrets/web/core.key` | [Core 密钥](getting-started/operations.md#core-key) | `oac rotate-core-key` |
| `data/secrets/core/credential.key` | 加密 Core 在数据库中封存内容的密钥 | 无。必须与数据库一同保留 |
| `data/secrets/core/core-key-digests.json` | Core 密钥的 SHA-256 | `oac rotate-core-key` |
| `data/secrets/database/password` | PostgreSQL 密码 | 无。PostgreSQL 仅在创建数据库时读取 |
| `data/database/` | PostgreSQL 数据 | PostgreSQL |
| `data/node-payload/` | Web 在 `/node-install/` 提供的节点文件 | 初始化 |
| `data/state/` | 私有 Provider 状态，包括 E2B 回执 | Core |
| `.oac.lock` | 安装锁 | 会修改安装状态的 `oac` 命令 |

Compose 项目名为 `oac-<10 hex digits>`。服务包括 `init`、`database`、`core` 和 `web`。Core 启动时执行数据库迁移。`web` 提供控制台并把 `/v1` 和 `/api/v1` 转发到 Core，是唯一发布端口（`OAC_WEB_PORT`）的服务。没有服务持有 Docker 套接字。除 Docker 存储外，不会向安装目录之外写入任何内容。

## 附录：没有安装程序时的 Core 环境 {#appendix-core-environment-without-the-installer}

Core 只读取其环境。Compose 把 `.env` 插值进服务环境。Compose 必须为 2.26.0 或更高版本。如果你自行运行 Core，请设置这些变量；见[服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md)。

| 变量 | 设置来源 |
| --- | --- |
| `OAC_PUBLIC_URL` | `public_url`，或 Core 的回环源地址。Core 从中派生守护进程 WebSocket URL、自托管 `remote_url`、托管沙箱地址和部署的只读 `core_url`，绝不从请求标头派生。未设置时，Core 不运行 Runtime 网关，也不执行任何 Session |
| `OAC_ADDR` | 安装程序在容器中设置为 `:8091`。独立启动的 Core 在未设置或为空时，默认使用 `127.0.0.1:8091` |
| `OAC_DATABASE_URL` | 该安装不含密码的 PostgreSQL URL，并将 `core.database_pool` 作为 `pool_*` 查询参数附加到其中 |
| `OAC_DATABASE_PASSWORD_FILE` | `data/secrets/database/password`。此时 URL 不得包含密码 |
| `OAC_CREDENTIAL_KEY_FILE` | `data/secrets/core/credential.key` |
| `OAC_CORE_KEY_DIGESTS_FILE` | `data/secrets/core/core-key-digests.json`：一个包含 Core 密钥 SHA-256 的 JSON 数组 |
| `OAC_INSTALLATION_ID_FILE` | `data/secrets/core/installation.id`：安装 ID，采用规范 UUID 格式。它会启用沙箱部署和节点路由，并要求设置 `OAC_PUBLIC_URL` 和 `OAC_CORE_KEY_DIGESTS_FILE`。如果 ID 与数据库记录的 ID 不一致，Core 会拒绝它，因此必须将两者一同保留 |
| `OAC_EXECUTION_CONCURRENCY`、`OAC_DEFAULT_HARNESS`、`OAC_HARNESSES`、`OAC_WRITE_AUDIT_RETENTION`、`OAC_OAUTH_TRUSTED_ORIGINS` | 对应的[进程设置](#settings)。`oac-core check-config` 会在不启动 Core 的情况下校验它们 |
| `OAC_HISTORY_SETTINGS_FILE` | 可选的 Runtime 历史文件。敏感；安装报告只说明它是否已设置 |
| `OAC_LOG_LEVEL`、`OAC_LOG_FORMAT`、`OAC_LOG_ADD_SOURCE` | `log.*`；Web 也读取这三个设置 |
| `OAC_PROVIDER_ROOT` | 适配器构件的绝对根目录。Core 镜像设置为 `/opt/oac`。每个适配器都拥有此根目录下的辅助路径 |
| `OAC_PROVIDER_STATE_ROOT` | 绝对私有状态根目录：Core 镜像中为 `/state`。每个适配器都拥有自己的子目录；E2B 使用 `e2b/`，该目录归 Core 的用户所有，不允许组或其他用户访问。将其与数据库和 `credential.key` 一起备份；不要将其挂载到 Web 或 Runtime 中 |
| `OAC_NATIVE_INSTALLER_DIR` | 自托管守护进程安装程序：Compose 文件中为 `/opt/oac/native-installers`。未设置时 Core 不提供安装程序。提供目录清单前，Core 会将其与自身发行版进行核对 |

Core 会记录所加载文件的路径，但绝不记录环境变量的值或文件内容。

显式 OAuth 受信任源无效时，Core 会停止启动。条目必须是不含凭据、查询参数和非根路径的 HTTPS 源地址。[Vaults](../../contracts/agents-api/zh/vaults.md) 负责刷新和网络策略。私有颁发者还需要受信任的 CA：独立管理的 Unix Core 可以使用 Go 的 `SSL_CERT_FILE` PEM CA-bundle 覆盖机制，从而保留证书验证。托管安装没有自定义 CA 设置。

## 附录：没有安装程序时的 Web 环境 {#appendix-web-environment-without-the-installer}

Compose 为 Web 设置这些变量。仅在不使用 Compose 运行控制台时才自行设置。该安装的机密信息中，Web 只收到 `data/secrets/web/core.key`。

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | 监听地址 |
| `OAC_WEB_ORIGIN` | `http://127.0.0.1:8080` | 面向浏览器的准确源地址，可以使用 HTTP 或 HTTPS，且不得包含路径。Host 和源地址检查使用此值；HTTPS 会使 Session Cookie 具备 `Secure` 属性 |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core 的源地址，可以使用 HTTP 或 HTTPS，且不得包含凭据、查询参数或路径 |
| `OAC_WEB_CORE_KEY_FILE` | `/admin/core.key` | 常规文件的绝对路径，该文件没有组或其他用户权限，并保存 Core 密钥：至少 32 个字符、不含空白字符、最大 4 KiB |
| `OAC_WEB_DIST` | `/www` | 已构建控制台的绝对目录；必须包含 `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | 未设置 | 所匹配发行版的节点载荷（即安装程序的 `node-payload/`）的绝对路径。未设置时，不提供 `/node-install/*`，且 Add node 不可用 |

变量不存在时会应用默认值；显式空值会按已提供的值进行验证。无效的 `OAC_WEB_*` 值会阻止控制台启动，并显示一条指明变量名的消息。控制台还会读取 `OAC_LOG_LEVEL`、`OAC_LOG_FORMAT` 和 `OAC_LOG_ADD_SOURCE`（[Core 环境](#appendix-core-environment-without-the-installer)）；未知值会回退到其默认值。对于不在同一台计算机上的任何浏览器，请使用 HTTPS。
