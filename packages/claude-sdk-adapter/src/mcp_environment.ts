import { isAbsolute, normalize, parse } from "node:path";
import { parseHTTPServers, type HTTPServer } from "./mcp.js";

export type StdioServer = {
  server_label: string;
  command: string;
  args: string[];
  allowed_tools: null;
};
export type EnvironmentMCPServer = HTTPServer | StdioServer;

// The Runtime launcher resolves installed package identities and their commands.
export function parseEnvironmentMCP(value: unknown): EnvironmentMCPServer[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error("invalid_request");
  const labels = new Set<string>();
  for (const server of value) {
    if (!server || typeof server !== "object" || labels.has(server.server_label)) throw new Error("invalid_request");
    if ("command" in server) {
        if (Object.keys(server).some(key => !["server_label", "command", "args", "allowed_tools"].includes(key)) ||
            typeof server.server_label !== "string" || !/^[a-zA-Z0-9_-]+$/.test(server.server_label) || server.server_label === "functions" || server.allowed_tools !== null ||
            typeof server.command !== "string" || !isAbsolute(server.command) || normalize(server.command) !== server.command ||
            !Array.isArray(server.args) || server.args.length !== 4 || server.args[0] !== "runtime-mcp-exec" ||
            typeof server.args[1] !== "string" || !isAbsolute(server.args[1]) || normalize(server.args[1]) !== server.args[1] || server.args[1] === parse(server.args[1]).root ||
            typeof server.args[2] !== "string" || !server.args[2] || /[\\]/.test(server.args[2]) || server.args[2].split("/").some((part: string) => !part || part === "." || part === "..") ||
            server.args[3] !== server.server_label || [server.command, ...server.args].some((part: string) => /[\x00-\x1f\x7f]/.test(part))) throw new Error("invalid_request");
        labels.add(server.server_label);

    } else {
      parseHTTPServers([server]);
    }
    labels.add(server.server_label);
  }
  return value;
}
