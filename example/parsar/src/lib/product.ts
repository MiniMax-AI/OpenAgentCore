import { useQuery } from "@tanstack/react-query";
import type { CoreHarnessKind } from "@oac/agents-client";
export interface NamedResource {
  id: string;
  name: string;
  revision: number;
}
export interface ProviderProfile extends NamedResource {
  base_url?: string;
  has_api_key?: boolean;
}
export interface ModelProfile extends NamedResource {
  provider_id: string;
  model: string;
}
export interface MCPProfile extends NamedResource {
  label: string;
  url: string;
}
export interface RuntimeProfile extends NamedResource {
  environment: "none" | "openai_hosted" | "self_hosted";
  platform?: "linux" | "macos" | "windows";
  workspace_directory?: string;
  capability_directories?: string[];
}
export interface AgentProfile extends NamedResource {
  model_id: string;
  harness: CoreHarnessKind;
  instructions: string;
  skill_ids: string[];
  mcp_ids: string[];
}
export interface SessionRecord {
  id: string;
  name: string;
  agent_id: string;
  runtime_id: string;
  created_at: number;
  core_session_id?: string;
  self_hosted?: {
    platform: "linux" | "macos" | "windows";
    workspace_directory: string;
  };
}
export class ProductError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function product<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(`/app/${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  const data = await response.json();
  if (!response.ok)
    throw new ProductError(response.status, data.error?.message || "请求失败");
  return data as T;
}
export const useResources = <T>(kind: string) =>
  useQuery({ queryKey: [kind], queryFn: () => product<T[]>(kind) });
export const useProviders = () => useResources<ProviderProfile>("providers");
export const useModels = () => useResources<ModelProfile>("models");
export const useAgents = () => useResources<AgentProfile>("agents");
export const useRuntimes = () => useResources<RuntimeProfile>("runtimes");
export const useMCPs = () => useResources<MCPProfile>("mcps");
export const useSessions = () => useResources<SessionRecord>("sessions");

export const harnessNames = {
  claude_sdk: "Claude Code",
  codex: "Codex",
  mcode: "MiniMax Code",
};
