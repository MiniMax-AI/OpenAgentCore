---
title: "消息内容"
source: contracts/agents-api/message-content.md
source_hash: 085ade22b792243b8fea4fb798f1de3cbed872d27752afa4f30b93f251b829a8
---

用户消息和函数结果共享同一内容模型：由 `input_text` 与 `input_image` 部分组成的有序列表。Core 按发送形式准确存储消息边界、部分顺序和图像引用，并在用户 Item 中原样返回。不下载、转码或修复媒体。Session 创建的 `input` 与 `events.create` 消息共享验证和准入；[Session、事件与历史](sessions-events.md#send-input)定义准入、请求限制和错误。

## 消息 {#messages}

消息包含 `role: "user"`、可选 `type: "message"` 和非空 `content` 数组。事件 `input` 为消息数组；Session 创建的 `input` 也可为字符串，转换为一条文本消息。显式 null 或空消息 `type`、字符串 `content` 或字符串事件 `input` 均无效。

消息含图像或至少一个非空文本部分时有效。Core 不修剪文本。以下请求返回 400 `invalid_request`，不写入内容：

- 空 `input` 字符串、`input` 数组或 `content` 数组；
- 文本部分全为空且没有图像的消息。

其他内容旁的空文本部分（例如 `["", "text"]`）被接受并按发送形式存储。

## 图像 {#images}

`input_image` 部分通过内联 data URI 携带 `image_url`：`data:image/png;base64,…` 或 `data:image/jpeg;base64,…`。base64 必须为规范形式，解码图像必须匹配声明类型。Core 不接受远程 URL、`file_id` 或 `detail`。

| Harness | 消息图像 |
| --- | --- |
| Codex | 在 Harness 支持的所有位置接受：`none`、`openai_hosted`、`self_hosted` |
| Claude Code | 在 `none`、`openai_hosted`、`self_hosted` 接受 |
| MiniMax Code | 拒绝 |

任何写入之前，准入检查 Harness；Harness 无法接受的图像返回 400。Runtime 也必须报告消息图像支持：Core 仅将输入含图像的 Session 绑定到此类 Runtime，交付给不支持的 Runtime 会失败。应使用接受图像的模型。

## 仅空白文本 {#whitespace-only-text}

`"   "` 或 `"\n\t"` 等仅含空白的文本为有效内容，原样存储和返回。Harness 能否运行由引擎配置声明：

| Harness | 无图像且无非空白文本的消息 |
| --- | --- |
| Codex | 准入并原样交付 |
| Claude Code | 400 `unsupported_or_invalid_configuration` |
| MiniMax Code | 400 `unsupported_or_invalid_configuration` |

拒绝适用于 Session 创建（含流式和 `self_hosted` 创建）及 `events.create`，发生于任何写入、预留或 Turn 之前，因此不会干扰运行 Turn。同一消息中非空白文本旁的空白对所有 Harness 均准入。空白集合为 Go `unicode.IsSpace` 与 ECMAScript `String.prototype.trim` 的并集，例如 U+0085、U+FEFF；Core 准入和 Claude 桥接层使用相同集合。

## 函数结果 {#function-results}

`agent.session.input.tool_result` 事件包含 `success`、可选且可空的 `error` 字符串，以及可选且可空的 `output`：字符串或有序 `input_text` 和 `input_image` 部分数组。

- Core 按提交形式存储结果，包括 `output`、`error` 是否存在，并用其确定重试身份。公开 Item 始终包含两字段（[Item 规则](sessions-events.md#turns-and-items)）。
- Runtime 接收一个有序内容列表：先是 `output` 部分，再将 `error` 文本作为最后文本部分。转换不改变存储结果。
- 含图像结果要求 Runtime 报告函数结果图像支持；仅含图像结果检查该能力。Runtime 在准入后拒绝结果时，Turn 失败且没有确认应用，存储结果仍可读取。

| Harness | 函数结果 |
| --- | --- |
| Codex | 文本与有序文本/图像输出。Core 仅检查各部分格式正确，并将图像引用原样传给 Harness |
| Claude Code | 文本输出。仅成功结果可含内联 PNG 或 JPEG 图像；失败结果图像或远程引用在任何存储前返回 400，待处理调用保持开放。Harness 可在原生历史中调整图像尺寸或重新编码；公开 Item 保留提交字节 |
| MiniMax Code | 无公开函数 |

### 应用回执 {#application-receipts}

准入（202）不表示 Harness 使用了结果。适配器确认原生应用后，待处理调用清除：

- **Claude Code** 使用实时根原生工具结果确认，要求匹配 Session、调用 ID、成功标志、准确文本、块数和顺序，且每个图像位置都有原生图像。重放、合成和 Subagent 记录不能确认。
- **Codex** 使用实时根 dynamic-tool `item/completed` 观察确认，要求匹配线程、Turn、调用、函数名称、状态、成功标志和准确有序内容。仅写入 Harness 不确认。

两适配器等待回执最多 10 秒。超时或原生释放且未确认时，应用保持不确定。确认表示 Harness 记录了结果，不表示模型提供方消费了结果。Core 不自动重放结果；提交仍保留用于恢复读取。

## Runtime 边界 {#runtime-boundary}

Core–Runtime wire 在初始输入、准备后启动和引导中将消息作为 `MessageInput` 携带，使用与函数结果相同的有序 `InputContent` 部分（[Core–Runtime 协议](../../../docs/zh/runtime-protocol.md)）。准入检查 Harness 声明配置；绑定和交付检查 Runtime 报告。适配器负责原生编码和应用回执。仅文本适配器拒绝图像部分，不丢弃它们。

- **Codex** 将批次展平为原生输入列表，在公开消息之间插入空行分隔。公开消息边界保留于 Core 存储，原生历史不保留。
- **Claude Code** 发送原生图像块及每条原生用户消息的 UUID。一个公开输入仅在批次所有消息消费后视为已应用。单个原生 Turn 内桥接层最多接受 64 条用户消息（含开场提示）；超过界限的引导批次在提交任何部分前被拒绝，并结束运行 Turn。daemon 要求桥接协议 3。

原生消息与函数结果图像检查列于[验证适配器资格](harness-onboarding.md#qualify-the-adapter)。
