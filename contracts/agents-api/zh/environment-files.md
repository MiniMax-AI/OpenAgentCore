---
title: "Environment 文件与 Artifact"
source: contracts/agents-api/environment-files.md
source_hash: 1b58aa02aaccddb9675ef41ebfe2506a6fba0bb12139efb67e0da378d879aee7
---

Session 工作区保存由 agent 及其工具修改的实时文件。`/agents/environments/{environment_id}/files` 列出一个工作区目录，并在其中创建文件。Turn 完成时，Core 将工作区 `outputs/` 目录中的文件复制为不可变 Artifact，通过 `/agents/sessions/{session_id}/artifacts` 读取。Artifact 的生命周期长于 Environment；工作区文件则不是。

路由遵循 [upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json) 中固定版本 SDK：[Environment files 资源](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/files.py)、[列表参数](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/file_list_params.py)、[EnvironmentFile](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/environments/environment_file.py) 和 [TokenPage](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/pagination.py)。要求 `OpenAI-Beta: agents=v1` 头。

## 文件适用范围 {#where-files-work}

- Environment 文件适用于 `openai_hosted` 和 `self_hosted` Environment。`none` Environment 返回 503 `execution_unavailable`。
- 公开路径从 `/workspace` 开始，代表 Environment 在机器上实际位置的工作区目录。
- 首先在调用方 Project 查找 Environment；不存在或属于其他范围的 ID 返回 404。
- 仍为 `pending` 的 `openai_hosted` Environment，两种操作均返回 400 `the hosted environment is still provisioning; wait until it is connected before accessing files`。此检查在请求验证之后、任何源 File 查询之前执行。其他状态继续处理，Runtime 不可达时返回 503。
- 列表和写入不启动 Turn，也不向模型发送输入。

## 列出文件 {#list-files}

`GET /agents/environments/{environment_id}/files` 列出单个目录直接包含的普通文件，不递归。目录、符号链接和其他条目被排除。

| 参数 | 规则 |
| --- | --- |
| `path` | `/workspace` 本身或其下规范形式的绝对目录；默认 `/workspace` |
| `limit` | 1–100；默认 20 |
| `order` | `desc`（默认）或 `asc`，按路径字节序 |
| `page` | 上一页的 `next` token，`path`、`order`、`limit` 必须相同 |

响应为 `{"object": "page", "data": [...], "next": …, "has_more": …}`。最后一页的 `next` 为 null，只有设置 `next` 时 `has_more` 才为 true。每个文件包含 `environment_id`、`object: "agent.environment.file"`、绝对 `path` 和 `size_bytes`。

- `path` 不存在、指向普通文件或经过符号链接时返回空页。不跟随链接。
- 每页重新读取目录，不提供快照。token 签发后目录普通文件（名称或大小）或请求参数改变时，token 被拒绝。名称和大小未变不证明内容未变。
- 目录中任何类型条目合计超过 1,024 个时返回 503，不返回部分页。权限错误、工作区根缺失和传输失败也返回 503。
- daemon 无本地工作区绑定时，改由 Claude Code 适配器回答读取：路径缺失返回 404，普通文件或符号链接返回 503。

查询错误的 type 和 code 均为 `invalid_request_error`，除另有说明外 `param` 为 null：

