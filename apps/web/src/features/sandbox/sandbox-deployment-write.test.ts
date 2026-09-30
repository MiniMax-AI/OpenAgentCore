import { AgentCoreError, type SandboxDeployment } from "@oac/agents-client";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sandboxAdmin, sandboxDeploymentQuery } from "./sandbox-queries";
import { writeSandboxDeployment } from "./sandbox-deployment-write";
import { sandboxWriteOwnershipQuery } from "./sandbox-write-ownership";

const deployment: SandboxDeployment = { rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", owner_epoch: 1, generation: 1, provider: "docker", core_url: "http://core", mode: "nodes", reset: null, resources: { allocations: 0, pending: 0 }, suspension: null };
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const clients: QueryClient[] = [];
function cache() { const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } }); clients.push(client); return client; }
afterEach(() => { clients.splice(0).forEach((client) => client.clear()); vi.restoreAllMocks(); });

describe("deployment write ownership", () => {
  it.each([403, 409])("reconciles a definite %s refusal with a read, leaving the next write explicit", async (status) => {
    const client = cache(); client.setQueryData(sandboxDeploymentQuery.queryKey, deployment);
    const observer = new QueryObserver(client, { ...sandboxDeploymentQuery, staleTime: Infinity });
    const unsubscribe = observer.subscribe(() => {});
    const read = vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(deployment);
    const operation = vi.fn(async () => { throw new AgentCoreError("Refused", status); });
    await expect(writeSandboxDeployment(client, operation)).rejects.toThrow("Refused");
    await vi.waitFor(() => expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("idle"));
    expect(read).toHaveBeenCalledOnce(); expect(operation).toHaveBeenCalledOnce(); unsubscribe(); observer.destroy();
  });

  it("survives leaving and returning while pending, then requires a read begun after settlement", async () => {
    const client = cache();
    const response = deferred<SandboxDeployment>();
    const operation = vi.fn(() => response.promise);
    const observer = new QueryObserver(client, sandboxWriteOwnershipQuery);
    const leave = observer.subscribe(() => {});
    const write = writeSandboxDeployment(client, operation).catch((error: unknown) => error);
    await vi.waitFor(() => expect(operation).toHaveBeenCalledOnce());
    leave(); observer.destroy();
    const returned = new QueryObserver(client, sandboxWriteOwnershipQuery);
    expect(returned.getCurrentResult().data?.phase).toBe("pending");
    const replay = vi.fn(async () => deployment);
    expect(await writeSandboxDeployment(client, replay)).toBeNull();
    expect(replay).not.toHaveBeenCalled();

    const early = deferred<SandboxDeployment>();
    const read = vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValueOnce(deployment).mockReturnValueOnce(early.promise);
    await client.fetchQuery(sandboxDeploymentQuery);
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("pending");
    const inFlightRead = client.fetchQuery(sandboxDeploymentQuery);
    response.reject(new Error("response lost"));
    await expect(write).resolves.toEqual(new Error("response lost"));
    early.resolve(deployment); await inFlightRead;
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("reconcile");
    read.mockRejectedValueOnce(new Error("read unavailable"));
    await expect(client.fetchQuery(sandboxDeploymentQuery)).rejects.toThrow("read unavailable");
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("reconcile");
    read.mockResolvedValueOnce(deployment);
    await client.fetchQuery(sandboxDeploymentQuery);
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("idle");
    expect(operation).toHaveBeenCalledOnce(); returned.destroy();
  });

  it("publishes a confirmed response after route departure without replaying the mutation", async () => {
    // Browsers coarsen performance.now(); adjacent settlement/GET events can
    // share a timestamp while still having a definite causal order.
    vi.spyOn(performance, "now").mockReturnValue(100);
    const client = cache(); const response = deferred<SandboxDeployment>();
    const operation = vi.fn(() => response.promise);
    const pending = writeSandboxDeployment(client, operation);
    await vi.waitFor(() => expect(operation).toHaveBeenCalledOnce());
    const saved = { ...deployment, generation: 2 };
    response.resolve(saved); await expect(pending).resolves.toBe(saved);
    expect(client.getQueryData(sandboxDeploymentQuery.queryKey)).toEqual(saved);
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("reconcile");
    vi.spyOn(sandboxAdmin, "retrieveDeployment").mockResolvedValue(saved);
    await client.fetchQuery(sandboxDeploymentQuery);
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)?.phase).toBe("idle");
    expect(operation).toHaveBeenCalledOnce();
  });

  it.each(["success", "failure"])("discards a late %s after connection clear without disturbing a new write", async (outcome) => {
    const client = cache(); const old = deferred<SandboxDeployment>(); const next = deferred<SandboxDeployment>();
    const operation = vi.fn(() => old.promise);
    const pending = writeSandboxDeployment(client, operation);
    await vi.waitFor(() => expect(operation).toHaveBeenCalledOnce());
    client.clear();
    const nextOperation = vi.fn(() => next.promise);
    const newWrite = writeSandboxDeployment(client, nextOperation);
    await vi.waitFor(() => expect(nextOperation).toHaveBeenCalledOnce());
    const newOwner = client.getQueryData(sandboxWriteOwnershipQuery.queryKey);
    if (outcome === "success") old.resolve(deployment); else old.reject(new Error("old error"));
    await expect(pending).resolves.toBeNull();
    expect(client.getQueryData(sandboxDeploymentQuery.queryKey)).toBeUndefined();
    expect(client.getQueryData(sandboxWriteOwnershipQuery.queryKey)).toEqual(newOwner);
    next.resolve({ ...deployment, installation_id: "new" }); await newWrite;
    expect(client.getQueryData(sandboxDeploymentQuery.queryKey)?.installation_id).toBe("new");
  });
});
