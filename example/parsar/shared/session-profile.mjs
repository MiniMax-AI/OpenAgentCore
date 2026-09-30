// Product guidance only; Core remains authoritative for capability validation.
export function sessionRestriction(agent, runtime) {
  if (!runtime) return undefined;
  if (runtime.environment === "self_hosted") {
    if (agent.skill_ids.length)
      return "用户机器使用本地能力目录，不能绑定托管 Skill。请先编辑 Agent，移除托管 Skill。";
    if (agent.mcp_ids.length)
      return "用户机器的 MCP 需通过本地 Plugin 配置。请先编辑 Agent，移除 MCP 绑定。";
    if (agent.harness === "mcode")
      return "当前示例的用户机器支持 Codex 和 Claude Code，请先编辑 Agent 的执行引擎。";
  }
  if (runtime.environment === "none" && agent.skill_ids.length)
    return "Skills 需要托管运行环境，请选择托管沙箱或移除 Skill 绑定。";
  if (agent.harness === "mcode" && agent.mcp_ids.length)
    return "MiniMax Code 暂不支持此处的 HTTP MCP，请选择 Claude Code 或 Codex。";
}
