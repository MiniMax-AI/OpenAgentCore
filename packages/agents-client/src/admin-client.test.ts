import { describe, expect, it, vi } from "vitest";
import { AdminClient } from "./admin-client";
import { AgentCoreError, OpenAIAgentsClient } from "./client";

const projectId = "11111111-1111-4111-8111-111111111111";
const keyId = "44444444-4444-4444-8444-444444444444";
const sessionId = "22222222-2222-4222-8222-222222222222";
const resourceId = "33333333-3333-4333-8333-333333333333";
const key = {
  id: keyId, name: "SDK", prefix: "pc_example", project_id: projectId,
  created_at: "2026-09-24T00:00:00Z", revoked_at: null,
};
const project = {
  id: projectId, name: "Research", created_at: "2026-09-24T00:00:00Z",
  archived_at: null, active_key_count: 2,
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
  ["GET", "/projects", (client) => client.listProjects()],
  ["POST", "/projects", (client) => client.createProject({ name: "Research" })],
  ["POST", `/projects/${projectId}`, (client) => client.renameProject(projectId, { name: "Renamed" })],
  ["POST", `/projects/${projectId}/archive`, (client) => client.archiveProject(projectId)],
  ["GET", `/projects/${projectId}/keys`, (client) => client.listAPIKeys(projectId)],
  ["POST", `/projects/${projectId}/keys`, (client) => client.issueAPIKey(projectId, { name: "SDK" })],
  ["DELETE", `/projects/${projectId}/keys/${keyId}`, (client) => client.revokeAPIKey(projectId, keyId)],
  ["GET", "/audit-log", (client) => client.listAuditLog()],
  ["GET", "/summary", (client) => client.retrieveSummary()],
  ["GET", "/runtime-observations", (client) => client.listRuntimeObservations()],
  ["GET", `/projects/${projectId}/agents`, (client) => client.listAgents(projectId)],
  ["GET", `/projects/${projectId}/agents/a`, (client) => client.retrieveAgent(projectId, "a")],
  ["DELETE", `/projects/${projectId}/agents/a`, (client) => client.deleteAgent(projectId, "a")],
  ["GET", `/projects/${projectId}/skills`, (client) => client.listSkills(projectId)],
  ["GET", `/projects/${projectId}/skills/s`, (client) => client.retrieveSkill(projectId, "s")],
  ["DELETE", `/projects/${projectId}/skills/s`, (client) => client.deleteSkill(projectId, "s")],
  ["GET", `/projects/${projectId}/skills/s/versions`, (client) => client.listSkillVersions(projectId, "s")],
  ["GET", `/projects/${projectId}/skills/s/versions/2`, (client) => client.retrieveSkillVersion(projectId, "s", "2")],
  ["DELETE", `/projects/${projectId}/skills/s/versions/2`, (client) => client.deleteSkillVersion(projectId, "s", "2")],
  ["GET", `/projects/${projectId}/skills/s/content`, (client) => client.downloadSkill(projectId, "s")],
  ["GET", `/projects/${projectId}/skills/s/versions/2/content`, (client) => client.downloadSkillVersion(projectId, "s", "2")],
  ["GET", `/projects/${projectId}/environment-templates`, (client) => client.listEnvironmentTemplates(projectId)],
  ["GET", `/projects/${projectId}/environment-templates/t`, (client) => client.retrieveEnvironmentTemplate(projectId, "t")],
  ["DELETE", `/projects/${projectId}/environment-templates/t`, (client) => client.deleteEnvironmentTemplate(projectId, "t")],
  ["GET", `/projects/${projectId}/files`, (client) => client.listSourceFiles(projectId)],
  ["GET", `/projects/${projectId}/files/f`, (client) => client.retrieveSourceFile(projectId, "f")],
  ["DELETE", `/projects/${projectId}/files/f`, (client) => client.deleteSourceFile(projectId, "f")],
  ["GET", `/projects/${projectId}/vaults`, (client) => client.listVaults(projectId)],
  ["GET", `/projects/${projectId}/vaults/v`, (client) => client.retrieveVault(projectId, "v")],
  ["DELETE", `/projects/${projectId}/vaults/v`, (client) => client.deleteVault(projectId, "v")],
  ["GET", `/projects/${projectId}/vaults/v/credentials`, (client) => client.listVaultCredentials(projectId, "v")],
  ["GET", `/projects/${projectId}/vaults/v/credentials/c`, (client) => client.retrieveVaultCredential(projectId, "v", "c")],
  ["DELETE", `/projects/${projectId}/vaults/v/credentials/c`, (client) => client.deleteVaultCredential(projectId, "v", "c")],
  ["GET", `/projects/${projectId}/sessions`, (client) => client.listSessions(projectId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}`, (client) => client.retrieveSession(projectId, sessionId)],
  ["DELETE", `/projects/${projectId}/sessions/${sessionId}`, (client) => client.deleteSession(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/turns`, (client) => client.listTurns(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/turns/t`, (client) => client.retrieveTurn(projectId, sessionId, "t")],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/items`, (client) => client.listItems(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/artifacts`, (client) => client.listArtifacts(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/artifacts/a`, (client) => client.retrieveArtifact(projectId, sessionId, "a")],
  ["DELETE", `/projects/${projectId}/sessions/${sessionId}/artifacts/a`, (client) => client.deleteArtifact(projectId, sessionId, "a")],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/artifacts/a/content`, (client) => client.downloadArtifact(projectId, sessionId, "a")],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/execution-configuration`, (client) => client.retrieveSessionExecutionConfiguration(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/runtime-observation`, (client) => client.retrieveRuntimeObservation(projectId, sessionId)],
  ["GET", `/projects/${projectId}/sessions/${sessionId}/runtime-history?start=1&end=2`, (client) => client.retrieveRuntimeHistory(projectId, sessionId, { start: 1, end: 2 })],
  ["GET", `/projects/${projectId}/resource-owners?resource_type=agent&resource_ids=a`, (client) => client.retrieveResourceOwners(projectId, "agent", ["a"])],
  ["GET", `/projects/${projectId}/write-operations`, (client) => client.listWriteOperations(projectId)],
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
    for (const method of ["request", "resetAPIKey", "retrieveAPIKey", "createAPIKey", "createAgent", "updateAgent", "createSession", "sendMessage", "submitEvents", "cancelTurn", "streamEvents", "createVault", "createVaultCredential", "replaceVaultCredentialToken", "createEnvironmentTemplate", "downloadSourceFile", "uploadSourceFile"]) {
      expect(method in client).toBe(false);
    }
  });

  it("only uses an explicitly supplied admin credential and forwards cancellation", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => json({ data: [], has_more: false }));
    const signal = new AbortController().signal;
    const client = new AdminClient({ baseUrl: "https://core.test/core/v1/admin/", adminToken: () => "deployment-secret", fetch });
    await client.listAPIKeys(projectId, { after: keyId, limit: 5, order: "asc", signal });
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe(`https://core.test/core/v1/admin/projects/${projectId}/keys?after=${keyId}&limit=5&order=asc`);
    expect(init?.signal).toBe(signal);
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer deployment-secret");
  });

  it("does not retry uncertain writes or expose a reflected network error as a success", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("connection closed"));
    const client = new AdminClient({ fetch });
    await expect(client.issueAPIKey(projectId, { name: "SDK" })).rejects.toThrow("connection closed");
    expect(fetch).toHaveBeenCalledOnce();
    expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual({ name: "SDK" });
  });

  it("encodes identifiers and prevents normalized dot path traversal", async () => {
    const { client, fetch } = clientWith(key);
    await expect(client.listAPIKeys("../other")).rejects.toBeInstanceOf(AgentCoreError);
    expect(fetch.mock.calls[0]![0]).toBe("/core/v1/admin/projects/..%2Fother/keys");
    expect(() => client.deleteAgent(projectId, "..")).toThrow(TypeError);
    expect(fetch).toHaveBeenCalledOnce();
  });
});

