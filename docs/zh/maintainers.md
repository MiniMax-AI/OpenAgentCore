---
title: "构建并发布 OpenAgentCore"
source: docs/maintainers.md
source_hash: a6d2d1e9846b1f22a9566048fd3821eee601010aea572c6a402436800c44491b
---

本指南面向负责构建和发布 OpenAgentCore 的维护者。要安装 Core 和 Web，请使用 [安装指南](getting-started/install.md)。安装器代码遵循的规则见 [部署](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/README.md) 和 [节点安装器](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/node/README.md)；必需检查见 [CONTRIBUTING](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#required-checks)。

## 构建分发包 {#build-a-distribution}

分发包是从同一个提交构建的一组相互匹配的发布资源：控制归档（安装器、`oac` 命令，以及 Core、Web、ingress、agent-host 和 PostgreSQL 镜像）、作为独立文件的 Runtime 镜像和节点构件，以及原生安装器。

安装镜像和归档支持 Linux amd64。宿主机 `oac` 二进制仍提供 Linux amd64/arm64、macOS amd64/arm64 和 Windows amd64 版本，用于操作 Linux amd64 Docker 引擎；它们不启用 arm64 执行或模拟。每个版本索引校验其平台归档后才更新浮动标签。


请在 Linux x86_64 上构建，所需环境包括与 Debian 12 兼容的 glibc、Docker、`go.mod` 中指定的 Go 版本、C 编译器（microsandbox 辅助程序使用 CGO 构建）、Node、pnpm、Python 3.9 或更高版本、curl、tar、pigz 和 sha256sum。源代码必须保持干净并已提交。请先准备固定版本的 Codex 包和 MiniMax Code 配套程序，然后执行构建：

```sh
bash scripts/prepare-release-runtimes.sh
inputs="$HOME/.oac/build/release-inputs/inputs.json"
export CODEX_CLI_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["codex"])' "$inputs")"
export MCODE_HARNESS_BUILD_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mcode"])' "$inputs")"
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://github.com/MiniMax-AI/OpenAgentCore/releases/download/v1.2.3
make build-core-distribution
```

如果 `~/.oac/build/release-inputs` 已存在，`prepare-release-runtimes.sh` 会拒绝执行；请使用全新的构建主机。

| 变量 | 作用 |
| --- | --- |
| `CORE_DISTRIBUTION_RELEASE_BASE_URL` | 用于提供生成的资源文件名的带版本 HTTPS 目录（绝不能使用 `latest`）。除非 `CORE_DISTRIBUTION_OFFLINE=1`，否则为必填项 |
| `CORE_DISTRIBUTION_OFFLINE` | 设为 `1` 时还会构建离线归档 |
| `CODEX_CLI_DIR`、`MCODE_HARNESS_BUILD_DIR` | `prepare-release-runtimes.sh` 固定的 Runtime 输入 |
| `OAC_NATIVE_INSTALLER_BUILD_DIR` | 原生安装器目录；请参阅[原生安装器](#native-installers) |
| `CORE_DISTRIBUTION_BUILD_DIR` | `~/.oac` 下的输出目录。默认值：`~/.oac/build/core-distribution` |
| `CORE_DISTRIBUTION_BUILD_NETWORK` | Docker 构建网络：`default`、`host` 或 `none` |
| `CORE_DISTRIBUTION_MICROSANDBOX_ARCHIVE` | 已缓存的 microsandbox 发布归档。默认值：`~/.oac/cache/microsandbox-v0.7.2-linux-x86_64.tar.gz`，缺失时下载 |
| `CORE_DISTRIBUTION_DATABASE_IMAGE` | PostgreSQL 16 镜像；默认值通过其 linux/amd64 清单摘要固定 |

构建过程会复用 Core、Web、Runtime、SDK 和辅助程序构建器。清单会记录提交和源代码树、镜像配置及 OCI 清单摘要、Runtime OCI 清单摘要、microsandbox 运行时和固件哈希，以及每个 Runtime 和节点构件的大小与 SHA-256；原生安装器在[目录](#native-installers)中仅记录其 SHA-256。输出包括控制归档及其 `.sha256`、可选的离线归档，以及带版本号的 Runtime、节点和原生安装器资源。此过程不会发布任何内容。如果目标目录中已包含此提交的分发包，重建会拒绝执行。

ingress 镜像仅包含 `oac` 和四个节点元数据文件：`manifest.json`、`SHA256SUMS`、`node-install.pyz` 与 `runtime/seccomp.json`。构建流程根据与分发包相同的 Runtime 和构件身份生成其清单，再禁用网络验证初始化。节点清单不包含控制平面镜像身份，因此可在 ingress 镜像生成前打包。

控制归档不包含 Runtime 镜像或节点执行构件；离线归档包含这些内容。[下载契约](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/node/README.md#download-contract)说明了节点如何获取这些内容。

分发包会携带 `scripts/core-distribution-manifest.py` 中 `BUNDLED_DOCS` 列出的文档。随包文档之间的链接保持相对路径；其他所有相对链接都会重写为该捆绑包对应提交在 GitHub 上的同一文件。链接或锚点无法解析时，构建会失败；`make check-distribution` 会对 `example/` 之外每个受 Git 跟踪的 Markdown 文件运行相同检查。添加或移动安装器或其输出所引用的文档时，请更新该列表。

### 原生安装器 {#native-installers}

自托管机器使用 Linux amd64 安装器，包含 `oac-daemon`、`oac-sandbox-io` 及匹配的构建和平台元数据。`native-check` 构建并验证它，不打包 Node.js 或 Harness。手动打包和发布检查保留 `oac-native-installer-Linux-X64.tar.gz` 七天；普通 PR 检查不上传成功的软件包。从精确的源提交下载该构件，并在同一检出中组装目录：

```sh
node scripts/build-native-catalog.mjs INPUT_DIR OUTPUT_DIR
export OAC_NATIVE_INSTALLER_BUILD_DIR=OUTPUT_DIR
```

该目录会记录提交、Runtime 协议版本、每个归档的 SHA-256，并在提供发布基址时记录其带版本号的 URL。控制归档和 Core 镜像仅携带 `native-installers/catalog.json`；这些归档会成为独立的 `oac-native-<commit>-<platform>.tar.gz` 发布资源，离线归档则会在 Core 镜像之外为每个平台保留一份副本。如果没有目录，Sessions 会将安装命令报告为不可用，并且发布工作流会拒绝发布。

### Runtime 镜像和辅助程序 {#runtime-images-and-helpers}

`make build-core-distribution` 构建 `deploy/distribution/AgentHost.Dockerfile` 的 agent-host 和 sandbox 目标。在 Linux amd64 上从仓库根目录执行命令；输出默认位于 `${OAC_DEV_HOME:-$HOME/.oac}/build`。

[Harness 版本固定](../../contracts/agents-api/zh/harness-onboarding.md#native-version-pins)定义构建输入和生成的包投影。准备载荷前运行 `make check-harness-catalog`。

使用 `scripts/prepare-release-runtimes.sh` 准备固定版本的官方 Codex Linux x64 包和 MiniMax 配套程序，或通过 `CODEX_CLI_DIR` 和 `MCODE_HARNESS_BUILD_DIR` 指向已有输入。`scripts/build-{codex,claude,mcode}-runtime.sh` 为两个镜像构建器校验并准备 Harness 载荷，不构建客体 Runtime 镜像。Claude 载荷是固定 SDK 和适配器的带校验和导出：

```sh
make build-claude-sdk-runtime
export CODEX_CLI_DIR=/absolute/path/to/package MCODE_HARNESS_BUILD_DIR=/absolute/mcode-harness
bash scripts/build-agent-host-images.sh
```

脚本构建 `OAC_AGENT_HOST_IMAGE`（默认 `oac-agent-host:dev`）和 `OAC_SANDBOX_IMAGE`（默认 `oac-sandbox:dev`）。附加参数（例如 `--label`）传给两次 Docker 构建。agent-host 镜像包含 `oac-daemon`、`oac-process-shim`，以及 `/opt/oac/harnesses` 下的各 Harness；`/opt/oac/harnesses.json` 拥有其激活路径。沙箱镜像包含系统工具和以 UID/GID 1000 运行的 `oac-sandbox-io`，不含 Harness 或 daemon。分发包校验 Sandbox I/O 可执行文件，并通过节点构件已有的 `runtime` 槽位分发此镜像。沙箱的默认登录 profile 保留传入的非空 `PATH`，使[工具环境](../../contracts/agents-api/zh/environments.md#explicit-local-tool-environment)中的包命令仍然可用。路径为空或未设置时保留镜像的默认值；profile.d 脚本和用户启动文件仍可显式修改路径。第三方沙箱镜像自行负责其 shell 启动配置。E2B 模板从同一镜像提取 Sandbox I/O 和此 profile。[认定视图资格](../../contracts/agents-api/zh/harness-onboarding.md#qualify-the-view)运行两个镜像；[Agent-host 容器](configuration.md#agent-host-container)拥有宿主需求。

**E2B 辅助程序。**

```sh
make build-e2b-provider
```

Docker 使用固定版本的 CPython 和 Debian 12 镜像按 `GOARCH=amd64`（默认）或 `GOARCH=arm64` 构建 Linux 辅助程序。Python 依赖闭包（including PyInstaller）在 `services/core/tools/e2b-provider/requirements.lock` 中按哈希锁定；不需要 E2B 账户密钥。要使用其他输出目录，请设置 `E2B_PROVIDER_BUILD_DIR`。构建结果完全由辅助程序源代码、`LICENSE` 和构建脚本决定，因此会按它们的哈希缓存在 `~/.oac/cache/e2b-provider/` 下，仅在它们变化时重新构建。输出为 `oac-e2b-provider-linux-<architecture>.tar.gz` 及其 `.sha256`；解压后会得到 `oac-e2b-provider/`，其中包含可执行文件、`_internal/`、`licenses/`、`requirements.lock` 和 `manifest.json`。Core 镜像使用该目录树；主机需要兼容的 glibc 和 CA 证书，而不需要 Python。

**microsandbox 辅助程序。** 仅支持 Linux，并且需要 C 编译器：

```sh
make build-microsandbox-provider
make check-microsandbox-provider
```

该辅助程序会写入 `~/.oac/build/microsandbox-provider/oac-microsandbox-provider`。其独立的 Go 模块固定 microsandbox Go SDK v0.7.2，并嵌入匹配的 FFI 库；构建生产版本时，绝不能使用该 SDK 的 `microsandbox_ffi_path` 标签。Core 本身仍采用禁用 CGO 的构建。该辅助程序需要 glibc，并且只能在节点上运行。

**microsandbox 运行时。** 分发包使用官方的 [v0.7.2 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2) 归档 `microsandbox-linux-x86_64.tar.gz`，SHA256 为 `47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b`（即 `scripts/core-distribution-manifest.py` 中的 `RUNTIME_ARCHIVE_SHA256`）。构建过程会先验证校验和，再解压 `msb` 和 `libkrunfw.so.5.6.1`，并记录这两个文件的哈希。辅助程序会在每次调用时检查这些哈希，并且绝不安装或升级它们。

### 独立 Core 构建 {#standalone-core-builds}

`make build-core` 会将 `oac-core`、`oac-core-environment-key`、`oac-node` 和 `oac` 构建到 `${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core`（`OAC_DEV_CORE_BUILD_DIR` 可选择其他绝对目录）。构建过程仅将 `scripts/build-core.sh` 中列出的源文件集（Core 服务、其契约、所需的共享软件包以及根 Go 模块文件）复制到临时上下文，并使用禁用 CGO、只读模块和裁剪路径的方式构建。它不需要 Node、Docker 或其他应用程序。Core 新增共享依赖时，请将该软件包加入列表；绝不能复制整个仓库来使其完成编译。

`make docker-build-core` 会根据这些命令和 E2B 辅助程序构建 `oac-core:dev` 镜像（`OAC_DEV_CORE_IMAGE` 可选择其他名称）。基础镜像是通过摘要固定的 `debian:bookworm-slim`，包含 CA 证书以及辅助程序所需的 glibc 运行时；默认用户的 UID/GID 为 65532，Core 监听 `:8091`。此本地构建目标生成 Linux amd64 镜像；[分发构建](#build-a-distribution)使用同一架构。对镜像或其构建进行更改时，除了相关的源代码检查外，还必须运行 `make check-core-container`：它会在只读根文件系统上针对该镜像运行官方客户端测试套件，并且需要 Linux Docker、非 root 用户，以及服务检查中的[测试数据库和固定版本 SDK](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#official-client-verification)（`OAC_TEST_DATABASE_URL` 指向一个已应用迁移的 `oac_*_tests` 数据库，并设置 `OAC_TEST_OFFICIAL_SDK_PYTHON`）。

## 发布版本 {#publish-a-version}

在经过审查的提交上推送版本标签，以运行 `core-release` 工作流：

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

标签使用 `vMAJOR.MINOR.PATCH` 格式，可选用 `-rc.1` 等预发布后缀以及 `+build.1` 等构建元数据。预发布后缀会创建 GitHub 预发布版。推送标签即表示发布决定。自动检查用于确定构建和测试结果，而不是真实模型资格：推送标签前应评估实际执行证据。模型凭据和私有证书颁发机构绝不能进入 CI 或发布输入，包含它们的验收镜像也不例外。

工作流会在带标签的提交上运行 `check`，包括完整的本地门禁、官方客户端和镜像验收，以及启用打包构件的原生平台矩阵。检查成功后，`blacksmith-4vcpu-ubuntu-2204` 上的一个 `build` 作业会准备固定的 Runtime 输入、复用原生安装器、构建分发包，并直接从本地文件发布。此合并作业具有 `contents: write` 和 `packages: write` 权限；检出过程不会保留凭据。只有发布失败或手动构建不发布时，才会把这些文件保留为未压缩的 Actions 构建产物，供恢复使用。

分发归档和 Runtime 归档使用 `pigz` 级别 6，最多使用四个压缩工作线程，并且 gzip 头部中不包含文件名或时间戳。发布器会验证归档和原生安装器校验和、解析仓库身份、拒绝使用该标签已有的 Release 或草稿，并创建一个具有固定 ID 的草稿。最多四个资源可并发上传，按从大到小的顺序进行，且不会重试。确认完整的远程资源清单后，发布器会验证所有镜像归档和现有注册表标签，再并发推送最多四个镜像。每个镜像配置和注册表清单都会接受验证；任何错误都会使 Release 保持未发布状态。失败操作返回前，正在进行的传输会完成。发布器会按 ID 发布草稿，并在发布前重新检查版本标签。

### 容器注册表 {#container-registry}

版本发布和手动 `build-<full SHA>` 草稿使用 `ghcr.io/minimax-ai/openagentcore/<component>:<version>`，其中 `<component>` 为 `core`、`web`、`runtime`、`ingress` 或 `agent-host`。所有组件索引均包含 Linux amd64 镜像；`runtime` 组件包含沙箱镜像。各平台镜像使用 `<version>-<architecture>` 标签，从发行归档加载。已有版本标签必须与发行镜像及平台集合一致。发布器校验全部版本索引后，才为稳定版更新 `latest`；预发布版和草稿保持 `latest` 不变。PostgreSQL 使用上游镜像。容器标签中的 SemVer 构建元数据用 `_` 替换 `+`，版本字符串上限为 128 个字符。镜像校验后，发布器上传该版本的 `compose.yaml` 和校验和清单。Compose 用索引摘要固定 ingress，初始化时检查其构建版本与 Compose 版本一致。

合并的构建/发布作业使用具有 `packages: write` 权限的 `GITHUB_TOKEN`。首次发布时，GitHub 会将每个容器软件包创建为私有：软件包管理员必须先在各自的软件包设置中将全部五个软件包改为 **Public**，用户才能匿名拉取。请参阅 [GitHub container visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。更改可见性后，请验证未认证拉取。仅更改仓库可见性并不会使新的容器软件包变为公开。

GHCR 和 GitHub Releases 不共享事务。发布失败后，GHCR 中可能仍会保留一些匹配的版本标签；请保留这些镜像，并使用原始构件按照下文的草稿恢复流程操作。除清单缺失以外，注册表故障都会停止发布。作业摘要会记录按摘要固定的引用。这些镜像和渲染后的 Compose 文件仍需要[配置](configuration.md)中描述的配置、机密和路由。

[安装指南](getting-started/install.md#install)介绍版本选择和各平台的启动命令。

Go 检查和构建作业共享 `~/.oac/cache/` 下的 Go 模块和编译器缓存目录，缓存键由运行器 OS 和架构、全部 Go 模块文件、检查/构建分区以及提交确定。分区键可防止并发作业在同一个键下保存不同的编译器子集。发布构建既可以使用后端检查的缓存，也可以使用更早发布构建的缓存。较旧的缓存只会为下载和编译提供初始内容；每项检查仍会运行。发布作业还会缓存 npm 软件包下载内容和固定版本的 microsandbox 归档，并在每次构建时验证后者的校验和。Actions 缓存可见性遵循 GitHub ref 的作用域；特定标签的缓存不会与其他发布标签共享。只有作业成功后才会保存新键。

绝不移动发布标签或覆盖已发布的资源。发布失败时，请先检查 Release：即使响应丢失，发布也可能已经完成。对于完整的已发布 Release，请保持原样。对于不完整的草稿，仅在检查后将其删除，然后使用 `gh run download RUN_ID --name core-release-REVISION --dir ASSET_DIRECTORY` 下载原始 `core-release-<revision>` Actions 构建产物，并使用该确切源代码修订版本的检出运行 `python3 scripts/publish-core-release.py --assets ASSET_DIRECTORY`。将 `GH_REPO`、`GH_TOKEN`、`RELEASE_REVISION`、`RELEASE_TAG` 和 `RELEASE_MODE` 设置为原始发布输入，并将 Docker 登录到 GHCR。该脚本会重新验证资源，并拒绝使用已有 Release。恢复上传失败时，绝不能重新运行合并的构建作业，也绝不能重新创建标签。

### 构建候选版本但不发布 {#build-a-candidate-without-publishing}

手动运行需要完整的提交 SHA，会执行相同的检查和构建，默认生成离线归档，并且不会发布 Release：

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/OpenAgentCore --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

设置 `draft_release=true` 时，结果是未发布的 `build-<full SHA>` 草稿 Release，其镜像会以该标签推送；设置 `draft_release=false` 时，文件会保留在 Actions 构建产物中。请使用完全匹配的构件集合；绝不能混用构建结果，也绝不能通过 `latest` 解析组件。

## 持续集成 {#continuous-integration}

每个 PR 都会运行 `core-check` 并报告必需状态 `check`。main 使用 GitHub 分支保护，合并前必须通过此检查且分支必须为最新状态，因此合并不会启动另一份测试套件。所有更改都必须通过经过检查的 PR 提交；管理员绕过检查并不代表 CI 成功。推送到 main 时，如果输入发生变化，就会发布网站，并运行 `cache-warm`：它构建 E2B 辅助程序和 pnpm 存储，不运行测试，因为只有 main 上保存的缓存能被每个 PR 和发布标签恢复。版本标签和手动发布构建会在其确切源代码提交上运行完整的发布门禁。`scripts/ci_plan.py` 管理唯一的输入到检查映射。组件规则要求同时匹配目录或脚本前缀以及文件后缀；确切的依赖项、工作流和共享构建输入都有明确规则。规则会在共享使用方和混合变更之间累加。没有匹配构建/测试规则的路径仅运行 hygiene。引入新组件、语言、构建输入或资源位置时，请添加相应规则。

计划器会将 PR 事件所测试的合并提交与其已验证的第一个父提交进行比较。NUL 分隔的 Git 输出和禁用重命名检测会同时保留旧路径和新路径。计划及原因会显示在运行摘要中。历史记录缺失或不一致、检出不匹配、路径无效、计划器/编排发生变更以及共享构建输入发生变化时，都会选择完整门禁。经过验证的空差异仅选择 hygiene。发布、手动和显式 ref 调用始终选择所有组。

| 组 | 检查和使用方 |
| --- | --- |
| `hygiene` | 名称、仓库链接、随包文档完整性以及 CI 计划器/门禁测试；每次变更都会运行 |
| `distribution` | Harness 目录和安装器模式、安装/应用/恢复/清理测试、Compose 解析和初始化固定数据、发布/下载和捆绑包契约、Go 控制台测试与构建；模板解析需要 Docker Compose，不需要 pnpm install 或浏览器 |
| `compose` | 使用从当前检出构建的镜像从空数据卷实际启动、登录和 API 访问、上传文件和下载节点安装器，然后在保留凭据和数据的同时重新配置 URL 并重新创建容器；需要 Docker、Go 和网络访问，不需要模型凭据 |
| `backend` | 并行部分，每部分都有专用 PostgreSQL 保护检查：`runtime`（sqlc 新鲜度、Runtime/共享 Go 测试、Linux microsandbox 辅助程序、守护进程构建）、`core`（独立 Core 构建、Core 服务和客户端测试），以及串行 Core 集成测试包的三个 `integration` 分片 |
| `harness` | Claude SDK 测试和打包、MiniMax 配套脚本 |
| `example` | 可选的应用程序类型检查、测试、构建和隔离的浏览器验收 |
| `web` | TypeScript、Web/客户端测试和 Web 构建 |
| `website` | 针对网站、已发布文档和依赖项变更的网站构建与输出检查 |
| `web-acceptance` | Web 单元测试/构建成功后，以四个隔离分片运行完整 Web 浏览器测试套件；每个分片保留一个工作线程 |
| `api` | 针对独立命令和迁移的可复用官方客户端验收；当镜像/构建/辅助程序输入发生变化时进行镜像验收，并在每次完整门禁中执行 |
| `native` | Linux amd64 启动器、Sandbox I/O 和安装器检查；Linux、macOS 和 Windows 上的可移植 Core 启动器构建与测试 |
| `lint` | 可复用的 actionlint 检查，包括本地复合操作 |

`.github/actionlint.yaml` 会选择 hygiene 和 lint。已知工作流变更会选择其使用方：CI review 和 actionlint 工作流运行 hygiene 和 lint；原生工作流变更会添加原生检查；API 验收工作流变更会添加启用容器验收的 API 检查；网站工作流变更会添加网站检查。共享 Node 操作会选择使用它的每个作业以及 lint。新工作流或未分类的工作流/操作会选择完整门禁，直至在计划器中声明其使用方。计划器测试和 CI 测量脚本运行 hygiene；更改计划器本身会运行完整门禁。

Compose 模板和 Compose 测试发生变更时，会同时选择 `distribution` 固定数据和 `compose` 冒烟作业；Core、Web、守护进程、共享 Go 软件包和镜像 Dockerfile 的变更也会选择冒烟作业。安装 Docker 后，可在本地运行 `python3 scripts/compose-smoke.py` 重复该测试。该脚本使用唯一的项目、自动分配的回环端口，并将在 `~/.oac/tests/` 下生成构件；退出时移除其容器和数据卷。CI 还会在冒烟步骤失败或中断后执行清理。诊断信息会显示容器状态，但不会打印 HTTP 响应正文或登录密钥。Core、Web、agent host 和 ingress 镜像都从当前检出构建；Web 提供占位页面而不是控制台构建，agent-host 镜像不含 Harness；冒烟测试会检查 agent host 能连接到 Core。构建时的节点元数据来自 `deploy/compose/smoke-pins.json` 固定的发布版本；初始化容器禁用网络运行。冒烟测试在 Linux amd64 上运行；原生矩阵在 Linux、macOS 和 Windows 上构建并测试可移植 Core 启动器。

Go 模块和工作区输入会选择后端、API（包括容器）、原生和分发检查。每个 Node 模块都拥有自己的清单和锁文件。网站依赖项会选择网站检查；Web 依赖项会选择 Web 和浏览器检查；示例依赖项会选择示例检查；共享 TypeScript 客户端依赖项会选择 Web、浏览器和示例检查；Claude 适配器依赖项会选择 Harness、原生和分发检查。共享包管理器配置会选择所有 Node 使用方。根 TypeScript 配置会选择 Web 和示例检查；适配器 TypeScript 配置会选择 Harness 和原生检查。每个所选集合都包含 hygiene。混合变更会累加其使用方，并且每个作业都读取同一计划，而不是维护各自的路径列表。例如，仅修改通知的 PR 会跳过数据库、浏览器和原生作业，而同时修改通知和 Core 的 PR 会添加后端和 API 检查。

`docs/` 和 `contracts/` 下已发布的文档和资源，以及 `docs.json`，会选择 hygiene 和 website。其他普通 Markdown 文件（包括源代码目录中的文档）仅选择 hygiene。生成的目录文件和配置参考章节会保留其分发新鲜度检查。Core `.go`、`.sql`、辅助脚本和配置输入会选择后端/API 检查；Web 源代码、样式和资源会选择 Web 检查。嵌入式原生资源和声明的测试固定数据目录无论后缀如何都会选择其使用方，其中包括 Markdown 提示和没有扩展名的数据。安装器变更会添加分发检查。Web 变更会添加 Web 检查和所有浏览器分片；Core/DB 变更会添加后端和官方客户端验收。共享契约、SDK、Runtime 输入和依赖项会根据计划器传播到其使用方。生成的目录和协议输入包括安装器、客户端和 UI 使用方。不要在可复用工作流中重复路径列表，也不要在必需工作流上添加 `paths` 过滤器。

即使计划或依赖项失败，最终 `check` 仍会运行。它要求计划有效且成功、每个所选作业均成功，并且每个未选择作业均被跳过。失败、取消、作业缺失、意外跳过或意外执行都会导致门禁失败。API/原生可复用工作流是此门禁的直接依赖项。同一 PR 上较新的运行会取消之前的运行。发布检查在请求的不可变 ref 上运行；原生打包在这些检查中执行一次，分发构建会等待其完成。

要手动执行完整检查，请使用 **Actions → core-check → Run workflow**。遇到暂时性故障时，请使用 GitHub 的 **Re-run failed jobs**，这样已成功的作业可保持完成状态。PR 更新后，Actions 并发机制会取消已被取代的运行。构建和依赖项缓存可加快执行，但不能替代成功的测试。原生发布安装器通过 Actions 构建产物在同一发布工作流的不同作业之间传递。

`CI review and Feishu notification` 工作流在 PR 合入 main 后运行。矩阵中的 **Code review** 和 **Docs review** 使用独立的 LLM 上下文，并设置 `fail-fast: false`。两者读取合并后的提交和 PR 差异。代码审查检查实现、仓库规则和已有 CI 结果，不触发新一轮测试。文档审查会核对行为变化与文档是否一致，即使没有修改文档；修改文档时，还会检查矛盾、重复维护的事实、CONTRIBUTING 规定的主题归属及中英文含义。每项问题包含文件和行号、依据及最小修改建议。审查只读取仓库，不修改文件。

每项审查返回结构化结果（`ok`、`issues` 或 `incomplete`）及分项中文结论，作为 Actions 构建产物保留七天。**Combined Feishu notification** 等待两项审查结束，通过已有的 `FEISHU_WEBHOOK_URL` 发送一张包含两份结果的 Card 2.0 卡片；可选的 `FEISHU_WEBHOOK_SECRET` 用于签名。卡片固定分为 PR 信息、行为变化、代码与仓库规则、CI 结果和文档审查，各区之间使用分隔线。未发现问题时标题为绿色，发现问题时为红色，审查未完成且尚未发现问题时为黄色。只有通知 job 能读取 webhook 密钥。审查失败、报告缺失或格式无效时标为未完成，另一项已有的结果照常展示。某项审查失败时，通知 job 仍会运行。只有飞书返回 `code=0` 才确认送达；请求失败或送达状态不明时不自动重试，以免重复发消息。每项审查的执行超时为 25 分钟。审查和发送失败不会阻止合并；关闭未合并的 PR 不触发此流程。持有通知凭据时，工作流只检出合并后的提交。

浏览器作业各自拥有独立的固定数据和服务；对共享可变固定数据增加 worker 数不安全。失败的浏览器作业保留报告与 trace 七天。原生失败阶段摘要保留七天，详细输出留在 Actions 日志中；凭据和临时安装目录不上传。成功的原生归档仅用于显式手动打包或发布时上传，不重新压缩已压缩的归档。发布分发产物保留现有恢复策略；失败发布可以按前述方式复用原构建。

本地 Node 复合操作会安装固定版本的 pnpm，并根据锁文件、OS、架构、Node 版本和 pnpm 版本缓存其软件包存储。它缓存下载的软件包，而不是 `node_modules`；安装仍保持冻结状态。Go 分区会保留[发布](#publish-a-version)部分所述的现有模块/编译器缓存。三个原生平台独立运行；并行执行可缩短总耗时，但不会减少机器总时间。

对于本地更改，请检查所选的组，并根据[更改检查项](../../CONTRIBUTING.md#checks-for-a-change)选择针对性检查：

```sh
python3 scripts/ci_plan.py plan --base origin/main --head HEAD
make check-ci
```

`make check` 仍是完整的本地入口，使用未分片的 Web 测试套件和未分片的 Core 集成测试包。`make check-web-unit` 和 `make check-web-acceptance OAC_WEB_TEST_SHARD=1/4` 用于分别运行 Web 部分；`make check-core-packages` 和 `make check-core-integration OAC_CORE_INTEGRATION_SHARD=1/3` 用于分别运行 Core 部分，其中集成测试按名称的稳定哈希分配到分片。选择测试涵盖混合变更、共享使用方、重命名/删除、未知输入、浅合并检出以及失败/取消/缺失结果。对于选择映射的更改，请针对受影响的规则重放具有代表性的差异。对于工作流更改，请运行 actionlint，并验证更改后的调度或分区行为。仅在需要验证受变更影响的行为时，才运行真实组件测试。

使用 `python3 scripts/ci_metrics.py RUN_ID ...` 衡量已完成的运行。它会报告最近一次尝试的运行器分钟数总和、耗时、从该次尝试开始计算的初始排队延迟、并发作业峰值、平台明细以及作业结果/失败比例。只有在该次尝试中被分配了运行器的作业才计入机器时间和执行并发；排队期间被取消的作业仍保留其结果和实际耗时。更早的尝试不计入其中。失败作业的重新运行可能沿用更早的成功结果：这些结果会单独显示，且其原有执行时间会被排除。如果缺少重新运行的开始时间戳，测量会停止，因为无法可靠区分复用的作业。比较时应保留 run/head/attempt 标识，并分别报告取消和未完成的运行。原始运行器分钟数并非计费分钟数；估算成本前，应使用各平台公布的转换和配额规则。较小的成功样本不能作为长期失败率估计。定时完整运行不在此策略范围内。

### CI 运行器和免费配额 {#ci-runners-and-free-allowance}

Linux 检查作业使用 Blacksmith 的 2-vCPU Ubuntu 22.04 或 24.04 运行器；原生 Windows 使用其 2-vCPU Windows 2025 运行器。Blacksmith 没有 2-vCPU macOS 运行器，因此原生 macOS 使用标准 GitHub `macos-15` ARM64 运行器。发布构建和发布始终共用一个 Blacksmith `blacksmith-4vcpu-ubuntu-2204` 运行器，不受运行器切换设置影响。直接在 `runs-on` 中使用的自定义运行器标签在 `.github/actionlint.yaml` 中声明，以便进行工作流验证。

将仓库 Actions 变量 `OAC_USE_GITHUB_RUNNERS` 设为 `true`，即可改为在标准 GitHub 托管运行器上运行检查作业。Linux 会保留对应的 Ubuntu 版本，Windows 使用 `windows-2025`，macOS 继续使用 `macos-15`。删除该变量或将其设为 `false`，即可让可切换的检查作业恢复为 Blacksmith 的 2-vCPU 默认值。例如，维护者可以在组织的免费配额用尽时进行切换，并在配额重置后恢复 Blacksmith：

```sh
gh variable set OAC_USE_GITHUB_RUNNERS --body true --repo MiniMax-AI/OpenAgentCore
```

这是显式的操作员开关，而不是自动账单余额探测。运行器选择仅适用于新调度的运行。在将 2-vCPU 用量视为免费之前，请先查看当前配额和平台转换率：[Blacksmith's runner documentation](https://docs.blacksmith.sh/blacksmith-runners/overview)。Windows 分钟消耗的配额多于 Linux 分钟。标准 GitHub 运行器的用量取决于仓库可见性和 GitHub 套餐。发布构建使用 4-vCPU 运行器；这些工作流不会请求任何付费缓存附加服务。
