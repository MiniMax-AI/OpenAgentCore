"""Create a self-hosted Codex Session with the opt-in Parallel MCP Plugin."""

import os
from pathlib import Path

from openai import OpenAI


workspace = Path(os.environ["EXECUTOR_WORKSPACE"])
plugin = Path(os.environ["EXECUTOR_PLUGIN"])
if not workspace.is_absolute() or not plugin.is_absolute():
    raise ValueError("EXECUTOR_WORKSPACE and EXECUTOR_PLUGIN must be absolute paths")

client = OpenAI()  # OPENAI_BASE_URL and OPENAI_API_KEY name the Core Project.
session = client.beta.agents.sessions.create(
    environment={
        "type": "self_hosted",
        "workspace_directory": str(workspace),
        "capability_directories": [str(plugin)],
    },
    input=os.environ.get(
        "SEARCH_PROMPT",
        "Use Parallel web_search to find the OpenAgentCore repository. "
        "Then use web_fetch on https://github.com/MiniMax-AI/OpenAgentCore "
        "and summarize the project with source URLs.",
    ),
    extra_body={
        "agent": {
            "model": os.environ["MODEL_NAME"],
            "x_agents_core": {"harness": "codex"},
        },
        "x_agents_core": {
            "model_provider": {
                "protocol": "responses",
                "base_url": os.environ["MODEL_BASE_URL"],
                "api_key": os.environ["MODEL_API_KEY"],
            },
        },
    },
    extra_headers={"Idempotency-Key": os.environ["REQUEST_ID"]},
)
print(session.id)
installation = session.model_dump().get("x_agents_core", {}).get("installation")
if installation and installation.get("status") == "available":
    print(installation["commands"]["posix"])
else:
    print("Session created. Ask the Core operator to enable its matching native installer, "
          "then read the installation command from the Session in Web.")
