---
title: "配置参考"
source: docs/configuration.md
source_hash: d59774502bc1508bd7372abbb833abbed5629152ece25dc00fb4b6baab7f5f65
---

Core 安装的每项设置、其归属位置以及修改方式。安装程序写入初始值；之后请使用本参考。每项设置都恰好只有一个归属位置，分属以下三类：

| 类别 | 示例 | 归属位置 | 修改方式 | 生效方式 |
| --- | --- | --- | --- | --- |
| [进程设置](#process-settings) | 公共 URL、端口、日志、Harness、执行并发度、审计保留期、OAuth 来源、Runtime 历史记录 | 安装目录中的 `.env`（默认 `~/.oac/core`） | 编辑 `.env`，然后运行 `oac apply` | `oac apply` 会重新创建读取了这些已更改设置的服务 |
| [机密信息](#compose-installations) | 数据库密码、凭据加密密钥、安装 ID、Core 密钥及由其派生的 Core 密钥摘要 | Compose 数据卷中的 `secrets/`，每项一份 | 初始化时一次性生成；`oac rotate-core-key` 替换 Core 密钥及其摘要 | `oac rotate-core-key` 会重启 Core 和 Web |
| [运行时设置](#runtime-settings-web) | 沙箱提供商和大小、节点、项目和密钥、默认模型、执行器凭据 | Core 的 PostgreSQL 数据库 | 在 Web 中修改，或使用 Core 密钥调用 Core API（`/core/v1`） | 保存时无需重启 Core；节点会异步准备 Runtime 变更 |

**设置** 是 Web 中平台配置的唯一入口，包含 **沙箱与调度**、**工作区存储**、**模型与凭据** 和 **服务与部署** 四组。每项设置只有一个编辑位置；监控和节点页面跳转到对应设置。各表单通过所属 API 独立保存，不做跨资源整体保存。**服务与部署** 显示安装地址和 Core 已加载的进程设置；进程设置在 Web 中仍为只读。项目、API 密钥和 Session 执行器凭据仍在各自资源页面管理。没有任何配置文件定义项目或 API 密钥。

## 进程设置 {#process-settings}

[安装选项](getting-started/install-options.md#flags)中的安装标志只会一次性写入 `.env`。要更改设置，请编辑 `.env` 并应用：

```sh
~/.oac/core/oac apply
```

### 设置 {#settings}

模型提供商不属于进程设置；请参阅[默认模型](#default-models)。

以下配置参考表保留英文原文。

| Variable | Default | Meaning |
| --- | --- | --- |
| `OAC_PUBLIC_URL` | 必填；`compose.yaml` 设为 `http://localhost:` 加 `OAC_WEB_PORT` | 应用、节点、沙箱和自托管执行器使用的源地址。参阅[更改公共 URL](getting-started/public-address.md#changing-the-public-url) |
| `OAC_HOST` | `127.0.0.1` | `compose.yaml` 发布的 Web 绑定地址。安装程序设置为 `0.0.0.0` |
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

### oac apply 的工作方式 {#how-oac-apply-works}

1. 它运行 `init` 服务，确认数据卷属于已安装的版本；如果首次启动中途停止，就完成初始化。
2. 它用你改过的 `.env` 运行 `oac-core check-config`。值无效时不改动任何服务。
3. 它用主机上已有的镜像运行 `docker compose up -d --wait`。Compose 只重新创建配置有变化的服务，并移除该版本不再定义的容器。若有服务未变为健康，消息会指出该服务和打印其日志的命令。

用 `docker compose ps` 检查服务。重启会中断哪些操作，见[停止和重启](getting-started/operations.md#stop-and-restart)。

## 运行时设置：Web {#runtime-settings-web}

运行时设置存储在 Core 的数据库中。请在 Web 中修改；脚本使用同一个 Core API 和 Core 密钥。

| 设置 | Web 中的位置 | Core API | 注意事项 |
| --- | --- | --- | --- |
| 沙箱提供商：Docker、microsandbox 或 E2B | **设置** → **沙箱与调度**：设置向导，最后点击 **保存配置** | `/core/v1/sandbox/deployment` | 每个安装只能使用一个沙箱提供商，在首次登录后选择。要改用其他提供商，必须先执行 **重置部署**；请参阅[更改沙箱配置](getting-started/nodes.md#change-the-sandbox-configuration) |
| 沙箱大小、Runtime 发行版、E2B 密钥和模板构建 | **设置** → **沙箱与调度** → **修改资源** | `/core/v1/sandbox/deployment` | Web 会推荐 [提供商声明的默认大小](sandbox-provider.md#register-the-provider-kind)。现有沙箱会保留其大小和发行版。E2B 密钥仅可写入，并且已加密 |
| 节点及其容量 | **节点**：**添加节点**、**编辑节点** 和移除 | `/core/v1/sandbox/enrollment-tokens`、`/core/v1/sandbox/nodes` | 请参阅[节点容量](#node-capacity)和[添加和管理节点](getting-started/nodes.md) |
| 空闲挂起 | **设置** → **沙箱与调度** → **自动挂起** → **空闲多久后挂起（秒）** → **保存** | `PATCH /core/v1/sandbox/deployment` | 整个部署统一生效；参阅[挂起策略](../../contracts/agents-api/zh/sandbox-deployment.md#suspension-policy) |
| 独立工作区存储和租户容量 | **设置 → 工作区存储** | `/core/v1/workspace-storage` | 参阅[设置工作区存储](getting-started/workspace-storage.md)；现有工作区保留自身配置 |
| 项目和 API 密钥 | **项目与密钥**：**创建项目**、**改名**、**签发密钥**、**撤销**、**归档** | `/core/v1/projects` | 密钥只显示一次；Core 存储其摘要 |
| 每个 Harness 的默认模型 | **设置** → **模型与凭据**：**设置** | `/core/v1/harnesses/{harness}/model-configuration` | 请参阅[默认模型](#default-models) |
| 自托管 Session 的执行器凭据 | **Session 日志**，然后进入 **Session** 页面：**执行器凭据** | `/core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials` | 请参阅[自托管执行器](getting-started/self-hosted.md) |

哪些 Harness 已启用以及默认 Harness 属于进程设置（`OAC_HARNESSES`、`OAC_DEFAULT_HARNESS`）；**设置 → 服务与部署** 在 **启动设置** 中列出它们。**设置 → 模型与凭据** 在 Harness 已启用且设置了默认模型时将其标记为 **就绪**，否则指出缺少的步骤：**未设置模型提供商** 或 **已禁用**。[Core 管理 API](../../contracts/agents-api/zh/admin-api.md) 列出了所有 Core API 路由，[部署契约](../../contracts/agents-api/zh/sandbox-deployment.md) 定义了沙箱字段、限制和更改规则。

### 节点容量 {#node-capacity}

生成 **添加节点** 命令时，Core 会批准节点容量：**同时运行的沙箱**（`max_active`，默认值为 2），并且仅对 microsandbox 还会批准 **保留的沙箱**（`max_retained`，默认值为 8），其中 `max_retained >= max_active >= 1`。Docker 从不暂停沙箱，因此 Web 不会询问此项，并且 Core 会使 `max_retained` 保持等于 `max_active`。之后可在 **节点** 使用 **编辑节点** 修改这些值。预留和尚未确认的清理操作都会占用容量；调低限制不会停止任何正在运行的沙箱。节点自身的文件无法更改其容量、大小或 Runtime。

`OAC_EXECUTION_CONCURRENCY` 与此无关：它限制 Core 中并发执行的工作量。

### 默认模型 {#default-models}

在 **设置** → **模型与凭据** 中设置默认值，或使用 `PUT /core/v1/harnesses/{harness}/model-configuration`。Core 使用 `secrets/core/credential.key` 加密提供商密钥，并且绝不返回这些密钥。[模型执行](../../contracts/agents-api/zh/model-execution.md#deployment-defaults) 定义了请求字段和替换规则，[优先级](../../contracts/agents-api/zh/model-execution.md#saved-defaults-and-precedence)说明了哪些 Session 使用默认值。

### 租户容量 {#tenant-capacity}

`tenant_capacity_mib` 是安装唯一的租户容量策略，只能与选择一起通过 `PUT /core/v1/workspace-storage` 写入，并保存在 Core 数据库中。正值要求配额模式配置，配额模式配置也要求正值。它限制单个租户所有尚未确认删除的工作区的获准容量之和，并跨配置变更生效；计数规则由[能力验证](./workspace-provider.md#capability-validation)负责。调低该值只拒绝新工作区；删除 Session 后，其存储被移除时即释放容量。

Session 创建可以在异步工作区准入之前成功。容量耗尽时，Core 会重试，不为被拒绝的工作区分配存储或计算资源；待处理输入保留原有的 [Environment 输入截止时间](../../contracts/agents-api/zh/environments.md#reservations)。无输入创建的空闲 Session 没有输入截止时间，会等待容量释放。

非配额模式下创建的工作区保持无上限；不会调整其大小或迁移它们。只要保留此类工作区，在正策略下其租户在删除该 Session 前无法准入新工作区。

该策略限制每个租户可承诺的容量，而不限制文件系统。应按租户数乘以策略值，并为无上限工作区、标识、删除标记、日志库和 XFS 元数据预留余量来规划 XFS 文件系统大小；或者明知超额分配并监测可用空间。每个节点的 `checkpoint_root` 应放在独立存储上。容量上限不提供 I/O 公平性。

## Compose 安装 {#compose-installations}

发行版中的[独立 Compose 文件](getting-started/install-options.md#docker-compose-and-hosting-platforms)从平台环境读取进程设置。把 [`OAC_PUBLIC_URL`](#settings) 设成准确的公共源地址，不要带尾部斜杠，并在添加节点或执行器之前重新创建 Core 和 Web。平台终止 TLS，并把流量转到 `web:8080`。

初始化服务首次生成机密信息和安装 ID，随后在后续部署中验证它们。每项机密信息都只有一个持久来源；Core 的密钥摘要派生自 Web 的登录密钥。对于现有安装，初始化绝不会替换缺失或已更改的机密信息。Core 从 `.env` 读取进程环境。

| 数据目录路径 | 内容 | 读取方 |
| --- | --- | --- |
| `database/` | PostgreSQL 数据 | PostgreSQL；初始化会检查它是否为空 |
| `secrets/database/` | 生成的数据库密码 | PostgreSQL 和 Core |
| `secrets/core/` | 凭据加密密钥、安装 ID 和 Core 密钥摘要 | Core |
| `secrets/web/` | 生成的 Core 登录密钥 | Web |
| `state/` | 私有提供商状态，在 Core 中挂载到 `/state`。每个适配器拥有一个子目录；E2B 使用 `e2b/`，不允许组或其他用户访问 | Core |
| `node-payload/` | 已验证的节点安装元数据 | Web |

初始化会准备该目录；应用服务以只读方式接收各自的机密目录。`docker compose exec web oac-web core-key` 把 Core 密钥打印到运维人员终端，不写入容器日志。数据库密码和凭据加密密钥绝不打印。

上述路径位于 Docker 命名卷 `<project>_data` 中。所有主机平台都由 Docker 管理 Linux 文件权限；每个服务只挂载需要的子目录。数据卷必须和项目定义、公共 URL 一同保留。仅删除机密目录不会重置安装；数据库已存在时初始化会拒绝重建。Core 也会校验安装 ID 与数据库的绑定。运行时设置仍保存在 [Core 数据库](#runtime-settings-web)中。

## Docker 节点配置 {#docker-node-configuration}

节点安装程序会将 Docker 的主机设置写入节点配置文件的 `native` 对象，只有 Docker 适配器读取它。部署资源、Runtime 发行版本和容量仍存储在 [Core 的数据库](#runtime-settings-web)中。

| 字段 | 安装程序设置的值 | 含义 |
| --- | --- | --- |
| `host` | `unix:///var/run/docker.sock` | 显式 Docker Engine 套接字 |
| `image` | 加载后 Runtime 镜像的本地 ID | 发行版本的 `image_id` 或 `image_manifest_digest`。主机的镜像存储决定由哪个 digest 指代已加载的镜像，因此该值属于节点本地；适配器只接受这两个值 |
| `network` | `oac-node-<installation-id>` | Runtime 容器网络 |
| `seccomp_file` | `<node-root>/runtime/seccomp.json` | 所匹配发行版的 seccomp 配置文件 |
| `nested_sandbox` | `true` | 启用 Docker 适配器的 init 进程和 proc-mask 配置 |

[Docker 适配器](sandbox-provider.md#docker-adapter)负责容器隔离、卷布局和生命周期行为。

## 安装目录 {#installation-directory}

安装目录默认为 `~/.oac/core`（Windows 为 `$HOME/.oac/core`）。其中保存进程设置和原生管理命令；服务持久数据位于 [Compose 数据卷](#compose-installations)。

| 路径 | 内容 | 修改者 |
| --- | --- | --- |
| `.env` | 进程设置和固定的 Compose 项目名 | 用户修改后运行 `oac apply` |
| `compose.yaml` | 已校验的发行版服务定义 | 安装程序，在安装或[升级](getting-started/operations.md#upgrade)时 |
| `compose.override.yaml` | 可选的本地 Compose 条目。源码包安装中它是 JSON，还保存四个服务镜像和 Web 的 `/node-payload` 绑定 | 用户。`install.py` 只替换自己的条目，并要求 JSON 格式。`oac install --upgrade` 会删除 `/node-payload` 绑定和主机上的 `node-payload/` 目录，因为发布版本从数据卷提供该载荷 |
| `oac`（Windows 为 `oac.exe`） | 原生管理命令 | 安装程序 |

同级 `<install-dir>.lock` 目录用于同步操作并一直保留；`<install-dir>.staging` 保存尚未就位的安装文件。两者都不保存服务数据。Unix 上安装程序以 `0700` 创建私有目录，以 `0600` 创建配置文件。

Compose 项目名为 `oac-<10 hex digits>`，服务包括 `init`、`database`、`core` 和 `web`。Core 启动时执行数据库迁移。Web 提供控制台，是唯一发布端口（`OAC_WEB_PORT`）的服务；它转发的内容见[请求边界](./web/console-server.md#request-boundary)。没有服务持有 Docker 套接字，Web 和 Core 也没有 KVM 访问权限。

## 独立工作区存储 {#independent-workspace-storage}

支持的独立文件系统组合是 microsandbox 与[内核 NFS 适配器](./workspace-provider.md#kernel-nfs-adapter)。在 **设置 → 工作区存储** 中选择它（`/core/v1/workspace-storage`，参阅 [Core 管理 API](../../contracts/agents-api/zh/admin-api.md#workspace-storage)）；该选择及其租户容量适用于新工作区，现有工作区保留自身配置。[设置工作区存储](getting-started/workspace-storage.md)涵盖 NFS 导出、`oac-node` 账户、Compose 挂载、选择负载和配额模式存储服务器。

## 附录：没有安装程序时的 Core 环境 {#appendix-core-environment-without-the-installer}

Core 读取进程环境。Compose 将 `.env` 插值到环境中，并把机密文件挂载到下表中的容器路径。直接运行 Core 时，将文件变量设为 Core 进程可读取的绝对路径；见[服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md)。

| 变量 | 设置来源 |
| --- | --- |
| `OAC_PUBLIC_URL` | 必填。[公共 URL](#settings)。Core 只校验一次，并从中派生 Agents API 基地址、守护进程 WebSocket URL、自托管 `remote_url`、安装程序下载地址、托管沙箱地址和部署的只读 `core_url`，绝不从请求标头派生 |
| `OAC_ADDR` | 安装程序在容器中设置为 `:8091`。独立启动的 Core 在未设置或为空时，默认使用 `127.0.0.1:8091`。主机部分为空或未指定时，`oac-core healthcheck` 在 `127.0.0.1` 上探测其 `/healthz` |
| `OAC_DATABASE_URL` | 必填。不含密码的 PostgreSQL URL |
| `OAC_DATABASE_PASSWORD_FILE` | `/run/database/password`。此时 URL 不得包含密码 |
| `OAC_CREDENTIAL_KEY_FILE` | 必填。`/run/oac/credential.key`：Base64 编码的 32 字节随机密钥。Core 用它加密存储的凭据 |
| `OAC_CORE_KEY_DIGESTS_FILE` | 必填。`/run/oac/core-key-digests.json`：一个包含 Core 密钥 SHA-256 的 JSON 数组 |
| `OAC_INSTALLATION_ID_FILE` | 必填。`/run/oac/installation.id`：安装 ID，采用规范 UUID 格式。如果 ID 与数据库记录的 ID 不一致，Core 会拒绝它 |
| `OAC_EXECUTION_CONCURRENCY`、`OAC_DEFAULT_HARNESS`、`OAC_HARNESSES`、`OAC_WRITE_AUDIT_RETENTION`、`OAC_OAUTH_TRUSTED_ORIGINS`、`OAC_HISTORY_SETTINGS_FILE`、`OAC_LOG_LEVEL`、`OAC_LOG_FORMAT`、`OAC_LOG_ADD_SOURCE` | 对应的[进程设置](#settings)。Web 也读取三个日志设置 |
| `OAC_PROVIDER_ROOT` | 适配器构件的绝对根目录。Core 镜像设置为 `/opt/oac`。每个适配器都拥有此根目录下的辅助路径。当其中的 `native-installers/` 目录包含 `catalog.json` 时，Core 在核对该目录清单与自身发行版后提供自托管守护进程安装程序。适配器状态位于 `/state`，即数据卷的 [`state/`](#compose-installations) |

Core 会记录所加载的历史文件路径，但绝不记录环境变量的值或文件内容。

显式 OAuth 受信任源无效时，Core 会停止启动。条目必须是不含凭据、查询参数和非根路径的 HTTPS 源地址。[Vaults](../../contracts/agents-api/zh/vaults.md) 负责刷新和网络策略。私有颁发者还需要受信任的 CA：独立管理的 Unix Core 可以使用 Go 的 `SSL_CERT_FILE` PEM CA-bundle 覆盖机制，从而保留证书验证。托管安装没有自定义 CA 设置。

## 附录：没有安装程序时的 Web 环境 {#appendix-web-environment-without-the-installer}

Compose 为 Web 设置这些变量。仅在不使用 Compose 运行控制台时才自行设置。Compose 把数据卷的 `secrets/web/` 挂载到 `/run/oac`，并设置 `OAC_WEB_CORE_KEY_FILE=/run/oac/core.key`。

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `OAC_WEB_ADDR` | `:8080` | 监听地址。主机部分为空或未指定时，`oac-web healthcheck` 在 `127.0.0.1` 上探测其 `/healthz` |
| `OAC_PUBLIC_URL` | 必填 | [公共 URL](#settings)：面向浏览器的准确源地址，可以使用 HTTP 或 HTTPS，且不得包含路径。Host 和源地址检查使用此值；HTTPS 会使 Session Cookie 具备 `Secure` 属性 |
| `OAC_WEB_UPSTREAM` | `http://core:8091` | Core 的源地址，可以使用 HTTP 或 HTTPS，且不得包含凭据、查询参数或路径 |
| `OAC_WEB_CORE_KEY_FILE` | 必填 | 常规文件的绝对路径，该文件没有组或其他用户权限，并保存 Core 密钥：至少 32 个字符、不含空白字符、最大 4 KiB |
| `OAC_WEB_DIST` | `/www` | 已构建控制台的绝对目录；必须包含 `index.html` |
| `OAC_WEB_NODE_PAYLOAD_DIR` | 未设置 | 所匹配发行版的节点载荷（即安装程序的 `node-payload/`）的绝对路径。未设置时，不提供 `/node-install/*`，且 **添加节点** 不可用 |

未设置或为空的变量使用其默认值。无效值会阻止控制台启动，并显示一条指明变量名的消息。控制台还会读取三个日志[进程设置](#settings)，并拒绝 Core 拒绝的值。对于不在同一台计算机上的任何浏览器，请使用 HTTPS。

## 附录：Runtime 历史文件 {#runtime-history-file}

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

## 附录：Runtime 资源目录 {#runtime-resource-directories}

Runtime 在发现 Harness 适配器之前解析资源目录。`<RuntimeHome>` 是由 `OAC_RUNTIME_HOME` 选择的 Runtime 主目录。`OAC_RUNTIME_STATE_DIRECTORY` 仅在未设置时采用默认值；显式空值、相对路径或非规范路径会被拒绝。目录设置没有其他文件或环境变量回退来源。

| Runtime 进程设置 | 默认值 | 打包的 Linux 镜像 |
| --- | --- | --- |
| `OAC_RUNTIME_INITIALIZATION_DIRECTORY` | `<RuntimeHome>/initialization` | `/environment/initialization` |
| `OAC_RUNTIME_PACKAGE_DIRECTORY` | `<RuntimeHome>/packages` | `/environment/packages` |
| `OAC_RUNTIME_STATE_DIRECTORY` | `<RuntimeHome>` | `/environment/runtime-state` |

状态目录包含保留的原生 Harness 历史和能力安装完成记录，与声明的工作区目录分开。选择独立状态目录不会将 Runtime 设备凭据或连接身份移出 Runtime 主目录中的实例私有路径；不得随原生历史复制这些数据。[Harness 接入指南](../../contracts/agents-api/zh/harness-onboarding.md#register-the-adapter)定义适配器边界，[Environment 准备](../../contracts/agents-api/zh/environments.md)负责初始化和包的行为。

将状态放在独立文件系统上，可以在替换计算资源时保留这些文件。原生续接仍要求 Harness 已通过资格验证、所有权匹配，并确认前一个写入者已停止。这不保留进程内存或计算资源根磁盘上的任意文件。
