import { describe, expect, it, vi } from "vitest";

import { AdminClient, adminScopePath, projectProject, projectResourceOwners, projectSummary, projectWriteOperationPage } from "./admin-client";

const agent = {
  id: "agent_1", object: "agent", model: "provider/model", name: "Support", instructions: null, metadata: {},
  multi_agent: { enabled: false, max_concurrent_subagents: null }, reasoning: {}, service_tier: "auto",
  text: { format: { type: "text" }, verbosity: "medium" }, tools: [], created_at: 1, updated_at: 1,
};

function recorder(body: unknown = { object: "list", data: [], has_more: false, first_id: null, last_id: null }, status = 200) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const fetchImpl = vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init });
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  }) as unknown as typeof fetch;
  return { calls, fetchImpl };
}

describe("admin scope paths", () => {
  it("maps public paths onto a key space and refuses deployment-level ones", () => {
    expect(adminScopePath("/agents")).toBe("/agents");
    expect(adminScopePath("/agents?limit=100")).toBe("/agents?limit=100");
    expect(adminScopePath("/agents/agent_1")).toBe("/agents/agent_1");
    expect(adminScopePath("/agents/sessions?agent_id=agent_1")).toBe("/sessions?agent_id=agent_1");
    expect(adminScopePath("/agents/sessions/s1/turns")).toBe("/sessions/s1/turns");
    expect(adminScopePath("/agents/environments/templates/t1")).toBe("/environment-templates/t1");
    expect(adminScopePath("/skills/skill_1/versions")).toBe("/skills/skill_1/versions");
    expect(adminScopePath("/files?limit=100")).toBe("/files?limit=100");
    expect(adminScopePath("/vaults/v1/credentials")).toBe("/vaults/v1/credentials");
    expect(adminScopePath("/agents/core/startup-configuration")).toBeNull();
    expect(adminScopePath("/agents/runtime-observations")).toBeNull();
    expect(adminScopePath("/agents/environments/env_1")).toBeNull();
    expect(adminScopePath("/sandbox/nodes")).toBeNull();
  });
});

describe("AdminClient.scopeClient", () => {
  it("reads a space's resources through the admin route with the public projections", async () => {
    const { calls, fetchImpl } = recorder(agent);
    const client = new AdminClient({ fetch: fetchImpl }).scopeClient("proj_1");
    const loaded = await client.retrieveAgent("agent_1");
    expect(loaded.name).toBe("Support");
    expect(calls[0]!.url).toBe("/core/v1/admin/projects/proj_1/agents/agent_1");
    const headers = new Headers(calls[0]!.init.headers);
    expect(headers.has("OpenAI-Beta")).toBe(false);
    expect(headers.has("Authorization")).toBe(false);
    expect(calls[0]!.init.credentials).toBe("same-origin");
  });

  it("admits deletes but never creates or edits assets", async () => {
    const { calls, fetchImpl } = recorder({ id: "agent_1", object: "agent.deleted", deleted: true });
    const client = new AdminClient({ fetch: fetchImpl }).scopeClient("proj_1");
    await client.deleteAgent("agent_1");
    expect(calls[0]!.init.method).toBe("DELETE");
    await expect(client.createAgent({ model: "m" } as never)).rejects.toMatchObject({ status: 405 });
    await expect(client.retrieveStartupConfiguration()).rejects.toMatchObject({ status: 404 });
    expect(calls).toHaveLength(1);
  });
});

