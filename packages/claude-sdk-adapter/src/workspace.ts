import type { Subagents } from "./subagents.js";
import { parseEnvironmentMCP, type EnvironmentMCPServer } from "./mcp_environment.js";
import type { MCPProfile } from "./mcp.js";
import { parseSkills, workspaceSkills, type WorkspaceSkill } from "./workspace_skills.js";
import type { CanUseTool, HookCallback, Options } from "@anthropic-ai/claude-agent-sdk";
import { realpathSync, statSync } from "node:fs";
import { isAbsolute, parse, resolve } from "node:path";

export type Workspace = {
  tool_env?: Record<string, string>;
  home: string;
  state: string;
  scratch: string;
  env_names: string[];
  skills?: WorkspaceSkill[];
  capability_root?: string;
  mcp?: EnvironmentMCPServer[];
  network_access?: "enabled" | "disabled" | "restricted";
  allowed_domains?: string[];
};

const environmentNames = new Set([
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
  "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
  "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
]);
const nativeTools = ["Bash", "Read", "Edit"];
const denial = "Tool is outside the workspace execution profile.";
const invalidPath = /[\x00-\x1f\x7f]/;


function directory(value: unknown, canonical: boolean): string {
  if (typeof value !== "string" || !isAbsolute(value) || value === parse(value).root || invalidPath.test(value)) {
    throw new Error("invalid_request");
  }
  try {
    const actual = realpathSync(value);
    if (!statSync(actual).isDirectory() || (canonical && actual !== value)) throw new Error();
    return actual;
  } catch { throw new Error("invalid_request"); }
}

export function parseWorkspace(value: unknown, cwd: string): Workspace | undefined {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const config = value as Record<string, unknown>;
  if (Object.keys(config).some(key => !["tool_env", "home", "state", "scratch", "env_names", "network_access", "allowed_domains", "skills", "mcp", "capability_root"].includes(key)) ||
      (config.network_access !== undefined && config.network_access !== "enabled" && config.network_access !== "disabled" && config.network_access !== "restricted") ||
      !Array.isArray(config.env_names) ||
      config.env_names.some(name => typeof name !== "string" || !environmentNames.has(name)) ||
      new Set(config.env_names).size !== config.env_names.length) throw new Error("invalid_request");
  if (config.network_access !== undefined && config.network_access !== "enabled") throw new Error("invalid_request");
  if (config.tool_env !== undefined && (!config.tool_env || typeof config.tool_env !== "object" || Array.isArray(config.tool_env) || Object.values(config.tool_env).some(value => typeof value !== "string"))) throw new Error("invalid_request");
  const mcp = parseEnvironmentMCP(config.mcp);
  if (config.capability_root !== undefined) directory(config.capability_root, false);
  if ((Array.isArray(config.skills) && config.skills.length || mcp?.length) && !config.capability_root) throw new Error("invalid_request");
  if (mcp?.length && config.network_access !== "enabled") throw new Error("invalid_request");
  const domains = config.allowed_domains ?? [];
  if (!Array.isArray(domains) || domains.length !== 0) throw new Error("invalid_request");
  for(const root of [cwd,config.home,config.state,config.scratch]) directory(root,false);
  return { ...config, ...(config.skills === undefined ? {} : { skills: parseSkills(config.skills) }) } as Workspace;

}

export class WorkspaceProfile {
  readonly options: Options;
  private readonly skillNames: readonly string[];

  constructor(private readonly cwd: string, private readonly config: Workspace, private readonly functions: readonly string[] = [], private readonly mcp?: MCPProfile, private readonly subagents?: Subagents, private readonly structuredOutput = false) {
    config = parseWorkspace(config, cwd)!;
    // SDK history lookup reads the bridge environment, independently of query.env.
    if (process.env.HOME !== config.home || process.env.CLAUDE_CONFIG_DIR !== config.state ||
        process.env.CLAUDE_CODE_PROJECT_DIR_NAME !== undefined) throw new Error("invalid_request");
    const env: Record<string, string> = {
      ...Object.fromEntries(Object.entries(process.env).filter((entry): entry is [string,string] => entry[1] !== undefined)),
      HOME: config.home, TMPDIR: config.scratch, CLAUDE_CONFIG_DIR: config.state,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1", DISABLE_TELEMETRY: "1", DISABLE_ERROR_REPORTING: "1",
      DISABLE_AUTOUPDATER: "1", CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1",
    };
    for (const [name, value] of Object.entries(config.tool_env ?? {})) {
      // Initialization cannot redirect the native Session history lookup.
      if (!["HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_PROJECT_DIR_NAME"].includes(name.toUpperCase())) env[name] = value;
    }
    for (const name of config.env_names) {
      const value = process.env[name];
      if (value === undefined) throw new Error("invalid_request");
      env[name] = value;
    }
    for (const reference of mcp?.credentialReferences() ?? []) {
      if (!process.env[reference]) throw new Error("invalid_request");
      env[reference] = process.env[reference]!;
    }
    const skills = workspaceSkills(config.skills ?? [], config.capability_root ?? "");
    this.skillNames = skills?.names ?? [];
    const skillTools = skills ? ["Skill"] : [];
    this.options = {
      env, tools: [...nativeTools, ...skillTools, ...(subagents ? ["Agent", "SendMessage"] : [])],
      ...(skills ? { plugins: skills.paths.map(path => ({ type: "local" as const, path, skipMcpDiscovery: true })) } : {}), allowedTools: mcp?.allowed ?? [...functions], mcpServers: {}, strictMcpConfig: true,
      // The existing callback authorizes tools without CLI permission bypass,
      // which the native CLI refuses for root accounts.
      settingSources: [], permissionMode: "default", persistSession: true,
      settings: {},
      sandbox: { enabled: false },
      canUseTool: this.canUseTool,
      hooks: { PreToolUse: [{ hooks: [this.beforeTool] }] },
    };
  }

