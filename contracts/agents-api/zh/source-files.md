---
title: "文件与 Skill"
source: contracts/agents-api/source-files.md
source_hash: 320216a4e7ac0d1e3e2455dfe622e0f91576ea1d0aa00d11911683b0e98387f1
---

Files（`/v1/files`）和 Skills（`/v1/skills`）是具有独立生命周期的 Project 资源，不依赖 Session。File 保存上传字节，Environment 通过 ID 复制它们。Skill 保存不可变、版本化的包，由 Template 和 Session 引用。Project 的所有 API 密钥共享这些资源。

这些路由遵循 [upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json) 中固定版本 SDK：[Files 资源](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/files.py)、[创建参数](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_create_params.py)、[FileObject](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/file_object.py) 和 [Skills 资源](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/skills)。要求 Project API 密钥，不要求 `OpenAI-Beta` 头。不存在 ID 与其他 Project 的 ID 返回相同的 404。

## 文件 {#files}

| 操作 | 行为 |
| --- | --- |
| `POST /files` | Multipart 上传，包含一个 `file` 部分和 `purpose=user_data`，顺序不限。返回 200 和 File |
| `GET /files` | 列出 Project 的 File，不读取字节 |
| `GET /files/{file_id}` | 返回 File |
| `GET /files/{file_id}/content` | 400 `Not allowed to download files of purpose: user_data`，code 与 param 为 null。先检查 ID，因此 File 不存在时返回 404 |
| `DELETE /files/{file_id}` | 删除 File 及其字节；返回 `{"id": …, "object": "file", "deleted": true}` |