describe("AdminClient response contracts", () => {
  it("returns a secret only from issuance and binds every key to the requested project", async () => {
    const issued = clientWith({ ...key, key: "once-only" });
    expect((await issued.client.issueAPIKey(projectId, { name: "SDK" })).key).toBe("once-only");
    await expect(issued.client.issueAPIKey(sessionId, { name: "SDK" })).rejects.toMatchObject({ code: "invalid_admin_response" });
    await expect(clientWith({ data: [{ ...key, key: "leak" }], has_more: false }).client.listAPIKeys(projectId)).rejects.toMatchObject({ code: "invalid_admin_response" });
    const read = clientWith({ data: [key], has_more: false });
    expect((await read.client.listAPIKeys(projectId)).data[0]).toEqual(key);
    await expect(read.client.listAPIKeys(sessionId)).rejects.toMatchObject({ code: "invalid_admin_response" });
    expect(await clientWith({ id: keyId, deleted: true }).client.revokeAPIKey(projectId, keyId)).toEqual({ id: keyId, deleted: true });
  });

  it("reuses Vault metadata validation including write-only credential rejection", async () => {
    const credential = { id: resourceId, vault_id: sessionId, object: "vault.credential", name: "MCP", created_at: 1, updated_at: 1, auth: { type: "static_bearer", mcp_server_url: "https://mcp.test" } };
    const { client } = clientWith(credential);
    expect(await client.retrieveVaultCredential(projectId, sessionId, resourceId)).toEqual(credential);
    await expect(clientWith({ ...credential, auth: { ...credential.auth, token: "leak" } }).client.retrieveVaultCredential(projectId, sessionId, resourceId)).rejects.toMatchObject({ code: "invalid_vault_credential" });
  });

  it("reuses Session identity and state validation", async () => {
    const agent = { id: "a", model: "model", name: null, instructions: null, multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: {}, service_tier: "auto", text: { format: { type: "text" }, verbosity: "medium" }, tools: [] };
    const session = { id: sessionId, object: "agent.session", agent, environment: { type: "none" }, status: "idle", error: null, metadata: {}, required_actions: [], vault_ids: [], usage: null, created_at: 1, last_active_at: 1 };
    expect(await clientWith(session).client.retrieveSession(projectId, sessionId)).toEqual(session);
    await expect(clientWith(session).client.retrieveSession(projectId, resourceId)).rejects.toBeInstanceOf(AgentCoreError);
    expect((await clientWith(page([session])).client.listSessions(projectId)).data).toEqual([session]);
  });

  it("binds Skills, versions and Artifacts to requested resources", async () => {
    const skill = { id: "skill", object: "skill", created_at: 1, name: "helper", description: "help", default_version: "1", latest_version: "2" };
    expect(await clientWith(skill).client.retrieveSkill(projectId, "skill")).toEqual(skill);
    const version = { id: "version", object: "skill.version", created_at: 1, skill_id: "skill", version: "2", name: "helper", description: "help" };
    await expect(clientWith(version).client.retrieveSkillVersion(projectId, "other", "2")).rejects.toBeInstanceOf(AgentCoreError);
    const artifact = { id: "artifact", object: "agent.session.artifact", created_at: 1, session_id: sessionId, environment_id: resourceId, path: "/result.txt", size_bytes: 2, turn_id: "turn" };
    expect((await clientWith(page([artifact])).client.listArtifacts(projectId, sessionId)).data).toEqual([artifact]);
    await expect(clientWith(artifact).client.retrieveArtifact(projectId, resourceId, "artifact")).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("downloads permitted content as bytes without another API request", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response("bundle", { headers: { "Content-Type": "application/zip" } }));
    const result = await new AdminClient({ fetch }).downloadSkill(projectId, "skill");
    expect(await result.blob.text()).toBe("bundle");
    expect(result.contentType).toBe("application/zip");
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("retains owner ordering and strips no unexpected secret fields", async () => {
    const owners = { data: [{ resource_id: "a", api_key: null, source: null, admin_audit_id: null }] };
    expect(await clientWith(owners).client.retrieveResourceOwners(projectId, "agent", ["a"])).toEqual(owners);
    await expect(clientWith(owners).client.retrieveResourceOwners(projectId, "agent", ["b"])).rejects.toBeInstanceOf(AgentCoreError);
    await expect(clientWith({ data: [{ resource_id: "a", api_key: { id: projectId, name: "SDK", prefix: "p", kind: "issued", revoked_at: null, key: "leak" } }] }).client.retrieveResourceOwners(projectId, "agent", ["a"])).rejects.toBeInstanceOf(AgentCoreError);
  });
});


describe("AdminClient deployment read models", () => {
  it("projects summary usage and coverage without treating missing measurements as measured zero", async () => {
    const summary = {
      data: [{
        project_id: projectId, key_id: null, agent_id: null,
        assets: { agents: 1, skills: 0, environment_templates: 0, files: 0, vaults: 0, credentials: 0 },
        sessions: { total: 2, idle: 1, in_progress: 0, requires_action: 0, failed: 1 },
        usage: { input_tokens: 10, output_tokens: 5, total_tokens: 15, input_tokens_details: { cached_tokens: 2 }, output_tokens_details: { reasoning_tokens: 1 } },
        coverage: { measured_sessions: 1, total_sessions: 2, ratio: 0.5 }, last_active_at: 10,
      }], has_more: true, next_cursor: projectId,
    };
    const { client, fetch } = clientWith(summary);
    expect(await client.retrieveSummary({ project_id: projectId, group_by: "project", limit: 1, created_after: "2026-09-01T00:00:00Z" })).toEqual(summary);
    const url = new URL(fetch.mock.calls[0]![0] as string, "https://console.test");
    expect(url.searchParams.get("created_after")).toBe("2026-09-01T00:00:00Z");
    expect(url.searchParams.get("project_id")).toBe(projectId);
    await expect(clientWith({ ...summary, data: [{ ...summary.data[0], usage: null }] }).client.retrieveSummary()).rejects.toBeInstanceOf(AgentCoreError);
    await expect(clientWith({ ...summary, data: [{ ...summary.data[0], coverage: { measured_sessions: 3, total_sessions: 2, ratio: 1.5 } }] }).client.retrieveSummary()).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("validates historical copy provenance and safe audit mappings", async () => {
    const owners = { data: [{ resource_id: "a", api_key: null, source: "admin_copy", admin_audit_id: "audit" }] };
    expect(await clientWith(owners).client.retrieveResourceOwners(projectId, "agent", ["a"])).toEqual(owners);
    const audit = { data: [{ id: "audit", created_at: "2026-09-24T00:00:00Z", admin_credential_id: "digest", actor_label: "admin", action: "copy", project_id: projectId, resource_type: "agent", resource_id: "a", result_ids: [{ type: "agent", source_id: "a", target_id: "b" }], request_id: "request", trace_id: "trace" }], has_more: false, next_cursor: "" };
    const { client, fetch } = clientWith(audit);
    expect(await client.listAuditLog({ action: "copy", resource_type: "agent", project_id: projectId, after: "cursor" })).toEqual(audit);
    expect(fetch.mock.calls[0]![0]).toBe(`/core/v1/admin/audit-log?after=cursor&project_id=${projectId}&resource_type=agent&action=copy`);
    await expect(clientWith({ ...audit, data: [{ ...audit.data[0], request_body: { token: "leak" } }] }).client.listAuditLog()).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("passes provenance filters without a binding digest and preserves Vault status arrays", async () => {
    const operations = { data: [], has_more: false, next_cursor: "" };
    const { client, fetch } = clientWith(operations);
    await client.listWriteOperations(projectId, { key_id: resourceId, created_before: "2026-09-24T00:00:00Z", limit: 5 });
    const url = new URL(fetch.mock.calls[0]![0] as string, "https://console.test");
    expect(url.searchParams.get("key_id")).toBe(resourceId);
    expect(url.searchParams.has("binding_digest")).toBe(false);
    const vaults = clientWith(page([]));
    await vaults.client.listVaults(projectId, { status: ["active", "archived"] });
    expect(new URL(vaults.fetch.mock.calls[0]![0] as string, "https://console.test").searchParams.getAll("status[]")).toEqual(["active", "archived"]);
  });

  it("requires the project wrapper for global Runtime observations", async () => {
    expect(await clientWith(page([])).client.listRuntimeObservations()).toEqual(page([]));
    await expect(clientWith({ ...page([]), data: [{ project_id: projectId, observation: { id: sessionId, token: "leak" } }] }).client.listRuntimeObservations()).rejects.toBeInstanceOf(AgentCoreError);
  });
});

describe("AdminClient project lifecycle", () => {
  it("creates projects with server-owned IDs and lists their metadata", async () => {
    const { client, fetch } = clientWith(project);
    const input = { name: "Research", id: "must-not-be-sent", tenant_id: "must-not-be-sent" };
    expect(await client.createProject(input)).toEqual(project);
    expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual({ name: "Research" });
    const catalog = { data: [project, { ...project, id: resourceId, name: "Other", active_key_count: 3 }], has_more: false };
    expect(await clientWith(catalog).client.listProjects()).toEqual(catalog);
    await expect(clientWith({ ...catalog, data: [{ ...project, tenant_id: "hidden" }] }).client.listProjects()).rejects.toMatchObject({ code: "invalid_admin_response" });
    await expect(clientWith({ ...project, active_key_count: -1 }).client.createProject({ name: "Research" })).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it("binds rename/archive responses to the project without changing its keys", async () => {
    const renamed = { ...project, name: "Renamed" };
    const rename = clientWith(renamed);
    expect(await rename.client.renameProject(projectId, { name: "Renamed" })).toEqual(renamed);
    expect(JSON.parse(rename.fetch.mock.calls[0]![1]!.body as string)).toEqual({ name: "Renamed" });
    const archived = { ...project, archived_at: "2026-09-25T00:00:00Z", active_key_count: 0 };
    const archive = clientWith(archived);
    expect(await archive.client.archiveProject(projectId)).toEqual(archived);
    expect(archive.fetch.mock.calls[0]![1]!.body).toBeUndefined();
    await expect(clientWith(renamed).client.renameProject(sessionId, { name: "Renamed" })).rejects.toBeInstanceOf(AgentCoreError);
    await expect(clientWith(archived).client.archiveProject(sessionId)).rejects.toBeInstanceOf(AgentCoreError);
  });

  it("lists multiple equal keys within one project and issues only the requested name", async () => {
    const catalog = { data: [key, { ...key, id: resourceId, name: "Worker" }], has_more: false };
    expect(await clientWith(catalog).client.listAPIKeys(projectId)).toEqual(catalog);
    const { client, fetch } = clientWith({ ...key, key: "once-only" });
    const input = { name: "SDK", id: "must-not-be-sent" };
    expect((await client.issueAPIKey(projectId, input)).id).toBe(keyId);
    expect(JSON.parse(fetch.mock.calls[0]![1]!.body as string)).toEqual({ name: "SDK" });
    await expect(clientWith({ ...key, key: "once-only", tenant_id: "old-space-field" }).client.issueAPIKey(projectId, input)).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it.each([
    (client: AdminClient) => client.createProject({ name: "Research" }),
    (client: AdminClient) => client.renameProject(projectId, { name: "Renamed" }),
    (client: AdminClient) => client.archiveProject(projectId),
    (client: AdminClient) => client.issueAPIKey(projectId, { name: "SDK" }),
  ])("does not retry an uncertain project/key mutation", async (call) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError("response lost"));
    await expect(call(new AdminClient({ fetch }))).rejects.toThrow("response lost");
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("surfaces archived-project conflicts without another request", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => json({ error: { message: "Project is archived.", code: "conflict_error" } }, 409));
    await expect(new AdminClient({ fetch }).issueAPIKey(projectId, { name: "SDK" })).rejects.toMatchObject({ status: 409, code: "conflict_error" });
    expect(fetch).toHaveBeenCalledOnce();
  });
});

describe("AdminClient project monitoring", () => {
  it.each(["project", "agent", "key"] as const)("preserves the %s summary grouping", async (groupBy) => {
    const row = {
      project_id: projectId, agent_id: groupBy === "agent" ? resourceId : null, key_id: groupBy === "key" ? keyId : null,
      assets: groupBy === "project" ? { agents: 1, skills: 0, environment_templates: 0, files: 0, vaults: 0, credentials: 0 } : null,
      sessions: { total: 1, idle: 1, in_progress: 0, requires_action: 0, failed: 0 },
      usage: { input_tokens: 10, output_tokens: 5, total_tokens: 15, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } },
      coverage: { measured_sessions: 1, total_sessions: 1, ratio: 1 }, last_active_at: 10,
    };
    const data = groupBy === "key" ? [row, { ...row, key_id: null }] : [row];
    const summary = { data, has_more: false, next_cursor: "" };
    const { client, fetch } = clientWith(summary);
    expect(await client.retrieveSummary({ project_id: projectId, group_by: groupBy })).toEqual(summary);
    expect(fetch.mock.calls[0]![0]).toBe(`/core/v1/admin/summary?project_id=${projectId}&group_by=${groupBy}`);
    const { project_id: _, ...oldRow } = row;
    await expect(clientWith({ ...summary, data: [oldRow] }).client.retrieveSummary()).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it("retains the project label on global Runtime observations", async () => {
    const observation = {
      id: sessionId, object: "agent.runtime_observation", session_id: sessionId, environment_id: resourceId,
      mode: "openai_hosted", provider_type: "docker",
      instance: { kind: "managed_allocation", allocation_id: resourceId, device_id: keyId, connection_generation: null },
      lifecycle_state: "active", status: "observed", reason: null,
      allocation_created_at: 10, resolved_at: 30, observed_at: 20, started_at: 10,
      cpu: { usage_seconds_total: 0, capacity_cores: 2, usage_cores: null, utilization_ratio: null },
      memory: { usage_bytes: 0, limit_bytes: 1024 },
      disk: null,
    };
    const value = { object: "list", data: [{ project_id: projectId, observation }], has_more: false, first_id: sessionId, last_id: sessionId };
    expect(await clientWith(value).client.listRuntimeObservations()).toEqual(value);
    await expect(clientWith({ ...value, data: [{ key_id: keyId, observation }] }).client.listRuntimeObservations()).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it("projects E2B utilization and disk on global Runtime observations", async () => {
    const observation = {
      id: sessionId, object: "agent.runtime_observation", session_id: sessionId, environment_id: resourceId,
      mode: "openai_hosted", provider_type: "e2b",
      instance: { kind: "managed_allocation", allocation_id: resourceId, device_id: keyId, connection_generation: null },
      lifecycle_state: "active", status: "observed", reason: null,
      allocation_created_at: 10, resolved_at: 30, observed_at: 20, started_at: 10,
      cpu: { usage_seconds_total: null, capacity_cores: 2, usage_cores: null, utilization_ratio: 0.1955 },
      memory: { usage_bytes: 183836672, limit_bytes: 2079141888 },
      disk: { usage_bytes: 1593188352, limit_bytes: 23511863296 },
    };
    const value = { object: "list", data: [{ project_id: projectId, observation }], has_more: false, first_id: sessionId, last_id: sessionId };
    expect(await clientWith(value).client.listRuntimeObservations()).toEqual(value);
    const { disk: _, ...older } = observation;
    expect((await clientWith({ ...value, data: [{ project_id: projectId, observation: older }] }).client.listRuntimeObservations()).data[0]!.observation.disk).toBeNull();
    const invalid = { ...observation, disk: { usage_bytes: 1, limit_bytes: 0 } };
    await expect(clientWith({ ...value, data: [{ project_id: projectId, observation: invalid }] }).client.listRuntimeObservations()).rejects.toMatchObject({ code: "invalid_admin_response" });
  });
});

describe("AdminClient database-owned identities", () => {
  it("requires persisted creation timestamps on projects and keys", async () => {
    await expect(clientWith({ data: [{ ...project, created_at: null }], has_more: false }).client.listProjects()).rejects.toMatchObject({ code: "invalid_admin_response" });
    await expect(clientWith({ data: [{ ...key, created_at: null }], has_more: false }).client.listAPIKeys(projectId)).rejects.toMatchObject({ code: "invalid_admin_response" });
  });

  it("preserves zero-limit Skill and version pages with more resources", async () => {
    const page = { object: "list", data: [], has_more: true, first_id: null, last_id: null };
    const { client } = clientWith(page);
    expect(await client.listSkills(projectId, { limit: 0 })).toEqual(page);
    expect(await client.listSkillVersions(projectId, "skill", { limit: 0 })).toEqual(page);
  });

  it.each(["issued", "static", "console"])("preserves %s key provenance in historical ownership records", async (kind) => {
    const owner = { resource_id: resourceId, api_key: { id: keyId, name: "Original key", prefix: "p", kind, revoked_at: null }, source: "api_key", admin_audit_id: null };
    expect(await clientWith({ data: [owner] }).client.retrieveResourceOwners(projectId, "agent", [resourceId])).toEqual({ data: [owner] });
  });
});
