---
source: example/rl-eval/README.md
source_hash: 09095ac4d87e83d491aec1f9464e56af1f04eab6b9e28b548d2e1d72102665e8
---

# RL 与评测 rollout

[English](README.md)

一个 Python 脚本，把 OpenAgentCore 的公开 `/v1` Agents API 当作 rollout 引擎：在托管沙箱中以任意 Harness 和模型批量运行任务，并在沙箱外评分。输出是一个 JSONL 文件，每行一次 rollout，含奖励、答案和完整轨迹。它说明 Core 除了聊天产品，也能服务 RL 训练与评测。

## rollout 如何运行 {#how-a-rollout-works}

一次 rollout 是一个 Session，对应一组（任务、Harness、模型、样本）。脚本依次：

1. 读取任务的 `files/`，作为 `openai_hosted` Environment 的内联 `files` 放到 `/workspace/` 下，并把网络 `access` 设为 `disabled`，沙箱只能访问 Core 和模型提供方。
2. 以任务的 `instruction.md` 作为 `input` 创建 Session。新的托管 Session 在 Core 准备沙箱期间读数为 `idle`，Environment 就绪后 `input` 启动第一个 Turn。
3. 轮询 Turn 列表，直到 Turn 为 `completed`、`failed` 或 `cancelled`。超时则取消 Turn 并记录 `timeout`。
4. 从 Artifact `/workspace/outputs/answer.json` 读取答案；Core 在 Turn 完成时捕获该文件。
5. 评分，读取 Items 和 Turn 用量，然后删除 Session。

评分在脚本中进行。`expected.json` 从不进入沙箱，Agent 无法读到标准答案。评分为二值：答案必须是包含全部期望键且值匹配的 JSON 对象。数字按 1e-6 的相对容差匹配，字符串去除首尾空白后匹配，多余的键忽略。

