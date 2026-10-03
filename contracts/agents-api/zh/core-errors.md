---
title: "Core 管理错误"
source: contracts/agents-api/core-errors.md
source_hash: d5c4450c0c74927115c79dca70d29372a7316d5d2591c7e18af688d08257a98c
---

`/core/v1` 上的错误使用此封装结构。`message` 是安全的英文文本；`code` 和 `param` 可以为 null。客户端依据稳定的 `code` 和可选的 `param` 进行处理，对未知代码显示 `message`，绝不解析消息，也绝不自动重试被拒绝的写操作。

```json
{"error":{"message":"A valid Core key is required as the bearer credential.","type":"invalid_request_error","code":"invalid_admin_key","param":null}}
```

`/v1` 和 `/api/v1` 上的错误仍使用各自的封装结构，且绝不包含 `details`。

## 可选详细信息 {#optional-details}

存在时，`error.details` 是一个非空的扁平对象。其值可以是字符串、有限数值、null 或字符串数组（数组可以为空）。它仅包含 Core 自身的事实；绝不包含已提交的名称、URL 或密钥、回显的请求值、原生错误文本或提供商响应正文。每个包含详细信息的代码都在下表列出了其确切键名。

| 代码 | 详细信息 |
| --- | --- |
| `sandbox_generation_stale` | `current_generation` |
| `sandbox_in_use` | `allocations`、`pending` |
| `sandbox_reset_required` | `current_provider`、`requested_provider` |
| 操作验证代码 | 请参阅 [operation validation](#operation-validation) |

在 TypeScript 客户端中，`AgentCoreError.details` 是可选的 `CoreErrorDetails`。Core 客户端仅接受上述值类型，会复制字符串数组，并忽略格式错误或为空的 `details`，且不会改变错误的 message、status、code、param 或 type。公开的 `OpenAIAgentsClient` 不读取 `details`。

## 控制台自身故障 {#console-generated-failures}

Web 的控制台服务器在 `/core` 路径上发生自身故障时使用此封装结构（[request boundary](../../../docs/zh/web/console-server.md#request-boundary)）。它绝不暴露请求值或传输层异常，并原样透传 Core 的响应。

| HTTP 状态 | 代码 | 含义 | `type` |
| --- | --- | --- | --- |
| 401 | `console_sign_in_required` | 控制台会话缺失或已过期 | `invalid_request_error` |
| 403 | `console_origin_rejected` | Host、Origin 或 Fetch Metadata 检查失败 | `invalid_request_error` |
| 400 | `console_request_invalid` | 路径、方法或升级不安全 | `invalid_request_error` |
| 502 | `core_unreachable` | 无法连接 Core，或 Core 返回了重定向 | `server_error` |

这些错误的 `param` 为 null，且没有 `details`。因此，Core 的 `401 invalid_admin_key` 仍可与缺少 console sign-in 区分开来。Console sign-in 路由保留其 `{"error":"…"}` 错误（[sign-in](../../../docs/zh/web/console-server.md#sign-in)）。

## 沙箱提供商验证 {#sandbox-provider-verification}

当 `POST` 或 `PUT /core/v1/sandbox/deployment`（[sandbox deployment](sandbox-deployment.md#reset)）的提供商像 E2B 一样验证凭据或配置时，请求会因以下固定错误而失败。这些错误均不会返回提供商文本、模板名称、密钥或资源数量。

| HTTP | 代码 | 含义 | `param` |
| --- | --- | --- | --- |
| 400 | `sandbox_credential_invalid` | 提供商拒绝了候选凭据 | `credential` |
| 400 | `sandbox_configuration_invalid` | 候选配置（例如 E2B 模板构建）未同时满足就绪和不可变要求，或者与资源不匹配 | `configuration` |
| 409 | `sandbox_credential_ownership` | 候选凭据无法管理保留的部署；更换账户前必须重置 | `credential` |
| 503 | `sandbox_verification_unconfirmed` | 无法确认验证结果、回执结算结果或凭据隔离状态 | null |

每次写入部署时，类型化客户端都会将上述代码及其他 `sandbox_*` 部署代码的消息替换为固定的本地文本。`details` 中仅保留 `current_generation`、`allocations`、`pending`、`min` 和 `max`，并且仅当 status、code 和 param 与上表或下方 `invalid_sandbox_configuration` 各行完全匹配时，才保留 `param`。`409 sandbox_configuration_error` 会转换为有关公开 URL 的固定指引，并将 `param` 设为 null，即使对于未提供密钥的 `PUT` 也是如此。其他任何错误都会转换为 `sandbox_configuration_unconfirmed` 且不会重新发送，因为拒绝响应可能会回显密钥。

## 操作验证 {#operation-validation}

每个代码均返回 HTTP 400，并带有 `type: "invalid_request_error"`。如果模型提供商配置包缺失、格式错误或类型错误，系统会在检查任何字段之前返回 `invalid_model_provider`。JSON 正文解析保留其自身错误；其他格式错误的管理请求返回 `invalid_request`。

| 代码 | Param | 详细信息 | 含义 |
| --- | --- | --- | --- |
| `invalid_name` | `name` | `max_length`：Projects 和节点为 128，Project 键为 80 | 名称未通过相应资源的验证器 |
| `invalid_node_capacity` | `max_active` 或 `max_retained` | `min`：1，`max`：1000000 | 容量无效；保留容量还必须至少等于活动容量 |
| `invalid_model_provider` | null | 省略 | 必须提供完整的模型提供商配置包 |
| `model_provider_base_url_invalid` | `base_url` | 省略 | 必须使用 HTTPS，且不得包含凭据、查询或片段 |
| `model_provider_protocol_unsupported` | `protocol` | `harness` 和 `allowed_protocols`，来自该构建的适配器目录 | 协议未知，或所选 Harness 不支持该协议 |
| `model_provider_api_key_invalid` | `api_key` | `max_length`：16384 | 密钥为空、过长或包含禁止字符 |
| `model_provider_token_limits_invalid` | `context_window` 或 `max_output_tokens` | 省略 | 限制无效，或 Harness 要求的正数限制缺失 |
| `model_configuration_model_invalid` | `model` | 省略 | 部署默认配置的 model 不是非空模型标识符 |
| `harness_config_invalid` | `harness_config` | 省略 | 部署默认配置的原生参数不受支持或无效 |
| `invalid_sandbox_configuration` | `resources.cpus` | `min`：1，`max`：255 | CPU 数量超出支持范围 |
| `invalid_sandbox_configuration` | `resources.memory_mib` | `min`：512，`max`：1048576 | 内存超出支持范围 |
| `invalid_sandbox_configuration` | `resources.root_disk_mib` 或 `resources.environment_disk_mib` | `min`：microsandbox 为 1024；Docker 和 E2B 的 `min`：0，`max`：0 | 磁盘容量缺失或提供商不支持 |
| `invalid_sandbox_configuration` | `runtime` | 省略 | Runtime release 缺失、可变、无效或 E2B 不允许 |

这些边界是验证常量，绝不是提交的值。节点名称按字节数限制；Project 名称和键名称按去除首尾空白后的 Unicode 字符数限制，且不得包含控制字符。系统仅按以下顺序报告第一个失败项：模型提供商 URL、协议、密钥、常规限制、Harness 协议，然后是 Harness 的必需限制；沙箱资源依次为 CPU、内存、磁盘，然后是 Runtime。`model_provider` 对象内的模型提供商字段错误仍以该对象的相应字段作为 `param`。未知的沙箱提供商返回一个不含这些字段的错误。

## 诊断失败类别 {#diagnostic-failure-categories}

[Session and Turn diagnostics reads](session-diagnostics.md) 会在成功的 200 快照内返回以下类别，而不是以错误封装的形式返回。公开 `/v1` 的 Turn 错误保持不变。除非表格另有说明，否则 `params` 为 `{}`。

| 代码 | 存储原因或安全含义 |
| --- | --- |
| `harness_error` | `engine_failed`，且没有原生分类 |
| `authentication_error` | 原生提供商拒绝了身份验证 |
| `rate_limit_exceeded` | 原生速率限制分类 |
| `usage_limit_exceeded` | 原生计费或使用量限制分类 |
| `server_overloaded` | 原生过载分类 |
| `server_error` | 原生服务器故障分类 |
| `invalid_request` | 原生请求被拒绝 |
| `resource_not_found` | 未找到原生资源或模型 |
| `request_timeout` | 保留的中性超时类别；当前没有适配器生成该类别 |
| `context_length_exceeded` | 原生上下文限制分类 |
| `cyber_policy` | 原生网络安全策略拒绝 |
| `connection_failed` | 原生连接故障；params 包含 `http_status`，其值为 100–599 范围内的整数或 null |
| `model_provider_required` | 缺少已冻结的模型提供商 |
| `runtime_unavailable` | `execution_device_unavailable`、`execution_unavailable` |
| `runtime_disconnected` | `device_disconnected`、`event_stream_incomplete` |
| `runtime_preparation_failed` | `preparation_start_failed`、`preparation_interrupted` |
| `execution_interrupted` | Core 执行被中断 |
| `delivery_unconfirmed` | `delivery_unknown`、`input_outcome_unknown`、`cancel_unconfirmed`、`cancel_outcome_unavailable`、`function_result_unconfirmed` |
| `input_rejected` | `invalid_input`、`input_not_applied`、`message_input_unsupported`，以及确切的 steering 结果 `input_invalid_input`、`input_run_inactive`、`input_input_conflict`、`input_input_limit`、`input_unsupported`、`input_rejected`、`input_not_ready`、`input_busy` |
| `executor_protocol_error` | `invalid_executor_result`、`interaction_not_supported`、`execution_state_unavailable`、`execution_state_changed`、`function_call_invalid`、`function_result_invalid` |
| `core_storage_failed` | `event_persistence_failed`、`artifact_capture_failed` |
| `internal_error` | 结果未知或格式错误；不返回原始值 |
| `environment_connection_timeout` | 初始输入连接截止时间已过 |
| `environment_unavailable` | 初始输入所需环境不可用 |
| `environment_provisioning_failed` | 托管预置失败；params 包含来自已清理回执的可空 `step`、`index`、`exit_code` |

数据库故障属于错误，绝不会产生空快照或健康快照。绝不会解析预置原因或原生消息以确定类别或参数。

原生类别仅适用于 outcome 中含有 `error_code: engine_failed` 的失败 Turn。Core 仅接受列出的 `engine_error_code` 值；如果该值未知、格式错误或缺失，则仍归为 `harness_error`。只有 `connection_failed` 使用 `engine_http_status`。绝不会根据嵌套元数据或提供商文本来划分失败类别。Core 存储、流不完整和取消故障具有更高优先级；已取消或已完成的 Turn 没有故障。[Native error classification](../../../docs/zh/runtime-protocol.md#native-failure-classification) 列出了各适配器会报告哪些类别。
