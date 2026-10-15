---
title: "开发 OpenAgentCore"
source: docs/development.md
source_hash: 3516f7088aadb05c19a61187a3c66fcf39503c24635b38509349d05eecf06623
---

准备工作副本，构建组件并验证修改。如需使用已安装的实例，从[入门指南](getting-started/index.md)开始。修改代码前阅读[贡献者规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md)。

组件职责和执行流程参见[架构](architecture.md)。

## 准备工作副本 {#set-up-a-checkout}

使用独立 worktree，让实验与验证不影响其他工作副本。从 `main` 已更新的现有克隆执行：

```sh
git worktree add ../openagentcore-change -b codex/my-change main
cd ../openagentcore-change
```

安装 [go.mod](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/go.mod) 中指定版本的 Go、Node 22.13 或更高版本、[package.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/package.json) 中指定版本的 pnpm，以及 Python 3.9 或更高版本。完整检查在 Linux 上运行，需要专用 PostgreSQL 数据库、microsandbox helper 使用的 OpenSSL 开发库、发行包压缩使用的 pigz 和 Playwright 浏览器。提供商和 Runtime 构建的其他前提条件见各组件指南。

```sh
make node-deps
python3 -m venv .venv
.venv/bin/python -m pip install -r services/core/tests/requirements.txt
.venv/bin/python - <<'PYTHON'
import json
import subprocess
import sys

pin = json.load(open("contracts/agents-api/upstream.json"))
subprocess.check_call([
    sys.executable, "-m", "pip", "install",
    "git+" + pin["repository"] + "@" + pin["commit"],
])
PYTHON
pnpm --filter @oac/web exec playwright install --with-deps chrome
export OAC_TEST_OFFICIAL_SDK_PYTHON="$PWD/.venv/bin/python"
```

### Node 依赖边界 {#node-dependency-boundaries}

每个 pnpm 模块在 `package.json` 旁拥有自己的 `pnpm-lock.yaml`。Website 和 Claude SDK adapter 是独立 pnpm 项目，各自拥有工作区边界。Web/example/client 工作区使用 `sharedWorkspaceLockfile: false`：链接已声明的工作区依赖，但不共享依赖解析或虚拟存储。根脚本仅编排模块命令，不安装工具依赖。构建与测试工具应在导入或执行它们的模块中声明。MiniMax companion 使用自己的 npm manifest 和 `package-lock.json`，位于 pnpm 工作区之外。

