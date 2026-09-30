import type { SandboxDeployment } from "@oac/agents-client";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { sandboxAdmin, sandboxDeploymentQuery, sandboxScope } from "../sandbox/sandbox-queries";
import { fleetQuery } from "./fleet-queries";

const deployment: SandboxDeployment = { rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "http://core", owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 1, pending: 0 }, suspension: null,
  reset: { clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null, remaining: { busy: 1, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } } };

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("fleet deployment evidence", () => {
  it("retains a successful reset read in the shared cache when node reads fail", async () => {
    const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(deployment);
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("node read failed")));
    await expect(cache.fetchQuery(fleetQuery(false))).rejects.toThrow();
    expect(cache.getQueryData(sandboxDeploymentQuery.queryKey)).toEqual(deployment);
    cache.clear();
  });

  it("does not clear the previous reset when its next deployment read fails", async () => {
    const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    cache.setQueryData(sandboxDeploymentQuery.queryKey, deployment);
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockRejectedValue(new Error("deployment read failed"));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ object: "list", data: [] }), { status: 200 })));
    await expect(cache.fetchQuery(fleetQuery(false))).rejects.toThrow();
    expect(cache.getQueryData(sandboxDeploymentQuery.queryKey)).toEqual(deployment);
    expect(cache.getQueryState(sandboxDeploymentQuery.queryKey)?.status).toBe("error");
    cache.clear();
  });

  it("cannot publish an older reset read after a sandbox write cancels the shared query", async () => {
    const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let resolveRead!: (value: SandboxDeployment) => void;
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockImplementation(() => new Promise((resolve) => { resolveRead = resolve; }));
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ object: "list", data: [] }), { status: 200 })));
    const fleetRead = cache.fetchQuery(fleetQuery(false));
    const rejected = expect(fleetRead).rejects.toThrow();
    await cache.cancelQueries({ queryKey: sandboxScope });
    const confirmed = { ...deployment, reset: null };
    cache.setQueryData(sandboxDeploymentQuery.queryKey, confirmed);
    resolveRead(deployment);
    await rejected;
    expect(cache.getQueryData(sandboxDeploymentQuery.queryKey)).toEqual(confirmed);
    cache.clear();
  });

});
