import { useQuery } from "@tanstack/react-query";
import { useCallback } from "react";

import { consoleConfigQuery, fleetQuery, type FleetSnapshot } from "./fleet-queries";

export type { FleetSnapshot };

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
 * routes, read through the query cache so a revisit opens at once and a
 * refresh keeps the last snapshot on screen. Node writes stay on the Nodes page.
 */
export function useSandboxFleet({ poll = true, allocations = false }: { poll?: boolean; allocations?: boolean } = {}) {
  const config = useQuery(consoleConfigQuery);
  // A failed configuration read is a failure, not "no sandbox administration".
  const configFailed = config.isError && config.data === undefined;
  const adminAvailable = config.isPending || configFailed ? null : config.data?.sandbox_admin === true;
  const fleet = useQuery({
    ...fleetQuery(allocations),
    enabled: adminAvailable === true,
    refetchInterval: poll ? FLEET_REFRESH_MS : false,
    refetchIntervalInBackground: false,
  });

  let state: FleetState;
  if (configFailed) state = config.isFetching ? { status: "checking" } : { status: "failed", error: config.error };
  else if (adminAvailable === null) state = { status: "checking" };
  else if (!adminAvailable) state = { status: "unconfigured" };
  else if (fleet.data) state = { status: "ready", snapshot: fleet.data, refreshing: fleet.isFetching, error: fleet.isError ? fleet.error : null };
  else if (fleet.isError && !fleet.isFetching) state = { status: "failed", error: fleet.error };
  else state = { status: "loading" };

  const { refetch: refetchFleet } = fleet;
  const { refetch: refetchConfig } = config;
  // Without sandbox administration a refresh asks the console again whether it has it.
  const refresh = useCallback(() => {
    void (adminAvailable === true ? refetchFleet() : refetchConfig());
  }, [adminAvailable, refetchConfig, refetchFleet]);
  return { state, refresh };
}

export function fleetSnapshot(state: FleetState): FleetSnapshot | null {
  return state.status === "ready" ? state.snapshot : null;
}
