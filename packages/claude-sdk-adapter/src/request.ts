import { parseMessageInput, type MessageInput } from "./message_input.js";
import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { isAbsolute } from "node:path";
import { parseHTTPServers, type HTTPServer } from "./mcp.js";
import { parseWorkspace, type Workspace } from "./workspace.js";

export type Start = {
  type: "start";
  output_format?: { type: "json_schema"; schema: Record<string, unknown> };
  input: MessageInput;
  model: string;
  system_prompt: string;
  cwd: string;
  resume?: string;
  require_history?: boolean;
  observe_messages?: boolean;
  subagents?: { max_concurrent: number };
  tool_search?: boolean;
  functions?: { name: string; description: string; parameters: Tool["inputSchema"]; defer_loading?: boolean }[];
  mcp_http_servers?: HTTPServer[];
  workspace?: Workspace;
};
export type Prepare = Omit<Start, "type" | "input" | "workspace"> & { type: "prepare"; workspace: Workspace };

// MCP startup confirms its hooks before the native input iterator yields.
export function immediateInput(request: Start | Prepare): MessageInput | undefined {
  return request.type === "start" && request.mcp_http_servers === undefined && !request.workspace?.mcp?.length ? request.input : undefined;
}

export function parseRequest(line: string): Start | Prepare {
  const value: unknown = JSON.parse(line);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  const allowed = new Set(["type", "input", "model", "system_prompt", "cwd", "resume", "require_history", "observe_messages", "output_format", "subagents", "functions", "tool_search", "mcp_http_servers", "workspace"]);
  if (Object.keys(request).some(key => !allowed.has(key)) ||
      (request.type !== "start" && request.type !== "prepare") ||
      (request.type === "start" ? !Array.isArray(request.input) : "input" in request) ||
      typeof request.model !== "string" || !request.model.trim() ||
      typeof request.system_prompt !== "string" ||
      typeof request.cwd !== "string" || !isAbsolute(request.cwd) ||
      (request.observe_messages !== undefined && typeof request.observe_messages !== "boolean") ||
      (request.require_history !== undefined && typeof request.require_history !== "boolean") ||
      (request.resume !== undefined && (typeof request.resume !== "string" || !request.resume))) throw new Error("invalid_request");
  if (request.functions !== undefined && (!Array.isArray(request.functions) || request.functions.some(tool =>
      !tool || typeof tool.name !== "string" || !tool.name || typeof tool.description !== "string" ||
      (tool.defer_loading !== undefined && typeof tool.defer_loading !== "boolean") ||
      !tool.parameters || tool.parameters.type !== "object"))) throw new Error("invalid_request");
  const deferred = (request.functions as Start["functions"])?.some(tool => tool.defer_loading) ?? false;
  if ((request.tool_search !== undefined && typeof request.tool_search !== "boolean") ||
      (!!request.tool_search !== deferred) ||
      (request.tool_search && (request.subagents || request.workspace || request.mcp_http_servers !== undefined || request.output_format))) throw new Error("invalid_request");
  if (request.subagents !== undefined) {
    const value = request.subagents as Record<string, unknown>;
    if (!value || typeof value !== "object" || Object.keys(value).length !== 1 || !Number.isSafeInteger(value.max_concurrent) || (value.max_concurrent as number) < 1 ||
        (request.functions as unknown[] | undefined)?.length || request.mcp_http_servers !== undefined) throw new Error("invalid_request");
  }
  if (request.output_format !== undefined) {
    const format = request.output_format as Start["output_format"];
    if (!format || format.type !== "json_schema" || Object.keys(format).some(key => !["type", "schema"].includes(key)) ||
        !format.schema || format.schema.type !== "object" || !request.observe_messages || request.subagents ||
        request.workspace || request.mcp_http_servers !== undefined) throw new Error("invalid_request");
  }
  if (request.type === "start") requestInput(request.input);
  parseHTTPServers(request.mcp_http_servers);
  const workspace = parseWorkspace(request.workspace, request.cwd);
  if (request.subagents && workspace?.mcp?.length) throw new Error("invalid_request");
  if (request.require_history && !workspace) throw new Error("invalid_request");
  if ((workspace && "mcp_http_servers" in request) ||
      (request.type === "prepare" && !workspace)) throw new Error("invalid_request");
  return request as Start | Prepare;
}

export function parseStart(line: string): Start {
  const request = parseRequest(line);
  if (request.type !== "start") throw new Error("invalid_request");
  return request;
}

export function preparedInput(value: unknown): MessageInput {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  if (request.type !== "start" || Object.keys(request).some(key => key !== "type" && key !== "input") ||
      !Array.isArray(request.input)) throw new Error("invalid_request");
  return requestInput(request.input);
}

function requestInput(value: unknown): MessageInput {
  try { return parseMessageInput(value); } catch { throw new Error("invalid_request"); }
}
