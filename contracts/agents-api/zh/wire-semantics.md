---
title: "Core 协议行为"
source: contracts/agents-api/wire-semantics.md
source_hash: 5524db9b90aaf51026746316d6305da396033f50e88ed50792a8f52cf5aac9db
---

已锁定版本的 OpenAI Python SDK（[upstream.json](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/contracts/agents-api/upstream.json)）定义了 `/v1` 路由、字段和类型。本页面说明这些类型未作规定之处 Core 的行为，例如状态码、错误字段、默认值和列表边界，以及 Core 与官方服务存在差异的地方。[coverage ledger](index.md) 列出了这些差异和尚存缺口；[Sessions, events and history](sessions-events.md)、[message content](message-content.md)、[Vaults](vaults.md)、[source Files and Skills](source-files.md) 和 [Environment files and Artifacts](environment-files.md) 分别负责各自资源的规则。

下文所称“Beta 路由”是 `/v1/agents` 和 `/v1/vaults` 下的路由。“Files and Skills”是 `/v1/files` 和 `/v1/skills` 下的路由，这些路由会忽略 `OpenAI-Beta`。

## 请求 {#requests}

### 路径和方法 {#paths-and-methods}

| 情况 | Core 行为 |
| --- | --- |
| 为空、`.` 或 `..` 的路径段 | 在规范路径上提供服务，绝不重定向。路径段按 Go ServeMux 语义解析，并保留尾斜杠，因此 `/v1/agents/x/../` 会命中带尾斜杠的 404。 |
| 百分号编码的未保留字符（`A–Z`、`a–z`、`0–9`、`-`、`.`、`_`、`~`） | 在路由前解码，包括 `%2E` 点路径段。其他转义形式（如 `%2F`、`%5C` 和双重编码）保持编码状态，绝不会分隔路径段。每种拼写形式都按其规范路径进入对应路由并接受身份验证。 |
| 对 `GET` 路由使用 `HEAD` | 在执行相同的 Beta 和身份验证检查后运行 `GET` 路由，并返回其响应头但不返回正文。 |
| 对事件流、File 内容、Skill 内容、Skill 版本内容、Artifact 内容或 Environment 文件列表使用 `HEAD` | 返回 405，因此 `HEAD` 绝不会保持流打开或读取内容。 |
| 不支持的方法，包括 `FOO` 等未知方法 | 返回 405、代码 `unsupported_operation`、消息 "This API method is not supported."，并返回一个 `Allow` 头，按 `GET,HEAD,POST,DELETE` 的顺序列出该路由支持的方法。 |
| `/v1` 下的未知子路由，包括带尾斜杠的情况 | 在 Beta 和身份验证检查后返回 404、代码 `unsupported_operation`。 |
| `OPTIONS` 和 CORS | 不处理 CORS。 |

创建 Agent、Vault、Credential、Environment Template、Environment 文件或 Session 均返回 201，流式 Session 创建也是如此。对 Agent 或 Environment Template 发送空更新正文会推进 `updated_at`，但不会更改其他内容；时间戳精确到秒。

### 标头 {#headers}

Beta 路由要求 `OpenAI-Beta` 标头中恰好有一个值，且该值必须等于 `agents=v1`。如果该标头缺失、值不同或值重复出现，则返回 400，`type` 和 `code` 均为 `invalid_beta`，消息为 "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'."。此检查在身份验证之前执行。`agents=v0` 会被拒绝。

每个 Agents API 响应（包括错误响应和事件流）都包含：

| 标头 | 值 |
| --- | --- |
| `X-Request-Id` | 一个新生成的 `req_`，后跟 32 个小写十六进制字符。Core 也会将其作为 `request_id` 写入日志。不会回显调用方提供的值。 |
| `OpenAI-Version` | `2020-10-01` |
| `OpenAI-Processing-Ms` | 写入标头时的处理耗时 |
| `X-Content-Type-Options` | `nosniff` |
| `Cache-Control` | JSON 响应上为 `no-store` |

### 身份验证 {#authentication}

