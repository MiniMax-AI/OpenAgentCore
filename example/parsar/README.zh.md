---
source: example/parsar/README.md
source_hash: a5a198d59cb8cf06cca3ae0d26ef141ef6d8310948625d31417803e2806359c7
---

# Parsar 工作空间

[English](README.md)

Parsar 是可独立启动的可选产品示例，只通过 OpenAgentCore 公开 `/v1` Agents API 执行任务。密集列表、详情侧栏、导航、主题和会话控件沿用原 Parsar 的设计。Core 管执行；Parsar 管 Agent、项目、任务、能力、定时工作和飞书投递。执行始终使用 bypass。账号和细粒度权限暂缓；本轮 IM 接入仅支持飞书。

## 启动 {#run}

使用 Node 22.13+ 和 pnpm 10.30.3，准备好 [Core](../../docs/zh/getting-started/install.md)、模型连接和专用 Project key。密钥配置放在仓库外的私有环境文件中：

```sh
export OPENAI_BASE_URL='http://127.0.0.1:8091/v1'
export OPENAI_API_KEY='<project-api-key>'
pnpm --filter @oac/parsar-example... install --frozen-lockfile
pnpm --filter @oac/parsar-example dev
```

打开 http://127.0.0.1:18180。`OPENAI_BASE_URL` 以 `/v1` 结尾；远程地址必须使用 HTTPS，也可通过 SSH 隧道将远程 Core 映射至本机。`OAC_EXAMPLE_PORT` 调整端口。构建运行使用 `pnpm --filter @oac/parsar-example build`，再运行 `pnpm --filter @oac/parsar-example start`。服务只监听本机回环地址。允许从其他站点的链接顶层打开首页，API 请求仍检查同源。本地隧道可使用不同端口；浏览器 Host 必须为 `localhost` 或 `127.0.0.1`，写请求的 Origin 必须与该 Host 完全一致，包括端口；每个数据目录只运行一个服务进程。

**设置**可修改工作空间名称和中英文界面。侧栏与 Agent 详情栏可调整宽度并记住选择，主题支持浅色、深色和跟随系统。**对话**、**运行记录**、**用量**、**审计日志**分别展示会话引用、公开 Turn、Token 用量和产品变更。日期筛选按浏览器本地日期计算；用量汇总覆盖所有匹配记录，而非仅当前页。缺失用量保持未知。模型可配置每百万输入/输出 Token 的美元价格，在会话创建时冻结；费用按该平价估算，不是服务商账单。

## Agent、能力与会话 {#agents-capabilities-and-sessions}

**构建**包含三个入口：**Agent 模板**定义职责，**环境模板**定义工作环境的准备内容，**资源库**提供可复用资源。**接入与设置**包含**飞书接入**与**设置**；网页任务和会话从**工作**与**活动**进入。

Agent 详情默认打开**配置**。编辑器分为**系统提示词**、**模型与环境**、**工具与知识**和**账号与行为**。**主指令**定义 Agent 自身的职责；可选的**指令片段**按选择顺序追加，最终组成一份系统提示词。资源绑定使用统一的可搜索选择器，仅展示已选资源，并按需提供版本选择、**查看或编辑**和**移除绑定**操作；移除绑定不会删除资源。指令片段、Plugins 和知识合计最多绑定 8 个，选择器会限制超额添加。**管理连接**可打开账号管理，并保留 Agent 草稿。在编辑器内新建或编辑模型、Skill、MCP、环境或能力会保留 Agent 草稿，新建资源自动绑定。修改共享资源也会影响使用它的其他 Agent。草稿按 Agent 分别保存，可从**未保存的草稿**恢复，在当前浏览器标签页跨导航和刷新保留，直到保存或选择**放弃草稿**，不包含 Provider 密钥。环境列表展示准备内容摘要、用途说明和引用该模板的 Agent。

会话中的**本次运行**展示公开 Core Session/Environment 引用，以及首次尝试创建时记录的安全配置摘要。重试沿用摘要，之后修改模板不会改写历史。未固定版本的 Skill 标注创建时使用默认版本，不冒充已知的实际版本；旧会话未记录摘要会明确说明。不暴露完整请求或凭据。

