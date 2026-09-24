import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SandboxAdminClient, type SandboxAllocation, type SandboxDeployment, type SandboxNode } from "@agents-core-web/agents-client";

import { isLocalProxyBaseUrl } from "../../lib/connection";
import { sandboxConsoleConfig } from "../sandbox/console-config";

export interface FleetSnapshot {
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  allocations: SandboxAllocation[];
  /** Epoch milliseconds of the last complete read. */
  loadedAt: number;
}

export type FleetState =
  /** The console connects to another Core, so it cannot administer this deployment. */
  | { status: "remote" }
  | { status: "checking" }
  /** The paired console has no sandbox administration credential. */
  | { status: "unconfigured" }
  | { status: "loading" }
  | { status: "ready"; snapshot: FleetSnapshot; refreshing: boolean; error: unknown | null }
  | { status: "failed"; error: unknown };

export const FLEET_REFRESH_MS = 30_000;

/**
 * Read-only deployment fleet: sandbox deployment, nodes and their allocations
 * through the paired console's allowlisted admin routes. Writes stay in the
 * node manager.
 */
export function useSandboxFleet(coreBaseUrl: string, { poll = true }: { poll?: boolean } = {}) {
  const local = isLocalProxyBaseUrl(coreBaseUrl);
  const client = useMemo(() => new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }), []);
  const [state, setState] = useState<FleetState>(local ? { status: "checking" } : { status: "remote" });
  const [revision, setRevision] = useState(0);
  const [adminAvailable, setAdminAvailable] = useState<boolean | null>(null);
  const snapshotRef = useRef<FleetSnapshot | null>(null);

  useEffect(() => {
    if (!local) {
      setState({ status: "remote" });
      return;
    }
    const controller = new AbortController();
    setState({ status: "checking" });
    void sandboxConsoleConfig(controller.signal).then((config) => {
      if (controller.signal.aborted) return;
      const available = config?.sandbox_admin === true;
      setAdminAvailable(available);
      if (!available) setState({ status: "unconfigured" });
    });
    return () => controller.abort();
  }, [local]);

  useEffect(() => {
    if (!local || adminAvailable !== true) return;
    const controller = new AbortController();
    const previous = snapshotRef.current;
    setState(previous ? { status: "ready", snapshot: previous, refreshing: true, error: null } : { status: "loading" });
    void (async () => {
      const [deployment, nodes] = await Promise.all([
        client.retrieveDeployment({ signal: controller.signal }),
        client.listNodes({ signal: controller.signal }),
      ]);
      const allocations = await Promise.all(nodes.data.map((node) => client.listAllocations(node.id, { signal: controller.signal })));
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
  }, [adminAvailable, client, local, revision]);

  useEffect(() => {
    if (!poll || !local || adminAvailable !== true) return;
    const timer = window.setInterval(() => {
      if (!document.hidden) setRevision((value) => value + 1);
    }, FLEET_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [adminAvailable, local, poll]);

  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { state, refresh };
}

export function fleetSnapshot(state: FleetState): FleetSnapshot | null {
  return state.status === "ready" ? state.snapshot : null;
}
