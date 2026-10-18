import type { Subagents } from "./subagents.js";
import type { MCPProfile } from "./mcp.js";
import { parseEnvironmentMCP, type EnvironmentMCPServer } from "./mcp_environment.js";
import { parseSkills, workspaceSkills, type WorkspaceSkill } from "./workspace_skills.js";
import type { HookCallback, Options } from "@anthropic-ai/claude-agent-sdk";
import { realpathSync, statSync } from "node:fs";
import { isAbsolute, parse } from "node:path";

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

  constructor(cwd: string, config: Workspace, mcp?: MCPProfile, subagents?: Subagents, disableWebSearch = false) {
    config = parseWorkspace(config, cwd)!;
    // SDK history lookup reads the bridge environment, independently of query.env.
    if (process.env.HOME !== config.home || process.env.CLAUDE_CONFIG_DIR !== config.state ||
        process.env.CLAUDE_CODE_PROJECT_DIR_NAME !== undefined) throw new Error("invalid_request");
    const env: Record<string, string> = {
      ...Object.fromEntries(Object.entries(process.env).filter((entry): entry is [string,string] => entry[1] !== undefined)),
      // Private state paths, a pinned read-only binary and Turns that leave no detached work.
      HOME: config.home, TMPDIR: config.scratch, CLAUDE_CONFIG_DIR: config.state,
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
    this.options = {
      env, tools: { type: "preset", preset: "claude_code" },
      // Nobody answers a question, no Turn leaves scheduled work behind, and Core declares subagents and search.
      disallowedTools: ["AskUserQuestion", "CronCreate", "CronDelete", "CronList", "ScheduleWakeup",
        ...(subagents ? [] : ["Agent", "SendMessage"]), ...(disableWebSearch ? ["WebSearch", "WebFetch"] : [])],
      ...(skills ? { plugins: skills.paths.map(path => ({ type: "local" as const, path, skipMcpDiscovery: true })) } : {}),
      // The native CLI refuses permission bypass for root accounts, so the callback approves every tool.
      permissionMode: "default", canUseTool: async (_name, input) => ({ behavior: "allow", updatedInput: input }),
    };
  }

  // Public Skill names resolve to their namespaced native plugin identities.
  readonly beforeTool: HookCallback = async input => {
    if (input.hook_event_name !== "PreToolUse" || input.tool_name !== "Skill") return {};
    const skill = this.skillName((input.tool_input as { skill?: unknown } | null)?.skill);
    return skill === undefined ? {} : { hookSpecificOutput: { hookEventName: "PreToolUse",
      updatedInput: { ...(input.tool_input as object), skill } } };
  };

  private skillName(value: unknown): string | undefined {
    if (typeof value !== "string") return undefined;
    // Public names are unique in the installed snapshot; native plugins add a namespace.
    return this.skillNames.find(name => name === value || name.endsWith(":" + value));
  }
}
