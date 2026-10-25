import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { isAbsolute } from "node:path";
import { parseHTTPServers, type HTTPServer } from "./mcp.js";
import { parseWorkspace, type Workspace } from "./workspace.js";

import { parseNativeModelOptions, type NativeModelOptions } from "./native_model_options.js";

export type ExecutorPrepare = {
  native_model_options?: NativeModelOptions;
  type: "executor_prepare";
  output_format?: { type: "json_schema"; schema: Record<string, unknown> };
  model: string;
  system_prompt: string;
  cwd: string;
  resume?: string;
  require_history?: boolean;
  subagents?: { max_concurrent: number };
  tool_search?: boolean;
  reasoning_summary?: boolean;
  web_search?: "disabled" | "live";
  functions?: { name: string; description: string; parameters: Tool["inputSchema"]; defer_loading?: boolean }[];
  mcp_http_servers?: HTTPServer[];
  workspace?: Workspace;
};

export function parseRequest(line: string): ExecutorPrepare {
  const value: unknown = JSON.parse(line);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_request");
  const request = value as Record<string, unknown>;
  const allowed = new Set(["native_model_options", "type", "model", "system_prompt", "cwd", "resume", "require_history", "output_format", "subagents", "functions", "tool_search", "reasoning_summary", "web_search", "mcp_http_servers", "workspace"]);
  if (Object.keys(request).some(key => !allowed.has(key)) ||
      request.type !== "executor_prepare" ||
      typeof request.model !== "string" || !request.model.trim() ||
      typeof request.system_prompt !== "string" ||
      typeof request.cwd !== "string" || !isAbsolute(request.cwd) ||
      (request.web_search !== undefined && request.web_search !== "disabled" && request.web_search !== "live") ||
      (request.require_history !== undefined && typeof request.require_history !== "boolean") ||
      (request.reasoning_summary !== undefined && typeof request.reasoning_summary !== "boolean") ||
      (request.resume !== undefined && (typeof request.resume !== "string" || !request.resume))) throw new Error("invalid_request");
  if (request.functions !== undefined && (!Array.isArray(request.functions) || request.functions.some(tool =>
      !tool || typeof tool.name !== "string" || !tool.name || typeof tool.description !== "string" ||
      (tool.defer_loading !== undefined && typeof tool.defer_loading !== "boolean") ||
      !tool.parameters || tool.parameters.type !== "object"))) throw new Error("invalid_request");
  const deferred = (request.functions as ExecutorPrepare["functions"])?.some(tool => tool.defer_loading) ?? false;
  if ((request.tool_search !== undefined && typeof request.tool_search !== "boolean") ||
      (!!request.tool_search !== deferred)) throw new Error("invalid_request");
  if (request.subagents !== undefined) {
    const value = request.subagents as Record<string, unknown>;
    if (!value || typeof value !== "object" || Object.keys(value).length !== 1 || !Number.isSafeInteger(value.max_concurrent) || (value.max_concurrent as number) < 1) throw new Error("invalid_request");
  }
  if (request.output_format !== undefined) {
    const format = request.output_format as ExecutorPrepare["output_format"];
    if (!format || format.type !== "json_schema" || Object.keys(format).some(key => !["type", "schema"].includes(key)) ||
        !format.schema || format.schema.type !== "object") throw new Error("invalid_request");
  }
  parseNativeModelOptions(request.native_model_options);
  parseHTTPServers(request.mcp_http_servers);
  const workspace = parseWorkspace(request.workspace, request.cwd);
  if (request.require_history && !workspace) throw new Error("invalid_request");
  if (workspace && "mcp_http_servers" in request) throw new Error("invalid_request");
  return request as ExecutorPrepare;
}
