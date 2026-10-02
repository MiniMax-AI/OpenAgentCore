---
title: "构建并发布 OpenAgentCore"
source: docs/maintainers.md
source_hash: 11e24e329928f4803518aeaf95a202dcbc98ff1c007860c2f94b1ca96adb5d6a
---

本指南面向负责构建和发布 OpenAgentCore 的维护者。要安装 Core 和 Web，请使用 [安装指南](getting-started/install.md)。安装器代码遵循的规则见 [安装器设计规则](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/install/README.md)；必需检查见 [CONTRIBUTING](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/CONTRIBUTING.md#required-checks)。

## 构建分发包 {#build-a-distribution}

分发包是从同一个提交构建的一组相互匹配的 Linux amd64 发布资源：控制归档（安装器、`oac` 命令，以及 Core、Web、gateway 和 PostgreSQL 镜像）、作为独立文件的 Runtime 镜像和节点构件，以及原生安装器。

请在 Linux x86_64 上构建，所需环境包括与 Debian 12 兼容的 glibc、Docker、`go.mod` 中指定的 Go 版本、C 编译器（microsandbox 辅助程序使用 CGO 构建）、Node、pnpm、Python 3.9 或更高版本、curl、tar、pigz 和 sha256sum。源代码必须保持干净并已提交。请先准备固定版本的 Codex 包和 MiniMax Code 配套程序，然后执行构建：

```sh
bash scripts/prepare-release-runtimes.sh
inputs="$HOME/.oac/build/release-inputs/inputs.json"
export AGENTS_RUNTIME_CODEX_PACKAGE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["codex"])' "$inputs")"
export MCODE_HARNESS_BUILD_DIR="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["mcode"])' "$inputs")"
export CORE_DISTRIBUTION_RELEASE_BASE_URL=https://github.com/MiniMax-AI/OpenAgentCore/releases/download/v1.2.3
make build-core-distribution
```

如果 `~/.oac/build/release-inputs` 已存在，`prepare-release-runtimes.sh` 会拒绝执行；请使用全新的构建主机。

| 变量 | 作用 |
| --- | --- |
| `CORE_DISTRIBUTION_RELEASE_BASE_URL` | 用于提供生成的资源文件名的带版本 HTTPS 目录（绝不能使用 `latest`）。除非 `CORE_DISTRIBUTION_OFFLINE=1`，否则为必填项 |
| `CORE_DISTRIBUTION_OFFLINE` | 设为 `1` 时还会构建离线归档 |
| `AGENTS_RUNTIME_CODEX_PACKAGE`、`MCODE_HARNESS_BUILD_DIR` | `prepare-release-runtimes.sh` 固定的 Runtime 输入 |
| `CORE_DISTRIBUTION_CODEX_IMAGE`、`CORE_DISTRIBUTION_CLAUDE_IMAGE`、`CORE_DISTRIBUTION_MCODE_IMAGE` | 使用现有 Harness 镜像，而不是构建这些镜像；值必须是以不可变 `sha256:` 镜像 ID 表示的现有镜像。三个变量必须全部设置或全部不设置；每个镜像都必须包含由该提交构建的守护进程 |
| `OAC_NATIVE_INSTALLER_BUILD_DIR` | 原生安装器目录；请参阅[原生安装器](#native-installers) |
| `CORE_DISTRIBUTION_BUILD_DIR` | `~/.oac` 下的输出目录。默认值：`~/.oac/build/core-distribution` |
| `CORE_DISTRIBUTION_BUILD_NETWORK` | Docker 构建网络：`default`、`host` 或 `none` |
| `CORE_DISTRIBUTION_MICROSANDBOX_ARCHIVE` | 已缓存的 microsandbox 发布归档。默认值：`~/.oac/cache/microsandbox-v0.7.2-linux-x86_64.tar.gz`，缺失时下载 |
| `CORE_DISTRIBUTION_DATABASE_IMAGE` | PostgreSQL 16 镜像；默认值通过其 linux/amd64 清单摘要固定 |

构建过程会复用 Core、Web、Runtime、SDK 和辅助程序构建器。清单会记录提交和源代码树、镜像配置及 OCI 清单摘要、Runtime OCI 清单摘要、microsandbox 运行时和固件哈希，以及每个 Runtime 和节点构件的大小与 SHA-256；原生安装器在[目录](#native-installers)中仅记录其 SHA-256。输出包括控制归档及其 `.sha256`、可选的离线归档，以及带版本号的 Runtime、节点和原生安装器资源。此过程不会发布任何内容。如果目标目录中已包含此提交的分发包，重建会拒绝执行。

控制归档不包含 Runtime 镜像或节点执行构件；离线归档包含这些内容。[下载契约](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/deploy/install/README.md#download-contract)说明了节点如何获取这些内容。

分发包会携带 `scripts/core-distribution-manifest.py` 中 `BUNDLED_DOCS` 列出的文档。随包文档之间的链接保持相对路径；其他所有相对链接都会重写为该捆绑包对应提交在 GitHub 上的同一文件。链接或锚点无法解析时，构建会失败；`make check-distribution` 会对 `example/` 之外每个受 Git 跟踪的 Markdown 文件运行相同检查。添加或移动安装器或其输出所引用的文档时，请更新该列表。

### 原生安装器 {#native-installers}

自托管机器通过各平台的原生安装器安装 `oac-daemon`：Linux amd64、macOS arm64 和 Windows amd64。每个安装器均由 `native-check` 工作流在各自的操作系统上构建（`scripts/build-native-installer.mjs`，其中的 `pins` 对象会固定 Node.js 和 Harness 版本），并在每次选中的原生检查中得到验证。手动打包运行和发布检查会上传 `oac-native-installer-<OS>-<ARCH>.tar.gz` 并保留七天；普通 PR 和 main 检查即使成功也不会上传软件包。要构建本地分发包，请从该确切提交的一次 `native-check` 运行中下载这三个构件（可以是手动运行或发布运行；PR 运行构建的是合并提交，因此不匹配），然后使用该检出内容组装目录：

```sh
node scripts/build-native-catalog.mjs INPUT_DIR OUTPUT_DIR
export OAC_NATIVE_INSTALLER_BUILD_DIR=OUTPUT_DIR
```

该目录会记录提交、Runtime 协议版本、每个归档的 SHA-256，并在提供发布基址时记录其带版本号的 URL。控制归档和 Core 镜像仅携带 `native-installers/catalog.json`；这些归档会成为独立的 `oac-native-<commit>-<platform>.tar.gz` 发布资源，离线归档则会在 Core 镜像之外为每个平台保留一份副本。如果没有目录，Sessions 会将安装命令报告为不可用，并且发布工作流会拒绝发布。

### Runtime 镜像和辅助程序 {#runtime-images-and-helpers}

`make build-core-distribution` 会构建以下全部内容。也可以单独构建其中一项，以测试某个 Harness 镜像或辅助程序。所有命令都必须从仓库根目录运行；默认输出位于 `${OAC_DEV_HOME:-$HOME/.oac}/build` 下。

**Codex Runtime 镜像。** 在 `~/.oac` 下解压官方 npm 包 `@openai/codex@0.153.4-linux-x64`（例如使用 `npm pack --ignore-scripts` 和 `tar -xzf`），然后执行：

```sh
export AGENTS_RUNTIME_CODEX_PACKAGE=/absolute/path/to/package
make build-agents-runtime
docker build --platform linux/amd64 -t oac-runtime:codex "${OAC_DEV_HOME:-$HOME/.oac}/build/agents-runtime"
```

该脚本会检查软件包版本，为 Linux amd64 构建 `oac-daemon`，并准备一个仅包含守护进程、未修改的原生可执行文件、相关资源和 `services/core/deploy/codex/Dockerfile` 的上下文。

**Claude Code Runtime 镜像。** 必须使用 Node 20 或更高版本以及 pnpm。

```sh
make build-claude-sdk-runtime
make build-claude-runtime
docker build --platform linux/amd64 -t oac-runtime:claude "${OAC_DEV_HOME:-$HOME/.oac}/build/claude-runtime"
```

第一步会将适配器和固定版本的 Claude Agent SDK（`packages/claude-sdk-adapter/package.json`）导出为适用于主机平台且带校验和的归档；第二步会验证该归档并添加守护进程。镜像步骤需要 `linux-x64-glibc` 归档，因此必须在带 glibc 的 Linux x86_64 上构建两者。请保持导出的归档不变。

**MiniMax Code Runtime 镜像。** 使用 `packages/mcode-harness/source.json` 中固定修订版本的检出，并使用相同版本的 `@minimax-ai/code` npm 包提供原生依赖，以构建配套程序。配套程序构建可在 Linux x86_64 或 macOS arm64 上运行，并输出到一个新目录；对于 Linux Runtime 镜像，请在 Linux x86_64 上构建它（macOS arm64 仅用于原生安装器）：

```sh
MCODE_NATIVE_SOURCE=/absolute/minimax-code \
MCODE_CLI_DIR=/absolute/node_modules/@minimax-ai/code \
MCODE_HARNESS_BUILD_DIR=/absolute/mcode-harness bash scripts/build-mcode-harness.sh
MCODE_HARNESS_BUILD_DIR=/absolute/mcode-harness bash scripts/build-mcode-runtime.sh
docker build --platform linux/amd64 -t oac-runtime:mcode "${OAC_DEV_HOME:-$HOME/.oac}/build/mcode-runtime"
```

`scripts/prepare-release-runtimes.sh` 会根据固定版本配置运行配套程序构建。

分发包将三个 Harness 镜像合并到一个 Runtime 镜像（`deploy/distribution/Runtime.Dockerfile`）中：以携带守护进程的 MiniMax Code 镜像为基础，并复制入 Codex 可执行文件及资源和 Claude SDK 包。构建过程会验证每个镜像都携带由同一提交构建的守护进程。

**E2B 辅助程序。**

```sh
make build-e2b-provider
```

Docker 使用固定版本的 CPython 和 Debian 12 镜像构建 Linux amd64 辅助程序。Python 依赖闭包（including PyInstaller）在 `services/core/tools/e2b-provider/requirements.lock` 中按哈希锁定；不需要 E2B 账户密钥。要使用其他输出目录，请设置 `E2B_PROVIDER_BUILD_DIR`；从导出的源代码树构建时，请设置 `E2B_SOURCE_REVISION`。输出为 `oac-e2b-provider-linux-amd64.tar.gz` 及其 `.sha256`；解压后会得到 `oac-e2b-provider/`，其中包含可执行文件、`_internal/`、`licenses/`、`requirements.lock` 和 `manifest.json`。Core 镜像使用该目录树；主机需要兼容的 glibc 和 CA 证书，而不需要 Python。

**microsandbox 辅助程序。** 仅支持 Linux，并且需要 C 编译器：

```sh
make build-microsandbox-provider
make check-microsandbox-provider
```

该辅助程序会写入 `~/.oac/build/microsandbox-provider/oac-microsandbox-provider`。其独立的 Go 模块固定 microsandbox Go SDK v0.7.2，并嵌入匹配的 FFI 库；构建生产版本时，绝不能使用该 SDK 的 `microsandbox_ffi_path` 标签。Core 本身仍采用禁用 CGO 的构建。该辅助程序需要 glibc，并且只能在节点上运行。

**microsandbox 运行时。** 分发包使用官方的 [v0.7.2 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2) 归档 `microsandbox-linux-x86_64.tar.gz`，SHA256 为 `47c223e3ef5298abf05f47ed9f87981106e400d99bb3f1d042d4d6881346b18b`（即 `scripts/core-distribution-manifest.py` 中的 `RUNTIME_ARCHIVE_SHA256`）。构建过程会先验证校验和，再解压 `msb` 和 `libkrunfw.so.5.6.1`，并记录这两个文件的哈希。辅助程序会在每次调用时检查这些哈希，并且绝不安装或升级它们。

### 独立 Core 构建 {#standalone-core-builds}

`make build-core` 会将 `oac-core`、`oac-core-migrate`、`oac-core-device`、`oac-core-environment-key` 和 `oac-node` 构建到 `${OAC_DEV_HOME:-$HOME/.oac}/build/oac-core`（`OAC_DEV_CORE_BUILD_DIR` 可选择其他绝对目录）。构建过程仅将 `scripts/build-core.sh` 中列出的源文件集（Core 服务、其契约、所需的共享软件包以及根 Go 模块文件）复制到临时上下文，并使用禁用 CGO、只读模块和裁剪路径的方式构建。它不需要 Node、Docker 或其他应用程序。Core 新增共享依赖时，请将该软件包加入列表；绝不能复制整个仓库来使其完成编译。

`make docker-build-core` 会根据这五个命令和 E2B 辅助程序构建 `oac-core:dev` 镜像（`OAC_DEV_CORE_IMAGE` 可选择其他名称）。基础镜像是通过摘要固定的 `debian:bookworm-slim`，包含 CA 证书以及辅助程序所需的 glibc 运行时；默认用户的 UID/GID 为 65532，Core 监听 `:8091`。该镜像仅支持 Linux amd64，并且不会推送到注册表。对镜像或其构建进行更改时，除了相关的源代码检查外，还必须运行 `make check-core-container`：它会在只读根文件系统上针对该镜像运行官方客户端测试套件，并且需要 Linux Docker、非 root 用户，以及服务检查中的[测试数据库和固定版本 SDK](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/README.md#official-client-verification)（`OAC_TEST_DATABASE_URL` 指向一个已应用迁移的 `oac_*_tests` 数据库，并设置 `OAC_TEST_OFFICIAL_SDK_PYTHON`）。

## 发布版本 {#publish-a-version}

在经过审查的提交上推送版本标签，以运行 `core-release` 工作流：

```sh
git tag -a v1.2.3 FULL_REVIEWED_COMMIT_SHA -m "OpenAgentCore v1.2.3"
git push origin v1.2.3
```

标签使用 `vMAJOR.MINOR.PATCH` 格式，可选用 `-rc.1` 等预发布后缀以及 `+build.1` 等构建元数据。预发布后缀会创建 GitHub 预发布版。推送标签即表示发布决定。自动检查用于确定构建和测试结果，而不是真实模型资格：推送标签前应评估实际执行证据。模型凭据和私有证书颁发机构绝不能进入 CI 或发布输入，包含它们的验收镜像也不例外。

工作流会在带标签的提交上运行 `check`，包括完整的本地门禁、官方客户端和镜像验收，以及启用打包构件的原生平台矩阵。检查成功后，GitHub 托管的 `ubuntu-22.04` 上的一个 `build` 作业会准备固定的 Runtime 输入、复用原生安装器、构建分发包，并直接从本地文件发布。此合并作业具有 `contents: write` 和 `packages: write` 权限；检出过程不会保留凭据。发布前会保留一份未压缩的 Actions 构建产物以供恢复使用，正常发布期间不会再次下载该构建产物。

分发归档和 Runtime 归档使用 `pigz` 级别 6，最多使用四个压缩工作线程，并且 gzip 头部中不包含文件名或时间戳。发布器会验证归档和原生安装器校验和、解析仓库身份、拒绝使用该标签已有的 Release 或草稿，并创建一个具有固定 ID 的草稿。最多四个资源可并发上传，按从大到小的顺序进行，且不会重试。确认完整的远程资源清单后，发布器会验证所有镜像归档和现有注册表标签，再并发推送最多四个镜像。每个镜像配置和注册表清单都会接受验证；任何错误都会使 Release 保持未发布状态。失败操作返回前，正在进行的传输会完成。发布器会按 ID 发布草稿，并在发布前重新检查版本标签。

### 容器注册表 {#container-registry}

版本发布会将 Linux amd64 镜像发布为 `ghcr.io/minimax-ai/openagentcore/<component>:<version>`，其中 `<component>` 为 `core`、`web`、`runtime` 或 `ingress`。例如，`ghcr.io/minimax-ai/openagentcore/core:v1.2.3`。PostgreSQL 使用其上游镜像，不会重新发布。注册表镜像从发布归档中加载，不会重新构建。仅当现有标签的镜像配置摘要与本次发布相同时才复用该标签；如果镜像不同，则停止发布。不会发布浮动 `latest` 标签。SemVer 构建元数据在容器标签中使用 `_` 代替 `+`；长度超过 128 个字符的版本字符串无法发布到 GHCR。手动草稿构建不会推送镜像。

合并的构建/发布作业使用具有 `packages: write` 权限的 `GITHUB_TOKEN`。首次发布时，GitHub 会将每个容器软件包创建为私有：软件包管理员必须先在各自的软件包设置中将全部四个软件包改为 **Public**，用户才能匿名拉取。请参阅 [GitHub container visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。更改可见性后，请验证未认证拉取。仅更改仓库可见性并不会使新的容器软件包变为公开。

GHCR 和 GitHub Releases 不共享事务。发布失败后，GHCR 中可能仍会保留一些匹配的版本标签；请保留这些镜像，并使用原始构件按照下文的草稿恢复流程操作。除清单缺失以外，注册表故障都会停止发布。作业摘要会记录按摘要固定的引用；安装归档及其校验和保持不变。这些镜像仍需要[配置](configuration.md)中描述的配置、机密和路由；发布镜像不会提供平台部署模板。

`install.sh` 会解析一次最新稳定版，或解析 `--version` 指定的发布版，验证控制归档并运行该捆绑包的安装器；其用法见[安装指南](getting-started/install.md#install)。

Go 检查和构建作业共享 `~/.oac/cache/` 下的 Go 模块和编译器缓存目录，缓存键由运行器 OS 和架构、全部 Go 模块文件、检查/构建分区以及提交确定。分区键可防止并发作业在同一个键下保存不同的编译器子集。发布构建既可以使用后端检查的缓存，也可以使用更早发布构建的缓存。较旧的缓存只会为下载和编译提供初始内容；每项检查仍会运行。发布作业还会缓存 npm 软件包下载内容和固定版本的 microsandbox 归档，并在每次构建时验证后者的校验和。Actions 缓存可见性遵循 GitHub ref 的作用域；特定标签的缓存不会与其他发布标签共享。只有作业成功后才会保存新键。

绝不移动发布标签或覆盖已发布的资源。发布失败时，请先检查 Release：即使响应丢失，发布也可能已经完成。对于完整的已发布 Release，请保持原样。对于不完整的草稿，仅在检查后将其删除，然后使用 `gh run download RUN_ID --name core-release-REVISION --dir ASSET_DIRECTORY` 下载原始 `core-release-<revision>` Actions 构建产物，并使用该确切源代码修订版本的检出运行 `python3 scripts/publish-core-release.py --assets ASSET_DIRECTORY`。将 `GH_REPO`、`GH_TOKEN`、`RELEASE_REVISION`、`RELEASE_TAG` 和 `RELEASE_MODE` 设置为原始发布输入，并将 Docker 登录到 GHCR 以发布版本。该脚本会重新验证资源，并拒绝使用已有 Release。恢复上传失败时，绝不能重新运行合并的构建作业，也绝不能重新创建标签。

### 构建候选版本但不发布 {#build-a-candidate-without-publishing}

手动运行需要完整的提交 SHA，会执行相同的检查和构建，默认生成离线归档，并且绝不发布：

```sh
revision=$(git rev-parse HEAD)
gh workflow run core-release --repo MiniMax-AI/OpenAgentCore --ref main \
  -f ref="$revision" -f offline=true -f draft_release=true
```

设置 `draft_release=true` 时，结果是未发布的 `build-<full SHA>` 草稿 Release；设置 `draft_release=false` 时，文件会保留在 Actions 构建产物中。请使用完全匹配的构件集合；绝不能混用构建结果，也绝不能通过 `latest` 解析组件。

## 持续集成 {#continuous-integration}

每个 PR 都会运行 `core-check` 并报告必需状态 `check`。main 使用 GitHub 分支保护，合并前必须通过此检查且分支必须为最新状态，因此合并不会启动另一份测试套件。所有更改都必须通过经过检查的 PR 提交；管理员绕过检查并不代表 CI 成功。推送到 main 时，如果输入发生变化，就会发布网站。版本标签和手动发布构建会在其确切源代码提交上运行完整的发布门禁。`scripts/ci_plan.py` 管理唯一的输入到检查映射。组件规则要求同时匹配目录或脚本前缀以及文件后缀；确切的依赖项、工作流和共享构建输入都有明确规则。规则会在共享使用方和混合变更之间累加。没有匹配构建/测试规则的路径仅运行 hygiene。引入新组件、语言、构建输入或资源位置时，请添加相应规则。

计划器会将 PR 事件所测试的合并提交与其已验证的第一个父提交进行比较。NUL 分隔的 Git 输出和禁用重命名检测会同时保留旧路径和新路径。计划及原因会显示在运行摘要中。历史记录缺失或不一致、检出不匹配、路径无效、计划器/编排发生变更以及共享构建输入发生变化时，都会选择完整门禁。经过验证的空差异仅选择 hygiene。发布、手动和显式 ref 调用始终选择所有组。

| 组 | 检查和使用方 |
| --- | --- |
| `hygiene` | 名称、仓库链接、随包文档完整性以及 CI 计划器/门禁测试；每次变更都会运行 |
| `distribution` | Harness 目录和安装器模式、安装/应用/恢复/清理测试、Compose 解析和初始化固定数据、发布/下载和捆绑包契约、Go 控制台测试与构建；模板解析需要 Docker Compose，不需要 pnpm install 或浏览器 |
| `compose` | 使用模板声明的发布镜像从空数据卷实际启动、登录和 API 访问、上传文件和下载节点安装器，然后在保留凭据和数据的同时重新配置 URL 并重新创建容器；需要 Docker 和网络访问，不需要构建镜像或模型凭据 |
| `backend` | 并行部分，每部分都有专用 PostgreSQL 保护检查：`runtime`（sqlc 新鲜度、Runtime/共享 Go 测试、Linux microsandbox 辅助程序、守护进程构建）、`core`（独立 Core 构建、Core 服务和客户端测试），以及串行 Core 持久化集成包的三个 `store` 分片 |
| `harness` | Claude SDK 测试和打包、MiniMax 配套脚本 |
| `example` | 可选的应用程序类型检查、测试、构建和隔离的浏览器验收 |
| `web` | TypeScript、Web/客户端测试和 Web 构建 |
| `website` | 针对网站、已发布文档和依赖项变更的网站构建与输出检查 |
| `web-acceptance` | Web 单元测试/构建成功后，以四个隔离分片运行完整 Web 浏览器测试套件；每个分片保留一个工作线程 |
| `api` | 针对独立命令和迁移的可复用官方客户端验收；当镜像/构建/辅助程序输入发生变化时进行镜像验收，并在每次完整门禁中执行 |
| `native` | 可复用的 Linux、macOS 和 Windows 构建、文件系统/进程/Harness 检查和原生安装；三个平台可并发运行 |
| `lint` | 可复用的 actionlint 检查，包括本地复合操作 |

`.github/actionlint.yaml` 会选择 hygiene 和 lint。已知工作流变更会选择其使用方：CI review 和 actionlint 工作流运行 hygiene 和 lint；原生工作流变更会添加原生检查；API 验收工作流变更会添加启用容器验收的 API 检查；网站工作流变更会添加网站检查。共享 Node 操作会选择使用它的每个作业以及 lint。新工作流或未分类的工作流/操作会选择完整门禁，直至在计划器中声明其使用方。计划器测试和 CI 测量脚本运行 hygiene；更改计划器本身会运行完整门禁。

Compose 模板和 Compose 测试发生变更时，会同时选择 `distribution` 固定数据和 `compose` 冒烟作业。安装 Docker 后，可在本地运行 `python3 scripts/compose-smoke.py` 重复该测试。该脚本使用唯一的项目、自动分配的回环端口，并将在 `~/.oac/tests/` 下生成构件；退出时移除其容器和数据卷。CI 还会在冒烟步骤失败或中断后执行清理。诊断信息会显示容器状态，但不会打印 HTTP 响应正文或登录密钥。该测试检查声明的发布镜像和通用 Compose 行为；它不会运行 Dokploy/Coolify 实例，也不会执行模型。

Go 模块和工作区输入会选择后端、API（包括容器）、原生和分发检查。每个 Node 模块都拥有自己的清单和锁文件。网站依赖项会选择网站检查；Web 依赖项会选择 Web 和浏览器检查；示例依赖项会选择示例检查；共享 TypeScript 客户端依赖项会选择 Web、浏览器和示例检查；Claude 适配器依赖项会选择 Harness、原生和分发检查。共享包管理器配置会选择所有 Node 使用方。根 TypeScript 配置会选择 Web 和示例检查；适配器 TypeScript 配置会选择 Harness 和原生检查。每个所选集合都包含 hygiene。混合变更会累加其使用方，并且每个作业都读取同一计划，而不是维护各自的路径列表。例如，仅修改通知的 PR 会跳过数据库、浏览器和原生作业，而同时修改通知和 Core 的 PR 会添加后端和 API 检查。

`docs/` 和 `contracts/` 下已发布的文档和资源，以及 `docs.json`，会选择 hygiene 和 website。其他普通 Markdown 文件（包括源代码目录中的文档）仅选择 hygiene。生成的目录文件和配置参考章节会保留其分发新鲜度检查。Core `.go`、`.sql`、辅助脚本和配置输入会选择后端/API 检查；Web 源代码、样式和资源会选择 Web 检查。嵌入式原生资源和声明的测试固定数据目录无论后缀如何都会选择其使用方，其中包括 Markdown 提示和没有扩展名的数据。安装器变更会添加分发检查。Web 变更会添加 Web 检查和所有浏览器分片；Core/DB 变更会添加后端和官方客户端验收。共享契约、SDK、Runtime 输入和依赖项会根据计划器传播到其使用方。生成的目录和协议输入包括安装器、客户端和 UI 使用方。不要在可复用工作流中重复路径列表，也不要在必需工作流上添加 `paths` 过滤器。

即使计划或依赖项失败，最终 `check` 仍会运行。它要求计划有效且成功、每个所选作业均成功，并且每个未选择作业均被跳过。失败、取消、作业缺失、意外跳过或意外执行都会导致门禁失败。API/原生可复用工作流是此门禁的直接依赖项。同一 PR 上较新的运行会取消之前的运行。发布检查在请求的不可变 ref 上运行；原生打包在这些检查中执行一次，分发构建会等待其完成。

要手动执行完整检查，请使用 **Actions → core-check → Run workflow**。遇到暂时性故障时，请使用 GitHub 的 **Re-run failed jobs**，这样已成功的作业可保持完成状态。PR 更新后，Actions 并发机制会取消已被取代的运行。构建和依赖项缓存可加快执行，但不能替代成功的测试。原生发布安装器通过 Actions 构建产物在同一发布工作流的不同作业之间传递。

`CI review and Feishu notification` 工作流仅在 PR 合并到 main 后运行一次。它检出合并后的提交，读取该 PR 已有的检查和日志，并报告实际状态，不会触发另一轮测试。关闭未合并 PR 不触发审查。该工作流只针对合并事件使用 `pull_request_target`，绝不在持有通知凭据时检出未合并 PR 的 head。

浏览器作业各自拥有独立的固定数据和服务；对共享可变固定数据增加 worker 数不安全。失败的浏览器作业保留报告与 trace 七天。原生失败阶段摘要保留七天，详细输出留在 Actions 日志中；凭据和临时安装目录不上传。成功的原生归档仅用于显式手动打包或发布时上传，不重新压缩已压缩的归档。发布分发产物保留现有恢复策略；失败发布可以按前述方式复用原构建。

本地 Node 复合操作会安装固定版本的 pnpm，并根据锁文件、OS、架构、Node 版本和 pnpm 版本缓存其软件包存储。它缓存下载的软件包，而不是 `node_modules`；安装仍保持冻结状态。Go 分区会保留[发布](#publish-a-version)部分所述的现有模块/编译器缓存。三个原生平台独立运行；并行执行可缩短总耗时，但不会减少机器总时间。

对于本地更改，请检查所选的组，并根据[更改检查项](../../CONTRIBUTING.md#checks-for-a-change)选择针对性检查：

```sh
python3 scripts/ci_plan.py plan --base origin/main --head HEAD
make check-ci
```

`make check` 仍是完整的本地入口，使用未分片的 Web 测试套件和未分片的 store 软件包。`make check-web-unit` 和 `make check-web-acceptance OAC_WEB_TEST_SHARD=1/4` 用于分别运行 Web 部分；`make check-core-packages` 和 `make check-core-store OAC_CORE_STORE_SHARD=1/3` 用于分别运行 Core 部分，其中 store 测试按名称的稳定哈希分配到分片。选择测试涵盖混合变更、共享使用方、重命名/删除、未知输入、浅合并检出以及失败/取消/缺失结果。对于选择映射的更改，请针对受影响的规则重放具有代表性的差异。对于工作流更改，请运行 actionlint，并验证更改后的调度或分区行为。仅在需要验证受变更影响的行为时，才运行真实组件测试。

使用 `python3 scripts/ci_metrics.py RUN_ID ...` 衡量已完成的运行。它会报告最近一次尝试的运行器分钟数总和、耗时、从该次尝试开始计算的初始排队延迟、并发作业峰值、平台明细以及作业结果/失败比例。只有在该次尝试中被分配了运行器的作业才计入机器时间和执行并发；排队期间被取消的作业仍保留其结果和实际耗时。更早的尝试不计入其中。失败作业的重新运行可能沿用更早的成功结果：这些结果会单独显示，且其原有执行时间会被排除。如果缺少重新运行的开始时间戳，测量会停止，因为无法可靠区分复用的作业。比较时应保留 run/head/attempt 标识，并分别报告取消和未完成的运行。原始运行器分钟数并非计费分钟数；估算成本前，应使用各平台公布的转换和配额规则。较小的成功样本不能作为长期失败率估计。定时完整运行不在此策略范围内。

### CI 运行器和免费配额 {#ci-runners-and-free-allowance}

Linux 检查作业使用 Blacksmith 的 2-vCPU Ubuntu 22.04 或 24.04 运行器；原生 Windows 使用其 2-vCPU Windows 2025 运行器。Blacksmith 没有 2-vCPU macOS 运行器，因此原生 macOS 使用标准 GitHub `macos-15` ARM64 运行器。发布构建和发布始终共用一个 Blacksmith `blacksmith-4vcpu-ubuntu-2204` 运行器，不受运行器切换设置影响。直接在 `runs-on` 中使用的自定义运行器标签在 `.github/actionlint.yaml` 中声明，以便进行工作流验证。

将仓库 Actions 变量 `OAC_USE_GITHUB_RUNNERS` 设为 `true`，即可改为在标准 GitHub 托管运行器上运行检查作业。Linux 会保留对应的 Ubuntu 版本，Windows 使用 `windows-2025`，macOS 继续使用 `macos-15`。删除该变量或将其设为 `false`，即可让可切换的检查作业恢复为 Blacksmith 的 2-vCPU 默认值。例如，维护者可以在组织的免费配额用尽时进行切换，并在配额重置后恢复 Blacksmith：

```sh
gh variable set OAC_USE_GITHUB_RUNNERS --body true --repo MiniMax-AI/OpenAgentCore
```

这是显式的操作员开关，而不是自动账单余额探测。运行器选择仅适用于新调度的运行。在将 2-vCPU 用量视为免费之前，请先查看当前配额和平台转换率：[Blacksmith's runner documentation](https://docs.blacksmith.sh/blacksmith-runners/overview)。Windows 分钟消耗的配额多于 Linux 分钟。标准 GitHub 运行器的用量取决于仓库可见性和 GitHub 套餐。发布构建使用 4-vCPU 运行器；这些工作流不会请求任何付费缓存附加服务。
