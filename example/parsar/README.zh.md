---
source: example/parsar/README.md
source_hash: 672f1c9d9344f60dde9e31fb0f5749f3518faf3f474817de882c49bbba4f0547
---

# Parsar 工作空间

[English](README.md)

Parsar 是可独立启动的可选对话工作空间，只通过 OpenAgentCore 公开 `/v1` Agents API 执行。用户在两个地方与 Agent 对话：本地网页聊天和飞书。Core 管执行；Parsar 管 Agent、环境模板、资源库和飞书投递。执行始终使用 bypass。账号和细粒度权限暂缓。

## 启动 {#run}

使用 Node 22.13+ 和 pnpm 10.30.3，准备好 [Core](../../docs/zh/getting-started/install.md)、模型连接和专用 Project key。密钥配置放在仓库外的私有环境文件中：

```sh
export OPENAI_BASE_URL='http://127.0.0.1:8091/v1'
export OPENAI_API_KEY='<project-api-key>'
pnpm --filter @oac/parsar-example... install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

打开 http://127.0.0.1:18180。`OPENAI_BASE_URL` 以 `/v1` 结尾；远程地址必须使用 HTTPS，也可通过 SSH 隧道将远程 Core 映射至本机。`OAC_EXAMPLE_PORT` 调整端口。构建运行使用 `pnpm --filter @oac/parsar-example build`，再运行 `pnpm --filter @oac/parsar-example start`。服务只监听本机回环地址。允许从其他站点的链接顶层打开首页，API 请求仍检查同源。本地隧道可使用不同端口；浏览器 Host 必须为 `localhost` 或 `127.0.0.1`，写请求的 Origin 必须与该 Host 完全一致，包括端口；每个数据目录只运行一个服务进程。

## 产品模型 {#product-model}

| 对象 | 含义 |
| --- | --- |
| **资源库** | 模型、Skills、MCP 服务、Plugins、指令片段、知识和凭据。资源是共享的：修改一项会影响所有使用它的 Agent。 |
| **环境模板** | 准备好的工作区：npm 与 Python 软件包、Bash 初始化脚本、环境变量、飞书机器人变量和初始文件。 |
| **Agent** | 用户对话的对象：名称、描述、模型与 Harness、指令、资源绑定、一个必选的环境模板和行为开关。**复制**会生成一份独立副本。 |
| **飞书机器人** | 一个机器人最多绑定一个 Agent。它把自己的聊天路由给该 Agent，并在所有对话中（包括网页对话）作为该 Agent 的飞书身份。 |
| **对话** | 一个公开 Core Session，拥有独立的 Environment，在开始时冻结 Agent 和环境配置。 |

修改 Agent 只影响新对话。Agent 或其环境模板变更后，已有对话会给出提示，并提供 **用新配置开始新对话**。

## 网页聊天 {#web-chat}

**新对话** 打开首页：选择 Agent、描述工作并发送。侧栏列出最近对话；**全部对话** 打开完整列表，支持搜索以及按 Agent、来源和日期筛选，并提供 **运行记录**、**用量** 和 **审计日志**。日期筛选按浏览器本地日期计算；用量汇总覆盖所有匹配记录，缺失用量保持未知。模型可配置每百万输入/输出 Token 的美元价格，在对话开始时冻结；费用为估算，不是服务商账单。

对话会流式展示回复和工具活动，归组工作步骤，可用对话导航条跳转用户轮次，支持追问和取消，并可重新打开持久历史。Agent 执行中发送的消息会进入正在运行的任务；业务提问请在提问卡片上回答。Enter 发送，Shift+Enter 换行。草稿在同一浏览器标签页中刷新后保留。文件夹按钮可在空闲时上传最大 5 MiB 的输入文件，并把路径插入草稿；实时工作区回收后仍可下载 Artifacts，请让 Agent 将交付物保存到 `./outputs`。信息按钮展示 Agent、模型、Harness、环境模板、飞书身份、来源以及创建时记录的配置，不含变量值、脚本内容或凭据。创建响应丢失时，对话页提供 **恢复创建**，复用冻结的请求和幂等键。

可选的业务提问使用声明的 `ask_user` 函数。问题出现在对话中，并通过带稳定请求 ID 的公开 function-result 事件返回。这是收集信息，不是执行审批。

**设置** 可修改工作空间名称、中英文界面以及浅色、深色或跟随系统的主题。

## Agent 与资源 {#agents-and-resources}

**Agent** 以卡片列出 Agent，展示模型、Harness、环境模板和绑定的飞书机器人。Agent 页面分为 **基本信息**、**指令**、**模型与环境**、**工具与知识** 和 **行为**。**主指令** 是 Agent 自己的指令文本；可选的 **指令片段** 按选择顺序追加，合并为一份系统提示词。指令片段、Plugins 和知识合计最多 8 个，选择器会阻止超出。**推理强度** 发送 `reasoning.effort`（低、中或高），或保留 Harness 默认值。**网页搜索** 为 Codex 和 Claude Code 发送 `live` 模式的 `web_search`，关闭时发送 `disabled`；MiniMax Code 无法开启。网页对话和飞书执行记录中的搜索显示为 **搜索网页**。在 Agent 页面中新建或编辑模型、Skill、MCP、环境模板或能力时，未保存的字段会保留，新建的资源会自动选中。在同一浏览器标签页中回到 Agent 页面时，会恢复未保存的修改；点击 **保存** 时会指出缺少的必填项。

**模型** 按 Provider 分组管理模型 ID。可获取 Provider 的 OpenAI 格式 `GET /v1/models` 目录，也可手动填写。密钥只保存在服务端，留空编辑会保留已保存的密钥。获取目录时只发送该 Provider 的密钥，且不跟随重定向。所有 Harness 都使用所选的 HTTPS Provider：Codex 使用 Responses 协议，Claude Code 和 MiniMax Code 使用 Anthropic 协议。不支持的组合会明确失败，请参阅 [Harness 覆盖记录](../../contracts/agents-api/zh/index.md)。

- **Skills** 支持创建 SKILL.md、上传 ZIP、版本管理、默认版本选择、按 Agent 固定版本以及有限的文件预览。**浏览目录** 搜索 skills.sh 索引；预览在临时目录中使用固定版本的 `skills@1.5.23` CLI，再走常规公开 Skill 上传。这需要 Parsar 主机具备 `npx` 和网络访问。导入的压缩包限制为 32 MiB 和 1,000 个文件。
- **MCP** 提供静态服务目录和 HTTPS 端点配置。可选的 Vault 凭据使用官方公开 MCP 工具，连接来源为 environment。
- **凭据** 创建 Vault 和只写的 bearer 凭据。**OAuth 授权** 使用维护中的 MCP SDK 完成发现、动态客户端注册、签发方绑定和 PKCE，再通过公开 Vault API 保存令牌。OAuth 令牌不会返回浏览器。
- 指令片段和知识具有不可变的编号版本、默认版本、固定版本和停用状态。知识版本会成为初始工作区文件。标准 Claude/Codex Plugin ZIP 会成为公开 Environment Plugin，并列出其中声明的 `.mcp.json` 服务。stdio 服务所需的可执行文件必须存在于环境中。

**环境模板** 只在 Core 托管的工作区中运行。每个新对话会先写入文件、安装软件包并完成初始化，再启动 Agent；初始化失败会停止执行。初始文件使用规范的 `/workspace/` 路径，脚本在工作区中运行。变量值在产品响应中只写：`null` 保留已有值，字符串替换，删除名称即删除变量。**飞书机器人变量** 把变量名映射到所绑定机器人的 **App ID**、**App Secret** 或地区；使用这类模板的对话要求 Agent 已绑定机器人。Parsar 通过公开 Session Environment 合约内联发送模板，不创建 Core Template 资源。

## 飞书 {#feishu}

**飞书接入** 为每个机器人展示一张卡片。**添加机器人** 提供 **扫码创建**（通过飞书设备流创建应用）和 **已有应用**（填写 App ID 与 App Secret）。新机器人在配置完成前保持停用。密钥保存在受限的本地数据库中。官方 SDK 负责鉴权、消息规范化和长连接，不需要公网回调。

1. 在卡片上选择 **Agent**，打开 **私聊** 和/或 **群聊**，然后 **保存并启用**。启用需要应用凭证、已配置环境模板的 Agent，以及两者至少开启一项。打开 **群聊** 后，机器人会回复它所在的任何群里的 @ 消息，把机器人拉进群即可使用。谁能添加机器人由开发者后台中应用的可用范围控制。
2. 在开发者后台启用机器人、长连接事件推送和 `im.message.receive_v1`，发布应用并把它加入目标群。授予接收 @ 消息和以机器人身份发送消息的权限；私聊、消息资源和表情回复需要各自的权限。缺少表情权限不会丢弃消息。
3. 私聊为每个发送者保持一个对话，直到发送 `/new` 或 `/reset`。群聊中 @ 机器人会开启一个话题，每个话题是一个对话，所有在其中回复的人共享。只有应用开通了敏感权限“获取群组中所有消息”（`im:message.group_msg`），话题中不带 @ 的后续消息才会推送给机器人；否则需要在话题中再次 @ 机器人。其他机器人和 `@所有人` 会被忽略；关闭 **群聊** 时忽略群消息，关闭 **私聊** 时忽略私聊。更换绑定的 Agent 只影响新话题和新的私聊对话。
4. 文本、富文本、图片、文件、音视频、引用消息和合并转发会在投递前规范化。附件上限为 8 个、合计 5 MiB。后续消息中的文件会等到对话空闲后写入。引用消息最多读取四层；不读取更早的群消息。
5. `/cancel`、`/answer <call_id> <回答>`、`/new` 和 `/help` 在同一话题中使用。只有一个待回答的业务问题时，直接回复即可作答。取消优先于排队中的追问，并会先确认不确定的创建再取消。

收到的消息在 WebSocket ACK 之前写入 SQLite；请求 ID 和冻结的负载在重试与重启后保持不变。工作器原地更新同一张进度卡片，并以结果收尾。进度每五秒轮询一次。完整历史和 Artifacts 保留在网页对话中。已结束的话题在五分钟后停止轮询，收到新消息时恢复。**消息记录** 列出最近 100 条投递以及未解决的记录；**重试** 保留原有标识。入站消息失败会阻塞该话题后续消息。不确定的出站消息超过 55 分钟后不再重放。停用机器人会暂停投递，但不会取消 Core 中的执行。

### 机器人身份 {#bot-identity}

Agent 以绑定的机器人身份工作。使用 **飞书机器人变量** 时，Parsar 会把机器人的 App ID 和 App Secret 写入对话 Environment 的变量。飞书案例把它们映射为 `FEISHU_*` 变量，初始化脚本运行 `lark-cli config init --app-secret-stdin`，由 CLI 自行获取并刷新租户令牌，长对话也能持续工作。不要映射为 `LARKSUITE_CLI_APP_ID` 或 `LARKSUITE_CLI_APP_SECRET`：这两个变量会让 lark-cli 进入环境凭据模式，该模式要求直接提供租户令牌。任何能与机器人对话的人都能使用机器人的权限，Environment 中的工具也能读取该密钥。只为机器人授予其 Agent 需要的权限，尽量一个 Agent 使用一个机器人。轮换后的密钥只对新对话生效。

### 飞书工作环境案例 {#feishu-workspace-case}

[飞书工作环境](examples/feishu/)由普通模板组装而成。[environment.json](examples/feishu/environment.json) 声明软件包、机器人变量和文件，[setup.sh](examples/feishu/setup.sh) 安装经过校验和验证的官方 CLI 并用机器人凭证完成配置，[agent.json](examples/feishu/agent.json) 定义一个 Codex Agent。配置好模型，导入官方 `lark-shared` Skill 和需要的业务 Skill，添加机器人后运行：

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --bot BOT_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

`--bot` 是 Parsar 中的机器人 ID，不是飞书 App ID。脚本通过与界面相同的 `/app/runtimes` 和 `/app/agents` 操作创建环境模板和 Agent，绑定机器人并输出 Agent 链接。第一条消息可以要求执行 `lark-cli api GET /open-apis/bot/v3/info --as bot --json`。运行节点必须能解析并访问飞书开放平台；模板无法修复节点 DNS 或网络策略。

## 数据归属与本地 API {#ownership-and-local-api}

Core 的 Project key 只保存在服务端。对话创建成功后，只保留 Core 引用和产品元数据；创建未确认时保留冻结的公开请求直到确认。Core 仍是执行、Items、Turns、Skills、Vaults 和 Artifacts 的权威来源。SQLite 保存产品配置、能力版本、飞书投递状态以及临时 OAuth/注册状态。文件位于 `~/.oac/data/parsar-example/`，可用绝对路径 `OAC_EXAMPLE_DATA_DIR` 覆盖；由 Core 地址和 Project key 指纹选择具体文件。请在服务停止时备份。Provider 和机器人密钥保存在其中，Core Project key 不在其中。构建和缓存使用 `${OAC_DEV_HOME:-$HOME/.oac}`。

| 产品路由组 | 调用方与凭据 |
| --- | --- |
| `/app/providers`、`/app/models`、`/app/agents`、`/app/runtimes`、`/app/mcps`、`/app/sessions` | 同源本地 UI；飞书工作器在进程内复用对话创建 |
| `/app/feishu/bots`、`/app/feishu/provision`、`/app/feishu/deliveries` | 同源本地 UI；从不返回凭据 |
| `/app/capabilities`、`/app/skills/registry/preview` | 同源本地 UI |
| `/app/mcp-oauth/<operation-id>`、`/oauth/callback` | 本地 OAuth 流程；随机 state、PKCE 和同源完成 POST |
| `/app/settings`、`/app/audit` | 同源本地 UI；审计只含变更元数据，不含请求体 |
| 允许列表内的 `/v1` 代理 | 同源本地 UI；服务端注入 Project key |

Core 独立于本示例构建和分发。产品代理不提供 `/core/v1` 或机器 API。

## 验证 {#validation}

```sh
make check-example
```

该检查运行 TypeScript、Node 代理/产品/飞书测试、生产构建和浏览器验收。浏览器夹具使用 18180/18181 端口和 `~/.oac/tests/parsar-example/` 下的隔离数据；18180 被占用时设置 `OAC_EXAMPLE_PORT`。需要时用 `pnpm --filter @oac/parsar-example exec playwright install chrome` 安装 Chrome。测试覆盖持久重试、重复入站、飞书路由、共享话题、机器人身份变量、扫码状态、OAuth PKCE/Vault 持久化和界面流程。夹具测试不代表真实模型或飞书服务可用。

可选的真实浏览器探针需要一个已运行、并连接独立真实 Project 的示例：

```sh
OAC_EXAMPLE_LIVE_URL=http://127.0.0.1:18180 \
OAC_EXAMPLE_LIVE_MODEL='<configured-model>' \
pnpm --filter @oac/parsar-example test:live
```

它会调用真实模型，并保留资源供检查。真实飞书验收需要一个具备相应权限的专用测试机器人和已授权的会话；仅完成配置并不能证明投递可用。