飞书环境策略可选**使用 Agent 默认环境**或显式指定环境模板。继承在新话题创建任务时解析，已有话题保留绑定。默认环境缺失或不支持自动运行时明确拒绝，`/select` 同样验证，不自动回退。

**模型**按 Provider 管理模型 ID。可以读取 Provider 返回 OpenAI 格式的 `GET /v1/models` 列表，也可以手动输入。保存时事务提交 Provider 与所选模型。密钥留在服务端，编辑时留空保留旧值；发现模型仅发送该 Provider 的密钥，不跟随重定向。模型可停用而不删除历史引用。Codex、Claude Code 工作空间会话使用所选 HTTPS Provider 地址、密钥和对应 Harness 协议；MiniMax Code 使用 Core 配置的连接，创建表单会说明区别。不支持的组合会明确报错，具体边界见 [Harness 覆盖清单](../../contracts/agents-api/zh/index.md)。

**资源库**集中管理供 Agent 模板绑定的模型、Skills、MCP、Plugins、指令片段、知识和凭据：

- **Skills**支持创建 SKILL.md、上传 ZIP、版本管理、默认版本、Agent 版本固定和有大小限制的文件预览。**浏览目录**搜索 skills.sh 索引，也可直接填写来源仓库和 Skill 名称。服务端使用维护中的固定版本 `skills@1.5.23` CLI 下载至临时目录，再走普通公开 Skill 上传。主机需要 `npx` 和网络连接；导入最多 32 MiB、1,000 个文件。Core 是 Skill 的权威存储。
- **MCP**保留原服务目录和 HTTPS 端点配置。可绑定公开 Vault/Credential。使用 environment origin；不生成匿名兼容 Plugin。
- **凭据**可创建 Vault、添加只写 Bearer 凭据、更换 Token 和删除凭据。**OAuth 授权**使用维护中的 MCP SDK 完成发现、动态客户端注册、签发方绑定和 PKCE。本机回调页的**保存凭据**操作通过公开 Vault API 存储访问令牌及可用的刷新配置；OAuth Token 不返回浏览器。服务商需支持所选 SDK 授权流程和回环重定向；需要重新同意时再次授权。
- 提示词和知识能力有不可变编号版本、默认版本、固定版本和停用状态。提示词扩展冻结的 Agent 指令，知识版本成为初始工作空间文件。标准 Claude/Codex Plugin ZIP 映射为公开 Environment Plugin，编辑器展示 `.mcp.json` 声明的服务；stdio 所需可执行程序必须存在于运行环境。
- Plugin 能力可附带最多 1 MiB 的预构建**前端扩展 JavaScript（可选）**。代码只留在 Parsar，不发给 Core。启用的前端扩展以本地页面权限执行，应仅安装可信代码。共享 `window.__PARSAR_PLUGIN_API__` 提供原 Parsar 的 React、`createContext` 和 `definePlugin`。插槽协议位于 [plugin-slots.ts](src/lib/plugin-slots.ts)，支持 `workspace.main`、`workspace.content`、`layout.header.actions`、`layout.nav.bottom`、`conversation.header.actions`、`conversation.input.dock`、`conversation.tool-card` 和 `agent.settings.section`。工具卡匹配器通过 `item` 读取公开 `mcp_call`，不能依赖执行引擎原生数据；其他上下文为插槽传入的 Agent/Session 引用。停用能力会移除已注册 UI。跨工作空间市场发布依赖暂缓的账号/工作空间模型。

**Agent 模板**组合模型、Harness、指令、能力和可选默认环境模板。创建、复制、启停、编辑配置不会分配执行资源，修改作用于新会话。可选业务提问使用声明的 `ask_user` 函数，在会话和**收件箱**中显示，通过带稳定请求标识的公开函数结果事件提交回答。这用于补充信息，不是执行审批。

