# Parallel Search MCP example

Create a self-hosted Codex Session that can search the web and fetch pages using the anonymous [Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp) endpoint. No Parallel API key or Vault is needed. Free access has rate limits; model inference uses your separately configured Responses provider.

This example selects an [Environment Plugin](../../contracts/agents-api/environments.md#plugin-mcp) through `capability_directories`. The Plugin uses HTTP MCP and sends `OpenAgentCore-Parallel-Example/1.0` as its User-Agent. It changes no default provider or saved Agent. Use Codex: the Claude and MiniMax adapters do not accept this Plugin's literal HTTP header.

## Run

Start with a configured [Core installation](../../docs/getting-started/install.md) and a Project API key. The following commands target a Linux amd64 executor. Follow [self-hosted execution](../../docs/getting-started/self-hosted.md) for machine prerequisites, connection, credential handling and operation.

On the executor, copy `plugin/` into your workspace before connecting the daemon. Select that directory itself, rather than its parent, so the Runtime activates its MCP declaration:

```sh
export EXECUTOR_WORKSPACE="$HOME/oac-search-workspace"
mkdir -p "$EXECUTOR_WORKSPACE"
cp -R example/parallel-search/plugin "$EXECUTOR_WORKSPACE/parallel-search"
export EXECUTOR_PLUGIN="$EXECUTOR_WORKSPACE/parallel-search"
```

Install this example's pinned official Agents SDK in a clean environment:

```sh
python3 -m venv "$HOME/.oac/parallel-example-venv"
"$HOME/.oac/parallel-example-venv/bin/python" -m pip install -r example/parallel-search/requirements.txt
```

Configure the Core endpoint and Project key as `OPENAI_BASE_URL` (including `/v1`) and `OPENAI_API_KEY`. Configure `MODEL_NAME`, `MODEL_BASE_URL` (your Responses provider's `/v1` endpoint) and `MODEL_API_KEY` separately. Supply those keys privately; do not put them in the Plugin files. The Session carries its own model provider, as required for self-hosted execution.

```sh
export REQUEST_ID="$(python3 -c 'import uuid; print(uuid.uuid4())')"
"$HOME/.oac/parallel-example-venv/bin/python" example/parallel-search/create_session.py
```

The script prints the Session ID followed by its private installation command when Core serves a matching native installer. If the installer is unavailable, it prints how to obtain the command after the operator enables it. Run that command on the executor, selecting Codex when prompted. Keep the command private because it contains a short-lived installation grant. Reuse the same `REQUEST_ID` and unchanged inputs if Session creation needs to be retried; generate a new ID for a new Session. `SEARCH_PROMPT` optionally replaces the example's search-and-fetch request.

The initial Turn waits for the executor to connect. Read the reply and `mcp_call` Items on the Session's page in Web or through the [Session API](../../docs/api/public-agent-api.md#sessions). The Plugin is snapshotted before execution; create a new Session to pick up changes to its files.
