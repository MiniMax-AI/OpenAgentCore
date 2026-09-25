import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode, type SandboxProvider } from "@agents-core-web/agents-client";
import { queryOptions, useQuery } from "@tanstack/react-query";

import { sandboxConsoleConfig } from "./console-config";

/**
 * Cache entries for the sandbox deployment (`/core/v1/sandbox`). Every read
 * lives under `["sandbox"]`; deployment writes are never retried and never
 * written into the cache except as Core's confirmed response.
 */
export const sandboxAdmin = new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" });

export const sandboxScope = ["sandbox"] as const;

/** Whether this console may administer sandboxes, and its node installer. */
export const sandboxConsoleConfigQuery = queryOptions({
  queryKey: ["console-config"],
  queryFn: async ({ signal }) => {
    const config = await sandboxConsoleConfig(signal);
    // Never cache a result read after cancellation.
    signal.throwIfAborted();
    return config;
  },
});

/** The deployment alone, for pages that only describe it. */
export const sandboxDeploymentQuery = queryOptions({
  queryKey: [...sandboxScope, "deployment"],
  queryFn: ({ signal }) => sandboxAdmin.retrieveDeployment({ signal }),
});

/**
 * The deployment's sandbox provider from the cached deployment read: "" before
 * setup, null while unknown or when this console has no sandbox administration.
 * Pages that differ for E2B (no machines) and own nodes read it.
 */
export function useSandboxProvider(): SandboxProvider | "" | null {
  const config = useQuery(sandboxConsoleConfigQuery);
  const deployment = useQuery({ ...sandboxDeploymentQuery, enabled: config.data?.sandbox_admin === true });
  return deployment.data?.provider ?? null;
}

export interface SandboxSnapshot {
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  /** `performance.now()` when this read began: only a read begun after an uncertain write confirms it. */
  readAt: number;
}

/** The Nodes page: deployment, its nodes and their allocations, read together. */
export const sandboxSnapshotQuery = queryOptions({
  queryKey: [...sandboxScope, "snapshot"],
  queryFn: async ({ signal }): Promise<SandboxSnapshot> => {
    const readAt = performance.now();
    const deployment = await sandboxAdmin.retrieveDeployment({ signal });
    const nodes = deployment.provider === "e2b" ? { data: [] } : await sandboxAdmin.listNodes({ signal });
    const allocations = await Promise.all(nodes.data.map((node) => sandboxAdmin.listAllocations(node.id, { signal })));
    return { deployment, nodes: nodes.data, allocations: allocations.flatMap((page) => page.data), readAt };
  },
});
