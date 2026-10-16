// Product guidance only; Core remains authoritative for capability validation.
export function sessionRestriction(agent, runtime) {
  if (!runtime) return undefined;
  if (runtime.environment !== "openai_hosted")
    return "Parsar 仅支持托管工作环境，请重新配置环境模板。";
  if (agent.harness === "mcode" && agent.mcp_ids.length)
    return "MiniMax Code 暂不支持此处的 HTTP MCP，请选择 Claude Code 或 Codex。";
}
