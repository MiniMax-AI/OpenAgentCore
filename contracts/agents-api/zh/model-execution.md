---
title: "模型执行"
source: contracts/agents-api/model-execution.md
source_hash: d657e6189ebc5e55ed5201dceaa304b157a6b6ca1f45dcbe9756ef866d63ed05
---

每个 Session 都运行一个 Harness，并使用一个模型提供商。Core 通过三个固定版本上游协议未定义的 Core 扩展来选择它们：`x_agents_core.harness` 选择 Harness，`x_agents_core.model_provider` 提供端点和密钥，`x_agents_core.harness_config` 携带原生模型参数。Core 没有提供商目录、模型别名解析或产品权限模型；除 Session 和已保存 Agent 配置包外，唯一存储的配置包是每个 Harness 的一个 [deployment default](#deployment-defaults)。本文档定义 Harness—模型提供商协议：[`internal/modelprovider/config.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/modelprovider/config.go) 负责验证冻结的提供商连接，每个 Harness 则通过 [`internal/harnessconfig/harness.go`](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/internal/harnessconfig/harness.go) 声明其协议和原生参数。

## Harness 选择 {#harness-selection}

```json
{"x_agents_core": {"harness": "claude_sdk"}}
```

已保存 Agent 在创建、更新和读取时接受 `x_agents_core.harness`，Session 则在内联的 `agent.x_agents_core` 中接受它。标识符来自 [Harness catalog](harness-catalog.md)；未知标识符和未知嵌套字段均会被拒绝，空的内联 Session 扩展也会被拒绝。部署启用哪些 Harness 及其默认值由进程设置 `core.harnesses` 和 `core.default_harness` 决定（[configuration](../../../docs/zh/configuration.md#settings)）。

- 省略：Session 继承其已保存 Agent 的 Harness；内联 Agent 使用部署默认 Harness。
- Session 的内联扩展显式为 null：重置为部署默认 Harness，同时保留继承的提供商配置包。对于已保存 Agent，null 扩展会清除其 Harness 和提供商。
- 所选 Harness 必须已启用；Core 绝不会回退到其他 Harness。
- 创建 Session 时，Core 在处理已保存 Agent 的覆盖值后解析该选择，验证 Harness 配置文件，并将结果存储为 Session 的引擎。当生效的 Agent 包含该扩展时，Session 读取结果会报告它；其他 Session 保持官方 Agent 结构。读取操作从不查询当前 Agent 或部署默认值。
- 使用显式选择器重试创建时会保留调用方意图；在已有 Idempotency-Key 下更改选择器会产生冲突。

Session 的 `environment` 和 Environment Templates 用于选择准备流程，而不是 Harness 或提供商。该扩展仅在 `contracts/agents-api/v1` 中定义一次；校验器从目录派生，任何处理程序或 schema 都不会维护自己的名称列表。

## 已保存默认值与优先级 {#saved-defaults-and-precedence}

已保存 Agent 是可编辑配置，而不是绑定的运行时。创建或更新时，应提供 `model`、可选的 `x_agents_core.harness` 以及可选但完整的 `x_agents_core.model_provider`。响应仅返回安全的提供商字段和只读输出标志 `api_key_configured`，绝不返回 `api_key`、密文或可复用的凭据引用。Agent JSON 仅存储安全视图；密钥配置包拥有自己加密后的数据库行，使用独立的加密用途绑定到 Project 和 Agent，并与 Agent 在同一事务中写入。仅编辑模型时不需要密钥。

创建 Session 时，Core 会先解析每个显式的模型或 Harness 覆盖值，再应用已保存默认值；未选择 Harness 时，应用部署默认值。已保存 Agent 必须指定模型。内联 `openai_hosted` 或 `none` Session 可以省略模型，以使用解析后 Harness 的部署模型；`self_hosted` 绝不会使用部署模型设置。Core 绝不会根据模型名称推断模型。

提供商配置的优先级依次为：完整的 Session 配置包、完整的已保存配置包，以及解析后 Harness 的部署默认值。Core 绝不会将替换后的端点与继承的密钥合并；仅覆盖模型时会复用整个继承的配置包。每个 Harness 仅通过其原生协议连接：

| Harness | 支持的协议（默认项在前） |
| --- | --- |
| Codex | `responses` |
| Claude SDK | `anthropic` |
| MiniMax Code | `anthropic`、`responses`、`chat_completions` |

MiniMax Code 要求上下文限制和输出限制均为正数。Core 会在写入 Session 前验证解析后的组合。Core 和 Runtime 读取 `internal/harnessconfig` 中相同的有序 `protocols` 声明。不存在模型 API 代理、直通网关或跨协议转换，Harness 内部也不例外。不受支持的已保存配置和 Session 快照一旦使用便会失败；它们绝不会在何处被重写、创建别名或迁移。

适用来源取决于接收密钥的计算资源由谁拥有：

| Environment | Session 或已保存 Agent 配置包 | 部署默认值 | 未解析到配置包 |
| --- | --- | --- | --- |
| `openai_hosted` | 接受 | 应用 | 400 `model_provider_required` |
| `self_hosted` | 接受 | 从不应用 | 400 `model_provider_required` |
| `none` | 以 400 拒绝 | 已配置时应用 | 接受；模型由设备自身的环境提供 |

部署默认值保存运营方的密钥，因此仅保留在运营方拥有的计算资源上：Core 管理的沙箱和运营方注册的 `none` 设备。`self_hosted` 执行器属于应用程序，由应用程序提供自己的配置包。托管 Runtime 和自托管 Runtime 均不携带自己的模型配置，因此其中的 Session 如果没有配置包，就会在发生任何写入之前被拒绝；错误参数为 `x_agents_core.model_provider`，并会返回说明应配置内容的消息。

| 操作 | 省略 | 显式 null |
| --- | --- | --- |
| Agent 更新 `x_agents_core` | 保留两个默认值 | 清除 Harness 和提供商，包括其密钥 |
| Agent 更新嵌套的 `model_provider` | 保留配置包 | 清除整个已保存配置包 |
| Agent 更新嵌套的 `harness` | 保留 Harness | 拒绝；请使用 null 扩展进行重置 |
| Session 顶层 `x_agents_core` | 继承提供商默认值 | 继承提供商默认值 |
| Session 嵌套的 `model_provider` | 继承提供商默认值 | 继承提供商默认值 |
| Session 内联的 `agent.x_agents_core` | 继承已保存的 Harness | 重置为部署 Harness |

空的 Session 执行扩展无效。显式 null 提供商会请求继承；空的或不完整的提供商对象无效。已保存提供商配置中的未知字段、重复字段或只读输出字段均会被拒绝。没有 Harness 的已保存 Agent 可以保存有效配置包；其 Harness 兼容性会在 Session 准入时检查。仅更新提供商的 Agent 更新会保留已保存的 Harness，并在行锁保护下验证合并后的组合。Session 内联的 `agent.x_agents_core` 接受 `harness` 和 `harness_config`；提供商覆盖值必须放在请求顶层。

Core 从同一个数据库快照读取 Agent 配置和加密配置包；显式提供完整 Session 覆盖值时，无需解密已保存的配置包。Session 自身的加密快照会与 Session 及其 Environment 原子写入。现有 Session 绝不会再次查询 Agent：Agent 编辑、密钥替换、删除、暂停和重启均无法改变其模型、Harness 或提供商。加密密钥缺失或错误时会安全失败；重启前后应保持相同的 [credential key](../../../docs/zh/configuration.md#installation-directory)。不存在 Turn 级覆盖。

新的托管请求以及省略内联模型的请求，会在解析可变默认值之前记录调用方意图。其他内联请求，例如 `none`，继续遵循已解析请求的重试规则；该哈希不包含部署默认值，因此更改默认值不会改变其重试标识。匹配的创建重试会在再次解析 Agent 或提供商之前恢复已提交的 Session，并且不会进一步加入输入。流式传输不参与重试标识的计算。[TypeScript client](https://github.com/MiniMax-AI/OpenAgentCore/blob/main/packages/agents-client/README.md#saved-agent-and-deployment-defaults) 展示了已保存 Agent 和部署默认值。

## Session 覆盖 {#session-override}

```json
{
  "agent": {"model": "exact-provider-model", "x_agents_core": {"harness": "mcode"}},
  "environment": {"type": "openai_hosted"},
  "x_agents_core": {
    "model_provider": {
      "protocol": "anthropic",
      "base_url": "https://provider.example/anthropic",
      "api_key": "<private key>",
      "context_window": 200000,
      "max_output_tokens": 8000
    }
  }
}
```

- `protocol` 指定上游 API（`anthropic`、`responses` 或 `chat_completions`），而不是引擎。所选 Harness 必须原生支持它。
- `base_url` 使用 HTTPS 和有效主机名，且不得包含凭据、查询参数或片段。
- `api_key` 不得为空，最长为 16 KiB，并且不得包含 NUL、CR 或 LF。
- `context_window` 和 `max_output_tokens` 是可选的非负整数，输出限制不得大于上下文限制；对于 MiniMax Code，两者都必须为正数。请使用真实模型的限制。
- `agent.model` 是准确的提供商模型 ID；只要提供该值，就始终会替换部署模型。
- Session 的 `x_agents_core` 接受 `model_provider`、`harness_config` 和 `environment`（[Environments](environments.md#preparation-order)）；任何其他成员，例如 `sandbox_node_id`，都会以 400 拒绝。托管节点放置自动完成。

不受支持的协议、Harness 或 Environment 组合会在创建 Session 前被拒绝。提供商可用性在执行期间检查，而不是通过探测检查。

解析后的提供商配置会在 Session 创建事务中被冻结并加密，使用自己的加密用途，并绑定到 Project 和 Session。创建重试会将其纳入请求哈希，因此使用相同 Idempotency-Key 时，更改密钥或端点会产生冲突；密钥进入任何存储哈希时，只会表现为由部署凭据密钥加键控的指纹。任何公开的 Session、Agent、Environment、事件或常规配置均不包含该密钥。顶层扩展仅可写入，无法更新。

在分派时，Core 会通过绑定到 Session 的 daemon 连接，将快照作为一个机密提供商配置包发送出去；适配器会原生应用该配置并直接连接提供商。快照缺失或无法解密时，Core 绝不会回退到其他凭据。对于 `self_hosted`，接收方 daemon 是为该 Session 自身 Environment 注册的执行器，并持有 Session 创建者主体的当前执行器凭据；凭据轮换或吊销会在继续分派前关闭套接字。执行器主机将该配置包存放在其原生 Harness home 中，与托管 Runtime 的做法相同。原生工具以启动账户的权限运行，并且可以读取该账户有权读取的内容；吊销凭据不会擦除已经交付的配置包。

## 原生模型参数 {#native-model-parameters}

`harness_config` 保存所选 Harness 的原生模型参数。已保存 Agent 在 `x_agents_core` 中接受它，Session 则在内联的 `agent.x_agents_core` 和顶层 `x_agents_core` 中接受它；顶层值优先。

| Harness | 接受的字段 | 应用方式 |
| --- | --- | --- |
| Codex | `model_reasoning_effort`：`none`、`minimal`、`low`、`medium`、`high`、`xhigh` | App-server `-c model_reasoning_effort=...` 以及每个 Turn 的 `collaborationMode.settings.reasoning_effort` |
| Claude SDK | `effort`：`low`、`medium`、`high`、`xhigh`、`max`；`thinking`：SDK 的 `adaptive`、`enabled` 或 `disabled` 对象 | SDK `Options.effort` 和 `Options.thinking` |
| MiniMax Code | 仅空对象 | 仍必须提供提供商 token 限制 |

对于 `adaptive` 和 `enabled`，Claude `thinking` 接受 `display`（`summarized` 或 `omitted`）；只有 `enabled` 接受正整数 `budgetTokens`；拒绝 `maxThinkingTokens`。这些是原生设置，而不是通用推理词汇表；模型可用性和提供商支持均由 Harness 负责。

提供对象时会替换整个对象；`{}` 会清除它，null 则无效。Session 在选择模型或提供商但未提供原生参数时，会使用 `{}`，而不会继承另一个模型的参数。否则，使用已保存 Agent 提供的对象；使用部署模型的内联 Session 使用部署对象。更改 Harness 会清除继承的参数；对模型、提供商或 Harness 的 Agent 更新如果未提供 `harness_config`，也会清除继承的参数。不进行深度合并。仅设置原生参数的内联扩展会保留已保存的 Harness。该对象和部署默认值都不会启用公开的 `reasoning` 选项。

Core 会在写入 Session 前验证解析后的配置，并将其冻结在 Session 的 Agent 配置中；管理员的 execution-configuration 读取结果会显示其值和来源。原生参数不属于机密信息；提供商密钥保存在单独加密的配置包中。重新连接会使用冻结的配置，无法更改提供商、协议或参数；不兼容的冻结配置会导致失败。仅仅支持某种协议，并不能说明结构化输出、工具发现、Web 搜索、详细程度或图像输入是否可用；连接受支持也不能证明远程模型接受某个参数。

## 部署默认值 {#deployment-defaults}

部署默认值是存储在 Core 中的一项运行时设置：每个 Harness 有一个完整的模型配置，可使用 Core 密钥通过 Web 或 `/core/v1` 进行管理。

| 方法和路由 | 结果 |
| --- | --- |
| `GET /core/v1/harnesses` | 此构建支持的每个 Harness，包含来自进程配置的 `enabled` 和 `default`、其 `model_configuration`（安全视图）或 null，以及 `model_configuration_support` |
| `GET /core/v1/harnesses/{harness}/model-configuration` | 安全视图；未设置时返回 404 |
| `PUT /core/v1/harnesses/{harness}/model-configuration` | 使用共享的提供商和原生参数校验器替换 `{model_provider, model, harness_config}` |
| `DELETE /core/v1/harnesses/{harness}/model-configuration` | 移除配置；幂等，返回 204 |

PUT 要求提供 `model` 和完整的 `model_provider`；`harness_config` 默认为 `{}`。读取操作返回 `model`、`harness_config`、安全的 `model_provider` 视图（`protocol`、`base_url`、可选的 token 限制和 `api_key_configured`）、observations 和 `updated_at`，绝不返回密钥。该配置包使用自己的加密用途加密并绑定到 Harness。每次写入都会记录一条管理员审计条目（`resource_type: deployment_model_provider`，Harness 作为 `resource_id`，操作为 `set` 或 `delete`，`project_id` 为 null），且不包含密钥。加密密钥缺失或错误时会安全失败：写操作以及需要使用默认值的 Session 创建都会返回 503 `credential_storage_unavailable`。

创建 Session 时，Core 会解密解析后 Harness 的默认值，并像处理其他配置包一样将其冻结在 Session 的加密快照中，因此更改或移除默认值绝不会影响现有 Session。execution-configuration 读取结果会显示冻结的安全视图，其来源为 `deployment`。提供商快照缺失或无效时会安全失败，并且不会回退到其他模型或提供商。

`model_configuration_support` 派生自 Core 和 Runtime 共享的适配器声明：`protocols` 列出可选择的原生协议，并将默认项放在首位；`accepts_harness_config` 表示是否接受原生参数；`token_limits_required` 表示是否必须提供提供商 token 限制。它描述的是构建版本，而不是实时 Runtime 或远程模型。

### 部署默认值观测 {#deployment-default-observations}

Harness 和默认模型读取结果包含可空的 `last_used_at`、`last_error_code` 和 `last_error_at`；即使配置包未发生变化，PUT 也会重置这三项。

- 只有当 Session 冻结的正是当前默认修订版时，已完成的根 Turn 才会记录使用情况。
- 失败的根 Turn 仅记录原生提供商错误码 `authentication_error`、`connection_failed`、`rate_limit_exceeded`、`usage_limit_exceeded`、`server_overloaded`、`server_error`、`resource_not_found`、`request_timeout` 和 `invalid_request`。输入策略、Core 或 Runtime 导致的失败，以及 cancelled 和 waiting 结果均不计入。
- 成功不会清除较早的错误；比较时间戳只是一种显示约定。
- 无论错误码是什么，错误观测都会节流 30 秒，常规成功观测也会节流 30 秒；错误后的首次成功会立即记录恢复。对于未变化的修订版，这意味着数据库时间的任意 30 秒窗口内最多允许三次有效写入。
- 每次观测的预算为 1 秒，包括获取连接池资源和行锁的时间，并且无法更改已提交的 Turn。崩溃、失败或节流都可能导致最新观测缺失。

这些时间是在终态提交后由 Core 尽力记录的接收时间，而不是提供商健康状态时间或远程完成时间。旧 Session、显式指定提供商的 Session 和更早的 Session 均无法更新替换后的默认配置。公开的 Session 和 Turn 字段以及重试标识不受影响。Core 没有就绪探测、自动刷新、观测历史，也不会读取凭据或原始错误。

## 验收 {#acceptance}

`TestNativeModelProtocolPublicExecution`（`services/core/internal/store/model_protocol_native_test.go`）结合 `services/core/tests/official_model_protocol_native.py`，通过固定版本的官方客户端针对真实提供商 API 运行每个 Harness。当 `OAC_TEST_OFFICIAL_SDK_PYTHON`、`OAC_TEST_NATIVE_DAEMON_BIN`、`OAC_TEST_NATIVE_PROOF_DIR` 和 `OAC_TEST_MODEL_PROTOCOL_OPTIONS` 均已设置时运行；最后一项指定一个私有模型设置文件。绝不提交这些设置或打印其值。
