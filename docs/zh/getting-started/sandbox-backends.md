---
title: "选择沙箱提供商"
source: docs/getting-started/sandbox-backends.md
source_hash: 5b374c1a7387110371ca16d4817ee52da4e23794e15edd9530599bb934e02afd
---

比较 microsandbox、Docker 和 E2B，然后在 Web 中选择一个。本指南面向已完成[安装 Core 和 Web](./install.md)、需要为 Core 托管的 Session 提供计算资源的运维人员。

安装程序不选择提供商。一个安装只使用一个提供商。以后要更改，请先重置部署；参阅[更改沙箱配置](./nodes.md#change-the-sandbox-configuration)。

## 比较各提供商 {#compare-the-backends}

| | microsandbox（推荐） | Docker | E2B |
| --- | --- | --- | --- |
| 沙箱运行位置 | Linux 节点上的 microVM | Linux 节点上的容器 | E2B 云端 |
| 需要添加节点 | 是 | 是 | 否 |
| 你需要提供 | 带 systemd 和 `/dev/kvm` 的 Linux amd64 主机 | 带 systemd 和 rootful Docker Engine 的 Linux amd64 主机 | E2B API 密钥和模板；Core 的公共 HTTPS URL |
| 隔离 | 每个沙箱有独立内核 | 容器共享节点内核 | 由 E2B 提供 |
| 空闲沙箱可挂起和恢复 | 是，通过检查点 | 否 | 否 |
| 独立工作区存储 | 可选，使用 NFS | 否 | 否 |
| 磁盘限制 | 根磁盘和数据磁盘 | 无 | 由模板设置 |

字段级差异见[部署契约](../../../contracts/agents-api/zh/sandbox-deployment.md#what-each-field-means-per-sandbox-provider)。节点的主机要求见[添加和管理节点](./nodes.md#host-requirements)。

Docker 节点在其主机上[等同于 root](./nodes.md#what-the-installer-sets-up)：仅在专用于沙箱的主机上添加。

沙箱节点运行在 Linux amd64 上。Core 运行在 macOS 或 Windows 上时，请连接 Linux 节点或使用 E2B。应用也可以在自己的机器上运行[自托管 Session](./self-hosted.md)，无需提供商。

## 在 Web 中选择提供商 {#select-the-backend-in-web}

1. 打开 **设置 → 沙箱与调度**。设置向导会询问沙箱运行位置：**自有机器**（microsandbox 或 Docker）或 **E2B 云端**。
2. 选择提供商和沙箱规格。Web 会把[提供商声明的默认大小](../sandbox-provider.md#register-the-provider-kind)推荐为 **标准**。
3. 选择 **保存配置**。该选择保存在 Core 的数据库中。

选择 **自有机器** 后，继续阅读[添加和管理节点](./nodes.md)。要让工作区不保存在节点上，请先[设置工作区存储](./workspace-storage.md)。microsandbox 节点间的检查点恢复，参阅[跨节点恢复检查点](./nodes.md#checkpoint-recovery-across-nodes)。

## 使用 E2B {#use-e2b}

E2B 不需要节点，但其沙箱会从 E2B 云端调用 Core，因此 Core 需要一个不是回环地址的公共 HTTPS URL；参阅[配置公开地址](./public-address.md)。

1. 按[构建模板](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/services/core/deploy/e2b/README.md#build-a-template)，用 Runtime 镜像构建模板。其输出记录了不可变的 `templateID:build_UUID` 值。
2. 在设置向导中选择 **E2B 云端**，输入 **E2B API 密钥**，并选择模板构建或输入其 `templateID:build_UUID`。密钥必须属于拥有该模板的团队。
3. 选择 **保存配置**。Core 加密保存该密钥，且不会再返回它。

密钥和模板规则（包括密钥替换）见[初始设置与同提供商变更](../../../contracts/agents-api/zh/sandbox-deployment.md#initial-setup-and-same-provider-changes)。

## 后续步骤 {#next-steps}

- microsandbox 或 Docker：[添加和管理节点](./nodes.md)。
- 使用 NFS 的工作区：[设置工作区存储](./workspace-storage.md)。
- 模型和密钥：继续阅读[安装 Core 和 Web](./install.md#set-a-default-model)。
