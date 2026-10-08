import { isAbsolute, normalize } from "node:path";
import { parseHTTPServers, type HTTPServer } from "./mcp.js";

export type StdioServer = {
  server_label: string;
  command: string;
  allowed_tools: null;
};
export type EnvironmentMCPServer = HTTPServer | StdioServer;

// A stdio server's command is its agent-host view alias, which runs without
// arguments; the process broker resolves the installed command behind it.
export function parseEnvironmentMCP(value: unknown): EnvironmentMCPServer[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("invalid_request");
  const labels = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" || labels.has(server.server_label)) throw new Error("invalid_request");
    if ("command" in server) {
        if (Object.keys(server).some(key => !["server_label", "command", "allowed_tools"].includes(key)) ||
            typeof server.server_label !== "string" || !server.server_label || server.allowed_tools !== null ||
            typeof server.command !== "string" || !isAbsolute(server.command) || normalize(server.command) !== server.command || /[\x00-\x1f\x7f]/.test(server.command)) throw new Error("invalid_request");
        labels.add(server.server_label);

    } else {
      parseHTTPServers([server]);
    }
    labels.add(server.server_label);
  }
  return value;
}
