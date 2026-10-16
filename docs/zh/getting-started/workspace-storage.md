---
title: "设置工作区存储"
source: docs/getting-started/workspace-storage.md
source_hash: c236fdfcbcdcf01856361813476c7d6b1f6e5de2e28022ac5d3b2b3bb49c2a1b
---

将 Session 工作区保存在沙箱节点之外的 NFS 文件系统上，可选择是否设置每个工作区的配额。本指南面向运行 microsandbox、希望工作区比计算资源存活更久的运维人员。

支持的组合是 microsandbox 与[内核 NFS 适配器](../workspace-provider.md#kernel-nfs-adapter)。Docker 和 E2B 不支持独立工作区存储；参阅[选择沙箱提供商](./sandbox-backends.md)。[配置参考](../configuration.md#independent-workspace-storage)列出了该设置所在的位置。

```mermaid
flowchart LR
  A[Prepare NFS export] --> B[Prepare oac-node]
  B --> C[Mount into Core]
  C --> D[Select the storage]
  D --> E[Configure microsandbox]
  E --> F[Add nodes]
  F --> G[Validate with a Session]
```

## 准备 NFS 导出 {#prepare-the-nfs-export}

启动服务前，在 Linux Core 主机和所有参与节点上将同一个 NFSv4.2 导出挂载到相同的绝对路径，例如 `/srv/oac-workspaces`。运维人员负责导出、挂载可用性和服务启动顺序。使用带有 `root_squash` 的受信任客户端 AUTH_SYS 导出。

> [!WARNING]
> 不要启用 `no_root_squash` 或放宽权限来使检查通过。

按照适配器的[所有权要求](../workspace-provider.md#kernel-nfs-adapter)准备命名空间和服务身份。

## 准备节点账户 {#prepare-the-node-account}

Core 的 Compose 服务已使用 UID 65532 运行。添加节点前，创建同 UID 的 `oac-node`，主目录为 `/var/lib/oac-node`，使用 nologin shell 和非零主组。附加组只能包含其主组、`docker` 和 `kvm`；已有主目录必须属于该账户。节点安装程序会接管此账户。如果没有预创建账户，安装程序分配的系统 UID 不一定与 Core 一致。

注册前应通过主机管理流程解决已有 UID 或账户冲突；修改运行中账户的 UID 不属于存储配置步骤。

## 将导出挂载到 Core {#mount-the-export-into-core}

通过部署平台的 Compose 自定义能力（例如 `compose.override.yaml`），在现有 `core` 服务的 `volumes` 中添加可读写绑定挂载。保留现有服务卷和用户。

```yaml
services:
  core:
    volumes:
      - type: bind
        source: /srv/oac-workspaces
        target: /srv/oac-workspaces
        read_only: false
        bind:
          create_host_path: false
```

主机源目录必须已经是 NFS 挂载；`create_host_path: false` 只防止创建不存在的源目录，并不能证明 NFS 已挂载。先挂载再创建容器；主机卸载或重新挂载后，应重新创建容器，使其挂载命名空间使用预期挂载。

## 选择存储 {#select-the-storage}

打开 **设置 → 工作区存储** 并选择 **选择新配置**，或按 [Core 管理 API](../../../contracts/agents-api/zh/admin-api.md#workspace-storage)的说明，使用 Core 密钥调用 `PUT /core/v1/workspace-storage`。表单接受 adapter 标识、JSON 对象形式的参数和可选的只写凭据 JSON 对象。Core 验证这些由 adapter 定义的值；Web 不定义提供商专属存储字段。

首次设置时，分别为不可变配置 `id` 和命名空间标记生成规范 UUID，然后使用实际值提交以下结构。恢复时保留已有标识。

```json
{
  "id": "11111111-1111-4111-8111-111111111111",
  "adapter": "nfs",
  "parameters": {
    "root": "/srv/oac-workspaces",
    "namespace_id": "22222222-2222-4222-8222-222222222222",
    "uid": 65532
  },
  "tenant_capacity_mib": 0
}
```

选择操作会在 Core 中运行适配器的可用性、所有权和 xattr 检查；它不证明每个节点均能解析该挂载。此配置不实施单个工作区配额，因此 `tenant_capacity_mib` 必须为 `0`。如需有上限的工作区，应准备[配额模式](#quota-mode-storage-server)，并改为选择带[租户容量](../configuration.md#tenant-capacity)的配额模式配置。

**修改租户容量** 保留已选的不可变配置。选择另一份配置需要新的 ID，不会迁移现有工作区。

## 配置 microsandbox 并添加节点 {#configure-microsandbox-and-add-nodes}

通过[沙箱部署 API](../../../contracts/agents-api/zh/sandbox-deployment.md#routes)创建或更新 microsandbox 部署，并保留所需计算资源、Runtime 和提供商配置。`resources.environment_disk_mib` 是每个新工作区的容量：非配额模式下为 `0`，正数会被拒绝；配额模式下至少为 1024 MiB，`0` 会在工作区准入时被拒绝。部署变更时，既有工作区保持其获准时的容量。

部署的工作区要求和每个 Session 的挂载凭据均从已选数据库配置派生。已选配置是唯一的存储设置。

然后在每台已准备的主机上使用 **节点 → 添加节点**；参阅[添加和管理节点](./nodes.md)。

## 用 Session 验证 {#validate-with-a-session}

计算资源已连接且就绪并不足以证明外部文件系统具备资格。准入应用流量前，创建 Session，并通过每个参与节点读写文件。将独立命名空间纳入[停止写入后的备份恢复流程](./operations.md#back-up)。

## 配额模式存储服务器 {#quota-mode-storage-server}

配额模式通过 NFS 服务器上的 XFS project quota 为每个工作区的已分配字节和 inode 设定上限，由 Core 经受限 SSH 强制命令调用的 `oac-workspace-quota` 命令设置。协议和强制执行语义由[工作区文件系统协议](../workspace-provider.md#quota-mode)负责。以存储服务器管理员身份完成以下准备：

1. 将导出放在启用强制 project quota（`prjquota`，而非 `pqnoenforce`）挂载的本地 XFS 文件系统上。导出根目录必须是该挂载点的子目录，例如挂载 `/srv/xfs`、导出 `/srv/xfs/oac-workspaces`；该命令将日志库保存在导出之外的 `/srv/xfs/.oac-quota`。按上文准备命名空间根目录和标记。
2. 安装 `xfsprogs` 和 Python 3.9 或更高版本。从匹配的发行包将 `storage/oac-workspace-quota` 安装为 `/usr/local/sbin/oac-workspace-quota`，所有者为 root，权限 `0755`。
3. 写入 `/etc/oac/workspace-quota.json`，所有者为 root，组和其他用户不可写：`{"export_root": "/srv/xfs/oac-workspaces", "mount_point": "/srv/xfs"}`。
4. 预留该文件系统上其他 project 均未使用的 project ID 范围，然后仅初始化一次日志库：`oac-workspace-quota --config /etc/oac/workspace-quota.json init --projid-start 100000 --projid-end 199999`。它拒绝在已有日志库上运行；绝不要初始化恢复后的存储。
5. 生成专用密钥对，例如 `ssh-keygen -t ed25519 -N '' -f oac-workspace-quota`。在一个具有可用 shell 的账户（如 `oac-quota`）的 `authorized_keys` 中授权其公钥：`restrict,from="<Core address>",command="sudo -n /usr/local/sbin/oac-workspace-quota --config /etc/oac/workspace-quota.json serve" ssh-ed25519 AAAA...`，并添加 sudoers 条目 `oac-quota ALL=(root) NOPASSWD: /usr/local/sbin/oac-workspace-quota --config /etc/oac/workspace-quota.json serve`。也可以在 `PermitRootLogin forced-commands-only` 下为 root 使用相同选项授权该密钥，命令中不带 `sudo -n`。保持 `PermitUserEnvironment no`。
6. 在服务器本机的 `/etc/ssh/` 中读取其公共主机密钥，不要通过未经认证的网络扫描获取。

选择配额模式配置时，将私钥作为只写凭据提交。选择成功后删除本地私钥文件；Core 使用 `secrets/core/credential.key` 加密保存它：

```json
{
  "id": "33333333-3333-4333-8333-333333333333",
  "adapter": "nfs",
  "parameters": {
    "root": "/srv/oac-workspaces",
    "namespace_id": "22222222-2222-4222-8222-222222222222",
    "uid": 65532,
    "quota": {
      "endpoint": "storage.internal:22",
      "user": "oac-quota",
      "host_key": "ssh-ed25519 AAAA..."
    }
  },
  "credential": {
    "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----\n"
  },
  "tenant_capacity_mib": 65536
}
```

选择操作会通过固定的主机密钥调用该命令的只读检查。随后在 `resources.environment_disk_mib` 中设置每个工作区的容量并更新部署。在此更新之前，从非配额配置切换过来的安装会拒绝新工作区；既有工作区继续使用其自身配置。

凭据属于不可变配置。如需轮换，先授权新密钥，使用新密钥选择新的配置 ID，并在仍有绑定旧配置的工作区处于创建过程中时保留旧密钥的授权。

配额模式支持的恢复方式是对 XFS 文件系统进行块级复制；参阅[备份](./operations.md#back-up)。