`/v1` 仅接受 Project API 密钥，形式为 `Authorization: Bearer <key>`。同一 Project 的所有密钥都视为同一调用方：它们共享该 Project 的资源和 Session 创建重试。Core 在每次请求时都在数据库中解析密钥及其 Project，不缓存凭据，超时时间为五秒。撤销密钥或归档其 Project 会在下一次请求时生效。Project 的所有密钥都使用主体 `service_account/project:<Project ID>`。[Projects and keys](admin-api.md#projects-and-keys) 介绍了密钥管理。

可选的 `OpenAI-Organization` 和 `OpenAI-Project` 标头在发送时必须各出现一次，并分别等于 `core` 和 `proj_<Project ID>`；任何其他值都会导致密钥被拒绝。

| 失败情况 | 响应 |
| --- | --- |
| 未提供密钥、使用其他方案、`Authorization` 标头为空或重复、密钥未知或已撤销、密钥属于已归档的 Project、使用 Core 密钥，或作用域标头不匹配 | 401，`type` 为 `invalid_request_error`，消息为 "A valid Agents API bearer key is required."，`WWW-Authenticate: Bearer`。在 Beta 路由上，`code` 为 null。在 Files and Skills 上，当发送且被拒绝的 Bearer 凭据恰好只有一个时，`code` 为 `invalid_api_key`；其他情况下为 null。 |
| 密钥查找失败，例如数据库不可用 | 503，`type` 为 `server_error`，`code` 为 `authentication_unavailable` |

### 请求正文 {#request-bodies}

每个 `/v1` JSON 路由在路由解码、验证或查找之前都会经过一个正文门禁：Agent 创建和更新、Vault 创建、Credential 创建和更新、Environment Template 创建和更新、Environment 文件创建、Session 创建和更新，以及 Session 事件。DELETE 路由、multipart Files 和 Skills 上传以及 Skill 更新使用各自的读取器。除下述 413 大小错误外，门禁错误均返回 400，`type` 和 `code` 为 `invalid_request_error`，param 为 null。

| 顺序 | 情况 | 响应 |
| --- | --- | --- |
| 1 | `Content-Type` 缺失、不是 JSON 或格式错误，包括无正文的 `POST`。`application/json` 和 `application/*+json` 不区分大小写，并允许附带参数 | "expected request with Content-Type: application/json" |
| 2 | 正文超过路由限制：Session 创建和 Environment Templates 为 16 MiB，文件创建采用 [Environment file](environment-files.md) 的限制，其他路由为 1 MiB | 413，代码 `request_too_large` |
| 3 | 无效 UTF-8 | "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode." |
| 4 | JSON 格式错误、尾随数据、两个值、字节顺序标记、仅含空白字符的正文，或形成孤立 UTF-16 代理项的转义 | "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)" |
| 5 | 一个对象内任意深度出现重复键 | `"Invalid body: duplicate JSON key '<key>' at '<path>'. Duplicate JSON keys are not supported."` 路径使用 `.` 连接对象键并省略数组索引，例如 `metadata.k` 或 `tools.type`。键在反转义后按大小写敏感方式比较。报告文档顺序中首次出现的重复键。 |
| 6 | 根节点不是对象，包括数组 | `"Invalid type: expected an object, but got <kind> instead."` |
| — | 空正文或 `null` | 视为 `{}` |

成员名称必须完全匹配。`Metadata` 等大小写变体或嵌套的 `Role` 属于未知成员，并且会在任何写入之前收到该路由的未知成员错误。

受 `echotext.Allowed` 保护的错误（例如未知成员、枚举、schema 根和游标错误）仅当调用方值不超过 256 字节的可打印 UTF-8 时才会原样返回。无法返回的未知成员会收到通用消息和 null param。Metadata 错误使用自身的验证规则，并且可以返回更长的键。

### 资源标识符 {#resource-identifiers}

格式错误的路径标识符会收到与该路由上格式正确但不存在的标识符完全相同的响应，即使正文或查询也无效也是如此。缺失、格式错误和外部资源之间无法区分。UUID 标识符采用 Go UUID 解析器接受的其他拼写形式时也能解析，例如大写字母、花括号或 `urn:uuid:` 形式。

## 错误 {#errors}

### 错误类型 {#error-types}

| 状态 | `type` | `code` |
| --- | --- | --- |
| 缺失或无效的 `OpenAI-Beta` 导致 400 | `invalid_beta` | `invalid_beta` |
| Beta 路由上的资源缺失、格式错误或属于外部租户，导致 404 | `not_found_error` | `not_found_error`，消息 "Resource not found." |
| File 或 Skill 缺失，导致 404 | `invalid_request_error` | null |
| 401 | `invalid_request_error` | 见 [Authentication](#authentication) |
| 409 | `conflict_error` | 官方服务报告的冲突使用 `conflict_error`；仅 Core 存在的冲突保留自己的代码，例如 `idempotency_conflict` |
| 5xx | `server_error` | Core 的代码 |

其他 400 响应使用 `invalid_request_error` 类型。存在官方等价项的验证失败使用 `invalid_request_error` 代码以及观察到的 param 和消息；其他请求错误保留 Core 的本地代码，例如 `invalid_request` 或 `unsupported_or_invalid_configuration`。

### 验证错误 {#validation-errors}

| 情况 | 响应 |
| --- | --- |
| Agent 或 Session 创建或更新时，metadata 对超过 16 个、键超过 64 个字符或值超过 512 个字符 | Param 为 `metadata` 或 `metadata.<key>`，并使用带有实际数量或长度的官方消息。先检查键值对，再检查键和值，键按排序顺序检查。 |
| Agent 或 Session 创建或更新时，或 Vault 创建时，metadata 值不是字符串 | Param 为 `metadata.<key>`，消息为 `"Invalid type for 'metadata.<key>': expected a string, but got <kind> instead."`。按文档顺序报告的第一个此类值会被优先报告。 |
| Agent `name` 超过 128 个字符 | Param 为 `name`。允许使用空名称和未去除首尾空白的名称。 |
| metadata 键或值中包含 U+0000 | Param 为 `metadata.<key>` |
| 其他任何存储字符串或查询筛选条件中包含 U+0000 或无效 UTF-8 | Param 为 null，消息为 "Request text contains characters this service cannot store or compare, such as U+0000 or invalid UTF-8."。不会写入任何内容。PostgreSQL 无法存储 U+0000，而官方服务允许存储。 |
| Environment Template 或 Session 内联网络配置被拒绝：通配符、端口、方案、IPv6、空主机、没有域名的 `restricted` 策略、超过 100 个域名，或域名使用其他访问模式 | Param 为 null，并使用 Core 的消息 |
| Session 更新正文为空 | "At least one update field is required" |

## 列表 {#lists}

列表返回 `object: "list"`、`data`、`has_more`、`first_id` 和 `last_id`；空页面的 first ID 和 last ID 均为 null。`order` 默认为 `desc`。[Environment files](environment-files.md) 使用自己的 `page` token 分页，不在此处涵盖。

### 查询参数 {#query-parameters}

| 情况 | Beta 列表 | Files | Skills 和 Skill 版本 |
| --- | --- | --- | --- |
| 未知键，包括 `tenant_id` | 忽略，在列表和单资源路由上均如此 | 忽略 | 忽略 |
| 重复的支持键，包括标量 `status` | 400 `invalid_request_error`，param 为 null，"Failed to deserialize query string: duplicate field `<key>`" | 400 `unsupported_parameter` | 400 `duplicate_parameter`，param 为 `<key>`，使用官方消息 |
| `order` 不是 `asc` 或 `desc`，包括显式空值 `order=` | 400 `invalid_request_error`，param 为 null，"Failed to deserialize query string: order: unknown variant `<value>`, expected `asc` or `desc`" | 400，code 为 null，"order must be asc or desc." | 400 `invalid_value`，param 为 `order`，`"Invalid value: '<value>'. Supported values are: 'asc' and 'desc'."` |

已锁定版本的 Python SDK 会丢弃空查询值，因此 `list(order="")` 不会发送 `order`，而是使用默认值。`after` 会去除首尾空白。检查按以下顺序执行：重复键、`limit`、`order`；Vault 和 Credential 的 `status` 最先检查。这些检查都在任何资源查找之前执行。

Vault 和 Credential 列表接受标量 `status`、`status[]` 条目或两者，并按其并集进行筛选。默认会列出两个状态。其他值会返回 400 `invalid_request_error`，param 为 null，消息为 "Failed to deserialize query string: status: data did not match any variant of untagged enum VaultStatusFilterParam"。

### 页面大小 {#page-size}

| 列表 | 默认值 | 接受范围 | 其他值 |
| --- | --- | --- | --- |
| Agents、Sessions、Items、Environment Templates、Subagent Items、Subagent Turn Items | 20 | 1–100 | 0 变为 1；大于 100 的值变为 100 |
| Vaults、Credentials | 20 | 1–100 | 0、负数和更大的整数，包括溢出值，都会限制在 1–100 范围内 |
| Turns、Subagents、Subagent Turns、Artifacts | 20 | 1–100 | 400 `invalid_request_error`，"limit must be between 1 and 100" |
| Skills、Skill 版本 | 20 | 0–100 | 0 返回空页面，其 `has_more` 表示游标后是否还有资源。负数：400 `integer_below_min_value`，param 为 `limit`。大于 100：400 `integer_above_max_value`，param 为 `limit` |
| Files | 10000 | 1–10000 | 400，code 为 null，"limit must be between 1 and 10000." |

`limit` 不是十进制整数时（包括空值），Beta 列表返回 400 `invalid_request_error` 和 "Failed to deserialize query string: limit: invalid digit found in string"；在 Vault 和 Credential 列表之外，超出有符号 64 位范围的值会返回 "Failed to deserialize query string: limit: number too large to fit in target type"。编码为 `%2B` 的前导 `+` 会被接受；前导 `-` 在 Beta 列表上会返回无效数字错误，但 Vaults 和 Credentials 除外。Skills 返回 `invalid_request` 和 "limit must be an integer between 0 and 100."；Files 返回 `invalid_request` 以及 Files 范围消息。

### 游标 {#cursors}

`after` 指定同一列表中的某个资源，并且该资源必须位于已经解析出的父资源和租户内。首先解析父资源：父资源缺失或属于外部租户时，会先返回其 404，然后才读取游标。无法解析的游标，无论是随机值、格式错误、类型不同、父资源不同、已删除还是属于其他租户，均返回：

| 列表 | 响应 |
| --- | --- |
| Agents、Sessions、Turns、Environment Templates、Vaults、Credentials | 404，`type` 和 `code` 均为 `not_found_error`，"Resource not found." |
| Session Items、Subagent Items、Subagent Turn Items | 400 `invalid_request_error`，param 为 null，"Invalid session item ID in `after`" |
| Subagents、Subagent Turns | 400 `invalid_request_error`，param 为 null，"Invalid resource ID in `after`" |
| Session Artifacts | 400 `invalid_request_error`，param 为 null，"after is not a valid artifact ID" |
| Skill 版本 | 不以 `skillver` 开头的值：400 `invalid_value`，param 为 `after`，`"Invalid 'after': '<value>'. Expected an ID that begins with 'skillver'."`。其他 Skill 的版本：字段相同，消息为 "Skill version cursor does not match this skill."。格式错误的 `skillver` 后缀，或版本缺失、已删除、属于外部租户：404，code 和 param 均为 null |
| Skills | 404，code 和 param 均为 null |
| Files | 404，param 为 `after` |

## Agent {#agents}

### 已保存的配置 {#saved-configuration}

Agent 创建要求提供 `model`。对于省略的字段，Core 会保存并返回以下值：

| 字段 | 保存值 |
| --- | --- |
| `name`、`instructions` | null |
| `metadata` | `{}` |
| `tools` | `[]` |
| `text` | `{"format": {"type": "text"}, "verbosity": "medium"}` |
| `reasoning` | 按发送内容保存；省略 effort 时保持未设置，而不是采用模型默认值。Agent 和 Session 响应始终包含 `reasoning.effort` 和 `reasoning.summary`，未设置时为 null |
| `service_tier` | `auto` |
| `multi_agent` | 禁用。启用但未提供 `max_concurrent_subagents` 时为 6 |
| Function `defer_loading` | `false` |
| `programmatic_tool_calling.enabled` | `true` |
| `web_search` | 保存每个已锁定模式；请参阅 [tool policy](execution-tools.md#web-search-and-programmatic-tool-calling) |
| HTTP MCP 传输方式 | 以 `headers: {}` 保存；拒绝非空标头。Origin 和 allowlist 默认值见 [public MCP connection origin](environments.md#public-mcp-connection-origin) |

保存某个值并不会使其可执行。Session 创建允许的配置集合更小；请参阅 [Session admission](#session-admission)。

### 配置验证 {#configuration-validation}

Agent 创建和更新正文以及 Session 创建中的内联 `agent`，会在其解析器和 Harness 准入之前，根据已锁定的 `tools`、`text`、`reasoning`、`service_tier`、`multi_agent`、`model`、`name` 和 `instructions` 形状进行检查。失败时返回 400，`type` 和 `code` 均为 `invalid_request_error`：

| 情况 | Param | 消息 |
| --- | --- | --- |
| 缺少必需成员 | JSON 路径，例如 `tools[0].parameters`；在 Session 创建中为 `agent.tools[0].parameters` | `Missing required parameter: '<path>'.` |
| 未知成员，包括大小写变体和未锁定的 `tool_choice` | JSON 路径 | `Unknown parameter: '<path>'.` |
| JSON 类型错误 | JSON 路径 | `Invalid type for '<path>': expected <kind>, but got <kind> instead.` |
| 不支持的枚举值 | JSON 路径 | `Invalid value: '<value>'. Supported values are: ...`，后跟已锁定的值 |
| 整数低于最小值 | JSON 路径 | `Invalid '<path>': integer below minimum value. Expected a value >= 1, but got <n> instead.` |
| 重复的函数名称、多个 `web_search`、多个 `tool_search` | null | `duplicate function tool name: <name>`、`duplicate web_search tool`、`duplicate tool_search tool` |
| Function `parameters` 的字符串根 `type` 不是 `object` | null | `Invalid schema for function '<name>': schema must be a JSON Schema of 'type: "object"', got 'type: "<type>"'.` |
| `text.format` JSON schema 的字符串根 `type` 不是 `object` | null | `agent.text.format.schema must have top-level type "object"; got "<type>"`，Agent 请求上也会返回此消息 |

在同一个对象内，Core 会先报告联合类型的 `type`，然后报告未知成员，再按文档顺序报告成员值，最后报告缺失成员；先检查 tools，再检查 `text`，并在重复项和 schema 根检查之前检查整个对象。没有字符串根 `type` 的 schema 不会被检查。Function 和 output schema、MCP `transport`、`request_metadata`、`metadata` 和 `x_agents_core` 使用各自的解析器。更新正文和 Session 内联 Agent 会在查找 Agent 之前进行验证，因此属于当前租户、属于外部租户、缺失和格式错误的 Agent ID 会得到相同的响应。

即使无法执行，Core 也会保存已锁定形状允许的值：任意长度的函数名称、已启用的 programmatic tool calling、reasoning effort `max` 和 service tier `flex`。

### Session 准入 {#session-admission}

Session 的有效配置还必须通过执行准入，无论配置来自保存还是内联方式。准入会先报告上表中的协议错误，包括已保存 Agent 中的重复工具和 schema 根，然后报告以下错误；在任何写入之前，这些错误均为 400 `unsupported_or_invalid_configuration`：

| 配置 | 消息 |
| --- | --- |
| 显式 `reasoning.effort` 或 `reasoning.summary` | "Explicit reasoning execution options are not supported by this service yet." |
| `service_tier` 不是 `auto` | "Execution currently supports service_tier=auto only." |
| 已启用的 `web_search` 或省略模式的 `web_search`、已启用的 `programmatic_tool_calling` | 见 [tool policy](execution-tools.md#web-search-and-programmatic-tool-calling) |
| 超过 64 个函数，或函数名称为空或超过 512 字节 | "This service supports at most 64 function tools." 或 "Function names must be nonempty, unique and at most 512 bytes." |
| 两个 `programmatic_tool_calling` 声明、两个使用同一标签的 MCP 服务器 | "Execution requires distinct tool controls."、"Execution requires distinct MCP server labels." |

每 Session 的 `tools` 替换可以允许一个其已保存 tools 原本会被拒绝的 Session。每种工具和 Harness 的支持情况见 [execution and tools](execution-tools.md)。

省略、null 和显式 `medium` text verbosity 会产生相同的 Session 配置。对于原生目录未声明 verbosity 支持的模型，Codex 适配器会丢弃 `medium` 设置并使用模型默认值，同时拒绝 `low` 或 `high`。

### 更新、删除和列表 {#update-delete-and-list}

| 操作 | Core 行为 |
| --- | --- |
| `POST /agents/{agent_id}` | 仅替换提供的字段。嵌套对象会替换整个字段；null `name` 或 `instructions` 会将其清除；null 或 `{}` metadata 会清除所有键值对，而对象会替换这些键值对。现有 Session 保留其快照。 |
| `DELETE /agents/{agent_id}` | 返回 `{id, object: "agent.deleted", deleted: true}`。由该 Agent 创建的 Session、其历史及其创建重试均不受影响。重复删除或删除不存在的 Agent 会返回 404，指定该 Agent 的新 Session 也会返回 404。 |
| `GET /agents` | 先按创建时间、再按 ID 排序分页。 |

## Session {#sessions}

### 配置快照 {#configuration-snapshot}

Session 创建会将 Agent 的有效配置复制到不可变快照中。使用 `agent_id` 时，已保存的 Agent 只会读取一次；内联 `agent` 中的字段会整体替换已保存字段，包括数组，而 null `tools` 会清空列表。省略的字段会继承保存值；如果内联 `x_agents_core` 省略 `harness`，则保留已保存的 harness。已保存的 Agent metadata 永远不会成为 Session metadata。后续更新或删除 Agent 只会影响新的 Session。

`stream` 默认为 false。`stream` 和 `agent_id` 不能为 null。省略或 null 的 `metadata` 为 `{}`。

创建过程会在查找创建重试之前，验证正文和 metadata 类型、请求字段及初始输入，以及放置和流式输入要求。对于新工作，Core 会解析 Template、已保存的 Agent 和模型配置，绑定 Vault Credential，然后验证所选 Harness 和执行配置，再执行写入。依赖项查找失败时会重新检查重试标识，以确保已经提交的创建仍可恢复。

### 创建重试 {#creation-retries}

发送一个 1–128 字节且不只包含空白字符的 `Idempotency-Key`；更长或仅包含空白字符的键会返回 400 `invalid_request`。空标头视为未提供键。没有键时，每个请求都会创建一个新的 Session。官方服务即使收到相同的键，也会为每个请求创建新的 Session；Core 则返回原有 Session。

| 情况 | 响应 |
| --- | --- |
| 键、请求和 Project 均相同 | 返回 201以及 Session 的当前状态。不会再次接纳任何输入。`stream=true` 的重试返回 201，不含任何事件并关闭连接。 |
| 键相同但请求不同 | 409 `idempotency_conflict` |
| 删除 Session 后使用相同键 | 409 `idempotency_conflict` |

键的作用域限定为 Project；该 Project 的任何键，包括轮换后签发的键，都可以用于重试。如果请求包含缺少 `model` 的内联 Agent，或者指定了已保存的 Agent、Template、初始文件或准备配置、`vault_ids` 或凭据引用、`x_agents_core`，或者 `openai_hosted` 环境，则会在读取上述任何来源之前按发送内容进行比较：即使 Agent、Template、Credential 或 deployment 默认值已更改或删除，匹配的重试仍会返回原有 Session。其他请求则按解析后的配置进行比较。模型提供商密钥仅以指纹形式参与比较。

### 更新和列表 {#update-and-list}

`POST /agents/sessions/{session_id}` 仅接受必需的 `metadata`：null 或 `{}` 会将其清除，对象会替换所有键值对。执行状态和创建重试标识保持不变。

`GET /agents/sessions` 接受 `agent_id`，它会匹配 Session 不可变的根 Agent ID，包括内联 Agent ID，以及此后已更新或删除的 Agent。筛选在分页之前应用；空 `agent_id` 是一个筛选条件，而不是省略该筛选条件。

### 删除 {#delete}

`DELETE /agents/sessions/{session_id}` 会删除处于空闲或失败状态、没有排队中、运行中或等待中的根 Turn，且没有待处理输入预留的 Session。

| 情况 | 响应 |
| --- | --- |
| 可删除 | 200 `{id, object: "agent.session.deleted", deleted: true}`。此后，对该 Session 的读取、更新、输入、Turn 和 Items 均返回 404，其打开的事件流也会结束。Core 会释放该 Session 由 Core 管理的沙箱；`self_hosted` 机器及其文件保持不变。 |
| 根 Turn 正在排队、进行中或等待必需操作，或者输入正在等待准入、自托管连接或托管预配 | 409，`type` 和 `code` 均为 `conflict_error`，param 为 null，"session must be durably idle or failed without required actions before deletion"。不会发生任何更改。 |
| 调用方自己的 Session，但已被删除 | 返回 200，并提供相同的确认信息 |
| 缺失、格式错误或属于外部租户 | 404 |

Subagent 子 Turn 和待处理的 Environment 文件写入不会阻止删除。要删除正在运行的工作，请发送 `agent.session.input.cancel`，等待 Session 变为空闲状态，然后执行删除。等待 Environment 的输入无法取消；当输入开始、其五分钟期限届满或 Environment 失败时，Session 即可删除。Core 会在为其输入返回 202 的同一事务中接纳 Turn，因此紧接在该 202 响应之后执行删除会返回 409。

### 响应字段 {#response-fields}

Session 的 `agent.tools` 会省略 `tool_search` 声明，因为已锁定的 Session 工具联合类型不包含这些声明；冻结配置仍会保留它们。在 `self_hosted` Session 上，`environment.remote_url` 是 Core 守护进程的 WebSocket URL，即公共 URL 下的 `/api/v1/agent-daemon/ws`，只有 OpenAgentCore 的 Runtime 守护进程会与其通信；请求无法设置此值。其他 Session 字段遵循已锁定的类型；`x_agents_core` 的说明见 [Agents API guide](../../../docs/zh/api/public-agent-api.md#core-extensions-x-agents-core)。
