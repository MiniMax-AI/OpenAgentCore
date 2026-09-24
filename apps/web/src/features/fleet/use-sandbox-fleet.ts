import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";

import { sandboxConsoleConfig } from "../sandbox/console-config";

export interface FleetSnapshot {
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  /** Epoch milliseconds of the last complete read. */
  loadedAt: number;
}

export type FleetState =
  | { status: "checking" }
  /** The console has no sandbox administration credential. */
  | { status: "unconfigured" }
  | { status: "loading" }
  | { status: "ready"; snapshot: FleetSnapshot; refreshing: boolean; error: unknown | null }
  | { status: "failed"; error: unknown };

export const FLEET_REFRESH_MS = 30_000;

/**
 * Read-only deployment fleet: the sandbox deployment, its nodes and their
 * allocations (only when asked for) through the console's `/core/v1/sandbox`
 * routes. Node writes stay on the Nodes page.
 */
export function useSandboxFleet({ poll = true, allocations: readAllocations = false }: { poll?: boolean; allocations?: boolean } = {}) {
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }), []);
  const [state, setState] = useState<FleetState>({ status: "checking" });
  const [revision, setRevision] = useState(0);
  const [adminAvailable, setAdminAvailable] = useState<boolean | null>(null);
  const snapshotRef = useRef<FleetSnapshot | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void sandboxConsoleConfig(controller.signal).then((config) => {
      if (controller.signal.aborted) return;
      const available = config?.sandbox_admin === true;
      setAdminAvailable(available);
      if (!available) setState({ status: "unconfigured" });
    });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (adminAvailable !== true) return;
    const controller = new AbortController();
    const previous = snapshotRef.current;
    setState(previous ? { status: "ready", snapshot: previous, refreshing: true, error: null } : { status: "loading" });
    void (async () => {
      const [deployment, nodes] = await Promise.all([
        client.retrieveDeployment({ signal: controller.signal }),
        client.listNodes({ signal: controller.signal }),
      ]);
      const allocations = readAllocations
        ? await Promise.all(nodes.data.map((node) => client.listAllocations(node.id, { signal: controller.signal })))
        : [];
      if (controller.signal.aborted) return;
      const snapshot: FleetSnapshot = {
        deployment,
        nodes: nodes.data,
        allocations: allocations.flatMap((page) => page.data),
        loadedAt: Date.now(),
      };
      snapshotRef.current = snapshot;
      setState({ status: "ready", snapshot, refreshing: false, error: null });
    })().catch((error: unknown) => {
      if (controller.signal.aborted) return;
      const last = snapshotRef.current;
      setState(last ? { status: "ready", snapshot: last, refreshing: false, error } : { status: "failed", error });
    });
    return () => controller.abort();
  }, [adminAvailable, client, readAllocations, revision]);

  useEffect(() => {
    if (!poll || adminAvailable !== true) return;
    const timer = window.setInterval(() => {
      if (!document.hidden) setRevision((value) => value + 1);
    }, FLEET_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [adminAvailable, poll]);

  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { state, refresh };
}

export function fleetSnapshot(state: FleetState): FleetSnapshot | null {
  return state.status === "ready" ? state.snapshot : null;
}
