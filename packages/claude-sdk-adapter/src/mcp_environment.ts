import { parseHTTPServers, type HTTPServer } from "./mcp.js";

export type StdioServer = {
  server_label: string;
  command: string;
  args: string[];
  allowed_tools: null;
};
export type EnvironmentMCPServer = HTTPServer | StdioServer;

// This private projection accepts only the Runtime-owned sandbox entry. Package
// command/env/cwd are resolved by the shared Go helper after entering isolation.
export function parseEnvironmentMCP(value: unknown): EnvironmentMCPServer[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("invalid_request");
  const labels = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" || labels.has(server.server_label)) throw new Error("invalid_request");
    if ("command" in server) {
      if (Object.keys(server).some(key => !["server_label", "command", "args", "allowed_tools"].includes(key)) ||
          typeof server.server_label !== "string" || !/^[a-zA-Z0-9_-]+$/.test(server.server_label) ||
          server.server_label === "functions" || server.allowed_tools !== null || server.command !== "/usr/bin/python3" ||
          !Array.isArray(server.args) || server.args.length !== 6 ||
          server.args[0] !== "-I" || server.args[1] !== "-S" ||
          server.args[2] !== "/usr/local/bin/oac-runtime-initialize" || server.args[3] !== "stdio" ||
          typeof server.args[4] !== "string" || !server.args[4] || server.args[4].startsWith("/") ||
          server.args[4].split("/").some((part: string) => !part || part === "." || part === "..") ||
          /[\x00-\x1f\x7f\\]/.test(server.args[4]) || server.args[5] !== server.server_label) throw new Error("invalid_request");
    } else {
      parseHTTPServers([server]);
      if (server.allowed_tools !== null || server.required !== undefined) throw new Error("invalid_request");
    }
    labels.add(server.server_label);
  }
  return value;
}
