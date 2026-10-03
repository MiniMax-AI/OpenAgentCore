---
title: "添加和管理节点"
source: docs/getting-started/nodes.md
source_hash: e1af3f17292dc491cb7c041f9e6c7ea335ec763149368c0d4898828b210605f7
---

节点是一台 Linux 主机，在沙箱后端为 Docker 或 microsandbox 时，为 Core 托管 Session 运行沙箱。Core 将新 Session 分配给有空余容量的节点；节点创建沙箱，沙箱回连 Core。E2B 不需要节点。应用为自己的 Session 连接的机器是[自托管执行器](self-hosted.md)，而不是节点。

添加节点的方法是在 Web 生成命令并在主机上执行。[沙箱部署协议](../../../contracts/agents-api/zh/sandbox-deployment.md)定义沙箱设置及其变更规则，[节点协议](../../../contracts/agents-api/zh/node-generation-protocol.md)定义节点如何准备和保留 Runtime 代次。

## 添加节点前 {#before-you-add-a-node}

- **Core 已有主机及沙箱可访问的 HTTPS 公开 URL。** 节点从 Core 控制台下载文件，并通过 `public_url` 连接 Core。设置前，Add node 显示 *Configure a domain and HTTPS in System before adding nodes*；参阅[配置域名和 HTTPS](install.md#configure-the-domain-and-https)，使用外部入口时则参阅[修改公开 URL](../configuration.md#changing-the-public-url)。
- **沙箱配置已保存。** 安装程序会保存 Standard 规格的 microsandbox。要使用 Docker 或其他规格，打开 **System** → **Manage sandbox configuration**，选择 **Reset deployment**，然后选择 **Own machines**、后端和沙箱规格，最后选择 **Save configuration**。同一安装的所有节点使用同一后端。
- **控制台能提供节点文件。** 节点从控制台下载 Runtime 和提供商文件；控制台缺少文件时重定向到发行下载地址。节点依据发行清单检查各文件的大小和 SHA-256。主机无法访问发行下载地址时，从[离线包](install-options.md#offline-hosts)安装 Core，让控制台持有全部文件。缺少文件时，Add node 显示 *This console has no node files for …*。

Core 主机与其他主机一样加入：要在它上面运行沙箱，将它添加为节点。

## 添加节点 {#add-a-node}

1. 在 Web 打开 **Nodes**，选择 **Add node**。
2. 设置 **Sandboxes at once**，使用 microsandbox 时还设置 **Retained sandboxes**，即节点[容量](../configuration.md#node-capacity)。之后可以通过 **Edit node** 修改。
3. 选择 **Generate command** 并复制命令。命令仅注册一个节点、只能使用一次，且必须在 10 分钟内执行；Web 显示倒计时，过期后提供 **Generate new command**。
4. 在主机上执行。Web 跟踪节点从注册、连接到就绪的过程。

命令从你的控制台下载节点安装程序、检查 SHA-256，并使用一次性注册令牌运行它。安装程序下载节点文件并逐一对照发行清单验证、导入 Runtime 镜像、注册节点、启动服务，并等待 Core 报告节点已连接且就绪。程序不安装软件；前置条件缺失时，会在修改任何内容之前停止并输出一行提示。

安装和移除节点需要 root。除非 shell 已是 root，Web 命令会使用 `sudo`。安装程序创建 `oac-node` 服务用户和系统服务；节点以该用户运行，而非 root。普通用户运行安装程序时，会在读取注册令牌或修改主机前失败。

### 命令 {#the-command}

Web 生成如下命令，填入本安装的值：

```sh
 (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
printf '\n==> Downloading node installer...\n' &&
curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
printf '==> Verifying node installer...\n' &&
printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
printf '%s\n' '<enrollment-token>' | $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --enrollment-token-stdin --source-url 'https://core.example' --core-url 'https://core.example' --provider 'docker' --installation-id '<installation-id>')
```

- 下载到私有临时目录、检查 SHA-256，再通过 `sudo` 执行；root shell 中则直接执行。
- 令牌通过标准输入传给安装程序，不出现在进程参数、环境变量或 sudo 日志中。
- 当 `HISTCONTROL` 忽略以空格开头的行时（Debian 和 Ubuntu 默认如此），前导空格能避免命令进入 shell 历史。

安装程序逐阶段展示执行过程，Core 确认节点后展示状态与日志命令摘要。`export NO_COLOR=1` 禁用颜色；命令将其作为 `--no-color` 传过 sudo。程序不输出令牌。

### 主机要求 {#host-requirements}

- Linux amd64 和 systemd；Python 3.9+、`curl` 和 `sha256sum`；root 或 sudo。
- SELinux 不处于 enforcing 模式。安装程序不支持 enforcing SELinux 主机。
- Docker：正在运行的 rootful Docker Engine，其 `/var/run/docker.sock` 套接字属于 `docker` 组，权限为 `0660`，并强制执行 CPU 和内存限制（cgroup v2）。
- microsandbox：`/dev/kvm` 属于 `kvm` 组（硬件或嵌套虚拟化），并具有 microsandbox 链接的库（glibc）。
- CPU 和内存至少足以运行一个所配置规格的沙箱，以及约 2 GB 的 Runtime 镜像磁盘空间。
- 可通过公开 URL 以 HTTPS 访问控制台和 Core；沙箱也能访问 Core。

### 通过代理下载 {#download-through-a-proxy}

执行生成的命令前，导出 `http_proxy`、`https_proxy` 和 `no_proxy`。大写的 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY` 也支持；大小写同时设置时，小写优先，即使值为空。使用 HTTP 代理 URL，可包含 URL 编码的凭据，并在 `no_proxy` 中提供逗号分隔的排除主机。HTTPS 下载代理必须支持 CONNECT。

命令要求 sudo 仅保留这六个变量名，安装程序降权到 `oac-node` 时继续保留它们。如果 sudo 策略拒绝，请让主机管理员允许这些变量，或在 root shell 中导出变量后执行。你自行添加的外层 `sudo` 可能丢弃它们。不要将凭据放进命令参数，也不要分享 shell 跟踪输出。

代理仅用于安装下载，不保存到节点配置或服务中，因此运行中的节点仍需独立访问 Core。

## 安装程序设置的内容 {#what-the-installer-sets-up}

| 项目 | 详情 |
| --- | --- |
| 服务用户 | 系统用户 `oac-node`，主目录 `/var/lib/oac-node`，无登录 shell。已有账号的主目录相同且 shell 为 nologin 时会复用；其他名为 `oac-node` 的账号会被拒绝 |
| 用户组 | 拥有 `/var/run/docker.sock`（Docker）或 `/dev/kvm`（microsandbox）的组：仅 `docker` 或 `kvm` |
| 服务 | `/etc/systemd/system/oac-node-<installation-id>.service`，root 所属的系统单元，使用 `User=oac-node`，开机启用。Core 无法访问时每 5 秒重启；Core 不再接受节点后永久停止 |
| 节点状态 | `/var/lib/oac-node/.oac/nodes/<installation-id>/`：身份、配置和节点文件。microsandbox 在 `/var/lib/oac-node/.oac/m/` 保存镜像和沙箱 |
| 记录 | `/etc/oac-node/`：安装程序创建或修改的内容，用于重新运行和卸载 |
| Docker | 一次性导入 Runtime 镜像，以及网络 `oac-node-<installation-id>` |
| microsandbox 网络策略 | 沙箱仅能访问 Core、主机上的 DNS 和公开地址：无入站连接、不可访问私有网络。私有模型或 MCP 端点需要在节点上修改策略 |

root 只准备账号、组和服务单元；其他操作（包括 Docker 网络）都以 `oac-node` 在没有终端的独立会话中执行。安装程序不安装 Docker、KVM 或软件包，不启动 Docker，不修改设备权限、sudoers、防火墙或 SELinux 设置，也不修改其他账号。

**Docker 模式等同于 root 权限。** 加入 `docker` 组后，`oac-node` 以及任何能控制节点的主体，都可以在主机上执行 root 操作。容器共享主机内核，因此容器逃逸能访问主机。只在沙箱专用主机上添加 Docker 节点。microsandbox 节点只需要 `kvm` 组，每个沙箱运行在独立 microVM 中。

**每台主机只服务一个 Core。** 节点共享 `oac-node` 账号，因此主机只服务一个 Core；第二个 Core 的命令会被拒绝。

**一个发行版本。** 节点运行添加它的控制台所提供的程序，不进行原地升级；参阅[安装版本策略](operations.md#installation-version-policy)。

**令牌。** 令牌仅用于注册节点，一次性使用，10 分钟后过期。安装程序只从标准输入接收它，拒绝环境变量中的令牌，因为 `sudo VAR=… python3` 会在 sudo 日志中记录它。启用了 `log_input` 的 sudoers 策略会记录标准输入，从而记录令牌。

## 重新运行、过期与慢速网络 {#rerun-expiry-and-slow-links}

- 重新执行同一命令是安全的。节点注册后，重新运行使用节点自身凭据，不改变已匹配的内容，也无需令牌。保留的节点身份属于其他 Core 地址或安装时，程序拒绝且不修改任何内容。
- 已过期或已在其他主机使用的命令立即失败，显示 `Core rejected the node configuration read (HTTP 401)`：生成新命令并在 10 分钟内执行。
- 慢速下载期间命令过期时，注册失败并显示 `The enrollment command expired or was already used`。下载文件保留：在 Web 生成新命令后执行。
- 下载从断点继续。每分钟下载不足 64 KiB 时会停止并保留已有内容；重新执行命令即可。
- Docker Runtime 镜像约 500 MB。网络较慢时可提前加载：将发行资产 `oac-<commit>-linux-amd64-runtime.tar.gz` 复制到主机并运行 `sudo docker load -i`。安装程序发现精确匹配的镜像后跳过下载。
- 中断安装程序或关闭终端会停止程序；重新执行命令继续。

## 日志 {#logs}

安装结束时输出节点日志命令（`Logs: …`）。节点报告问题，或注册约一分钟后仍未连接且就绪时，Add node 对话框也会展示它。

运行 `sudo journalctl -u oac-node-<installation-id>.service`。root shell 中省略 `sudo`。安装 ID 位于命令的 `--installation-id` 参数和 **System** 页面。

## 修改沙箱配置 {#change-the-sandbox-configuration}

沙箱设置适用于整个安装，节点跟随这些设置。

- **规格、Runtime 版本、E2B 密钥或模板构建。** 打开 **System** → **Manage sandbox configuration** → **Change resources**，修改并保存。已有沙箱保持其配置。每个节点继续服务旧配置，同时准备新配置；**Nodes** 展示进度：**Preparing target**、**Ready for target**，或带[原因](#readiness-codes)的 **Preparation failed**。Core 优先在已准备好新配置的节点上放置新 Session；这些节点没有容量时，会使用仍服务旧配置的节点。E2B 变更立即生效。
- **后端。** 同一页面选择 **Reset deployment**。自动重置立即归档空闲托管 Session，并让运行中的工作在设定截止时间前完成；强制重置立即取消工作。离线节点必须恢复连接，让 Core 确认清理。重置完成时，Core 已撤销所有节点和未使用命令：配置新后端后重新添加节点。**Cancel reset** 停止剩余工作；已归档 Session 仍保持归档。

[重置协议](../../../contracts/agents-api/zh/sandbox-deployment.md#generation-ownership-and-rollout)描述重置会归档和保留哪些内容。归档单个托管 Session 时，使用 [Core API](../../../contracts/agents-api/zh/admin-api.md#session-archive)。

## 移除节点 {#remove-a-node}

1. 在 Web 打开 **Nodes**，在节点页面选择 **Remove node**，或在列表行选择 **Remove**，再选择 **Confirm removal**。节点仍持有沙箱、快照或待清理资源时，Core 拒绝操作；等待完成或归档对应 Session。移除是永久的：主机只能作为新节点重新加入。移除所有节点仍保留沙箱配置。
2. Web 随后展示 **Clean up the host** 和卸载命令。在主机执行：

   ```sh
    (umask 077; d=$(mktemp -d) || exit; trap 'rm -rf "$d"' EXIT; s=; [ "$(id -u)" -eq 0 ] || s=sudo
   printf '\n==> Downloading node installer...\n' &&
   curl -fsS --max-time 30 --max-filesize 1048576 'https://core.example/node-install/node-install.pyz' -o "$d/node-install.pyz" &&
   printf '==> Verifying node installer...\n' &&
   printf '%s  %s\n' '<installer-sha256>' "$d/node-install.pyz" | sha256sum -c --status &&
   $s python3 "$d/node-install.pyz" ${NO_COLOR+--no-color} --uninstall --installation-id '<installation-id>')
   ```

卸载先向节点注册时使用的 Core 地址确认节点是否已移除；Core 仍列出该节点时拒绝卸载。注册地址与当前公开 URL 不同时，对话框还展示 **Old Core address gone?**：该地址不再响应时，提供带 `--force` 的命令以跳过检查；先在 Nodes 页面移除节点。不使用对话框时，从 `https://core.example/node-install/SHA256SUMS` 的 `node-install.pyz` 行获取安装程序 SHA-256。卸载停止并移除服务、节点状态、记录和 Docker 网络。只有安装程序创建了 `oac-node` 且不再有节点时，才删除该账号；复用的账号仅移除安装程序添加的组。

程序不删除沙箱、卷或镜像。保留 Runtime 镜像并输出 `docker image rm` 命令。microsandbox 保留 `/var/lib/oac-node/.oac/m/` 存储，并输出删除方法（`sudo -u oac-node rm -rf <store>`）；存储删除前保留所创建的账号，之后重新卸载。使用 `--force` 时 microVM 可能仍使用存储，请先检查 `pgrep -u oac-node`。可以重复卸载直到完成。

## 手动注册节点 {#register-a-node-manually}

如果自行管理节点文件和服务，而不执行 Web 命令，请使用手动注册。手动注册的节点只服务注册时的配置：规格或 Runtime 修改后，**Nodes** 显示 **Node software incompatible**；它继续服务旧配置，直到移除节点并重新注册主机。

1. 使用与 Core 同一发行版本的 `oac-node`。
2. 获取注册令牌：使用 **Add node** 命令中的令牌，或通过 Core 密钥调用 `POST /core/v1/sandbox/enrollment-tokens`。令牌一次性使用，包含批准的节点容量；响应的 `expires_at` 给出过期时间。在主机上存入权限为 `0600` 的文件。
3. 使用令牌读取节点配置，不会消耗令牌：`GET /api/v1/sandbox-node/configuration`，带 `Authorization: Bearer <token>`。
4. 写入私有提供商文件。从响应复制 `provider`、`installation_id`、`core_url`、`generation` 和 `specification`，并为主机添加一个适配器对象：
   - `docker`：`host`（显式 Unix 套接字）、`image`（本地导入的批准发行版 Runtime 镜像）、`network`、`extra_hosts`、绝对路径 `seccomp_file` 和 `nested_sandbox`。
   - `microsandbox`：绝对路径 `helper_path`、`runtime_path` 和 `firmware_path`，以及对应的 `runtime_sha256` 和 `firmware_sha256`；`image`；沙箱的 `cpus`、`memory_mib`、`root_disk_mib` 和 `environment_disk_mib`；`network` 策略；以及 `runtime_home` 私有目录。目录缺失时辅助程序以 `0700` 创建。microsandbox 在其中放置 Unix 套接字，因此路径不要超过 48 字节；安装程序对自管节点拒绝更长路径。
5. 使用真实绝对路径注册，然后通过主机服务管理器运行节点：

   ```sh
   oac-node register \
     --config /var/lib/oac/provider.json \
     --state-dir /var/lib/oac/node \
     --core-url https://core.example \
     --name worker-1 \
     --enrollment-token-file /var/lib/oac/enrollment-token
   oac-node run \
     --config /var/lib/oac/provider.json \
     --state-dir /var/lib/oac/node
   ```

节点移除后，Core 拒绝其凭据时，`oac-node run` 以状态 78 退出；配置管理器不要在该情况下重启（systemd：`RestartPreventExitStatus=78`）。

节点向外连接 Core；Core 不需要通过 SSH 或 Docker TCP 访问主机。注册在联系 Core 前先将节点身份写入状态目录，因此响应丢失时可以复用同一身份重试。状态目录放在持久存储上，仅节点账号可访问，每次只由一个进程使用。不要复制到其他目录或主机：节点已有连接打开时，Core 拒绝第二个连接。提供商文件不能修改节点容量或沙箱配置。Core 在注册及每次连接时比较摘要；文件不匹配的节点在恢复批准配置前不接收工作。

## 节点主机故障时 {#when-a-node-host-fails}

重启节点服务会保留身份并重新发现已有沙箱。Core 不会自行替换缺失沙箱，也不会将 Session 移到其他节点：Session 资源显示 **Node disconnected** 或 **Sandbox resource missing**，直到原主机及存储恢复，或你归档 Session。丢失节点状态目录属于恢复事件：从[备份](operations.md#back-up)恢复，并同时恢复数据库和提供商存储；不要在已有资源上重新注册主机。

## 问题排查 {#troubleshooting}

### 就绪状态码 {#readiness-codes}

节点在线但沙箱提供商未就绪时，**Nodes** 和 **Overview** 显示 **Provider not ready**。原因展示位置取决于节点添加方式：

- **使用 Web 命令添加的节点**：当前沙箱配置检查失败时，在目标状态显示 **Preparation failed**，帮助提示中给出原因（`GET /core/v1/sandbox/nodes` 的 `rollout.diagnostic`）。**Provider not ready** 旁的提示仅显示 *Sandbox provider unavailable*。
- **手动注册的节点**：在 **Provider not ready** 旁的帮助提示展示原因（`diagnostic`）。

节点日志包含状态码背后的本地错误。

节点按如下顺序仅报告首个失败检查：Docker 守护进程或 KVM、Docker 限制支持、主机容量、已安装的 Runtime 文件。因此无法访问 Docker 守护进程时，会隐藏镜像缺失问题。修复后约十秒的下一次心跳会清除或替换状态码。离线节点保留最后状态码，Web 在节点重连前隐藏它。

| 状态码 | 帮助提示 | 原因 | 解决方法 |
| --- | --- | --- | --- |
| `docker_unavailable` | Docker unavailable | Docker 套接字不可达、无权访问，或 Docker info/镜像请求失败 | 启动 Docker 并赋予节点用户访问 `/var/run/docker.sock` 的权限 |
| `docker_limits_unsupported` | Docker limits unsupported | Docker 报告不支持 CPU 配额或内存限制 | 使用 cgroups 强制执行 CPU 与内存限制的主机（cgroup v2） |
| `capacity_insufficient` | Host too small | 主机 CPU 或内存不足以运行一个沙箱 | 使用更大主机或修改沙箱规格 |
| `runtime_image_unavailable` | Runtime image missing | Docker 中没有固定版本的 Runtime 镜像 | Web 命令添加的节点自动重新下载；其他节点加载匹配发行版镜像 |
| `kvm_unavailable` | KVM unavailable | 节点无法读写 `/dev/kvm` | 启用硬件虚拟化，通过 `kvm` 组赋予节点用户 KVM 访问权限 |
| `microsandbox_artifacts_unavailable` | microsandbox components missing | Runtime 或固件缺失、SHA-256 检查失败，或辅助程序缺失 | Web 命令添加的节点自动下载缺失文件；其他节点从匹配发行版恢复 |
| `runtime_download_failed` | Runtime download failed | 准备新配置时无法下载或验证 Runtime 文件 | 检查节点到控制台和发行下载地址的 HTTPS 访问。节点以递增间隔重试，最长间隔 30 分钟 |
| `provider_unavailable` | Sandbox provider unavailable | 其他失败 | 阅读节点日志 |

新用户组成员关系仅对新进程生效。重启节点服务：`sudo systemctl restart oac-node-<installation-id>.service`。已注册但从未连接的节点通常无法通过公开 URL 访问 Core，或 `/api/v1` WebSocket 无法通过反向代理。

### 安装程序消息 {#installer-messages}

| 消息 | 解决方法 |
| --- | --- |
| 下载前出现 `Core rejected the node configuration read (HTTP 401)`，或 `The enrollment command expired or was already used` | 在 Web 生成新命令并在 10 分钟内执行 |
| Core's public URL changed after this command was generated | 在 Web 生成并执行新命令 |
| This host's node uses `<address>`, but this command uses `<address>` | 节点在旧公开 URL 下添加。在 Web 移除、卸载，然后重新添加 |
| Docker Engine is not installed, or Docker is not running | 安装 Docker Engine，或运行 `sudo systemctl enable --now docker` 后重试 |
| Docker on this host does not enforce CPU and memory limits | 使用 cgroup v2 后重试 |
| KVM is unavailable, or `/dev/kvm` must be group-accessible | 启用虚拟化；发行版的 KVM 包会设置 `root:kvm 0660` |
| This host has N CPUs and M MiB of memory; each sandbox needs … | 使用更大主机或修改沙箱规格 |
| SELinux is enforcing on this host | 使用安装程序支持的主机；程序不修改 SELinux 设置 |
| This host already runs a sudo-mode node for another Core | 先移除并卸载该节点 |
| Node installation and removal require root | 通过 sudo 或 root shell 执行 Web 命令 |
| Core still lists this node | 先在 Nodes 页面移除 |
| Core no longer accepts this node | 节点已移除或重置已撤销它。卸载后使用新命令重新添加主机 |
