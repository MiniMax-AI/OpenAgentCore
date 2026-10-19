import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { parseStart } from "../dist/adapter.js";
import { MCPProfile } from "../dist/mcp.js";

const reference = "OAC_RUNTIME_MCP_BEARER_" + "A".repeat(26);
const server = { server_label: "private", server_url: "https://example.invalid/mcp", allowed_tools: ["echo.v1"], bearer_token_env_var: reference };
const start = servers => ({ type: "start", input: [{ content: [{ type: "input_text", text: "hello" }] }], model: "fixture", system_prompt: "", cwd: "/tmp", mcp_http_servers: servers });

test("bearer references remain literal in native configuration and private status is not retained", t => {
  const token = "fixture-private-bearer+/==";
  process.env[reference] = token;
  t.after(() => { delete process.env[reference]; });
  const declarations = [server, { server_label: "anonymous", server_url: "http://example.invalid/mcp", allowed_tools: [] }];
  const parsed = parseStart(JSON.stringify(start(declarations)));
  const profile = new MCPProfile(parsed.mcp_http_servers, []);
  assert.equal(profile.servers.private.headers.Authorization, "Bearer ${" + reference + "}");
  assert.equal(profile.servers.anonymous.headers.Authorization, "");
  assert.equal(JSON.stringify(profile.servers).includes(token), false);
  profile.verify(["mcp__private__echo_v1"], [
    { name: "private", status: "connected", tools: [{ name: "echo.v1" }], config: { ...profile.servers.private, headers: { Authorization: "Bearer " + token } } },
    { name: "anonymous", status: "connected", tools: [] },
  ], "session");
  assert.deepEqual([...profile.identities.values()], [{ server: "private", name: "echo.v1" }]);
  assert.equal(JSON.stringify(profile).includes(token), false);
  assert.deepEqual(parsed.mcp_http_servers, declarations);
});

test("untrusted header expressions, raw tokens and non-HTTPS authenticated declarations are rejected", () => {
  for (const invalid of [
    { ...server, bearer_token: "fixture-secret" },
    { ...server, headers: { "X-Tenant": 1 } },
    { ...server, server_url: "http://example.invalid/mcp" },
    ...[null, "", "ANTHROPIC_AUTH_TOKEN", "OAC_RUNTIME_MCP_BEARER_", "${OPERATOR_TOKEN}", reference + ":-fallback"].map(bearer_token_env_var => ({ ...server, bearer_token_env_var })),
  ]) assert.throws(() => parseStart(JSON.stringify(start([invalid]))));
  assert.throws(() => parseStart(JSON.stringify(start([server, { ...server, server_label: "other" }]))));
});

test("custom headers reach the native configuration beside the bearer", t => {
  process.env[reference] = "fixture-private-bearer";
  t.after(() => { delete process.env[reference]; });
  const profile = new MCPProfile([{ ...server, headers: { "X-Tenant": "a" } }], []);
  assert.deepEqual(profile.servers.private.headers, { "X-Tenant": "a", Authorization: "Bearer ${" + reference + "}" });
  const literal = new MCPProfile([{ ...server, bearer_token_env_var: undefined, headers: { authorization: "Bearer literal" } }], []);
  assert.deepEqual(literal.servers.private.headers, { authorization: "Bearer literal" });
});

test("missing credential environment fails before the native query", () => {
  const missing = "OAC_RUNTIME_MCP_BEARER_" + "B".repeat(26);
  delete process.env[missing];
  assert.throws(() => new MCPProfile([{ ...server, bearer_token_env_var: missing }], []), /missing MCP credential environment/);
});

test("subset listing sends each server only its own bearer", async t => {
  const seen = { a: new Set(), b: new Set() };
  const listeners = await Promise.all(["a", "b"].map(async label => {
    const http = createServer(async (req, res) => {
      seen[label].add(req.headers.authorization);
      const mcp = new McpServer({ name: label, version: "0" });
      for (const name of ["echo", "extra"]) mcp.registerTool(name, {}, async () => ({ content: [] }));
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
      await mcp.connect(transport);
      let body = "";
      for await (const chunk of req) body += chunk;
      await transport.handleRequest(req, res, body ? JSON.parse(body) : undefined);
    });
    await new Promise(resolve => http.listen(0, "127.0.0.1", resolve));
    return http;
  }));
  const references = ["A", "B"].map(letter => "OAC_RUNTIME_MCP_BEARER_" + letter.repeat(26));
  references.forEach((name, i) => { process.env[name] = "token-" + i; });
  t.after(() => { references.forEach(name => delete process.env[name]); listeners.forEach(http => http.close()); });
  const profile = new MCPProfile(["a", "b"].map((label, i) => ({ server_label: label,
    server_url: `http://127.0.0.1:${listeners[i].address().port}/mcp`, allowed_tools: ["echo"], bearer_token_env_var: references[i] })), []);
  assert.deepEqual(await profile.unselectedTools(new AbortController().signal), ["mcp__a__extra", "mcp__b__extra"]);
  assert.deepEqual([...seen.a], ["Bearer token-0"]);
  assert.deepEqual([...seen.b], ["Bearer token-1"]);
});