describe("admin projections", () => {
  it("normalises projects with either timestamp form and marks archived ones", () => {
    const project = projectProject({ id: "proj_1", name: "Production", source: "console", status: "active", created_at: "2026-09-24T00:00:00Z", archived_at: null, active_key_count: 2 });
    expect(project.created_at).toBe(1790208000);
    expect(project.active_key_count).toBe(2);
    expect(projectProject({ id: "proj_2", name: "Ops", source: "config", created_at: 1, archived_at: 5, active_key_count: 0 }).status).toBe("archived");
    expect(() => projectProject({ id: "proj_3", name: "", source: "console", created_at: 1, active_key_count: 0 })).toThrow();
    expect(() => projectProject({ id: "proj_3", name: "X", source: "elsewhere", created_at: 1, active_key_count: 0 })).toThrow();
  });

  it("maps #87 owners and keeps a malformed owner unknown", () => {
    const owners = projectResourceOwners({ data: [
      { resource_id: "agent_1", api_key: { id: "key_1", name: "alice", prefix: "pc_a", kind: "issued", revoked_at: null } },
      { resource_id: "agent_2", api_key: null },
      { resource_id: "agent_3", api_key: { id: "key_3", kind: "unknown" } },
      { resource_id: "agent_9", api_key: null },
    ] }, ["agent_1", "agent_2", "agent_3"]);
    expect(owners.get("agent_1")?.name).toBe("alice");
    expect(owners.get("agent_2")).toBeNull();
    expect(owners.get("agent_3")).toBeNull();
    expect(owners.has("agent_9")).toBe(false);
  });

  it("keeps summary usage null when no Session reported it and rejects impossible coverage", () => {
    const row = { project_id: "proj_1", assets: { agents: 2, skills: 1, environment_templates: 0, files: 3, vaults: 1 }, sessions: { total: 4, idle: 3, in_progress: 1, requires_action: 0, failed: 0 }, usage: null, coverage: { sessions: 4, reported: 0 }, last_active_at: null };
    expect(projectSummary({ data: [row] })[0]!.usage).toBeNull();
    expect(() => projectSummary({ data: [{ ...row, coverage: { sessions: 1, reported: 2 } }] })).toThrow();
  });

  it("reads #87 write operations with empty parents and a cursor", () => {
    const page = projectWriteOperationPage({ data: [{ id: "op_1", created_at: "2026-09-24T12:00:00Z", api_key: { id: "key_1", name: "SDK", prefix: "pc_x", kind: "issued", revoked_at: null }, action: "send_events", resource_type: "session", resource_id: "s1", parent_id: "", request_id: "r1", trace_id: "t1" }], has_more: true, next_cursor: "c1" });
    expect(page.data[0]!.api_key?.name).toBe("SDK");
    expect(page.data[0]!.parent_id).toBe("");
    expect(page.next_cursor).toBe("c1");
    expect(projectWriteOperationPage({ data: [{ id: "op_2", created_at: 1, api_key: null, action: "create", resource_type: "agent", resource_id: "agent_1" }], has_more: false }).data[0]!.api_key).toBeNull();
  });
});

describe("AdminClient key management", () => {
  it("returns the plaintext only from issuing and posts copies with an idempotency key", async () => {
    const issued = recorder({ id: "key_2", name: "ci", prefix: "pc_live_Zz", created_at: 1, revoked_at: null, key: "pc_live_Zz000000000000000000000000" });
    const admin = new AdminClient({ fetch: issued.fetchImpl });
    const key = await admin.issueKey("proj_1", { name: "ci" });
    expect(key.key).toMatch(/^pc_live_Zz/);
    expect(issued.calls[0]!.url).toBe("/core/v1/admin/projects/proj_1/keys");
    expect(JSON.parse(String(issued.calls[0]!.init.body))).toEqual({ name: "ci" });
    await expect(admin.issueKey("proj_1", { name: " padded" })).rejects.toThrow();

    const copies = recorder({ mappings: [{ type: "agent", source_id: "agent_1", target_id: "agent_9" }], skipped: [] });
    const result = await new AdminClient({ fetch: copies.fetchImpl }).copy(
      { source_project_id: "proj_1", target_project_id: "proj_2", resource_type: "agent", resource_id: "agent_1", include_dependencies: true },
      { idempotencyKey: "idem-1" },
    );
    expect(result.mappings[0]!.target_id).toBe("agent_9");
    expect(new Headers(copies.calls[0]!.init.headers).get("Idempotency-Key")).toBe("idem-1");
    await expect(new AdminClient({ fetch: copies.fetchImpl }).copy({ source_project_id: "p", target_project_id: "p", resource_type: "agent", resource_id: "a", include_dependencies: false }, { idempotencyKey: "x" })).rejects.toThrow();
  });
});
