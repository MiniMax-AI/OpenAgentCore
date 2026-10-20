import { tr } from "./i18n";
import { useAgents, useMCPs, useProfiles } from "./product";

// Who uses a building block, so lists can show it and block removal up front.
export function useUsage() {
  const agents = useAgents().data || [],
    profiles = useProfiles().data || [],
    mcps = useMCPs().data || [];
  const count = (n: number, unit: string) =>
    n ? tr("被 {{count}} 个{{unit}}使用", { count: n, unit }) : "";
  return {
    model: (id: string) =>
      count(profiles.filter((row) => row.model_id === id).length, tr("执行配置")),
    runtime: (id: string) =>
      count(agents.filter((row) => row.runtime_id === id).length, "Agent"),
    mcp: (id: string) =>
      count(agents.filter((row) => row.mcp_ids.includes(id)).length, "Agent"),
    skill: (id: string) =>
      count(agents.filter((row) => row.skill_ids.includes(id)).length, "Agent"),
    capability: (id: string) =>
      count(
        agents.filter((row) => row.capability_ids?.includes(id)).length,
        "Agent",
      ),
    credential: (id: string) =>
      count(mcps.filter((row) => row.credential_id === id).length, "MCP"),
  };
}