[Agents API 指南](../../docs/zh/api/public-agent-api.md)说明每个调用：[创建 Session](../../docs/zh/api/public-agent-api.md#create-a-session)、[取消](../../docs/zh/api/public-agent-api.md#cancel)、[Turns 和 Items](../../docs/zh/api/public-agent-api.md#turns-and-items)、[Artifacts](../../docs/zh/api/public-agent-api.md#artifacts)和 [Core 扩展](../../docs/zh/api/public-agent-api.md#core-extensions)。初始文件限制和受限网络由 [Environment 契约](../../contracts/agents-api/zh/environments.md)定义。

## 启动 {#run}

使用 Python 3.9+。按[快速开始](../../docs/zh/getting-started/quickstart.md)准备好 [Core](../../docs/zh/getting-started/install.md)、模型和 Project API 密钥：

```sh
cd example/rl-eval
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_BASE_URL=https://core.example/v1
read -rs OPENAI_API_KEY && export OPENAI_API_KEY
python3 rl_eval.py --harness codex --harness claude_sdk --samples 4
```

不指定 `--harness` 和 `--model` 时，rollout 使用部署默认 Harness 和安装默认模型，标记为 `default`。

| 选项 | 含义 |
| --- | --- |
| `--harness`、`--model` | 可重复，每种组合都会运行 |
| `--samples K` | 每个任务、Harness 和模型的 rollout 数，默认 1 |
| `--concurrency N` | 并行 rollout 数，默认 4 |
| `--tasks DIR`、`--task ID` | 任务目录（默认 `tasks/`）和可重复的任务 id 过滤 |
| `--timeout SECONDS` | 每次 rollout 的时限，含沙箱准备，默认 900 |
| `--out PATH` | JSONL 文件，默认 `runs/<UTC 时间戳>.jsonl` |
| `--model-base-url`、`--model-protocol` | 自有模型端点及其协议（`anthropic`、`responses` 或 `chat_completions`）。密钥只从 `MODEL_API_KEY` 读取，需同时指定 `--model` |

运行结束时按 Harness 和模型各输出一张 Markdown 表：rollout 数、平均奖励、pass@k（至少有一个奖励为 1 的样本的任务占比）、错误与超时数、平均时长。即使奖励全为 0，退出码也是 0。

## 输出 {#output}

每个完成的 rollout 向输出文件追加一行 JSON：

| 字段 | 含义 |
| --- | --- |
| `rollout_id`、`task`、`harness`、`model`、`sample` | rollout 的标识。未设置时 `harness` 和 `model` 为 `default` |
| `session_id` | Core Session，rollout 结束后删除 |
| `status` | `completed`、`failed`、`cancelled`、`timeout` 或 `error` |
| `reward` | `1.0` 或 `0.0` |
| `answer` | 解析后的 `answer.json`，没有则为 `null` |
| `duration_s`、`usage` | 耗时，以及最后一个 Turn 的 token 用量。不报告用量的 Harness 为 `null`，见[使用量](../../contracts/agents-api/zh/sessions-events.md#usage) |
| `items` | 轨迹：API 返回的全部 Session Items，按顺序 |
| `error` | `status` 为 `error` 时的异常信息 |

## 任务 {#tasks}

| 任务 | Agent 需要 |
| --- | --- |
| `sales-by-region` | 按地区和季度汇总含退款和带引号字段的 CSV |
| `failed-logins` | 统计 syslog 文件中每个地址的 SSH 密码失败次数 |
| `inventory-bug` | 按 `PRICING.md` 修复定价缺陷，运行报表并读取总额 |
| `orders-json` | 在嵌套 JSON 中查询某个 SKU 的客户数和消费 |
| `salaries-sqlite` | 在 SQLite 数据库中比较各部门的年度平均薪资 |

它们只需要托管沙箱镜像自带的 bash、git、python3、node 和 ripgrep，不需要网络。

新增任务时创建 `tasks/<id>/`，包含：

- `instruction.md`：准确陈述问题，并以输出约定结尾：用给定的键写入 `/workspace/outputs/answer.json`。
- `expected.json`：包含期望值的 JSON 对象，留在沙箱之外。
- `files/`：保持相对路径递归复制到 `/workspace/`。Core 最多接受 50 个内联文件，单个 5 MiB，总计 10 MiB。

## 接入训练 {#connect-training}

每个 Harness 原生调用模型，因此训练侧的推理服务看得到每个请求，并记录 token id 和 logprob；Core 不记录这些。让 rollout 指向该服务：

```sh
export MODEL_API_KEY=<key-of-the-inference-server>
python3 rl_eval.py --model my-policy --model-protocol responses \
  --model-base-url 'https://infer.example/rollouts/{rollout_id}/v1' --samples 8
```

`{rollout_id}` 每次 rollout 替换为输出中的 `rollout_id`，服务据此把每个请求归属到对应的 rollout。同一任务的 K 个样本构成一个 GRPO 组，`reward` 是该组的结果奖励。网络为 `disabled` 时，沙箱仍可访问模型提供方 `base_url` 端口上的公网地址；私有推理主机需要部署的[网络上限](../../contracts/agents-api/zh/environments.md#restricted-network)覆盖其名称。

## 适用范围 {#scope}

脚本覆盖在沙箱外评分的任务。需要在自身环境内评分的任务（如 SWE-bench、Terminal-Bench）需要你自己的容器和 `self_hosted` Environment，本示例不涉及。

## 结果 {#results}

2026-10-29 在一套由 `main` 构建的测试部署上记录，沙箱为 microsandbox 节点，两个 Harness 的默认模型均为 MiniMax-M2.7：

```sh
python3 rl_eval.py --harness codex --harness claude_sdk --samples 2 --concurrency 2
```

| harness | model | rollouts | mean reward | pass@k | errors/timeouts | mean duration (s) |
| --- | --- | --- | --- | --- | --- | --- |
| claude_sdk | default | 10 | 1.000 | 1.000 | 0 | 25.7 |
| codex | default | 10 | 1.000 | 1.000 | 0 | 17.6 |

两个 Harness 解出了全部内置任务，所以这些任务只演示流程，不提供训练信号。GRPO 组只有在样本结果不一致时才有学习信号：请用策略时对时错的任务来训练。

## 验证 {#validation}

```sh
python3 rl_eval_test.py
```

测试不需要网络，也不需要 `openai` 包，覆盖评分、pass@k 汇总和加载全部内置任务。