**环境模板**定义 npm 与 Python 软件包、Bash 初始化脚本、环境变量和初始文本文件。Parsar 固定使用 Core 托管工作环境，不提供部署位置、用户机器或沙箱供应商选择。每个新会话先安装文件与软件包，完成初始化后再启动 Agent；初始化失败会停止执行。软件包可固定版本；文件使用规范的 `/workspace/` 路径；脚本从工作目录执行，建议使用相对路径。系统依赖由托管镜像提供，限制网络的执行尚不受支持，因此不将这两项作为可编辑设置。

这些可编辑模板由应用拥有，准备字段通过公开 Session Environment 协议内联传入，不另外创建一份 Core Template 资源。产品响应中的环境变量值为只写：`null` 保留同名已有值，字符串替换值，移除名称则删除变量。密钥应放入变量，不写入脚本或文件内容；Agent 工具可以读取所选变量。网页、飞书、定时任务和 MCP 派发都将选定模板冻结到同一套待创建请求中；模板更新仅影响新会话，重试保留原准备配置。**本次运行**展示软件包名称、变量名称、初始文件路径及是否配置脚本，不包含变量值或脚本正文。

会话支持流式回复、工具步骤折叠、原 Parsar 用户轮次导航、追问、取消和持久化历史回读。草稿及实际观测的请求/首文本耗时保存在当前浏览器标签页。**文件**在空闲时上传不超过 5 MiB 的输入，将路径加入草稿。Artifact 分页下载不依赖当前工作空间在线；可要求 Agent 将产物写入 `./outputs`。下载保持二进制。**对话**通过公开 API 重命名和删除会话；执行中的会话应先停止。

沙箱列表/重建/续期以及外部 HTTP/A2A Agent 执行器受公开 API 边界阻塞，暂不实现；不使用管理员接口、共享 Core 表或伪造替代语义。

## 任务与项目 {#tasks-and-projects}

**项目**保存公共上下文和可选 HTTPS 仓库 URL。上下文进入每次新派发；仓库 URL 不会自动配备密钥或克隆。**任务**提供列表/看板、搜索、项目/Agent 筛选、优先级、评论和执行链接。可先规划，再补执行配置。

**分配执行**在创建公开 Session 前冻结任务说明、项目上下文和 Agent 配置。**重新执行**创建独立会话。**恢复执行**在响应丢失或重启后复用原请求及标识；未确认创建、准备中或执行中的会话会阻止重叠派发。

评论是持久化产品记录。勾选**提及已分配的 Agent**后，若没有合适执行，则带着评论启动任务；已有执行则继续同一 Agent 会话。自动启动使用选定的环境模板。失败或结果未确认的评论保留**重试发送**，重用原请求标识；普通评论不执行任务。

业务状态**待办**、**进行中**、**受阻**、**待验收**、**已完成**独立于执行状态。派发进入**进行中**，执行结束后由操作者记录验收。**待验收**可选，不限制引擎权限。完成的任务需重新打开后才能再派发；有执行历史的任务和仍被引用的资源不能删除。

## 定时工作 {#recurring-work}

**定时任务**基于任务模板，支持 5–525600 分钟间隔或五段 Cron 与 IANA 时区。**立即执行**适用于暂停的计划，重试保留请求标识。每次冻结模板并创建独立任务/Session。停机期间错过的间隔合并为一次，上轮未结束则跳过下一次；Parsar 服务需要持续运行。

创建暂时失败时恢复同一次执行。确定拒绝会暂停计划，修改后可启用，已手动恢复的任务会被复用。连续五次自动执行失败或取消后暂停，成功执行清零，重新启用也清零；手动执行不计入连续失败。**执行历史**链接任务和会话，并展示观测结果与投递错误。暂停不取消已提交工作；删除计划保留任务，但未确认创建会阻止删除。

计划可向已启用飞书机器人的允许群投递。后台发送初始卡片并建立持久化话题，后续结果与回复继续同一会话；机器人或路由变更导致的投递错误会显示在历史中，不会悄悄转发到其他目标。

## 个人连接 {#personal-connections}

