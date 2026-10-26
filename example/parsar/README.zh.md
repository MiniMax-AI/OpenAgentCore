---
source: example/parsar/README.md
source_hash: 62934f80a9b631d42ea26739bc2439bc11093975debcbf789fe48f567603a1fe
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

打开 http://127.0.0.1:18180。`OPENAI_BASE_URL` 以 `/v1` 结尾；远程地址必须使用 HTTPS，也可通过 SSH 隧道将远程 Core 映射至本机。`OAC_EXAMPLE_PORT` 调整端口。构建运行使用 `pnpm --filter @oac/parsar-example build`，再运行 `pnpm --filter @oac/parsar-example start`。服务监听 `OAC_EXAMPLE_HOST`，默认 `127.0.0.1`。设为本机的某个 IPv4 地址（例如局域网地址）即可向该网络提供服务；Parsar 没有登录，能访问该地址的人都可以使用工作空间、其中的模型和飞书机器人。允许从其他站点的链接顶层打开首页，API 请求仍检查同源。浏览器 Host 必须是该地址（默认回环地址时也接受 `localhost`），本地隧道可使用不同端口，写请求的 Origin 必须与 Host 完全一致，包括端口；每个数据目录只运行一个服务进程。

## 产品模型 {#product-model}

| 对象 | 含义 |
| --- | --- |
| **资源库** | 组装用的零件，按使用方分组：模型供 Agent 的执行引擎使用；Skills、MCP 服务、Plugins、知识和指令片段供 Agent 使用；凭据供 MCP 使用。资源是共享的：修改一项会影响所有使用它的对象。只有仍然有效的配置会阻止删除：Agent 绑定了这项能力、Agent 使用这个环境模板或模型，或者机器人绑定了这个 Agent。对话不算：它保留创建时的配置快照和完整历史。 |
| **环境模板** | 准备好的工作区：npm 与 Python 软件包、Bash 初始化脚本、环境变量、飞书机器人变量和初始文件。 |
| **Agent** | 用户对话的对象：名称、描述、指令、运行方式、一个必选的环境模板、能力和行为开关。运行方式对应本地 Codex 的配置：Harness、模型、推理强度、网页搜索，以及 **高级** 中的推理摘要、输出详细度和服务层级。**复制**会生成一份独立副本。 |
| **飞书机器人** | 一个机器人最多绑定一个 Agent。它把自己的聊天路由给该 Agent，并在所有对话中（包括网页对话）作为该 Agent 的飞书身份。 |
| **对话** | 一个公开 Core Session，拥有独立的 Environment，由 Agent 对应的 Core Agent 和环境模板对应的 Core Environment Template 创建。Session 开始时，Core 会复制这两份配置。 |

配置只在模板和 Agent 中设定，对话不会覆盖。修改只影响新对话。Agent 或其环境模板变更后，已有对话会给出提示，并提供 **用新配置开始新对话**。

## CLI 与管理 Agent {#cli-and-management-agent}

[cli.mjs](cli.mjs) 使用 Node 22.13+，无额外依赖，直接调用网页使用的 `/app` 和已开放的 `/v1` 接口，不新增产品服务端接口或执行路径。`PARSAR_URL` 填 Parsar 网页地址，不是 Core 地址。权限沿用当前部署的可信网络边界：能访问 Parsar 的调用方可以管理资源。CLI 和 Agent 不持有 Core key；不要把工作空间直接开放到公网，也不要把管理环境交给不可信的 Agent。

```sh
export PARSAR_URL=http://127.0.0.1:18180
node example/parsar/cli.mjs --help
node example/parsar/cli.mjs GET /app/agents
node example/parsar/cli.mjs uuid
node example/parsar/cli.mjs PUT /app/runtimes/REPLACE_WITH_UUID --file environment.json
```

CLI 输出 JSON，`--file -` 从标准输入读取 JSON 请求体。不跟随重定向，不自动重试写入。创建前先生成并保留资源 ID；写入结果不确定时，先 GET 同一个 ID。编辑时先 GET，修改后 PUT 完整对象，保留 `revision` 和未修改字段；遇到 409 需重新读取并核对修改。环境变量返回的 `null` 原样传回即可保留原值。客户端支持 JSON 接口；二进制下载、multipart 上传和 SSE 继续使用现有网页或客户端。

同一个文件也可作为 JavaScript SDK 使用：

```js
import { createParsarClient } from "./cli.mjs";
const parsar = createParsarClient(process.env.PARSAR_URL, { token: process.env.PARSAR_TOKEN });
const agents = await parsar("GET", "/app/agents");
```

