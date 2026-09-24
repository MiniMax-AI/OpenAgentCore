import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchActivity, fetchOwners, OWNER_BATCH, OwnershipUnavailableError, projectActivityPage, projectActor, projectOwners } from "./ownership";

const key = { type: "project_api_key", id: "fb533e99-524f-4e44-94bc-8f6e571646a7", name: "Production app", prefix: "pc_live_7Hq", revoked_at: null };

afterEach(() => vi.unstubAllGlobals());

describe("API key ownership projection", () => {
  it("accepts issued, static and console actors and rejects incomplete ones", () => {
    expect(projectActor(key)).toEqual(key);
    expect(projectActor({ type: "console", id: null, name: null, prefix: null, revoked_at: null })?.type).toBe("console");
    expect(projectActor({ type: "static", id: null, name: null, prefix: "sk_ab", revoked_at: null })?.prefix).toBe("sk_ab");
    expect(projectActor({ ...key, id: null })).toBeNull();
    expect(projectActor({ ...key, id: "not-a-uuid" })).toBeNull();
    expect(projectActor({ ...key, type: "user" })).toBeNull();
    expect(projectActor({ ...key, revoked_at: "yesterday" })).toBeNull();
  });

  it("maps owners by requested ID, keeps null owners and drops malformed entries", () => {
    const owners = projectOwners({ object: "list", data: [
      { resource_type: "agent", resource_id: "agent_1", owner: key, created_at: "2026-09-24T12:00:00Z" },
      { resource_type: "agent", resource_id: "agent_2", owner: null, created_at: null },
      { resource_type: "agent", resource_id: "agent_3", owner: { type: "unknown" }, created_at: null },
      { resource_type: "file", resource_id: "agent_4", owner: key, created_at: null },
      { resource_type: "agent", resource_id: "agent_9", owner: key, created_at: null },
    ] }, "agent", ["agent_1", "agent_2", "agent_3", "agent_4"]);
    expect(owners.get("agent_1")?.owner?.name).toBe("Production app");
    expect(owners.get("agent_2")).toEqual({ resource_type: "agent", resource_id: "agent_2", owner: null, created_at: null });
    expect(owners.has("agent_3")).toBe(false);
    expect(owners.has("agent_4")).toBe(false);
    expect(owners.has("agent_9")).toBe(false);
    expect(() => projectOwners({ data: [] }, "agent", [])).toThrow();
  });

  it("projects activity pages and drops only malformed rows", () => {
    const page = projectActivityPage({ object: "list", has_more: true, last_id: "act_2", data: [
      { id: "act_1", created_at: "2026-09-24T12:00:00Z", actor: key, action: "create", resource_type: "agent", resource_id: "agent_1", parent_resource_id: null, trace_id: "abc" },
      { id: "act_2", created_at: "2026-09-24T11:00:00Z", actor: key, action: "delete", resource_type: "vault_credential", resource_id: "cred 1", parent_resource_id: null, trace_id: null },
    ] });
    expect(page.data.map((entry) => entry.id)).toEqual(["act_1"]);
    expect(page).toMatchObject({ has_more: true, last_id: "act_2" });
    expect(() => projectActivityPage({ object: "list", data: [] })).toThrow();
  });
});

describe("API key ownership requests", () => {
  it("batches owner lookups at 100 IDs and never sends malformed IDs", async () => {
    const urls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      urls.push(url);
      return new Response(JSON.stringify({ object: "list", data: [] }), { status: 200 });
    }));
    const ids = Array.from({ length: OWNER_BATCH + 5 }, (_, index) => `agent_${index}`);
    await fetchOwners("agent", [...ids, "bad id", "agent_0"]);
    expect(urls).toHaveLength(2);
    expect(new URLSearchParams(urls[0]!.split("?")[1]).get("ids")!.split(",")).toHaveLength(OWNER_BATCH);
    expect(urls.join()).not.toContain("bad");
  });

  it("treats 400/404/405/503 from the bridge as an absent capability", async () => {
    for (const status of [400, 404, 405, 503]) {
      vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status })));
      await expect(fetchOwners("agent", ["agent_1"])).rejects.toBeInstanceOf(OwnershipUnavailableError);
    }
  });

  it("omits unset activity filters because the bridge rejects empty values", async () => {
    let url = "";
    vi.stubGlobal("fetch", vi.fn(async (value: string) => {
      url = value;
      return new Response(JSON.stringify({ object: "list", data: [], has_more: false, first_id: null, last_id: null }), { status: 200 });
    }));
    await fetchActivity({ actor_type: "console" });
    expect(url).toBe("/console/api-keys/activity?actor_type=console&limit=50");
  });
});
