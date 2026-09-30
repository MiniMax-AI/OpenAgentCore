import { QueryClient } from "@tanstack/react-query";
import type { SandboxDeployment, SandboxReset } from "@oac/agents-client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { node } from "../overview/test-fixtures";
import { sandboxAdmin, sandboxDeploymentQuery, sandboxResetPollInterval, sandboxSnapshotQuery, sandboxScope } from "./sandbox-queries";

const reset: SandboxReset = {
  clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null,
  remaining: { busy: 1, idle: 0, cleanup: 1, on_offline_nodes: 1, offline_nodes: [{ node_id: "node-a", name: "Offline host", resources: 1 }] },
};
const deployment = (overrides: Partial<SandboxDeployment> = {}): SandboxDeployment => ({ credential_configured: false, configuration: {}, metadata: {},
  rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "installation-a", provider: "docker", core_url: "https://core.example", reset,
  owner_epoch: 1, generation: 2, mode: "nodes", resources: { allocations: 2, pending: 0 }, suspension: null, ...overrides,
});
const clients: QueryClient[] = [];
const cache = () => { const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } }); clients.push(client); return client; };
afterEach(() => { for (const client of clients.splice(0)) client.clear(); vi.restoreAllMocks(); });

describe("sandbox reset reads", () => {
  it("polls only Core preparation or reset, never settled old resources or failed/unknown nodes", () => {
    const settled = deployment({ reset: null, rollout: { state: "settled", previous_generation_sandboxes: 9, nodes: { ready: 0, preparing: 0, failed: 2, update_required: 1, unknown: 3 } } });
    expect(sandboxResetPollInterval(settled)).toBe(false);
    expect(sandboxResetPollInterval({ ...settled, rollout: { ...settled.rollout, state: "preparing" } })).toBe(5000);
    expect(sandboxResetPollInterval({ ...settled, reset })).toBe(5000);
  });

  it("keeps owned node and allocation evidence when an online generation's inventory cannot be refreshed", async () => {
    const client = cache(); const first = deployment({ reset: null });
    const read = vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(first);
    const nodes = vi.spyOn(sandboxAdmin, "listNodes").mockResolvedValue({ data: [node("node-a")] });
    const allocation = { id: "owned", node_id: "node-a", tenant_id: "tenant", session_id: "session", environment_id: "environment", state: "active", compute_phase: "running", compute_phase_changed_at: null, diagnostic: "" as const, initialization: "ready", created_at: "2026-09-27T10:00:00Z", deployment_generation: first.generation };
    vi.spyOn(sandboxAdmin, "listAllocations").mockResolvedValue({ data: [allocation] });
    await client.fetchQuery(sandboxSnapshotQuery);
    read.mockResolvedValue({ ...first, generation: first.generation + 1 });
    nodes.mockRejectedValue(new Error("node read unavailable"));
    const observed = await client.fetchQuery(sandboxSnapshotQuery);
    expect(observed.nodes).toHaveLength(1); expect(observed.allocations).toEqual([allocation]);
    expect(observed.deployment.generation).toBe(first.generation);
    expect(observed.nodesError).toBeInstanceOf(Error);
    expect(client.getQueryData(sandboxDeploymentQuery.queryKey)?.generation).toBe(first.generation + 1);
    read.mockResolvedValue({ ...first, owner_epoch: 2 });
    const replaced = await client.fetchQuery(sandboxSnapshotQuery);
    expect(replaced.nodes).toEqual([]); expect(replaced.allocations).toEqual([]);
  });

  it("keeps Core reset and offline blockers when nodes cannot be read", async () => {
    const saved = deployment(); const error = new Error("offline");
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(saved);
    vi.spyOn(sandboxAdmin, "listNodes").mockRejectedValue(error);
    const allocations = vi.spyOn(sandboxAdmin, "listAllocations");
    const state = await cache().fetchQuery(sandboxSnapshotQuery);
    expect(state.deployment).toBe(saved);
    expect(state.nodesError).toBe(error);
    expect(state.deployment.reset?.remaining.offline_nodes).toEqual(reset.remaining.offline_nodes);
    expect(allocations).not.toHaveBeenCalled();
  });

  it("keeps node facts but marks allocation failure unconfirmed instead of proving cleanup", async () => {
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(deployment());
    vi.spyOn(sandboxAdmin, "listNodes").mockResolvedValue({ data: [node("node-a")] });
    const error = new Error("receipt unavailable");
    vi.spyOn(sandboxAdmin, "listAllocations").mockRejectedValue(error);
    const state = await cache().fetchQuery(sandboxSnapshotQuery);
    expect(state.nodes).toHaveLength(1);
    expect(state.nodesError).toBe(error);
    expect(state.deployment.resources.allocations).toBe(2);
    expect(state.deployment.reset?.remaining.cleanup).toBe(1);
  });

  it("does not turn a failed deployment read into setup, and retains active reset for polling", async () => {
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValueOnce(deployment()).mockRejectedValueOnce(new Error("read failed"));
    vi.spyOn(sandboxAdmin, "listNodes").mockResolvedValue({ data: [] });
    const client = cache(); const first = await client.fetchQuery(sandboxSnapshotQuery);
    await expect(client.fetchQuery(sandboxSnapshotQuery)).rejects.toThrow("read failed");
    expect(client.getQueryData(sandboxSnapshotQuery.queryKey)).toBe(first);
    expect(sandboxResetPollInterval(first.deployment)).toBe(5000);
    expect(sandboxResetPollInterval(deployment({ provider: "", generation: 3, reset: null }))).toBe(false);
    expect(sandboxResetPollInterval(undefined)).toBe(false);
  });

  it("preserves the generation of unconfigured reads, including first setup zero and post-reset nonzero", async () => {
    const read = vi.spyOn(sandboxAdmin, "retrieveDeployment");
    const nodes = vi.spyOn(sandboxAdmin, "listNodes");
    const client = cache();
    for (const generation of [0, 3]) {
      read.mockResolvedValueOnce(deployment({ provider: "", reset: null, generation }));
      expect((await client.fetchQuery(sandboxSnapshotQuery)).deployment.generation).toBe(generation);
    }
    expect(nodes).not.toHaveBeenCalled();
  });

  it("an older cancelled read cannot replace a confirmed write even when its node result arrives late", async () => {
    let finish!: () => void;
    const wait = new Promise<void>((resolve) => { finish = resolve; });
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(deployment({ reset: null }));
    const nodes = vi.spyOn(sandboxAdmin, "listNodes").mockImplementation(async () => { await wait; return { data: [] }; });
    const client = cache(); const pending = client.fetchQuery(sandboxSnapshotQuery).catch(() => undefined);
    await vi.waitFor(() => expect(nodes).toHaveBeenCalledOnce());
    await client.cancelQueries({ queryKey: sandboxScope });
    const confirmed = { deployment: deployment(), nodes: [], allocations: [], nodesError: null, readAt: performance.now() };
    client.setQueryData(sandboxSnapshotQuery.queryKey, confirmed);
    finish(); await pending;
    expect(client.getQueryData(sandboxSnapshotQuery.queryKey)).toEqual(confirmed);
  });
});
