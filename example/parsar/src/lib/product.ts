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
  label: string;
  url: string;
  // Each Agent that binds it chooses the Vault credential.
  requires_credential?: boolean;
}
export interface Secret extends NamedResource {
  has_value?: boolean;
  value?: string;
}
export interface RuntimeProfile extends NamedResource {
  environment: "openai_hosted";
  description?: string;
  packages?: { npm?: string[]; python?: string[] };
  setup_script?: string;
  // Variables the Agent supplies: the bound bot's identity or chosen secrets.
  credentials?: string[];
  network?: { access: "disabled" | "restricted"; allowed_domains?: string[] };
  env?: Record<string, string | null>;
  prepare?: boolean;
  files?: { path: string; content: string }[];
}
export interface AgentProfile extends NamedResource {
  description?: string;
  enabled?: boolean;
  ask_user?: boolean;
  harness: CoreHarnessKind;
  model_id: string;
  reasoning_effort?: string;
  reasoning_summary?: string;
  web_search?: string;
  verbosity?: string;
  service_tier?: string;
  runtime_id?: string;
  capability_ids?: string[];
  capability_versions?: Record<string, string>;
  instructions: string;
  skill_ids: string[];
  skill_versions?: Record<string, string>;
  mcp_ids: string[];
  // The secret that supplies each variable a bound capability needs.
  credentials?: Record<string, string>;
  // The Vault credential each bound MCP that needs one uses.
  mcp_credentials?: Record<string, { vault_id: string; credential_id: string }>;
}
export interface TurnSummary {
  outcome: "running" | "completed" | "failed" | "cancelled";
  stage?: "preparing" | "starting" | "working" | "waiting_user" | "retrying";
  kind: "cold" | "prepared" | "followup";
  started_at: number;
  elapsed_s: number;
  slow: boolean;
  retries: number;
  failure?: string;
  done_at?: number;
  first_output_s: number | null;
}
// One Turn in the 运行 view.
export interface TurnRow extends TurnSummary {
  id: string;
  session_id: string;
  core_session_id: string;
  agent_id: string;
  agent_name?: string;
  conversation: string;
  source: "web" | "feishu";
  chat_type?: "p2p" | "group";
  prompt: string;
  latest: boolean;
}
export interface TurnOverview {
  running: TurnRow[];
  recent: TurnRow[];
  stats: { running: number; waiting_user: number; failed_recent: number; concurrency: number[] };
}
export interface SessionRecord {
  turn?: TurnSummary;
  superseded_turns?: string[];
  configuration?: {
    agent_name: string;
    agent_revision?: number;
    runtime_revision?: number;
    bot_name?: string;
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
  source?: "web" | "feishu";
  feishu_chat_type?: "p2p" | "group";
  deleted_at?: number;
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
export const useSecrets = () => useResources<Secret>("secrets");
// The variables each Skill needs; Skills themselves live in Core.
export const useSkillCredentials = () =>
  useQuery({
    queryKey: ["skill-credentials"],
    queryFn: () => product<{ id: string; credentials: string[] }[]>("skills/credentials"),
  });
// Running and recent Turns; the sidebar count and the 运行 view share it.
export const useTurnOverview = () =>
  useQuery({
    queryKey: ["turns"],
    queryFn: () => product<TurnOverview>("turns"),
    refetchInterval: 2000,
  });
export const useRuntimes = () => useResources<RuntimeProfile>("runtimes");
export const useMCPs = () => useResources<MCPProfile>("mcps");
export const useSessions = () =>
  useQuery({
    queryKey: ["sessions"],
    queryFn: () => product<SessionRecord[]>("sessions"),
    refetchInterval: 10000,
  });

export const harnessNames = {
  claude_sdk: "Claude Code",
  codex: "Codex",
  mcode: "MiniMax Code",
};

export interface FeishuBot extends NamedResource {
  brand?: "feishu" | "lark";
  health?: { state: string };
  app_id: string;
  app_secret?: string;
  has_app_secret: boolean;
  enabled: boolean;
  agent_id: string;
  direct: boolean;
  groups: boolean;
}
export const useFeishuBots = () =>
  useQuery({
    queryKey: ["feishu"],
    queryFn: () => product<FeishuBot[]>("feishu/bots"),
    refetchInterval: 5000,
  });
