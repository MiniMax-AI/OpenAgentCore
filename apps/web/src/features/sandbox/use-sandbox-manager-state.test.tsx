import type { SandboxDeployment } from "@oac/agents-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { node } from "../overview/test-fixtures";
import { sandboxAdmin, sandboxDeploymentQuery, sandboxSnapshotQuery } from "./sandbox-queries";
import { useSandboxManagerState } from "./use-sandbox-manager-state";

const configured: SandboxDeployment = { rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "http://core", reset: null, owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 1, pending: 0 }, suspension: null };
const resetting: SandboxDeployment = { ...configured, reset: { clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null, remaining: { busy: 1, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } } };
const clients: QueryClient[] = [];
function cache() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } }); clients.push(client);
  client.setQueryData(sandboxSnapshotQuery.queryKey, { deployment: configured, nodes: [node("old")], allocations: [], nodesError: null, readAt: 1 });
  return client;
}
function Probe() {
  const state = useSandboxManagerState();
  return <div data-read={state.deploymentQuery.isError ? "failed" : "confirmed"} data-compatible={String(state.compatible)} data-older={String(state.inventoryOlder)} data-owner={state.ownership.data.phase} data-inventory-loading={String(state.inventoryLoading)} data-inventory-failed={String(Boolean(state.snapshot?.nodesError))}>
    {state.snapshot?.deployment.provider || "setup"}/{state.snapshot?.deployment.reset?.clear ?? "none"}/{state.snapshot?.nodes.length ?? 0}
  </div>;
}
const render = (client: QueryClient) => renderToStaticMarkup(<QueryClientProvider client={client}><Probe /></QueryClientProvider>);
afterEach(() => { clients.splice(0).forEach((client) => client.clear()); vi.restoreAllMocks(); });

describe("Nodes shared deployment evidence", () => {
  it("waits for first-arrival node inventory and distinguishes a settled failure from loading", async () => {
    const client = cache(); client.removeQueries({ queryKey: sandboxSnapshotQuery.queryKey });
    client.setQueryData(sandboxDeploymentQuery.queryKey, configured);
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(configured);
    let finish!: () => void;
    const held = new Promise<void>((resolve) => { finish = resolve; });
    const nodes = vi.spyOn(sandboxAdmin, "listNodes").mockImplementation(async () => { await held; return { data: [] }; });
    const pending = client.fetchQuery(sandboxSnapshotQuery);
    await vi.waitFor(() => expect(nodes).toHaveBeenCalledOnce());
    expect(render(client)).toContain('data-inventory-loading="true"');
    expect(render(client)).toContain('data-inventory-failed="false"');
    finish(); await pending;
    expect(render(client)).toContain('data-inventory-loading="false"');
    expect(render(client)).toContain('data-compatible="true"');
    nodes.mockRejectedValueOnce(new Error("Nodes unavailable"));
    await client.fetchQuery({ ...sandboxSnapshotQuery, staleTime: 0 });
    expect(render(client)).toContain('data-inventory-loading="false"');
    expect(render(client)).toContain('data-inventory-failed="true"');
  });

  it("uses another page's reset and completion immediately despite a fresh cached idle snapshot", () => {
    const client = cache();
    client.setQueryData(sandboxDeploymentQuery.queryKey, resetting);
    expect(render(client)).toContain("docker/auto/0");
    expect(render(client)).toContain('data-compatible="false"');
    expect(render(client)).toContain('data-inventory-loading="true"');
    client.setQueryData(sandboxDeploymentQuery.queryKey, { ...resetting, reset: { ...resetting.reset!, clear: "force", forced_at: "2026-09-27T10:01:00Z" } });
    expect(render(client)).toContain("docker/force/0");
    client.setQueryData(sandboxDeploymentQuery.queryKey, { ...configured, provider: "", mode: "", generation: 2 });
    expect(render(client)).toContain("setup/none/0");
    expect(client.getQueryData(sandboxSnapshotQuery.queryKey)?.deployment).toEqual(configured);
  });

  it("does not treat cached snapshot success as deployment success after a shared read fails", async () => {
    const client = cache(); client.setQueryData(sandboxDeploymentQuery.queryKey, configured);
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockRejectedValue(new Error("Core unavailable"));
    await expect(client.fetchQuery({ ...sandboxDeploymentQuery, staleTime: 0 })).rejects.toThrow("Core unavailable");
    expect(render(client)).toContain('data-read="failed"');
    expect(client.getQueryState(sandboxSnapshotQuery.queryKey)?.status).toBe("success");
  });

  it("retains older generation nodes but rejects a different installation, owner, provider or reset lifecycle", () => {
    const client = cache(); client.setQueryData(sandboxDeploymentQuery.queryKey, configured);
    expect(render(client)).toContain("docker/none/1");
    client.setQueryData(sandboxDeploymentQuery.queryKey, { ...configured, generation: 2 });
    expect(render(client)).toContain("docker/none/1");
    expect(render(client)).toContain('data-older="true"');
    for (const change of [{ installation_id: "next" }, { owner_epoch: 2 }, { mode: "direct" as const }, { provider: "e2b" as const }]) {
      client.setQueryData(sandboxDeploymentQuery.queryKey, { ...configured, ...change });
      expect(render(client)).toContain('data-compatible="false"');
    }
  });
});