使用 `pnpm --dir website install --frozen-lockfile` 安装一个模块，或使用 `pnpm --filter @oac/web... install --frozen-lockfile` 连同工作区依赖一起安装。通过同一 package filter 添加或更新依赖，并提交该模块的 manifest 与 lockfile。`make node-deps` 安装所有 pnpm 模块，用于完整本地验证。Web 和示例通过明确的 `workspace:*` 依赖共享 `packages/agents-client`；它们的检查也安装该 client。组件 Make target 使用过滤后的安装与检查。CI 缓存仅使用该作业的依赖锁；[CI 选择策略](maintainers.md#continuous-integration) 负责决定修改选择哪些检查。

私下将 `OAC_TEST_DATABASE_URL` 设置为专用 PostgreSQL 测试数据库。不要让测试套件连接安装实例或产品数据库。[测试数据库规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#test-database) 列出角色所需权限。

修改原生包固定版本时，遵循[真实执行验收规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#live-acceptance)。

## 不发布版本，直接安装工作副本 {#install-a-checkout-without-publishing-a-release}

使用此路径体验已提交工作副本中的完整 Core、Web 和受管沙箱节点。它复用正式发版的分发构建器、Compose 服务、初始化和节点安装器。构建时需要联网获取固定的上游依赖；生成的离线包不依赖 OpenAgentCore 发版或镜像仓库。在 Linux amd64 上按[分发构建前提](maintainers.md#build-a-distribution)构建和安装。需要 Docker Engine 26+ 和 Compose 2.26+。源码必须已提交，包括本地修改。

在仓库根目录一次性准备固定 Runtime 输入，然后构建和安装。如果 `~/.oac/build/release-inputs/inputs.json` 已存在，跳过第一条命令并复用它：

```sh
bash scripts/prepare-release-runtimes.sh
inputs="$HOME/.oac/build/release-inputs/inputs.json"
export CODEX_CLI_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["codex"])' "$inputs")"
export MCODE_HARNESS_BUILD_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mcode"])' "$inputs")"
revision="$(git rev-parse HEAD)"
export CORE_DISTRIBUTION_BUILD_DIR="$HOME/.oac/build/source-$revision"
export CORE_DISTRIBUTION_OFFLINE=1
export CORE_DISTRIBUTION_PLATFORMS=linux/amd64
unset CORE_DISTRIBUTION_RELEASE_BASE_URL
make build-core-distribution
bundle="$CORE_DISTRIBUTION_BUILD_DIR/oac-$revision-linux-amd64"
python3 "$bundle/install.py" \
  --install-dir "$HOME/.oac/local" \
  --host 0.0.0.0 --web-port 8080 \
  --public-url http://YOUR_CORE_HOST:8080
```

将 `YOUR_CORE_HOST` 替换为浏览器与沙箱节点都能访问的地址。需要时使用反向代理后的 HTTPS 地址；入口配置见[安装选项](getting-started/install-options.md)。新安装会输出用于登录的 Core 密钥；重新运行时输出显示该密钥的命令。它创建标准 Compose 安装，以及包含该构建公共节点制品的只读 `node-payload` 目录。磁盘需同时容纳解压后的构建包和安装副本。Core、Web 均不会获得 Docker socket。

构建还会生成 `oac-<commit>-linux-amd64-offline.tar.gz`。可将其复制到另一台 Linux amd64 Docker 主机，解压后执行其中的 `install.py`；目标主机只需要 Python 3.9+ 和 Docker，无需源码、Go、Node 或 pnpm。安装完成前保留解压目录；安装后的服务和节点下载不再依赖它的原始位置。

在 Web 中配置模型提供商，创建沙箱部署，再使用[节点](getting-started/nodes.md)中显示的命令添加节点。节点前提仍然适用，包括 microsandbox 所需的 KVM。命令直接从本次安装下载匹配的执行制品。此路径构建全部三个受管 Harness；除非提供[原生安装器目录](maintainers.md#native-installers)，否则可选的自托管原生安装器不可用，不会生成占位目录。

启动失败或安装已停止时，使用保存的配置，而不是重新安装：

```sh
cd "$HOME/.oac/local"
docker compose logs --tail 100
# Retry without downloading release images or replacing saved configuration.
docker compose up -d --wait --pull never
./oac core-key --show
```

对安装目录重新运行同一源码包的 `install.py`，会以已保存的设置继续该安装；只有新目录才需要 `--public-url`。其他提交的源码包会拒绝已有安装，除非传入 `--upgrade`，此时会[原地升级](getting-started/operations.md#upgrade)该安装。新安装若在启动服务之前失败，安装目录会保持运行前的状态；修复原因后重新运行同一命令即可。配置、密钥和停止服务参见[运维命令](getting-started/operations.md)。

## 构建与运行组件 {#build-and-run-components}

在仓库根目录执行：

```sh
make build-core
make build-daemon
```

Core 构建产物和输出目录设置见[独立 Core 构建](maintainers.md#standalone-core-builds)。daemon 写入 `${OAC_DEV_HOME:-$HOME/.oac}/build/daemon/oac-daemon`。

按照[服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#run-from-source)，使用独立开发数据库运行 Core migrator 和 server。[配置附录](configuration.md#appendix-core-environment-without-the-installer) 负责独立进程设置。如需完整运维安装，使用[安装指南](getting-started/install.md)；单独构建 Core 是独立的贡献者工作流。

前端开发使用 [Web 包指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md)中的 fixture 或 Core 连接运行 `pnpm dev:web`。

## 仓库地图 {#repository-map}

| 位置 | 职责 | 后续阅读 |
| --- | --- | --- |
| `services/core/internal/api` | 公开、管理员与机器 HTTP 边界 | [API 索引](api/index.md) |
| `services/core/internal/persistence`、`services/core/internal/db` 和 `services/core/migrations` | Core 的 PostgreSQL adapter、事务、查询和迁移 | [服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#database) |
| `services/core/tests/integration` | 共同驱动 HTTP 边界、执行 Worker 和 PostgreSQL adapter 的测试 | [服务指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#tests) |
| `services/core/internal/execution` | 持久化 Turn 分发与调度 | [Runtime 协议](runtime-protocol.md) |
| `services/core/internal/engine` | 对 Harness 操作与执行位置进行纯资格验证 | [Harness 接入](../../contracts/agents-api/zh/harness-onboarding.md) |
| `internal/agentdaemon/proto` | Core–Runtime wire 类型与验证器 | [Runtime 协议](runtime-protocol.md) |
| `internal/runtimebootstrap` | 提供商到 Runtime 的启动输入 | [Runtime 引导](runtime-bootstrap.md) |
| `apps/daemon/internal/dispatch` | Runtime 准备、Executor 复用、Turn 和清理所有权 | [Harness 生命周期](../../contracts/agents-api/zh/harness-onboarding.md#required-adapter-interfaces) |
| `apps/daemon/internal/agent` | 原生 Harness adapter | [原生参考](../../contracts/agents-api/zh/harness-onboarding.md#native-references) |
| `services/core/internal/sandbox` | 提供商接口与托管计算资源生命周期 | [提供商接入](sandbox-provider.md) |
| `services/web` | 控制台登录与服务端管理代理 | [控制台服务端](web/console-server.md) |
| `apps/web` 和 `packages/agents-client` | 控制台 UI 与类型化 client | [Web 指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/apps/web/README.md) |
| `deploy` 和 `scripts` | 发行、安装和验证工具 | [维护者指南](maintainers.md) |
| `contracts/agents-api` | 固定 schema、语义契约和覆盖台账 | [覆盖台账](../../contracts/agents-api/zh/index.md) |

## 选择扩展边界 {#choose-an-extension-boundary}

通过[协议地图](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#protocols-at-every-boundary)找到新增 Harness、沙箱提供商、模型提供商、API 操作或 Runtime 消息所需的代码与指南。指南负责注册、支持的操作和实现资格验证所需检查。工作区能力（如 Skill、Plugin、MCP 和系统包）从 [Environment](../../contracts/agents-api/zh/environments.md) 开始。

## 验证修改 {#validate-a-change}

按照[修改所需检查](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#checks-for-a-change)，为当前 diff 和直接受影响的行为选择针对性检查。下表列出各边界的入口；从中选择相关测试。

| 修改 | 针对性验证 |
| --- | --- |
| Core handler、持久化或 client | `make check-core` |
| SQL 查询 | `make sqlc-generate`，检查生成文件，再运行 `make check-sqlc` |
| 公共 schema、Core 扩展或内部处理函数注解 | `make openapi`、`make check-openapi`；检查生成类型和三个命名空间的 schema |
| 共享 Runtime 协议 | `make check-runtime-contract` |
| 提供商集成 | `make check-sandbox-provider-contract` 和提供商的原生检查 |
| Claude SDK bridge 与产物 | `make check-claude-sdk` |
| Web UI 与 client | `make check-web` |
| 发行或安装器 | `make check-distribution` |
| 文档 | `make check-names`；`make check-distribution` 验证 Markdown 链接与打包文档 |

Fixture 浏览器验收使用 loopback 端口 18092 和 4174。并行验证时通过 `AGENTS_FIXTURE_PORT` 和 `AGENTS_WEB_PORT` 选择空闲端口。验证 worker 之间使用独立数据库、端口和容器。编译、fixture 成功和真实模型/提供商验收分别证明不同事实；应明确报告跳过或不可用的检查。验证后遵循[独立盲审工作流](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#review)。

## 修改文档 {#change-documentation}

[网站指南](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/website/README.md)负责文档导航、本地预览、验证和 GitHub Pages 发布。

已发布章节索引使用 `index.md`。相对 Markdown 链接应明确写出（`./page.md` 或 `../page.md`），使其在仓库和发行包中都可用。仅属于仓库的指南与源码链接应指向 GitHub。字面的尖括号占位符写在代码 span 中；生成区域标记使用 Markdown 引用注释（`[//]: # (comment)`），使源码在 GitHub 和 VitePress 中都能渲染。通过生成器修改生成内容。

在[文档所有权地图](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#documentation-ownership)中找到所属源文档，并遵循[文档规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/AGENTS.md#documentation)。读者使用仓库中的 authored Markdown。生成的 OpenAPI schema 和 Harness catalog 有各自的生成器；参见[契约与 schema 规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#contract-and-schema-rules)。

发行包在 `scripts/core-distribution-manifest.py` 中维护明确的文档列表。移动已打包文件或修改标题时，更新指向它的链接，并运行[发行包文档检查](maintainers.md#build-a-distribution)。
