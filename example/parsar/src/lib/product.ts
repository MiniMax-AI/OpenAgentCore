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
  provider?: "orcarouter";
  credential_source?: "" | "api_key" | "pkce";
  /** Bumped on every credential replacement; a 401 marks one exact generation. */
  generation?: number;
  /** `ok`, or `needs_reauth` after the relay rejected this exact credential. */
  status?: "ok" | "needs_reauth";
  account?: string;
  orcarouter?: OrcaRouterView;
}
export interface OrcaRouterModel {
  id: string;
  name: string;
  context_window?: number | null;
  max_output_tokens?: number | null;
  input_modalities?: string[] | null;
  supported_endpoint_types?: string[] | null;
}
export interface OrcaRouterCatalog {
  models: OrcaRouterModel[];
  /** True when live discovery was unavailable and the verified seed is shown. */
  degraded: boolean;
  reason?: string;
  catalog_source: string;
  selector: { capability: string; input_modalities: string[] };
}
export interface OrcaRouterView {
  id: string;
  name: string;
  base_url: string;
  credential_source: "" | "api_key" | "pkce";
  generation: number;
  status: "ok" | "needs_reauth";
  account: string;
  revision: number;
  has_api_key: boolean;
  inference_base: string;
  authorize_url: string;
  key_console: string;
  models: ModelProfile[];
  login: { authorize_url: string; redirect_uri: string } | null;
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
  { keepalive = false }: { keepalive?: boolean } = {},
): Promise<T> {
  const response = await fetch(`/app/${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    keepalive,
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
