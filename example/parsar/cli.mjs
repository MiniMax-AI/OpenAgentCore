#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";

const help = `Parsar CLI (Node 22.13+, no dependencies)

  PARSAR_URL=http://127.0.0.1:18180 node cli.mjs METHOD /path [--file FILE|-]
  node cli.mjs uuid

METHOD: GET, PUT, POST, PATCH, DELETE. Bodies are JSON; --file - reads stdin.
Responses are JSON on stdout; failures are JSON on stderr with exit code 1.
Use /app for product configuration and /v1 for exposed Core resources.

  GET  /app/agents                 List Agent configurations
  GET  /app/runtimes               List environment templates
  GET  /app/models                 List configured models
  GET  /app/mcps                   List MCP connections
  GET  /v1/skills                  List Skills (follow has_more/after)
  PUT  /app/agents/UUID            Create or replace an Agent
  PUT  /app/runtimes/UUID          Create or replace an environment template
  POST /app/runtimes/UUID/trial    Start an environment trial
  GET  /app/runtimes/UUID/trial    Read its result

Generate a UUID once before creating a resource. Save it for recovery.
For edits, GET first, keep all fields and the revision, then PUT the full object.
On an uncertain write, GET the same ID before retrying; writes are not retried.
Uses the same trusted-network access as the Web UI. No Core key is needed.
For an authenticated HTTPS gateway, set PARSAR_TOKEN to its access token.
See examples/manager/GUIDE.md for complete creation examples.
`;

// The UI and this client share the server's validation, revisions and redaction.
export function createParsarClient(baseURL, { token } = {}) {
  const base = new URL(baseURL);
  if (
    !["http:", "https:"].includes(base.protocol) ||
    base.username || base.password || base.pathname !== "/" || base.search || base.hash
  ) throw new Error("PARSAR_URL must be an HTTP(S) origin without credentials or a path.");
  if (token !== undefined && (
    typeof token !== "string" || !/^[A-Za-z0-9._~+/-]+=*$/.test(token) ||
    (base.protocol !== "https:" && !["127.0.0.1", "localhost", "[::1]"].includes(base.hostname))
  )) throw new Error("PARSAR_TOKEN requires a valid bearer token and HTTPS (or loopback HTTP for local testing).");
  return async (method, path, body) => {
    if (!["GET", "PUT", "POST", "PATCH", "DELETE"].includes(method))
      throw new Error("Unsupported method.");
    const url = new URL(path, base);
    if (
      !/^\/(app|v1)\//.test(path) || url.origin !== base.origin ||
      !/^\/(app|v1)\//.test(url.pathname) || url.hash
    ) throw new Error("Use a relative /app/ or /v1/ path on this Parsar server.");
    if (method === "GET" && body !== undefined)
      throw new Error("GET does not accept a JSON body.");
    const response = await fetch(url, {
      method,
      redirect: "error",
      signal: AbortSignal.timeout(60_000),
      headers: { Origin: base.origin, "Content-Type": "application/json", ...(token === undefined ? {} : { Authorization: `Bearer ${token}` }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`Parsar returned a non-JSON response (HTTP ${response.status}).`); }
    if (!response.ok) {
      const error = new Error(value?.error?.message || `Parsar returned HTTP ${response.status}.`);
      error.status = response.status;
      throw error;
    }
    return value;
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const args = process.argv.slice(2);
    if (!args.length || (args.length === 1 && ["--help", "-h"].includes(args[0]))) {
      process.stdout.write(help);
    } else if (args.length === 1 && args[0] === "uuid") {
      console.log(randomUUID());
    } else {
      const [method, path, flag, file] = args;
      if (!(args.length === 2 || (args.length === 4 && flag === "--file")))
        throw new Error("Usage: node cli.mjs METHOD /path [--file FILE|-]");
      if (!process.env.PARSAR_URL) throw new Error("Set PARSAR_URL to the Parsar Web origin.");
      let body;
      if (file !== undefined) {
        const input = readFileSync(file === "-" ? 0 : file, "utf8");
        try { body = JSON.parse(input); }
        catch { throw new Error("The input file must contain valid JSON."); }
      }
      const result = await createParsarClient(process.env.PARSAR_URL, { token: process.env.PARSAR_TOKEN })(method, path, body);
      console.log(JSON.stringify(result, null, 2));
    }
  } catch (error) {
    console.error(JSON.stringify({ error: { message: error.message, ...(error.status ? { status: error.status } : {}) } }));
    process.exitCode = 1;
  }
}
