import type { StdioServer } from "./mcp_environment.js";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { HookCallback, McpServerConfig, McpServerStatus } from "@anthropic-ai/claude-agent-sdk";

export type HTTPServer = {
  server_label: string;
  server_url: string;
  allowed_tools: string[] | null;
  required?: boolean;
  bearer_token_env_var?: string;
  headers?: Record<string, string>;
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
  const references = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" ||
        Object.keys(server).some(key => !["server_label", "server_url", "allowed_tools", "bearer_token_env_var", "required", "headers"].includes(key)) ||
        (server.required !== undefined && typeof server.required !== "boolean") ||
        (server.headers !== undefined && (!server.headers || typeof server.headers !== "object" ||
          Object.values(server.headers).some(value => typeof value !== "string"))) ||
        typeof server.server_label !== "string" || !/^[a-zA-Z0-9_-]+$/.test(server.server_label) ||
        server.server_label === "functions" || labels.has(server.server_label) ||
        typeof server.server_url !== "string" ||
        (server.allowed_tools !== null && (!Array.isArray(server.allowed_tools) ||
          server.allowed_tools.some((name: unknown) => typeof name !== "string")))) {
      throw new Error("invalid_request");
    }
    const url = new URL(server.server_url);
    if (!["http:", "https:"].includes(url.protocol) || !url.hostname || url.username || url.password ||
        server.server_url.includes("#")) throw new Error("invalid_request");
    if (server.bearer_token_env_var !== undefined) {
      const reference = server.bearer_token_env_var;
      if (url.protocol !== "https:" || typeof reference !== "string" ||
          !/^OAC_RUNTIME_MCP_BEARER_[A-Z2-7]{26,}$/.test(reference) || references.has(reference)) throw new Error("invalid_request");
      references.add(reference);
    }
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
  private admitted = false;
  private release!: (ready: boolean) => void;
  private readonly ready = new Promise<boolean>(resolve => { this.release = resolve; });

  // Declared servers expose only their selected tools. Other native tools are not Core's to restrict.
  private unselected(name: string): boolean {
    return !this.identities.has(name) && this.declarations.some(server => name.startsWith(`mcp__${server.server_label}__`));
  }

  readonly beforeTool: HookCallback = async (input, id, { signal }) => {
    let stopped!: () => void;
    const interrupted = new Promise<boolean>(resolve => {
      stopped = () => resolve(false);
      signal.addEventListener("abort", stopped, { once: true });
    });
    try {
      if (!signal.aborted && await Promise.race([this.ready, interrupted]) && !signal.aborted && this.admitted &&
          input.hook_event_name === "PreToolUse" && this.permitsActor(input.agent_id) && input.session_id === this.sessionID &&
          (id === undefined || id === input.tool_use_id) && !this.unselected(input.tool_name)) return {};
      return { hookSpecificOutput: { hookEventName: "PreToolUse", permissionDecision: "deny",
        permissionDecisionReason: "Tool is outside the verified execution profile." } };
    } finally {
      signal.removeEventListener("abort", stopped);
    }
  };

  // permitsActor tells whether an agent may call tools: the root always, a Subagent once native has started it.
  constructor(private readonly declarations: (HTTPServer | StdioServer)[], private readonly functions: string[], private readonly permitsActor: (id?: string) => boolean = id => id === undefined) {
    this.allowed = [...functions];
    for (const server of declarations) {
      const prefix = `mcp__${server.server_label}__`;
      if ("command" in server) {
        this.servers[server.server_label] = { type: "stdio", command: server.command, args: [...server.args], env: {}, alwaysLoad: true };
      } else {
        const reference = server.bearer_token_env_var;
        if (reference && !process.env[reference]) throw new Error("missing MCP credential environment");
        // An explicit Authorization, empty unless declared, suppresses native OAuth and automatic auth.
        // Keep bearer references literal: SDK server configuration enters native argv.
        const declared = Object.keys(server.headers ?? {}).some(name => name.toLowerCase() === "authorization");
        this.servers[server.server_label] = { type: "http", url: server.server_url, alwaysLoad: true,
          headers: { ...server.headers, ...(!declared && { Authorization: reference ? `Bearer \${${reference}}` : "" }) } };
      }
      if (server.allowed_tools === null) this.allowed.push(prefix + "*");
      else if (!server.allowed_tools.length) this.denied.push(prefix + "*");
      else this.allowed.push(...server.allowed_tools.map(name => nativeToolName(server.server_label, name)));
    }
  }

  // Lists each subset server with the credentials of its native configuration and returns the native names of the tools it did not select.
  // A server that cannot be listed fails the query, as a native connection failure does at initialization.
  async unselectedTools(signal: AbortSignal): Promise<string[]> {
    const hidden: string[] = [];
    for (const server of this.declarations) {
      if ("command" in server || !server.allowed_tools?.length) continue;
      const selected = new Set(server.allowed_tools.map(name => nativeToolName(server.server_label, name)));
      const reference = server.bearer_token_env_var;
      const client = new Client({ name: "oac-claude-sdk-adapter", version: "0.0.0" });
      try {
        await client.connect(new StreamableHTTPClientTransport(new URL(server.server_url),
          { requestInit: { headers: { ...server.headers, ...(reference && { Authorization: `Bearer ${process.env[reference]}` }) } } }), { signal });
        let cursor: string | undefined;
        const cursors = new Set<string>();
        do {
          const page = await client.listTools({ cursor }, { signal });
          for (const tool of page.tools) {
            const native = nativeToolName(server.server_label, tool.name);
            if (!selected.has(native)) hidden.push(native);
          }
          cursor = page.nextCursor;
          // A repeated cursor would page forever.
          if (cursor !== undefined && cursors.has(cursor)) throw new Error("MCP tool listing repeated a cursor");
          if (cursor !== undefined) cursors.add(cursor);
        } while (cursor);
      } finally {
        await client.close();
      }
    }
    return hidden;
  }

  // With exact inventory the text profile accepts nothing but the declared servers and tools.
  verify(inventory: string[], statuses: McpServerStatus[], sessionID: string, exact = true, extra: string[] = []): void {
    // Native status.config can contain expanded headers. Retain only identities;
    // never publish or persist the private SDK control response.
    this.admitted = false;
    const expected = new Map<string, ToolIdentity>();
    const declared = new Map(this.declarations.map(server => [server.server_label, server]));
    const seen = new Set<string>();
    let matched = 0;
    const nativeNames = new Set(this.functions);
    for (const status of statuses) {
      if (seen.has(status.name)) throw new Error("duplicate native MCP server");
      seen.add(status.name);
      if (status.name === "functions" && this.functions.length) {
        if (status.status !== "connected") throw new Error("native function server unavailable");
        matched++;
        continue;
      }
      const server = declared.get(status.name);
      if (!server) {
        if (exact) throw new Error("native MCP server unavailable or undeclared");
        continue;
      }
      matched++;
      // Native runs on without an optional server that did not connect.
      if (status.status !== "connected") {
        if ("required" in server && server.required) throw new Error("required native MCP server unavailable");
        continue;
      }
      for (const tool of status.tools ?? []) {
        const native = nativeToolName(server.server_label, tool.name);
        if (nativeNames.has(native)) throw new Error("ambiguous native MCP identity");
        nativeNames.add(native);
        if (server.allowed_tools !== null && !server.allowed_tools.includes(tool.name)) continue;
        expected.set(native, { server: server.server_label, name: tool.name });
      }
    }
    if (matched !== declared.size + (this.functions.length ? 1 : 0) ||
        !exact && [...expected.keys()].some(name => !inventory.includes(name)) ||
        exact && (inventory.length !== expected.size + this.functions.length + extra.length || new Set(inventory).size !== inventory.length ||
          inventory.some(name => !expected.has(name) && !this.functions.includes(name) && !extra.includes(name)))) {
      throw new Error("unexpected native MCP inventory");
    }
    this.identities.clear();
    for (const [name, identity] of expected) this.identities.set(name, identity);
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

  credentialReferences(): string[] {
    return this.declarations.flatMap(server => "bearer_token_env_var" in server && server.bearer_token_env_var ? [server.bearer_token_env_var] : []);
  }

  close(): void { this.admitted = false; this.release(false); }
}