**我的连接**管理本地操作人的外部账号，目前支持飞书。OAuth 适配器负责授权、身份核验、续期、撤销和连接字段；通用任务生命周期不认识 CLI 名称、安装命令或厂商环境变量。**Agent 模板**的**所需用户连接**声明应用和**授权范围**。环境模板负责软件安装、固定配置和**连接变量映射**（`connection_env`）：每个变量绑定 `access_token`、`client_id` 或 `region`，不能与固定环境变量重名，也不能绑定刷新令牌或应用密钥。需要连接的 Agent 必须选用配置了映射的环境，带映射的环境也必须搭配账号连接。映射随任务冻结，变量名展示在**本次运行**；个人凭据不保存在任何模板中。飞书应用必须先开通相应用户权限及刷新令牌权限。

新任务会冻结 Agent 和环境配置；缺少授权时在创建 Core Session 之前等待。网页展示原始授权链接、二维码和验证码，飞书消息提供链接及验证码。服务端持久化轮询官方 OAuth 设备授权流程，核验账号后自动继续原任务。拒绝或过期需要点击**重新连接**（飞书中使用 `/connect`）；**取消任务**或 `/cancel` 取消等待中的工作。轮询和创建重试支持进程重启，并保留原执行幂等键。刷新令牌和应用密钥只保存在受限产品存储，不发送给 Core、浏览器或模型。

本地网页操作人是单一身份，不是产品账号或浏览器独立账号。飞书任务绑定发起人的应用内 open ID，授权结果必须是同一人。同一账号与应用的新授权请求会保留已有权限，避免在不同 Agent 之间切换时反复覆盖授权；需要缩小权限时先断开连接。已核验连接仅按同一应用和账号复用；群内其他成员不能继续使用发起人的个人连接。Agent MCP 客户端不能启动需要个人连接的 Agent。定时任务使用本地操作人的连接，缺少授权时保留任务等待。

每个新 Session 通过公开 Environment 的 `env` 合同接收短期访问令牌，服务端在必要时先续期。**本次运行**显示凭据快照有效期；公开 API 无法替换已有 Environment 的凭据，因此过期后必须新开会话，长时间运行的 Turn 也可能遇到过期。**断开连接**立即阻止产品继续使用该授权执行，并请求服务端撤销；未确认的撤销持久化重试，不宣称已完成。历史和产物仍保留。

