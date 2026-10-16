---
title: "故障排查"
source: docs/getting-started/troubleshooting.md
source_hash: c562ed30e22adc12c0be5dbfbcbf740aaa977c467d38bf6c2f1f5bc961d90ccc
---

按症状查找安装、访问、节点和 Session 问题的原因与解决方法。本页面向 Core 安装的运维人员。

对于失败的请求或 Turn，先查看 Web 的 **监控 → 请求日志** 和 **Session 日志**；参阅[请求诊断](./operations.md#request-diagnosis)和[服务健康状态](./operations.md#service-health)。

## 安装 {#installation}

| 症状 | 原因与解决方法 |
| --- | --- |
| `Web port unavailable; choose --web-port` | 其他程序占用该端口。停止它，或另选 `--web-port`。安装程序不会改用其他端口。如果消息点名了 Compose 项目 `oac-…`，说明目录已不存在的早期安装仍在运行：按[卸载](./operations.md#uninstall)移除它 |
| `incomplete installation: preserve … and choose another directory` | 保留目录内容，选择另一个 `--install-dir` |
| `configuration check failed; no service was changed` | `.env` 中有 Core 拒绝的值。消息只包含变量名。修正 `.env` 后再次运行 `oac apply` |
| `data volume check failed; no service was changed` | 初始化拒绝了数据卷；消息会说明原因，例如数据卷已被更新的版本升级。参阅[降级](./operations.md#downgrades) |
| `This installation runs release …` | 安装程序或初始化属于另一个版本。要换到更新的版本，请按[升级](./operations.md#upgrade)操作；降级会被拒绝 |
| `This account cannot use Docker` | 将该账号加入 `docker` 组后重新登录，或使用 Docker Desktop 或 rootless Docker。不要用 `sudo` 运行安装程序 |
| `This account cannot access …` | 之前用 `sudo` 运行留下了属于 root 的文件。运行消息给出的 `sudo chown` 命令，再以自己的账号重新运行 |
| `Docker Compose 2.26 or newer is required` | 安装或更新 Docker Compose 插件 |
| `Docker could not pull …` | Docker 守护进程无法访问容器注册表。配置其代理或镜像加速器，或使用[离线归档](./install-options.md#offline-hosts) |
| `Interrupted; rerun the installer to continue.` | Ctrl-C 中断了安装程序，安装保持可重新运行的状态。再次执行同一命令即可。在 Linux 和 macOS 上，启动脚本把未完成的 `oac` 下载保存在 `${TMPDIR:-/tmp}/oac-install-<uid>`，重新运行会继续该下载；删除该目录可让下载重新开始 |
| `startup failed; data retained, rerun the installer` | 消息会列出不健康的服务和查看其日志的 `docker compose logs --tail 50` 命令。修复原因，再[继续安装](./install.md#install)；不要删除数据。运行 `oac apply` 时，同一消息会提示再次运行 `oac apply` |

如果安装程序的某个步骤报告空间或配额不足，请释放错误所指文件系统上的空间。导入镜像失败还可能需要 Docker 存储中的空间，它可能位于另一个文件系统。

## Web 与 API 访问 {#web-and-api-access}

| 症状 | 原因与解决方法 |
| --- | --- |
| Web 返回 403 `Forbidden` | 浏览器主机名不是 `OAC_PUBLIC_URL`。打开该源地址；反向代理必须传递原始 Host |
| Web 显示 Core 不可用（502） | Core 停止或失败：先 `docker compose ps`，再查看 Core 日志 |
| 创建 Session 返回 400 `model_provider_required` | 缺少模型提供商：为 Harness 设置[默认模型](../configuration.md#default-models)，或显式提供；自托管 Session 始终自带提供商 |

对于已到达 Core 的请求，参阅 Agents API 指南中的[诊断失败](../api/public-agent-api.md#diagnose-a-failure)。执行失败的 Session 有 [Session 诊断](../../../contracts/agents-api/zh/session-diagnostics.md)，其中说明了失败类别。

## 节点 {#nodes}

**添加节点** 不展示命令：参阅[添加节点前](./nodes.md#before-you-add-a-node)。在线但未就绪的节点会报告一个状态码；参阅[就绪状态码](./nodes.md#readiness-codes)。

### 沙箱无法访问私有端点 {#sandboxes-cannot-reach-a-private-endpoint}

microsandbox 沙箱无法访问私有地址上的模型提供商或 MCP 服务器，Harness 停止重试后 Turn 失败。沙箱的 DNS 查询发往主机 `/etc/resolv.conf` 中列出的域名服务器，而 DNS rebind 保护会丢弃任何包含私有地址的应答。因此，这些域名服务器解析为私有地址的名称在沙箱内根本无法解析，即使是云 VPC 解析器之后的公共模型 API 这类公共服务也是如此。

安装程序添加的节点没有更改此行为的设置。请让这些域名服务器返回该服务的公共地址。microsandbox 不读取主机的 `/etc/hosts`，因此只有当 `/etc/resolv.conf` 指向提供该条目的本地存根解析器（例如 `127.0.0.53` 上的 systemd-resolved）时，那里的条目才有用。否则，请使用不过滤沙箱流量的 Docker 节点，或[手动注册节点](./nodes.md#register-a-node-manually)，并设置同样在不带端口的规则中放行该私有网段的 `network` 策略，因为 rebind 保护会忽略限定端口的规则。

### 连接失败 {#connection-failures}

已注册但无法连接 Core 的节点会在[日志](./nodes.md#logs)中说明原因：记录 `sandbox node connection to Core failed; retrying` 并附带 `cause`，无论重试多少次，每种原因只记录一次；连接成功后记录 `sandbox node connected to Core`。节点服务启动时若 Core 不可达，会记录 `cannot confirm approved node capacity:`，后接原因。

| 原因 | 解决方法 |
| --- | --- |
| `HTTP 400 on the WebSocket upgrade` | Core 前的反向代理没有在 `/api/v1` 上传递 WebSocket 升级；参阅 [将 Core 置于 HTTPS 之后](./public-address.md#https-and-the-reverse-proxy) |
| 其他状态码，例如 `HTTP 404 on the WebSocket upgrade` 或 `HTTP 502 …` | 反向代理没有将 `/api/v1` 转发到 Core 或没有保留 Host，或者 Core 未运行 |
| `dial tcp: …`，例如 `connection refused`、`i/o timeout` 或 `no such host` | 主机无法通过公开 URL 访问 Core。检查 DNS 和防火墙；节点服务使用安装程序运行时的[代理](./nodes.md#download-through-a-proxy)，代理变化后请用正确的设置重新执行命令 |
| `tls: …`，或 `TLS certificate expired or not yet valid` | 主机不信任 Core 的证书，或公开 URL 对不提供 HTTPS 的端口使用了 `https` |
| `Core did not accept the node: …` | 节点自我介绍后 Core 关闭了连接。Core 升级后，执行该发行版的[协议变更](./operations.md#protocol-changes)；否则查看 Core 日志 |

### 安装程序消息 {#installer-messages}

| 消息 | 解决方法 |
| --- | --- |
| `The enrollment command expired or was already used` | 在 Web 生成新命令并在 1 小时内执行 |
| `Core rejected the node configuration read (HTTP …)` | 检查 Core URL 及其前面的反向代理后重试 |
| `Not enough disk space under …` 或 `The disk is full` | 按提示释放空间后重试，下载会继续 |
| This host's node program differs from the one this console serves | 节点添加后 Core 已升级。选择 **移除节点**，在主机上卸载，再通过 **添加节点** 重新添加 |
| Another node installation is running, or this node is preparing a new sandbox configuration | 待 **节点** 不再显示 **正在准备目标配置** 后重试 |
| 注册前的沙箱提供商检查失败，例如 `This host lacks a capability …` | 修复提示的问题（参阅[就绪状态码](./nodes.md#readiness-codes)）后重试；令牌未被使用 |
| Core's public URL changed after this command was generated | 在 Web 生成并执行新命令 |
| This host's node uses `<address>`, but this command uses `<address>` | 节点在旧公开 URL 下添加。在 Web 移除、卸载，然后重新添加 |
| Docker Engine is not installed, or Docker is not running | 安装 Docker Engine，或运行 `sudo systemctl enable --now docker` 后重试 |
| Docker on this host does not enforce CPU and memory limits | 使用 cgroup v2 后重试 |
| KVM is unavailable, or `/dev/kvm` must be group-accessible | 启用虚拟化；发行版的 KVM 包会设置 `root:kvm 0660` |
| This host has N CPUs and M MiB of memory; each sandbox needs … | 使用更大主机或修改沙箱规格 |
| SELinux is enforcing on this host | 使用安装程序支持的主机；程序不修改 SELinux 设置 |
| This host already runs a sudo-mode node for another Core | 先移除并卸载该节点 |
| Node installation and removal require root | 通过 sudo 或 root shell 执行 Web 命令 |
| Core still lists this node | 先在 **节点** 页面移除 |
| Core no longer accepts this node | 节点已移除或重置已撤销它。卸载后使用新命令重新添加主机 |