[管理案例](examples/manager/configure.mjs) 用普通 Agent 和普通环境模板创建 **parsar他爹**。环境中包含 CLI 和[操作说明](examples/manager/GUIDE.md)，使用已有模型和托管 Codex、Claude Code 自带的 Node，无需 MCP 或特殊 Agent 类型。先将 `OAC_EXAMPLE_HOST` 设为配置脚本和沙箱都能访问的本机内网 IPv4，再启动 Parsar（见[运行](#run)）；默认的 loopback 监听无法服务托管沙箱：

```sh
node example/parsar/examples/manager/configure.mjs \
  --url http://PARSAR_LAN_IP:18180 \
  --model MODEL_ID --harness codex
```

`--url` 是配置脚本访问的地址；`--agent-url` 是沙箱能访问的同一台 Parsar 地址，默认等于 `--url`。只有部署确实提供地址间代理时，才使用不同地址。脚本在写入前输出资源 ID，拒绝覆盖已有资源；也可通过 `--agent-id` 和 `--environment-id` 指定 ID。若后续步骤失败，按输出的 ID 检查，已创建的部分会保留。打开返回的 Agent 链接发起对话，例如“创建一个能分析 Excel 的 Agent，复用现有模型，配置好环境后给我链接。”创建的资源直接出现在原有列表，仍可通过网页编辑。试运行只证明环境准备和首次回复正常，不代表完整业务任务通过；共享模板可能使用另一个绑定的 Agent 试运行。

也可以说“帮我给这个 Agent 开通飞书机器人。”管理 Agent 会按操作指南复用机器人或发起注册，给你授权链接，等你确认后绑定 Agent，再检查连接和投递状态。已有应用的密钥只在 **飞书接入** 页面填写，不发到聊天里。新接入默认仅开私聊，群聊需明确要求。注册成功、连接成功都不等于测试消息已经收到并回复。

### 托管沙箱与认证入口 {#hosted-sandboxes-and-an-authenticated-gateway}

托管 microsandbox 部署默认禁止访问私网，仅放行 Core 连接（见[节点默认设置](../../docs/zh/getting-started/nodes.md)）。因此主机能访问的内网页面地址，在 Agent 内仍可能报 `ECONNREFUSED`。不要放宽 Core 隔离规则或改用管理接口；应为 Parsar 提供带独立凭据的公网 HTTPS 入口。

[Caddy 配置示例](examples/manager/Caddyfile) 只监听 `127.0.0.1:18181`，转发前必须验证非空的 `PARSAR_TOKEN`，仅开放 `/app/*` 和 `/v1/*`，并在转发到 Parsar 前删除入口凭据。Host 和 Origin 会改为现有服务要求的 `PARSAR_UPSTREAM` 地址，不增加 Core 权限。令牌拥有与内网页面相同的工作空间访问权限；使用密码学随机令牌，保存在私有环境文件中，只将管理环境分配给可信 Agent。

在 Parsar 主机安装 [Caddy](https://caddyserver.com/docs/install) 和 [cloudflared](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/)，将 `PARSAR_UPSTREAM` 设为现有 Parsar IPv4 地址和端口（例如 `10.0.0.2:18180`），从私有存储加载 `PARSAR_TOKEN`。先运行 Caddy，再用另一个进程运行隧道：

```sh
caddy run --config example/parsar/examples/manager/Caddyfile --adapter caddyfile
cloudflared tunnel --url http://127.0.0.1:18181
```

隧道只能指向认证入口，不可直接指向无认证的 Parsar 监听端口。使用前确认公网地址在没有令牌或令牌错误时均返回 401。CLI 自动从环境读取 `PARSAR_TOKEN` 并作为 Bearer 请求头发送；使用令牌时必须为 HTTPS（本地测试允许 loopback HTTP），仍不跟随重定向。可用 `node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("hex"))'` 生成令牌并重定向到权限为 0600 的文件，不要将令牌贴入对话或 URL。

运行管理案例时传入 `--url https://<隧道域名> --web-url http://<Parsar内网IP>:18180`，并在配置进程中加载 `PARSAR_TOKEN`。脚本将令牌写入环境变量，产品读取时会隐藏其值，同时设置 `PARSAR_WEB_URL` 用于网页链接。已有管理 Agent 通过普通 GET/PUT 流程更新环境的 `PARSAR_URL`、`PARSAR_TOKEN`、`PARSAR_WEB_URL` 和初始 CLI、操作说明文件，保留 revision 与其他字段。更新后必须**新建对话**，已有对话仍使用旧地址和文件。

Quick Tunnel 地址只在该隧道进程运行期间有效，重启可能换地址。保持入口和隧道运行，地址变化后更新管理环境并新建对话。该预览入口用于 API，不提供公网网页会话或 SSE。停止隧道即可关闭公网访问；替换令牌并重启 Caddy 可轮换凭据。端口 18181 空闲时，用 `PARSAR_TEST_CADDY=/path/to/caddy node --test example/parsar/tests/gateway.test.mjs` 验证入口。

## 网页聊天 {#web-chat}

**新对话** 打开首页：选择 Agent、描述工作并发送。侧栏列出最近对话；**全部对话** 打开完整列表，支持搜索以及按 Agent、来源和日期筛选，并提供 **运行**、**用量** 和 **审计日志**。**运行** 也直接放在 **新对话** 下方，并显示正在进行的对话数。它列出网页和飞书中每一轮正在执行的对话，显示所处阶段、已等待时间和这个 Agent 通常的首次回复时间；也列出最近完成的轮次，显示用时、自动恢复次数和通俗的失败原因，并统计正在进行、等你回复和近 5 分钟失败的数量。可以在这里停止正在执行的轮次，或重试对话中最近失败的一轮。日期筛选按浏览器本地日期计算；用量汇总覆盖所有匹配记录，缺失用量保持未知。模型可配置每百万输入/输出 Token 的美元价格，在对话开始时冻结；费用为估算，不是服务商账单。

对话会流式展示回复和工具活动，归组工作步骤，可用对话导航条跳转用户轮次，支持追问和取消，并可重新打开持久历史。Agent 执行中发送的消息会进入正在运行的任务；业务提问请在提问卡片上回答。Enter 发送，Shift+Enter 换行。草稿在同一浏览器标签页中刷新后保留。文件夹按钮可在空闲时上传最大 5 MiB 的输入文件，并把路径插入草稿；实时工作区回收后仍可下载 Artifacts，请让 Agent 将交付物保存到 `./outputs`。信息按钮展示 Agent、模型、Harness、环境模板、飞书身份、来源以及创建时记录的配置，不含变量值、脚本内容或凭据。创建响应丢失时，对话页提供 **恢复创建**，复用冻结的请求和幂等键。

如需展示 thinking，将 Agent 的 **高级 → 推理摘要** 设为 `auto`，再新建对话。Harness 返回原生摘要时，**思考过程** 会直接出现在对话中，与工具步骤分开。生成时显示最新片段，结束后默认收成一行；点击可阅读完整摘要，断线后从历史恢复。已有对话保留创建时的配置；开启摘要不会为旧历史补出思考内容。

Parsar 会跟踪每一轮正在执行的对话，网页和飞书都一样。出现第一条输出之前，会显示所处阶段（**正在准备工作环境**、**正在启动**）、已等待的时间，以及这个 Agent 近 7 天通常的首次回复时间。自动恢复只在还没有执行任何内容时进行：环境初始化失败，或接管的提前准备会话 45 秒内仍未开始第一轮时，用同一份冻结的请求重建一次；一轮对话开始 45 秒后仍没有任何输出，会取消并重新发送一次输入；在任何输出之前就失败的一轮，会重新发送一次。被替换的轮次不会出现在对话中，也不会发到飞书。最终失败会用通俗的话说明原因，并提供 **重试**。**产物** 汇总最近对话生成的文件，并链接到对应的对话。

可选的业务提问使用声明的 `ask_user` 函数。问题出现在对话中，并通过带稳定请求 ID 的公开 function-result 事件返回。这是收集信息，不是执行审批。

**设置** 可修改工作空间名称、中英文界面以及浅色、深色或跟随系统的主题。

## Agent 与资源 {#agents-and-resources}

**Agent** 以卡片列出 Agent，展示执行引擎、模型、环境模板和绑定的飞书机器人。Agent 页面分为 **基本信息**、**执行与环境**、**指令**、**能力** 和 **行为**。**主指令** 是 Agent 自己的指令文本；可选的 **指令片段** 按选择顺序追加，合并为一份系统提示词。选择上限与 Core 的 [Environment 限制](../../contracts/agents-api/zh/environments.md#plugins-and-capability-directories)一致：Skills 和 Plugins 各最多 50 个，知识与环境模板文件合计最多 50 个；指令片段和 MCP 服务没有数量上限。只绑定 Agent 需要的资源，因为每个 Skill 和工具都会增加模型要读的内容。在 **执行与环境** 中，Harness 会把每个选项收窄到它声明支持的值（[Harness 能力](../../contracts/agents-api/zh/harness-capabilities.md)）；切换 Harness 时，新 Harness 不接受的值会恢复为默认，并说明是哪几项。留空的选项保留 Harness 默认值；网页搜索默认 **关闭**。网页对话和飞书执行记录中的搜索显示为 **搜索网页**。在 Agent 页面中新建或编辑模型、Skill、MCP、环境模板或能力时，未保存的字段会保留，新建的资源会自动选中。在同一浏览器标签页中回到 Agent 页面时，会恢复未保存的修改；点击 **保存** 时会指出缺少的必填项。已有 Agent 的页面会显示近 7 天的成功率、首次回复中位数和自动恢复次数。

**模型** 按 Provider 分组管理模型 ID。可获取 Provider 的 OpenAI 格式 `GET /v1/models` 目录，也可手动填写。密钥只保存在服务端，留空编辑会保留已保存的密钥。**测试** 会用已保存的连接发起一次极小的生成，显示耗时或服务商返回的错误，不会显示密钥。获取目录时只发送该 Provider 的密钥，且不跟随重定向。所有 Harness 都使用所选的 HTTPS Provider：Codex 使用 Responses 协议，Claude Code 和 MiniMax Code 使用 Anthropic 协议。不支持的组合会明确失败，请参阅 [Harness 覆盖记录](../../contracts/agents-api/zh/index.md)。

- **Skills** 支持创建 SKILL.md、上传 ZIP、版本管理、默认版本选择、按 Agent 固定版本以及有限的文件预览。**浏览目录** 搜索 skills.sh 索引；预览在临时目录中使用固定版本的 `skills@1.5.23` CLI，再走常规公开 Skill 上传。这需要 Parsar 主机具备 `npx` 和网络访问。导入的压缩包限制为 32 MiB 和 1,000 个文件。
- **MCP** 提供静态服务目录和 HTTPS 端点配置。可选的 Vault 凭据使用官方公开 MCP 工具，连接来源为 environment。
- **凭据** 创建 Vault 和只写的 bearer 凭据。**OAuth 授权** 使用维护中的 MCP SDK 完成发现、动态客户端注册、签发方绑定和 PKCE，再通过公开 Vault API 保存令牌。OAuth 令牌不会返回浏览器。
- 指令片段和知识具有不可变的编号版本、默认版本、固定版本和停用状态。知识版本会成为初始工作区文件。标准 Claude/Codex Plugin ZIP 会成为公开 Environment Plugin，并列出其中声明的 `.mcp.json` 服务。stdio 服务所需的可执行文件必须存在于环境中。

**环境模板** 只在 Core 托管的工作区中运行。每个新对话会先写入文件、安装软件包并完成初始化，再启动 Agent；初始化失败会停止执行。初始文件使用规范的 `/workspace/` 路径，脚本在工作区中运行。变量值在产品响应中只写：`null` 保留已有值，字符串替换，删除名称即删除变量。**飞书机器人变量** 把变量名映射到所绑定机器人的 **App ID**、**App Secret** 或地区；使用这类模板的对话要求 Agent 已绑定机器人。每个环境模板保存为一个 Core Environment Template，每个 Agent 保存为一个 Core Agent。它们本身或它们绑定的内容变化时，Parsar 会更新 Core 里的这两份资源；Core Project 更换后，下次使用时会重新创建。对话的 Session 引用这两份资源，只附加每次对话不同的内容：机器人变量、Agent 的 Skills、Plugins 和知识文件，以及附件（见[模板继承](../../contracts/agents-api/environments.md#inheritance)）。Core 可能在 24 小时内复用一次完全相同的准备结果（[准备结果复用](../../contracts/agents-api/zh/environments.md#preparation-reuse)），长时间空闲的对话也可能换到新沙箱；这两种情况下只保留 `/environment`（包括 `/workspace`），初始化脚本不会再次执行。因此初始化脚本必须把之后要用的内容写到 `/workspace` 或软件包目录下，不能写在 HOME 中。飞书工作环境通过 `LARKSUITE_CLI_CONFIG_DIR` 和 `LARKSUITE_CLI_DATA_DIR` 把 lark-cli 的配置和加密凭证放在 `/workspace/.lark-cli`。

**提前准备，新对话无需等待安装** 会为使用该模板的每个 Agent 保留一个准备好的 Session。Parsar 在没有输入的情况下创建它，Core 会提前准备好 Environment；下一个网页或飞书对话发送第一条消息时直接接管它，并保留自己的标题。Agent、其环境模板、机器人或模型服务商变更后，或超过 12 小时、或被接管后，准备好的 Session 会被替换；关闭开关会丢弃它。第一条消息带图片或文件时，会创建普通的 Session。每个准备好的 Session 在等待期间会占用一个工作区。

**试运行** 会用使用该模板的 Agent 发起一次真实对话，等待首次回复，显示成功及耗时或通俗的失败原因，然后删除这次对话。

## 飞书 {#feishu}

**飞书接入** 为每个机器人展示一张卡片。**添加机器人** 提供 **扫码创建**（通过飞书设备流创建应用）和 **已有应用**（填写 App ID 与 App Secret）。新机器人在配置完成前保持停用。密钥保存在受限的本地数据库中。官方 SDK 负责鉴权、消息规范化和长连接，不需要公网回调。

1. 在卡片上选择 **Agent**，打开 **私聊** 和/或 **群聊**，然后 **保存并启用**。启用需要应用凭证、已配置环境模板的 Agent，以及两者至少开启一项。打开 **群聊** 后，机器人会回复它所在的任何群里的 @ 消息，把机器人拉进群即可使用。谁能添加机器人由开发者后台中应用的可用范围控制。
2. 在开发者后台启用机器人、长连接事件推送和 `im.message.receive_v1`，发布应用并把它加入目标群。授予接收 @ 消息和以机器人身份发送消息的权限；私聊、消息资源和表情回复需要各自的权限。缺少表情权限不会丢弃消息。
3. 私聊为每个发送者保持一个对话，直到发送 `/clear`。群聊中 @ 机器人会开启一个话题，每个话题是一个对话，所有在其中回复的人共享。只有应用开通了敏感权限“获取群组中所有消息”（`im:message.group_msg`），话题中不带 @ 的后续消息才会推送给机器人；否则需要在话题中再次 @ 机器人。话题中只 @ 了其他人的回复是发给他们的，会被忽略。其他机器人和 机器人只服务自己所在的企业，外部联系人发来的消息会被忽略。`@所有人` 会被忽略；关闭 **群聊** 时忽略群消息，关闭 **私聊** 时忽略私聊。更换绑定的 Agent 只影响新话题和新的私聊对话。
4. 文本、富文本、图片、文件、音视频、引用消息和合并转发会在投递前规范化。附件上限为 8 个、合计 5 MiB。后续消息中的文件会等到对话空闲后写入。引用消息最多读取四层；不读取更早的群消息。
5. 命令：`/clear` 清空上下文：停止正在执行的任务，下一条消息开始新对话，私聊和群聊话题都适用（群聊仍在同一个话题里）。`/stop` 停止当前任务，优先于排队中的追问，并会先确认不确定的创建再停止。`/answer <call_id> <回答>` 回答待处理的业务问题；只有一个待回答的问题时，直接回复即可。`/help` 列出这些命令。

收到的消息在 WebSocket ACK 之前写入 SQLite；请求 ID 和冻结的负载在重试与重启后保持不变。工作器原地更新一张简洁进度卡片，将待回答的问题合并到卡片中，结束后改成结果。每次回复只展示当前交流的内容，包括回答提问后继续同一 Core Turn 的情况。正文突出当前回复；等待业务选择时优先展示问题，将补充说明折叠，二维码授权说明保持可见。卡片顶部是带颜色的状态标题栏。处理过程按发生顺序排成一条时间线。只有说明直接显示文字；思考折叠在第一句后面；连续的工具调用折叠成一行（例如“运行 3 条命令”，附总用时、失败次数或“进行中”），展开后每次调用再折叠在它的命令后面，可查看参数、输出、错误和退出码。执行中时，标题栏还会显示当前在做什么和已执行的步数；卡片显示最近四步，更早的步骤折叠进分隔线上方的浅色区块；完成后回复放在最前面，整个过程折叠进浅色的 **处理过程** 区块；失败时出错的那一步紧跟在失败原因下面。长内容按字节限制截取并明确标注；为了不超过飞书卡片的大小限制，最早的步骤会先移出卡片。进度每五秒轮询一次。完整历史和 Artifacts 保留在网页对话中。已结束的话题在五分钟后停止轮询，收到新消息时恢复。**消息记录** 列出最近 100 条投递以及未解决的记录；**重试** 保留原有标识。入站消息失败会阻塞该话题后续消息。不确定的出站消息超过 55 分钟后不再重放。停用机器人会暂停投递，但不会取消 Core 中的执行。

### 机器人身份 {#bot-identity}

Agent 以绑定的机器人身份工作。使用 **飞书机器人变量** 时，Parsar 会把机器人的 App ID 和 App Secret 写入对话 Environment 的变量。飞书案例把它们映射为 `FEISHU_*` 变量，初始化脚本运行 `lark-cli config init --app-secret-stdin`，由 CLI 自行获取并刷新租户令牌，长对话也能持续工作。不要映射为 `LARKSUITE_CLI_APP_ID` 或 `LARKSUITE_CLI_APP_SECRET`：这两个变量会让 lark-cli 进入环境凭据模式，该模式要求直接提供租户令牌。任何能与机器人对话的人都能使用机器人的权限，Environment 中的工具也能读取该密钥。只为机器人授予其 Agent 需要的权限，尽量一个 Agent 使用一个机器人。轮换后的密钥只对新对话生效。

### 飞书工作环境案例 {#feishu-workspace-case}

[飞书工作环境](examples/feishu/)由普通模板组装而成。[environment.json](examples/feishu/environment.json) 声明软件包、机器人变量和文件，[setup.sh](examples/feishu/setup.sh) 安装经过校验和验证的官方 CLI 并用机器人凭证完成配置，[agent.json](examples/feishu/agent.json) 定义一个 Codex Agent。配置好模型，导入官方 `lark-shared` Skill 和需要的业务 Skill，添加机器人后运行：

```bash
node example/parsar/examples/feishu/configure.mjs --url http://localhost:3001 --model MODEL_ID --bot BOT_ID --skills SHARED_SKILL_ID,DOMAIN_SKILL_ID
```

`--bot` 是 Parsar 中的机器人 ID，不是飞书 App ID。脚本通过与界面相同的 `/app/runtimes` 和 `/app/agents` 操作创建环境模板和 Agent，绑定机器人并输出 Agent 链接。第一条消息可以要求执行 `lark-cli api GET /open-apis/bot/v3/info --as bot --json`。运行节点必须能解析并访问飞书开放平台；模板无法修复节点 DNS 或网络策略。

## 数据归属与本地 API {#ownership-and-local-api}

Core 的 Project key 只保存在服务端。对话创建成功后，只保留 Core 引用和产品元数据；创建未确认时保留冻结的公开请求直到确认。Core 仍是执行、Items、Turns、Skills、Vaults 和 Artifacts 的权威来源，并以保存的 Agent 和 Environment Template 存放每个 Agent 和环境模板，这两类资源只由 Parsar 服务端写入。SQLite 保存产品配置、能力版本、飞书投递状态以及临时 OAuth/注册状态。文件位于 `~/.oac/data/parsar-example/`，可用绝对路径 `OAC_EXAMPLE_DATA_DIR` 覆盖；由 Core 地址和 Project key 指纹选择具体文件。请在服务停止时备份。Provider 和机器人密钥保存在其中，Core Project key 不在其中。构建和缓存使用 `${OAC_DEV_HOME:-$HOME/.oac}`。

| 产品路由组 | 调用方与凭据 |
| --- | --- |
| `/app/providers`、`/app/models`、`/app/agents`、`/app/runtimes`、`/app/mcps`、`/app/sessions`、`/app/artifacts` | 同源本地 UI；飞书工作器在进程内复用对话创建 |
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

### 聊天中的图片与文件

网页会话在回复旁预览 PNG、JPEG、GIF 和 WebP 产物，并提供文件下载。将输出保存到 `./outputs`；Markdown 中指向 `/workspace/outputs/` 的图片和文件链接会对应到已保存的产物。SVG 等其他格式只提供下载，不加载外部图片地址。飞书将每个产物发送为图片或文件消息，单个附件不能为空且不能超过 5 MiB。上传标识和消息标识会保留以供重试，失败记录显示在**消息记录**中。音视频作为文件下载，暂不在聊天中播放。

机器人注册返回 `verification_url` 和相对地址 `qr_url`。同时展示授权链接和 `![飞书授权二维码](<qr_url>)`，将占位符替换为返回的路径。PNG 由 Parsar 本地生成；注册过期或完成后，`GET /app/feishu/provision/<bot_id>/qrcode` 返回失效提示。网页和飞书使用同一张二维码，不会把授权地址发送给第三方二维码服务。
