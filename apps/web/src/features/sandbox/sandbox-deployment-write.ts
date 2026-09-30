import type { SandboxDeployment } from "@oac/agents-client";
import type { QueryClient } from "@tanstack/react-query";
import { sandboxWriteUncertain } from "../../lib/sandbox-labels";
import { sandboxDeploymentQuery, sandboxScope } from "./sandbox-queries";
import { beginSandboxWrite, ownsSandboxWrite, settleSandboxWrite } from "./sandbox-write-ownership";

/** A route change never cancels or forgets a submitted write. No operation is replayed. */
export async function writeSandboxDeployment(cache: QueryClient, operation: (signal: AbortSignal) => Promise<SandboxDeployment>): Promise<SandboxDeployment | null> {
  const attempt = beginSandboxWrite(cache);
  if (attempt === null) return null;
  const request = new AbortController();
  let refreshAfterSettlement = false;
  try {
    await cache.cancelQueries({ queryKey: sandboxScope });
    // A connection reset can clear the cache while cancellation is settling.
    if (!ownsSandboxWrite(cache, attempt)) return null;
    const deployment = await operation(request.signal);
    if (!ownsSandboxWrite(cache, attempt)) return null;
    await cache.cancelQueries({ queryKey: sandboxScope });
    if (!ownsSandboxWrite(cache, attempt)) return null;
    cache.setQueryData(sandboxDeploymentQuery.queryKey, deployment);
    refreshAfterSettlement = true;
    return deployment;
  } catch (error) {
    // Do not deliver an old connection's result or notice to a new one.
    if (!ownsSandboxWrite(cache, attempt)) return null;
    // A definite refusal may refresh the retained draft without replaying it.
    // Conflicts still require this new GET before any subsequent mutation.
    refreshAfterSettlement = !sandboxWriteUncertain(error);
    throw error;
  } finally {
    if (settleSandboxWrite(cache, attempt)) {
      void cache.invalidateQueries({ queryKey: sandboxScope, refetchType: refreshAfterSettlement ? "active" : "none" });
      void cache.invalidateQueries({ queryKey: ["sandbox-fleet"], refetchType: refreshAfterSettlement ? "active" : "none" });
    }
  }
}