| 情况 | 消息 |
| --- | --- |
| `path` 为相对路径、位于 `/workspace` 外、超过 4,096 字节、非 UTF-8，或包含反斜杠、NUL、CR、LF | `path must be an absolute directory inside /workspace` |
| `path` 非规范形式：末尾或重复 `/`、`.` 或 `..` | `path must identify a non-reserved directory inside /workspace` |
| token 格式错误、属于其他请求，或对应列表改变 | `Invalid file page token for this request` |
| `limit` 超出 1–100 | `limit must be between 1 and 100`；格式错误整数及无效 `order` 使用共享 [Beta 列表错误](wire-semantics.md#lists) |
| 重复 `path`、`limit`、`order` 或 `page` | 共享重复字段错误（[列表规则](wire-semantics.md)） |

未知查询键忽略。每个键显式空值均无效。`%GG` 或 `;` 分隔符等格式错误查询编码返回 400 `invalid_request`。

## 创建文件 {#create-a-file}

`POST /agents/environments/{environment_id}/files` 接受任一形式：

```json
{"type": "inline", "data": "<standard Base64>", "path": "/workspace/data/input.csv"}
{"type": "file_id", "file_id": "file-…", "path": "/workspace/data/input.csv"}
```

空 inline `data` 有效，会创建空文件。返回 201 和四个 EnvironmentFile 字段。`file_id` 指定同一 Project 的 [File](source-files.md)；Core 联系 Runtime 之前读取字节。

| 情况 | 结果 |
| --- | --- |
| 父目录不存在 | 创建为 mode 0700；文件为 mode 0600 |
| 父目录是指向工作区内目录的符号链接 | 跟随链接，在目标处创建文件 |
| 目标已存在为文件、符号链接或其他非目录条目 | 400 `environment.files paths must not traverse symlinks or overwrite existing files`。无变化 |
| 目标为已有目录 | 400 `file path conflicts with an existing environment file` |
| 父组件为普通文件，或符号链接指向工作区外 | 400 `invalid_request`，`Invalid resource identifier or request limits.` |
| `inline` 数据解码后超过 5 MiB | 在任何 Runtime 工作前返回 400 `environment.files[0].data exceeds the 5 MiB decoded limit`。恰好 5 MiB 接受 |
| `file_id` File 超过 50 MiB | 413 `request_too_large` |
| 请求体大于 50 MiB 的 Base64 形式加 16 KiB | 413 `request_too_large` |
| `path` 为相对路径、根本身、位于 `/workspace` 外、超过 4,096 字节、非 UTF-8，或包含反斜杠、NUL、CR、LF | 400 `environment.files[0].path must be an absolute POSIX path inside /workspace` |
| `path` 含空、`.` 或 `..` 组件 | 400 `environment.files[0].path cannot contain empty, . or .. path components` |
| 未知顶层字段 | 400 `Unknown parameter: '<field>'.`，`param` 为字段名。名称超过 256 字节或含不可打印字符时为 `Unknown parameter.`，`param` 为 null。仅报告正文首个未知字段 |
| 必需字段缺失或 null、使用另一形式字段、未知 `type` 或无效 Base64 | 400 `invalid_request` |
| 不存在或属于其他范围的 `file_id` | 404 `not_found_error` |

除表格明确指定代码外，400 错误的 type 和 code 为 `invalid_request_error`，`param` 为 null。已有目标不被替换：Runtime 写临时文件，通过硬链接发布，目标存在则失败。后续工具写入仍可修改所创建文件。

### 写入顺序与不确定结果 {#write-ordering-and-uncertain-outcomes}

- 发送任何字节之前，Core 在 Session 锁下记录写入。输入待处理、Turn 运行或较早写入未结算时，新写入返回 409 `turn_conflict`。未结算写入也使 Session 新消息返回 409。
- Runtime 在创建任何内容前根据摘要检查完整正文，因此不完整输入不创建内容。后续失败的写入可能留下新建空父目录。
- 仅 Runtime 的确定回执将写入结算为已提交或已拒绝。被拒绝写入不改变内容并释放 Session。连接断开、请求超时或无回执时，返回 503，写入持续未结算，Core 重启后仍如此。Core 不重发，也无自动恢复，因此 Session 不再接受写入或消息。读取仍可用。
- 源 File 字节读取后，删除该 File 不影响副本。

## Artifact {#artifacts}

### 捕获 {#capture}

`openai_hosted` 或 `self_hosted` Environment 的 Turn 完成时，Core 复制 `/workspace/outputs/` 下所有普通文件，在完成 Turn 的同一事务发布副本。Artifact 的 `path` 为文件绝对工作区路径，例如 `/workspace/outputs/report.md`。失败与取消的 Turn 不发布内容。没有 `outputs/` 目录时无内容可捕获。

- `outputs/` 下任何符号链接根据其类型跳过：不跟随、打开或解析，不成为 Artifact。其余文件仍被捕获。
- 同一 Session 后续 Turn 仅在该路径没有剩余 Artifact，或字节（SHA-256）与该路径最新剩余 Artifact 不同时发布路径。最新按生产 Turn 顺序判断。未变路径保留已有 Artifact 与 ID。已发布 Artifact 不修改。
- 以下情况捕获失败，Turn 以 `artifact_capture_failed` 失败（请求取消时以 `cancelled` 结束）：`outputs` 不是目录（包括指向目录的符号链接）、条目是 FIFO、socket 或设备、复制过程文件大小或修改时间改变，或超限：4,096 个条目、目录深度 64、路径 4,096 字节、单文件 200 MiB、单 Turn 500 MiB。

### 读取与删除 Artifact {#read-and-delete-artifacts}

| 操作 | 行为 |
| --- | --- |
| `GET /agents/sessions/{session_id}/artifacts` | 列出 Session 的 Artifact |
| `GET /agents/sessions/{session_id}/artifacts/{artifact_id}` | 返回 `id`、`object: "agent.session.artifact"`、`session_id`、`turn_id`、`environment_id`、`path`、`size_bytes` 和 `created_at`（发布时间，Unix 秒） |
| `GET /agents/sessions/{session_id}/artifacts/{artifact_id}/content` | 以 `application/octet-stream` 流式返回字节，以文件基本名作为附件文件名 |
| `DELETE /agents/sessions/{session_id}/artifacts/{artifact_id}` | 返回 `{"id": …, "object": "agent.session.artifact.deleted", "deleted": true}` |

- 无论 Environment 是否仍存在（包括过期后），均可读取。
- 删除 Artifact 不改变工作区文件。已进行的内容读取可以完成；后续读取返回 404。移除工作区文件不改变其 Artifact。
- 删除 Session 会删除其 Artifact。

列表参数：

| 参数 | 规则 |
| --- | --- |
| `order` | `desc`（默认）或 `asc`，按发布时间再按 ID |
| `after` | 此 Session 的 Artifact ID |
| `environment_id` | 仅返回该 Environment 产生的 Artifact。格式错误或未知 ID 返回空页；空值不筛选 |

`limit` 和游标错误遵循共享[列表规则](wire-semantics.md#lists)。响应为 `{"object": "list", "data": [...], "first_id", "last_id", "has_more"}`；空页 ID 为 null。先查询 Session，因此无论查询参数如何，不存在或属于其他范围的 Session 均返回 404。