飞书适配器遵循[官方 CLI v1.0.98 设备授权流程](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/device_flow.go)、[续期](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/uat_client.go)和[撤销](https://github.com/larksuite/cli/blob/v1.0.98/internal/auth/revoke.go)。这些生命周期属于产品，不增加 Core 协议或特权端点。

## 飞书工作环境案例 {#feishu-workspace-case}

Parsar 是 OpenAgentCore 的应用示例；[飞书工作环境](examples/feishu/)是用普通 Parsar 模板组合的案例。[environment.json](examples/feishu/environment.json)声明软件包、CLI 固定身份配置、文件及连接映射，[setup.sh](examples/feishu/setup.sh)安装并校验官方 CLI 二进制，[agent.json](examples/feishu/agent.json)定义 Codex 的工作方式。案例不增加服务端路由、执行器或 Core 功能。

通过现有资源页面配置模型、导入官方 `lark-shared` 和业务 Skill，并配置飞书应用。把对应 ID 填入下列命令；`--app` 是 Parsar 中机器人配置的 ID，不是外部 App ID。脚本使用与网页相同的 `/app/runtimes` 和 `/app/agents` 操作创建环境与 Agent，并打印 Agent 链接：

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --app BOT_CONFIGURATION_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

默认权限用于只读身份试用；用 `--scopes` 指定 Agent 需要的业务权限。在**Agent 模板**中开启对话，按提示通过**我的连接**授权。第一条消息可以要求执行 `lark-cli api GET /open-apis/authen/v1/user_info --as user --json`。官方 CLI 的外部令牌模式禁用 `auth` 管理命令，因此案例使用只读身份 API。其他工具可以通过自己的变量映射使用同一连接生命周期。

运行节点需要预先能够解析并访问服务端点。模板以托管环境用户执行，无法修复节点 DNS、修改 root 所有的 hosts 文件或覆盖网络策略。目前 mx 试用已经验证个人授权、自动启动任务和新 Session 复用，但 CLI 业务调用仍被安装环境的飞书内部网关 DNS／网络配置阻塞；不能据此宣称该环境上的业务闭环已经通过。

## 飞书 {#feishu}

**飞书接入**支持多个企业/个人机器人、飞书/Lark 区域、配置的群及可选私聊路由。密钥保存在受限本地数据库；官方 SDK 负责鉴权、标准化和长连接，无需公网入站回调。

1. **添加机器人**可填写已有 **App ID**/**App Secret**，或通过**扫码创建机器人**的设备流程创建应用。轮询支持重启恢复与节流，不公开设备码及最终密钥。创建后默认停用，需配置路由再启用。
2. 在开发者后台启用机器人、长连接事件投递与 `im.message.receive_v1`，发布应用并拉入群。授权提及消息接收和以机器人身份发送；私聊、消息资源、表情反馈及可选群历史另需对应权限。缺少历史或表情权限不会丢弃已接收任务。
3. 私聊时，选择**回复私聊的 Agent**，点击**启用私聊**。新建私聊接入跟随 Agent 默认环境，已有环境覆盖设置保留。重新启用带有群聊配置的机器人时，会明确提示群聊也将恢复；共享私聊接入明确标注为默认 Agent，并保留用户已有选择。实际连接建立后才显示**已连接，可以去飞书聊天了**；在飞书私聊机器人发送任务，需要个人账号时会引导授权。**高级设置：群聊、应用凭证与更多选项**保留应用凭证、群聊、项目、环境覆盖、共享 Agent 切换及启停配置。群成员和可私聊机器人的用户构成执行访问边界。**允许群成员切换 Agent（共享机器人）**开启后，可通过 `/list`、`/select <名称或 ID>` 选择新任务的 Agent；偏好按发送者和聊天保存。缺少默认环境或不支持自动运行的环境会明确拒绝，不会自动回退。修改配置仅影响新任务，已有话题保留执行绑定。
4. 群中新任务需提及机器人，已识别话题内的追问不再需要提及。私聊延续该发送者的会话；`/new`、`/reset` 新开会话。其他机器人、未配置聊天和 `@all` 不触发工作。路由变化不改变已有话题的执行绑定。
5. 支持文本、富文本、图片、文件、音视频资源、引用消息链和合并转发。上下文读取发生在持久化接收之后。每条最多八个附件、合计 5 MiB，内联图片另受公开消息体大小限制；追问文件等待会话空闲再写入。**新话题附带最近群消息（需要历史消息权限）**添加有数量上限的群上下文；引用链最多四层。成功派发后清理投递记录中的附件字节。
6. 同一话题可使用 `/cancel`、`/review`、`/done`、`/reopen`、`/help`。取消优先于排队追问，创建未确认时先恢复实际 Session 再取消。`/answer <call_id> <回答>`，或仅有一个业务问题时直接回复，会提交公开函数结果。执行权限始终 bypass。

消息写入 SQLite 后才确认 WebSocket ACK。公开请求标识及冻结载荷可跨重试/重启恢复。后台核对持久化 Turns/Items，更新接收卡片上的进度和最终状态，为每个终结 Turn 发送结果卡片，并在结果/提问后发送文本 @ 提醒。进度每五秒轮询，不运行另一套模型/工具循环；输入中表情为尽力提供。完整历史和 Artifact 下载保留在 Web。空闲话题经过五分钟宽限后停止轮询，新消息会唤醒。

**消息记录与排查**展开后，**最近投递**显示最新 100 条及所有未解决记录。**重试此投递**复用原标识。繁忙时文件写入十秒后重试，其他暂时错误按有上限的退避重试；失败消息阻止同话题后续消息。飞书 UUID 去重窗口为一小时，未确认回传超过 55 分钟后不自动重放，需要人工检查原话题。停用机器人会暂停接收和回传，不取消 Core 工作；应用或路由变化不会换身份发送旧消息。

## 通过 MCP 提供 Agent {#expose-an-agent-through-mcp}

Agent 的**对外提供**标签页可创建、替换、撤销本机 MCP Token，并展示客户端配置。密钥仅显示一次，数据库只保存哈希和 90 天有效期。端点为 `http://127.0.0.1:<port>/mcp/agents/<agent-id>`，使用维护中的 MCP SDK Streamable HTTP，仅在本机开放，独立于 Core API。

`ask_agent` 使用 Agent 默认的环境模板创建普通公开 Session，返回 `run_id` 和会话链接。`get_run` 获取通过该 Agent 端点启动的任务状态和输出，排队/运行时可继续查询。若执行已提交但首次读取状态失败，`ask_agent` 仍返回执行编号和 `status: "unknown"`，应通过 `get_run` 恢复查询，不要重复发起任务。业务问题在 Parsar 回答；Token 不能读取其他 Agent 的任务。不包含基于账号的共享或公网托管。

## 数据归属与本地 API {#ownership-and-local-api}

Core Project key 留在服务端。创建成功后只保留 Core 引用和产品元数据；未确认时保留冻结的公开请求。执行、Items、Turns、Skills、Vaults、Credentials、Artifacts 以 Core 为权威。SQLite 保存产品配置、能力版本、任务/评论、定时回执、飞书投递及临时 OAuth/注册状态。默认目录 `~/.oac/data/parsar-example/`，`OAC_EXAMPLE_DATA_DIR` 可设为其他绝对路径；Core 来源和 Project key 指纹决定文件。停服后备份。Provider/机器人密钥及待完成 OAuth 凭据在数据库中，Core Project key 不在。构建与缓存使用 `${OAC_DEV_HOME:-$HOME/.oac}`。

| 产品路由组 | 调用方与凭据 |
| --- | --- |
| `/app/providers`、`/app/models`、`/app/agents`、`/app/runtimes`、`/app/mcps`、`/app/sessions` | 同源本地 UI；产品后台在进程内复用操作 |
| `/app/projects`、`/app/tasks`、任务 `/runs` 和 `/activity` | 同源本地 UI 和已授权飞书/定时操作 |
| `/app/schedules`、计划 `/run` 和 `/history` | 同源本地 UI；后台保留稳定执行标识 |
| `/app/feishu/bots`、`/app/feishu/provision`、`/app/feishu/deliveries` | 同源本地 UI；不返回服务端密钥 |
| `/app/capabilities`、能力 `/client.js`、`/app/skills/registry/preview` | 同源本地 UI 和安装的可信前端扩展 |
| `/app/mcp-oauth/<operation-id>`、`/oauth/callback` | 本地 OAuth；随机 state、PKCE、同源完成 POST |
| `/app/agents/<id>/mcp-token` | 同源本地 UI；新 Token 仅创建响应可见 |
| `/mcp/agents/<id>` | 本机 MCP 客户端；Agent 范围 Bearer Token |
| `/app/settings`、`/app/audit` | 同源本地 UI；审计只记变更元数据，不记请求正文 |
| 白名单 `/v1` 代理 | 同源本地 UI；服务端注入 Project key |

Core 可独立构建和分发。产品代理不开放 `/core/v1` 或机器 API。

## 验证 {#validation}

```sh
make check-example
```

该检查覆盖 TypeScript、Node 代理/产品/后台测试、生产构建和浏览器验收。浏览器测试使用 18180/18181 端口及 `~/.oac/tests/parsar-example/` 独立数据。需要时运行 `pnpm --filter @oac/parsar-example exec playwright install chrome` 安装 Chrome。测试覆盖稳定重试、重复接收、飞书标准化/附件/上下文、二维码状态、计划失败、OAuth PKCE/Vault、MCP 客户端鉴权、前端扩展与 UI 流程。测试夹具不能证明真实模型或飞书服务可用。

可选真实浏览器验收需要先启动连接独立真实 Project 的示例：

```sh
OAC_EXAMPLE_LIVE_URL=http://127.0.0.1:18180 \
OAC_EXAMPLE_LIVE_MODEL='<configured-model>' \
pnpm --filter @oac/parsar-example test:live
```

它会执行真实模型并保留资源以便检查。飞书真实验收需要指定测试机器人、相应权限及允许测试的聊天；仅保存配置不等于投递验证通过。
