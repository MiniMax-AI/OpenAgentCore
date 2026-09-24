import { describe, expect, it, vi } from "vitest";
import { AdminClient } from "./admin-client";
import { AgentCoreError, OpenAIAgentsClient } from "./client";

const keyId = "11111111-1111-4111-8111-111111111111";
const sessionId = "22222222-2222-4222-8222-222222222222";
const resourceId = "33333333-3333-4333-8333-333333333333";
const key = {
  id: keyId, name: "SDK", prefix: "pc_example", kind: "issued",
  tenant_id: "tenant", organization_id: "core", project_id: "project",
  created_at: "2026-09-24T00:00:00Z", revoked_at: null,
};
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
function clientWith(value: unknown) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => json(value));
  return { client: new AdminClient({ fetch }), fetch };
}
const page = (data: Array<{ id: string }>) => ({ object: "list", data, has_more: false, first_id: data[0]?.id ?? null, last_id: data.at(-1)?.id ?? null });

const routeCases: Array<[string, string, (client: AdminClient) => Promise<unknown>]> = [
  ["GET", "/api-keys", (client) => client.listAPIKeys()],
  ["GET", `/api-keys/${keyId}`, (client) => client.retrieveAPIKey(keyId)],
  ["POST", "/api-keys", (client) => client.createAPIKey({ id: keyId, name: "SDK" })],
  ["POST", `/api-keys/${keyId}/reset`, (client) => client.resetAPIKey(keyId, { request_id: resourceId })],
  ["DELETE", `/api-keys/${keyId}`, (client) => client.revokeAPIKey(keyId)],
  ["GET", "/startup-configuration", (client) => client.retrieveStartupConfiguration()],
  ["GET", "/audit-log", (client) => client.listAuditLog()],
  ["GET", "/summary", (client) => client.retrieveSummary()],
  ["GET", "/runtime-observations", (client) => client.listRuntimeObservations()],
  ["GET", `/api-keys/${keyId}/agents`, (client) => client.listAgents(keyId)],
  ["GET", `/api-keys/${keyId}/agents/a`, (client) => client.retrieveAgent(keyId, "a")],
  ["DELETE", `/api-keys/${keyId}/agents/a`, (client) => client.deleteAgent(keyId, "a")],
  ["GET", `/api-keys/${keyId}/skills`, (client) => client.listSkills(keyId)],
  ["GET", `/api-keys/${keyId}/skills/s`, (client) => client.retrieveSkill(keyId, "s")],
  ["DELETE", `/api-keys/${keyId}/skills/s`, (client) => client.deleteSkill(keyId, "s")],
  ["GET", `/api-keys/${keyId}/skills/s/versions`, (client) => client.listSkillVersions(keyId, "s")],
  ["GET", `/api-keys/${keyId}/skills/s/versions/2`, (client) => client.retrieveSkillVersion(keyId, "s", "2")],
  ["DELETE", `/api-keys/${keyId}/skills/s/versions/2`, (client) => client.deleteSkillVersion(keyId, "s", "2")],
  ["GET", `/api-keys/${keyId}/skills/s/content`, (client) => client.downloadSkill(keyId, "s")],
  ["GET", `/api-keys/${keyId}/skills/s/versions/2/content`, (client) => client.downloadSkillVersion(keyId, "s", "2")],
  ["GET", `/api-keys/${keyId}/environment-templates`, (client) => client.listEnvironmentTemplates(keyId)],
  ["GET", `/api-keys/${keyId}/environment-templates/t`, (client) => client.retrieveEnvironmentTemplate(keyId, "t")],
  ["DELETE", `/api-keys/${keyId}/environment-templates/t`, (client) => client.deleteEnvironmentTemplate(keyId, "t")],
  ["GET", `/api-keys/${keyId}/files`, (client) => client.listSourceFiles(keyId)],
  ["GET", `/api-keys/${keyId}/files/f`, (client) => client.retrieveSourceFile(keyId, "f")],
  ["DELETE", `/api-keys/${keyId}/files/f`, (client) => client.deleteSourceFile(keyId, "f")],
  ["GET", `/api-keys/${keyId}/vaults`, (client) => client.listVaults(keyId)],
  ["GET", `/api-keys/${keyId}/vaults/v`, (client) => client.retrieveVault(keyId, "v")],
  ["DELETE", `/api-keys/${keyId}/vaults/v`, (client) => client.deleteVault(keyId, "v")],
  ["GET", `/api-keys/${keyId}/vaults/v/credentials`, (client) => client.listVaultCredentials(keyId, "v")],
  ["GET", `/api-keys/${keyId}/vaults/v/credentials/c`, (client) => client.retrieveVaultCredential(keyId, "v", "c")],
  ["DELETE", `/api-keys/${keyId}/vaults/v/credentials/c`, (client) => client.deleteVaultCredential(keyId, "v", "c")],
  ["GET", `/api-keys/${keyId}/sessions`, (client) => client.listSessions(keyId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}`, (client) => client.retrieveSession(keyId, sessionId)],
  ["DELETE", `/api-keys/${keyId}/sessions/${sessionId}`, (client) => client.deleteSession(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/turns`, (client) => client.listTurns(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/turns/t`, (client) => client.retrieveTurn(keyId, sessionId, "t")],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/items`, (client) => client.listItems(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/artifacts`, (client) => client.listArtifacts(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/artifacts/a`, (client) => client.retrieveArtifact(keyId, sessionId, "a")],
  ["DELETE", `/api-keys/${keyId}/sessions/${sessionId}/artifacts/a`, (client) => client.deleteArtifact(keyId, sessionId, "a")],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/artifacts/a/content`, (client) => client.downloadArtifact(keyId, sessionId, "a")],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/execution-configuration`, (client) => client.retrieveSessionExecutionConfiguration(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/runtime-observation`, (client) => client.retrieveRuntimeObservation(keyId, sessionId)],
  ["GET", `/api-keys/${keyId}/sessions/${sessionId}/runtime-history?start=1&end=2`, (client) => client.retrieveRuntimeHistory(keyId, sessionId, { start: 1, end: 2 })],
  ["GET", `/api-keys/${keyId}/resource-owners?resource_type=agent&resource_ids=a`, (client) => client.retrieveResourceOwners(keyId, "agent", ["a"])],
  ["GET", `/api-keys/${keyId}/write-operations`, (client) => client.listWriteOperations(keyId)],
];

describe("AdminClient transport boundary", () => {
  it.each(routeCases)("routes %s %s without credential or public API fallback", async (method, path, call) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => json({ error: { message: "not found", code: "not_found" } }, 404));
    const client = new AdminClient({ fetch });
    await expect(call(client)).rejects.toMatchObject({ status: 404, code: "not_found" });
    expect(fetch).toHaveBeenCalledOnce();
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe(`/core/v1/admin${path}`);
    expect(init).toMatchObject({ method, credentials: "same-origin", redirect: "error" });
    expect(new Headers(init?.headers).has("Authorization")).toBe(false);
    expect(new Headers(init?.headers).has("OpenAI-Beta")).toBe(false);
  });

  it("has no generic bypass, inherited execution, editing, or source-content capability", () => {
    const client = new AdminClient();
    expect(client).not.toBeInstanceOf(OpenAIAgentsClient);
    for (const method of ["request", "createAgent", "updateAgent", "createSession", "sendMessage", "submitEvents", "cancelTurn", "streamEvents", "createVault", "createVaultCredential", "replaceVaultCredentialToken", "createEnvironmentTemplate", "downloadSourceFile", "uploadSourceFile"]) {
      expect(method in client).toBe(false);
    }
  });

  it("only uses an explicitly supplied admin credential and forwards cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => json({ data: [], has_more: false }));
    const signal = new AbortController().signal;
    const client = new AdminClient({ baseUrl: "https://core.test/core/v1/admin/", adminToken: () => "deployment-secret", fetch });
    await client.listAPIKeys({ after: "static:a", limit: 5, order: "asc", signal });
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("https://core.test/core/v1/admin/api-keys?after=static%3Aa&limit=5&order=asc");
    expect(init?.signal).toBe(signal);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer deployment-secret");
  });

  it("does not retry uncertain writes or expose a reflected network error as a success", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("connection closed"));
    const client = new AdminClient({ fetch });
    await expect(client.resetAPIKey(keyId, { request_id: resourceId })).rejects.toThrow("connection closed");
    expect(fetch).toHaveBeenCalledOnce();
    expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual({ request_id: resourceId });
  });

  it("encodes identifiers and prevents normalized dot path traversal", async () => {
    const { client, fetch } = clientWith(key);
    await expect(client.retrieveAPIKey("../other")).rejects.toBeInstanceOf(AgentCoreError);
    expect(fetch.mock.calls[0]![0]).toBe("/core/v1/admin/api-keys/..%2Fother");
    expect(() => client.deleteAgent(keyId, "..")).toThrow(TypeError);
    expect(fetch).toHaveBeenCalledOnce();
  });
});

describe("AdminClient response contracts", () => {
  it("returns a secret only from explicit issuance/reset and rejects secrets in read metadata", async () => {
    const issued = clientWith({ ...key, key: "once-only" });
    expect((await issued.client.createAPIKey({ id: keyId, name: "SDK" })).key).toBe("once-only");
    expect((await issued.client.resetAPIKey(keyId, { request_id: resourceId })).id).toBe(keyId);
    await expect(issued.client.retrieveAPIKey(keyId)).rejects.toMatchObject({ code: "invalid_admin_response" });
    const read = clientWith({ ...key, kind: "static", created_at: "0001-01-01T00:00:00Z" });
    expect((await read.client.retrieveAPIKey(keyId)).kind).toBe("static");
    expect(await clientWith({ id: keyId, deleted: true }).client.revokeAPIKey(keyId)).toEqual({ id: keyId, deleted: true });
  });

  it("sends copy idempotency once and projects only safe mappings", async () => {
    const result = { mappings: [{ type: "credential", source_id: "old", target_id: "new" }], skipped: [{ type: "credential", source_id: "oauth", reason: "refresh" }] };
    const { client, fetch } = clientWith(result);
    const input = { source_key_id: keyId, target_key_id: sessionId, resource_type: "credential" as const, resource_id: "old", include_dependencies: true, target_vault_id: resourceId };
    expect(await client.copyResources(input, { idempotencyKey: "copy-1" })).toEqual(result);
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0]![0]).toBe("/core/v1/admin/copies");
    expect(new Headers(fetch.mock.calls[0]![1]?.headers).get("Idempotency-Key")).toBe("copy-1");
    expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual(input);
    await expect(clientWith({ ...result, token: "leak" }).client.copyResources(input)).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it("reuses Vault metadata validation including write-only credential rejection", async () => {
    const credential = { id: resourceId, vault_id: sessionId, object: "vault.credential", name: "MCP", created_at: 1, updated_at: 1, auth: { type: "static_bearer", mcp_server_url: "https://mcp.test" } };
    const { client } = clientWith(credential);
    expect(await client.retrieveVaultCredential(keyId, sessionId, resourceId)).toEqual(credential);
    await expect(clientWith({ ...credential, auth: { ...credential.auth, token: "leak" } }).client.retrieveVaultCredential(keyId, sessionId, resourceId)).rejects.toMatchObject({ code: "invalid_vault_credential" });
  });

  it("reuses Session identity and state validation", async () => {
    const agent = { id: "a", model: "model", name: null, instructions: null, multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: {}, service_tier: "auto", text: { format: { type: "text" }, verbosity: "medium" }, tools: [] };
    const session = { id: sessionId, object: "agent.session", agent, environment: { type: "none" }, status: "idle", error: null, metadata: {}, required_actions: [], vault_ids: [], usage: null, created_at: 1, last_active_at: 1 };
    expect(await clientWith(session).client.retrieveSession(keyId, sessionId)).toEqual(session);
    await expect(clientWith(session).client.retrieveSession(keyId, resourceId)).rejects.toBeInstanceOf(AgentCoreError);
    expect((await clientWith(page([session])).client.listSessions(keyId)).data).toEqual([session]);
  });

  it("binds Skills, versions and Artifacts to requested resources", async () => {
    const skill = { id: "skill", object: "skill", created_at: 1, name: "helper", description: "help", default_version: "1", latest_version: "2" };
    expect(await clientWith(skill).client.retrieveSkill(keyId, "skill")).toEqual(skill);
    const version = { id: "version", object: "skill.version", created_at: 1, skill_id: "skill", version: "2", name: "helper", description: "help" };
    await expect(clientWith(version).client.retrieveSkillVersion(keyId, "other", "2")).rejects.toBeInstanceOf(AgentCoreError);
    const artifact = { id: "artifact", object: "agent.session.artifact", created_at: 1, session_id: sessionId, environment_id: resourceId, path: "/result.txt", size_bytes: 2, turn_id: "turn" };
    expect((await clientWith(page([artifact])).client.listArtifacts(keyId, sessionId)).data).toEqual([artifact]);
    await expect(clientWith(artifact).client.retrieveArtifact(keyId, resourceId, "artifact")).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("downloads permitted content as bytes without another API request", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response("bundle", { headers: { "Content-Type": "application/zip" } }));
    const result = await new AdminClient({ fetch }).downloadSkill(keyId, "skill");
    expect(await result.blob.text()).toBe("bundle");
    expect(result.contentType).toBe("application/zip");
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("retains owner ordering and strips no unexpected secret fields", async () => {
    const owners = { data: [{ resource_id: "a", api_key: null, source: null, admin_audit_id: null }] };
    expect(await clientWith(owners).client.retrieveResourceOwners(keyId, "agent", ["a"])).toEqual(owners);
    await expect(clientWith(owners).client.retrieveResourceOwners(keyId, "agent", ["b"])).rejects.toBeInstanceOf(AgentCoreError);
    await expect(clientWith({ data: [{ resource_id: "a", api_key: { id: keyId, name: "SDK", prefix: "p", kind: "issued", revoked_at: null, key: "leak" } }] }).client.retrieveResourceOwners(keyId, "agent", ["a"])).rejects.toBeInstanceOf(AgentCoreError);
  });
});


describe("AdminClient deployment read models", () => {
  it("projects summary usage and coverage without treating missing measurements as measured zero", async () => {
    const summary = {
      data: [{
        key_id: keyId, agent_id: null,
        assets: { agents: 1, skills: 0, environment_templates: 0, files: 0, vaults: 0, credentials: 0 },
        sessions: { total: 2, idle: 1, in_progress: 0, requires_action: 0, failed: 1 },
        usage: { input_tokens: 10, output_tokens: 5, total_tokens: 15, input_tokens_details: { cached_tokens: 2 }, output_tokens_details: { reasoning_tokens: 1 } },
        coverage: { measured_sessions: 1, total_sessions: 2, ratio: 0.5 }, last_active_at: 10,
      }], has_more: true, next_cursor: keyId,
    };
    const { client, fetch } = clientWith(summary);
    expect(await client.retrieveSummary({ key_id: keyId, group_by: "key", limit: 1, created_after: "2026-09-01T00:00:00Z" })).toEqual(summary);
    const url = new URL(fetch.mock.calls[0]![0] as string, "https://console.test");
    expect(url.searchParams.get("created_after")).toBe("2026-09-01T00:00:00Z");
    expect(url.searchParams.get("key_id")).toBe(keyId);
    await expect(clientWith({ ...summary, data: [{ ...summary.data[0], usage: null }] }).client.retrieveSummary()).rejects.toBeInstanceOf(AgentCoreError);
    await expect(clientWith({ ...summary, data: [{ ...summary.data[0], coverage: { measured_sessions: 3, total_sessions: 2, ratio: 1.5 } }] }).client.retrieveSummary()).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("validates administrator copy provenance and safe audit mappings", async () => {
    const owners = { data: [{ resource_id: "a", api_key: null, source: "admin_copy", admin_audit_id: "audit" }] };
    expect(await clientWith(owners).client.retrieveResourceOwners(keyId, "agent", ["a"])).toEqual(owners);
    const audit = { data: [{ id: "audit", created_at: "2026-09-24T00:00:00Z", admin_credential_id: "digest", actor_label: "admin", action: "copy", target_key_id: keyId, resource_type: "agent", resource_id: "a", result_ids: [{ type: "agent", source_id: "a", target_id: "b" }], request_id: "request", trace_id: "trace" }], has_more: false, next_cursor: "" };
    const { client, fetch } = clientWith(audit);
    expect(await client.listAuditLog({ action: "copy", resource_type: "agent", key_id: keyId, after: "cursor" })).toEqual(audit);
    expect(fetch.mock.calls[0]![0]).toBe(`/core/v1/admin/audit-log?after=cursor&key_id=${keyId}&resource_type=agent&action=copy`);
    await expect(clientWith({ ...audit, data: [{ ...audit.data[0], request_body: { token: "leak" } }] }).client.listAuditLog()).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("passes provenance filters without a binding digest and preserves Vault status arrays", async () => {
    const operations = { data: [], has_more: false, next_cursor: "" };
    const { client, fetch } = clientWith(operations);
    await client.listWriteOperations(keyId, { key_id: resourceId, created_before: "2026-09-24T00:00:00Z", limit: 5 });
    const url = new URL(fetch.mock.calls[0]![0] as string, "https://console.test");
    expect(url.searchParams.get("key_id")).toBe(resourceId);
    expect(url.searchParams.has("binding_digest")).toBe(false);
    const vaults = clientWith(page([]));
    await vaults.client.listVaults(keyId, { status: ["active", "archived"] });
    expect(new URL(vaults.fetch.mock.calls[0]![0] as string, "https://console.test").searchParams.getAll("status[]")).toEqual(["active", "archived"]);
  });

  it("requires the key wrapper for global Runtime observations", async () => {
    expect(await clientWith(page([])).client.listRuntimeObservations()).toEqual(page([]));
    await expect(clientWith({ ...page([]), data: [{ key_id: keyId, observation: { id: sessionId, token: "leak" } }] }).client.listRuntimeObservations()).rejects.toBeInstanceOf(AgentCoreError);
  });
});
