import type { StdioServer } from "./mcp_environment.js";
import type { HookCallback, McpServerConfig, McpServerStatus } from "@anthropic-ai/claude-agent-sdk";

export type HTTPServer = {
  server_label: string;
  server_url: string;
  allowed_tools: string[] | null;
  required?: boolean;
};
export type ToolIdentity = { server: string; name: string };

function nativeToolName(server: string, tool: string): string {
  let name = tool.replace(/[^a-zA-Z0-9_-]/g, "_");
  if (tool.startsWith("claude.ai ")) name = name.replace(/_+/g, "_").replace(/^_|_$/g, "");
  return `mcp__${server}__${name}`;
}

export function parseHTTPServers(value: unknown): HTTPServer[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("invalid_request");
  const labels = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" ||
        Object.keys(server).some(key => !["server_label", "server_url", "allowed_tools", "required"].includes(key)) ||
        (server.required !== undefined && typeof server.required !== "boolean") ||
        typeof server.server_label !== "string" || !server.server_label || labels.has(server.server_label) ||
        typeof server.server_url !== "string" ||
        (server.allowed_tools !== null && (!Array.isArray(server.allowed_tools) ||
          server.allowed_tools.some((name: unknown) => typeof name !== "string")))) {
      throw new Error("invalid_request");
    }
    const url = new URL(server.server_url);
    if (!["http:", "https:"].includes(url.protocol) || !url.hostname || url.username || url.password ||
        server.server_url.includes("?") || server.server_url.includes("#")) throw new Error("invalid_request");
    labels.add(server.server_label);
  }
  return value;
}

export class MCPProfile {
  readonly servers: Record<string, McpServerConfig> = Object.create(null);
  readonly allowed: string[];
  readonly denied: string[] = [];
  readonly identities = new Map<string, ToolIdentity>();
  private sessionID = "";
  private localTools = new Set<string>();
  private admitted = false;
  private release!: (ready: boolean) => void;
  private readonly ready = new Promise<boolean>(resolve => { this.release = resolve; });

  readonly beforeTool: HookCallback = async (input, id, { signal }) => {
    let stopped!: () => void;
    const interrupted = new Promise<boolean>(resolve => {
      stopped = () => resolve(false);
      signal.addEventListener("abort", stopped, { once: true });
    });
    try {
      if (!signal.aborted && await Promise.race([this.ready, interrupted]) && !signal.aborted && this.admitted &&
          input.hook_event_name === "PreToolUse" && input.agent_id === undefined && input.session_id === this.sessionID &&
          (id === undefined || id === input.tool_use_id) &&
          (this.identities.has(input.tool_name) || this.functions.includes(input.tool_name) || this.localTools.has(input.tool_name))) {
        if (this.identities.has(input.tool_name)) this.admitCall(input.tool_use_id, input.tool_name);
        return {};
      }
      return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny",
        permissionDecisionReason: "Tool is outside the verified execution profile." } };
    } finally {
      signal.removeEventListener("abort", stopped);
    }
  };

  constructor(private readonly declarations: (HTTPServer | StdioServer)[], private readonly functions: string[], private readonly admitCall: (id: string, name: string) => void = () => {}) {
    this.allowed = [...functions];
    for (const server of declarations) {
      const prefix = `mcp__${server.server_label}__`;
      if ("command" in server) {
        this.servers[server.server_label] = { type: "stdio", command: server.command, args: [], env: {}, alwaysLoad: true };
      } else {
        // An explicit empty Authorization suppresses native OAuth and automatic
        // auth; the Session's gateway adds any credential.
        this.servers[server.server_label] = { type: "http", url: server.server_url, alwaysLoad: true,
          headers: { Authorization: "" } };
      }
      if (server.allowed_tools === null) this.allowed.push(prefix + "*");
      else if (!server.allowed_tools.length) this.denied.push(prefix + "*");
      else this.allowed.push(...server.allowed_tools.map(name => nativeToolName(server.server_label, name)));
    }
  }

  verify(inventory: string[], statuses: McpServerStatus[], sessionID: string, localTools: readonly string[] = []): void {
    // Native status.config can contain expanded headers. Retain only identities;
    // never publish or persist the private SDK control response.
    this.admitted = false;
    const expected = new Map<string, ToolIdentity>();
    const declared = new Map(this.declarations.map(server => [server.server_label, server]));
    const seen = new Set<string>();
    const baseline = [...this.functions, ...localTools];
    const nativeNames = new Set(baseline);
    for (const status of statuses) {
      if (seen.has(status.name)) throw new Error("duplicate native MCP server");
      seen.add(status.name);
      if (status.name === "functions" && this.functions.length) {
        if (status.status !== "connected") throw new Error("native function server unavailable");
        continue;
      }
      const server = declared.get(status.name);
      if (!server || status.status !== "connected") throw new Error("native MCP server unavailable or undeclared");
      for (const tool of status.tools ?? []) {
        const native = nativeToolName(server.server_label, tool.name);
        if (nativeNames.has(native)) throw new Error("ambiguous native MCP identity");
        nativeNames.add(native);
        if (server.allowed_tools !== null && !server.allowed_tools.includes(tool.name)) continue;
        expected.set(native, { server: server.server_label, name: tool.name });
      }
    }
    if (seen.size !== declared.size + (this.functions.length ? 1 : 0) ||
        inventory.length !== expected.size + baseline.length || new Set(inventory).size !== inventory.length ||
        inventory.some(name => !expected.has(name) && !baseline.includes(name))) {
      throw new Error("unexpected native MCP inventory");
    }
    this.identities.clear();
    for (const [name, identity] of expected) this.identities.set(name, identity);
    this.localTools = new Set(localTools);
    this.sessionID = sessionID;
    this.admitted = true;
    this.release(true);
  }

  verifyRequired(statuses: McpServerStatus[]): void {
    for (const server of this.declarations.filter(server => "required" in server && server.required)) {
      const matches = statuses.filter(status => status.name === server.server_label);
      if (matches.length !== 1 || matches[0].status !== "connected") {
        throw new Error("required native MCP server unavailable");
      }
    }
  }

  verifyReconnected(statuses: McpServerStatus[]): void {
    const identities = new Map(this.identities);
    const inventory = [...this.functions, ...this.localTools, ...this.identities.keys()];
    this.verify(inventory, statuses, this.sessionID, [...this.localTools]);
    for (const [name, identity] of identities) {
      const current = this.identities.get(name);
      if (current?.server !== identity.server || current.name !== identity.name) throw new Error("changed native MCP identity");
    }
  }

  permits(name: string): boolean { return this.admitted && this.identities.has(name); }

  close(): void { this.admitted = false; this.release(false); }
}