  verify(tools: string[], servers: { name: string; status: string; tools?: { name: string }[] }[], sessionID = ""): void {
    if (this.mcp) {
      this.mcp.verify(tools, servers as Parameters<MCPProfile["verify"]>[1], sessionID, [...nativeTools, ...(this.skillNames.length ? ["Skill"] : []), ...(this.subagents ? ["Task", "SendMessage"] : [])]);
      return;
    }
    const expected = [...nativeTools, ...(this.structuredOutput ? ["StructuredOutput"] : []), ...this.functions, ...(this.skillNames.length ? ["Skill"] : []), ...(this.subagents ? ["Task", "SendMessage"] : [])];
    if (servers.length !== (this.functions.length ? 1 : 0) ||
        servers.some(server => server.name !== "functions" || server.status !== "connected") ||
        tools.length !== expected.length || new Set(tools).size !== tools.length ||
        tools.some(name => !expected.includes(name))) throw new Error("unexpected native workspace inventory");
  }

  readonly canUseTool: CanUseTool = async (name, input, { signal, agentID }) => {
    if (!signal.aborted && (agentID === undefined || this.subagents?.permitsActor(agentID)) && this.permits(name, input)) {
      return { behavior: "allow", updatedInput: this.nativeInput(name, input) };
    }
    return { behavior: "deny", message: denial };
  };

  readonly beforeTool: HookCallback = async (input, id, { signal }) => {
    if (this.mcp) {
      const admission = await this.mcp.beforeTool(input, id, { signal });
      if ("hookSpecificOutput" in admission && admission.hookSpecificOutput?.hookEventName === "PreToolUse" &&
          admission.hookSpecificOutput.permissionDecision === "deny") return admission;
      if (input.hook_event_name === "PreToolUse" && this.mcp.permits(input.tool_name)) return admission;
    }
    if (!signal.aborted && input.hook_event_name === "PreToolUse" && (input.agent_id === undefined || this.subagents?.permitsActor(input.agent_id)) &&
        (id === undefined || id === input.tool_use_id) && this.permits(input.tool_name, input.tool_input)) {
      return !["Read", "Edit", "Skill"].includes(input.tool_name) ? {} : { hookSpecificOutput: { hookEventName: "PreToolUse",
        updatedInput: this.nativeInput(input.tool_name, input.tool_input as Record<string, unknown>) } };
    }
    return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: denial } };
  };

  private nativeInput(name: string, input: Record<string, unknown>): Record<string, unknown> {
    if (name === "Skill") return { ...input, skill: this.skillName(input.skill) };
    return name !== "Read" && name !== "Edit" ? input : { ...input, file_path: resolve(this.cwd, input.file_path as string) };
  }

  private skillName(value: unknown): string | undefined {
    if (typeof value !== "string") return undefined;
    // Public names are unique in the installed snapshot; native plugins add a namespace.
    return this.skillNames.find(name => name === value || name.endsWith(":" + value));
  }

  private permits(name: string, value: unknown): boolean {
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    const input = value as Record<string, unknown>;
    if (this.structuredOutput && name === "StructuredOutput") return true;
    if (this.functions.includes(name) || this.mcp?.permits(name)) return true;
    if (name === "Skill") return this.skillName(input.skill) !== undefined;
    if (name === "Bash") return typeof input.command === "string" && !!input.command.trim() &&
      (input.run_in_background === undefined || input.run_in_background === false);
    if ((name !== "Read" && name !== "Edit") || typeof input.file_path !== "string" || !input.file_path ||
        /[\x00-\x1f]/.test(input.file_path)) return false;
    return true;
  }
}