将 File ID 传给 [Environment 文件](environment-files.md#create-a-file)，或 Template、Session 的初始 `files`（[Environment](environments.md)）来使用文件。复制在内部读取字节；公开下载仍被拒绝。

### 上传 {#upload}

- 仅接受 `purpose=user_data`。不支持其他用途、`expires_after` 或 Uploads API。
- 文件可为空，最大 512 MiB；整个 multipart 正文可额外增加 64 KiB。更大上传返回 413 `request_too_large`。传输须在五分钟内完成。
- 部分缺失、重复或未知，存在 `Content-Encoding` 或 `Content-Transfer-Encoding` 头，或文件名为空、超过 1,024 字节、非 UTF-8、包含 NUL 时返回 400。完整请求通过验证之前 Core 不存储任何内容。
- Core 不对上传去重。响应丢失后，先列出 File 再次上传。

### File 对象 {#file-object}

| 字段 | 值 |
| --- | --- |
| `id`, `object` | File ID；`file` |
| `bytes` | 字节大小 |
| `created_at` | Unix 秒 |
| `filename` | 上传名称。仅作为元数据，不成为文件系统路径 |
| `purpose` | `user_data` |
| `status` | `processed`，表示字节已存储。Core 不解析、索引或扫描 |
| `expires_at`, `status_details` | `null` |

### 列出文件 {#list-files}

| 参数 | 规则 |
| --- | --- |
| `order` | `desc`（默认）或 `asc`，按创建时间再按 ID 排序 |
| `after` | 此 Project 可见的 File ID |
| `purpose` | `user_data`、`assistants`、`batch`、`fine-tune`、`vision`、`evals`、`assistants_output`、`batch_output`、`fine-tune-results` 之一。其他值（包括不同大小写）在解析游标前返回 400，`param: "purpose"`。非 `user_data` 值返回空页。空值表示不筛选 |

响应为 `{"object": "list", "data": [...], "first_id", "last_id", "has_more"}`；空页的 ID 为 null。`limit`、查询解析及其错误遵循共享[列表规则](wire-semantics.md#lists)。

### 错误 {#errors}

读取、内容和删除时，不存在或属于其他范围的 File 返回 404，type 为 `invalid_request_error`，code 为 null，`param: "id"`。

### 存储与删除 {#storage-and-deletion}

Core 在自身数据库中以 PostgreSQL 大对象存储 File 字节。上传和删除分别在单一事务中提交，失败不会留下部分字节或元数据。[备份](../../../docs/zh/getting-started/operations.md#back-up)数据库时包含大对象；删除 File 不会将其从预写日志或旧备份移除。

存在 File 行时，源文件 schema 拒绝降级。先通过 API 删除 File，以清理大对象。

复制到工作区时读取 File 一致快照，可在 File 删除后完成；后续查询失败。删除 File 不改变工作区副本。

## Skill {#skills}

| 操作 | 行为 |
| --- | --- |
| `POST /skills` | 上传新 Skill。首版同时为默认和最新版本 |
| `POST /skills/{skill_id}/versions` | 上传新版本。表单 `default` 为 `true` 时设为默认；`false` 或省略时默认不变 |
| `GET /skills`, `GET /skills/{skill_id}` | Skill 元数据，不解密任何包 |
| `POST /skills/{skill_id}` | `{"default_version": "<n>"}` 修改默认版本 |
| `DELETE /skills/{skill_id}` | 删除 Skill 与所有版本 |
| `GET /skills/{skill_id}/content` | 默认版本 ZIP |
| `GET /skills/{skill_id}/versions`, `GET /skills/{skill_id}/versions/{version}` | 版本元数据。列表按版本号排序，`after` 是版本 ID（`skillver_…`），不是数字 |
| `GET /skills/{skill_id}/versions/{version}/content` | 指定版本 ZIP |
| `DELETE /skills/{skill_id}/versions/{version}` | 见[删除版本](#delete-a-version) |

`limit`、游标和查询错误遵循共享[列表规则](wire-semantics.md#lists)。

### 上传包 {#upload-a-bundle}

将一个 ZIP 作为 `files` 部分发送，或将目录作为重复的 `files[]` 部分发送，文件名使用 `report/SKILL.md` 等相对路径。SDK 3.13.0 在 `files` 为单文件而非列表时不发送部分，因此上传单个 ZIP 应使用普通 HTTP：

```sh
curl "$OPENAI_BASE_URL/skills" -H "Authorization: Bearer $OPENAI_API_KEY" -F files=@report.zip
```

包包含一个顶层文件夹，其中包含 `SKILL.md` 和支持文件：

- `SKILL.md` 使用 UTF-8，最大 256 KiB，以 YAML frontmatter 开始。`name` 必填：小写字母与数字，可用单个 `-` 或 `_` 分隔，最多 64 字符。`description` 必填且非空。`license`、`compatibility` 和字符串值 `metadata` 映射可选；其他键被拒绝。
- 条目为普通文件或目录，使用规范相对路径。链接、特殊文件、绝对路径、`..` 组件和重复项被拒绝。
- 限制：压缩后 5 MiB，展开后 20 MiB，500 个文件，以及含目录在内的 1,000 个 ZIP 条目。

Core 将每个版本的包加密并绑定到 Project、Skill 与版本。ZIP 上传保留可执行位；目录上传以 mode 0644 存储文件。

### 版本与元数据 {#versions-and-metadata}

- 版本号从 1 开始，每次上传加一，即使最新版本删除也不复用。Template 和 Session 选择器按数字指定版本，复用数字可能使保存的选择器指向不同字节。
- Skill 的 `name` 和 `description` 来自默认版本。通过 `POST /skills/{skill_id}` 或 `default=true` 上传修改默认版本时，指针和两个字段一起更新；`id` 和 `created_at` 不变。
- `latest_version` 是剩余版本中的最大版本号。
- 同一 Skill 的上传与删除串行执行，删除不会移除已确认上传的版本。

Template 和 Session 如何选择版本（默认、`latest` 或数字）并冻结字节，见 [Environment](environments.md#skills-plugins-and-environment-mcp)。

### 删除版本 {#delete-a-version}

| 版本 | 结果 |
| --- | --- |
| 默认且唯一版本 | 200 `{"id": "skillver_…", "object": "skill.version.deleted", "deleted": true, "version": "1"}`。Skill 在同一事务中删除 |
| 默认版本且其他版本存在 | 400，type 为 `invalid_request_error`，code 为 `invalid_value`，`param: "version"`，`Cannot delete the default skill version.` |
| 其他版本 | 200，响应体相同。若为最新版本，`latest_version` 回退到剩余最大版本号 |
| 不存在或属于其他范围的 Skill 或版本 | 404 |

删除 Skill 或版本不改变已安装它的 Session；Template 保留所存储引用。
