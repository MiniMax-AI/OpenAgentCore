import type { ConnectionLogin } from "../Connections";
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
  enabled?: boolean;
  pricing?: { input: number; output: number };
  provider_id: string;
  model: string;
}
export interface MCPProfile extends NamedResource {
  vault_id?: string;
  credential_id?: string;
  label: string;
  url: string;
}
export interface RuntimeProfile extends NamedResource {
  environment: "openai_hosted";
  description?: string;
  packages?: { npm?: string[]; python?: string[] };
  setup_script?: string;
  connection_env?: Record<string, string>;
  env?: Record<string, string | null>;
  files?: { path: string; content: string }[];
}
export interface AgentProfile extends NamedResource {
  connection_requirement?: {
    service: string;
    bot_id: string;
    app_id?: string;
    scopes: string;
  };
  enabled?: boolean;
  ask_user?: boolean;
  runtime_id?: string;
  capability_ids?: string[];
  capability_versions?: Record<string, string>;
  model_id: string;
  harness: CoreHarnessKind;
  instructions: string;
  skill_ids: string[];
  skill_versions?: Record<string, string>;
  mcp_ids: string[];
}
export interface SessionRecord {
  connection_wait?: ConnectionLogin;
  connection_error?: string;
  connection?: { name: string; subject_id: string; expires_at: number };
  configuration?: {
    connection?: { service: string; name: string; expires_at: number };
    agent_name: string;
    model: string;
    harness: CoreHarnessKind;
    environment: string;
    environment_name: string;
    packages?: { npm?: string[]; python?: string[] };
    setup_configured?: boolean;
    env_names?: string[];
    file_paths?: string[];
    skills: { id: string; version: string | null }[];
    mcps: string[];
    capabilities: { name: string; kind: string; version: string }[];
  };
  pricing?: { input: number; output: number };
  source?: "web" | "task" | "feishu" | "schedule" | "mcp";
  deleted_at?: number;
  task_id?: string;
  brief?: string;
  id: string;
  name: string;
  agent_id: string;
  runtime_id: string;
  created_at: number;
  core_session_id?: string;
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

export interface ProjectRecord extends NamedResource {
  description: string;
  repository: string;
  created_at: number;
  updated_at: number;
}
import { taskStatuses, taskPriorities } from "../../shared/work-profile.mjs";
export { taskStatuses, taskPriorities };
export interface TaskRecord extends NamedResource {
  description: string;
  project_id: string;
  agent_id: string;
  runtime_id: string;
  status: keyof typeof taskStatuses;
  priority: keyof typeof taskPriorities;
  created_at: number;
  updated_at: number;
}
export interface TaskActivity {
  notify_agent?: boolean;
  delivery?: "pending" | "sent";
  delivery_error?: string;
  id: string;
  task_id: string;
  type: string;
  message: string;
  created_at: number;
  from?: TaskRecord["status"];
  to?: TaskRecord["status"];
  session_id?: string;
}
export const useProjects = () => useResources<ProjectRecord>("projects");
export const useTasks = () =>
  useQuery({
    queryKey: ["tasks"],
    queryFn: () => product<TaskRecord[]>("tasks"),
    refetchInterval: 5000,
  });
