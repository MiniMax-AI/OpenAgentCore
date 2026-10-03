---
title: "运行第一个 Session"
source: docs/getting-started/quickstart.md
source_hash: 8270c0ded9adbd5a31f2a29c8bc801fe3a857ca3d1a940cc9ee469f99c3d04f2
---

本教程从 Project API 密钥开始，让 Agent 创建一个文件并汇报结果。你需要：

- 管理员提供的 Project API 密钥和 API 基础 URL（[签发 Project API 密钥](install.md#issue-a-project-api-key)）；
- 已就绪的节点或 E2B 后端，让 Core 能运行 Agent（[节点](nodes.md)）；
- Python 3.9 或更高版本；
- 一个模型：安装的默认模型，或你自己的提供商的模型 ID、基础 URL 和 API 密钥。[模型执行](../../../contracts/agents-api/zh/model-execution.md#saved-defaults-and-precedence)说明 Session 使用哪个模型，以及每个 Harness 支持哪些协议。

示例使用 Codex，其模型提供商必须支持 OpenAI Responses API。

## 1. 连接 {#_1-connect}

Core 提供 OpenAI Agents API，因此无需修改官方 OpenAI SDK。SDK 读取两个环境变量：

| 变量 | 值 |
| --- | --- |
| `OPENAI_BASE_URL` | API 基础 URL：公开 URL 加 `/v1`，例如 `https://core.example/v1`。Web 的 **System** 页面会显示它 |
| `OPENAI_API_KEY` | 你的 Project API 密钥 |

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install openai==3.13.0
export OPENAI_BASE_URL=https://core.example/v1
read -rs OPENAI_API_KEY && export OPENAI_API_KEY   # paste the key; it is not echoed
```

检查访问情况。此操作不运行模型，也不创建沙箱：

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY
print(client.beta.agents.list().data)
```

空列表表示连接成功。使用 curl 做同样的检查：

```sh
curl "$OPENAI_BASE_URL/agents" -H "OpenAI-Beta: agents=v1" \
  -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY")
```

## 2. 启动 Session {#_2-start-a-session}

Session 是拥有独立工作区的一次 Agent 对话。使用 `openai_hosted` 时，Core 在节点或 E2B 上创建沙箱，并在其中启动 Harness。

使用安装的默认模型时，跳过下面这一步。如果使用自己的提供商：

```sh
export MODEL_NAME='your-model-id'
export MODEL_BASE_URL='https://your-provider.example/v1'
read -rs MODEL_API_KEY && export MODEL_API_KEY
```

```python
import os

extra = {"agent": {"x_agents_core": {"harness": "codex"}}}
if os.environ.get("MODEL_API_KEY"):
    extra["agent"]["model"] = os.environ["MODEL_NAME"]
    extra["x_agents_core"] = {"model_provider": {
        "protocol": "responses",
        "base_url": os.environ["MODEL_BASE_URL"],
        "api_key": os.environ["MODEL_API_KEY"],
    }}

session = client.beta.agents.sessions.create(
    environment={"type": "openai_hosted"},
    input="Create /workspace/hello.txt with a short greeting, then describe it.",
    extra_body=extra,
)
print(session.id)
```

这会发起真实模型请求，可能产生费用。

- `x_agents_core` 包含 Core 对 OpenAI API 的扩展；参阅 [Core 扩展](../api/public-agent-api.md#core-extensions-x-agents-core)。
- 将整个 `agent` 对象放入 `extra_body`。SDK 3.13.0 使用 `extra_body` 中的同名字段替换请求体字段，而非合并。

## 3. 等待结果 {#_3-wait-for-the-result}

Session ID 表示创建成功，不代表执行成功。轮询持久化状态；不要重新提交来“重试”：

```python
import time

for _ in range(120):
    turns = client.beta.agents.sessions.turns.list(session.id, order="desc").data
    if turns and turns[0].status in {"completed", "failed", "cancelled"}:
        turn = turns[0]
        print("Turn:", turn.id, turn.status)
        print(client.beta.agents.sessions.items.list(session.id).data)
        break
    if client.beta.agents.sessions.retrieve(session.id).status == "failed":
        raise RuntimeError("Session preparation failed; inspect its Environment")
    time.sleep(1)
else:
    raise TimeoutError(f"Session {session.id} is still running; inspect it before retrying")
```

成功意味着一个状态为 `completed` 的 Turn，其 Items 描述新文件。超时不会取消工作，也不能证明执行失败。

## 后续步骤 {#next-steps}

| 目标 | 阅读 |
| --- | --- |
| 流式输出、发送后续消息、上传文件、添加 Skills 或 MCP、取消 | [Agents API 指南](../api/public-agent-api.md#common-tasks) |
| 查看各资源的请求和响应示例 | [Agents API 指南](../api/public-agent-api.md) |
| 在自己的机器上运行 Agent | [自托管执行](self-hosted.md) |
| 查看完整应用 | [示例](../examples.md) |
