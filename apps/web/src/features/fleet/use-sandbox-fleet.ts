import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect } from "react";
import type { SandboxDeployment } from "@oac/agents-client";

import { sandboxDeploymentQuery } from "../sandbox/sandbox-queries";
import { consoleConfigQuery, fleetQuery, type FleetSnapshot } from "./fleet-queries";

export type { FleetSnapshot };

export type FleetState =
  | { status: "checking" }
  /** The console has no sandbox administration credential. */
  | { status: "unconfigured" }
  | { status: "loading" }
  | { status: "ready"; snapshot: FleetSnapshot; targetGeneration: number; refreshing: boolean; error: unknown | null }
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
  const deployment = useQuery({ ...sandboxDeploymentQuery, enabled: adminAvailable === true });
  const fleet = useQuery({
    ...fleetQuery(allocations),
    enabled: adminAvailable === true,
    refetchInterval: poll ? FLEET_REFRESH_MS : false,
    refetchIntervalInBackground: false,
  });

  const differentDeployment = fleet.data !== undefined && deployment.data !== undefined && !fleetMatchesLifecycle(fleet.data, deployment.data);
  const changedGeneration = fleet.data !== undefined && deployment.data !== undefined && fleet.data.deployment.generation !== deployment.data.generation;
  const { refetch: refetchFleet } = fleet;
  // A reset/configuration change can arrive through the faster deployment poll.
  // Compatible prior-generation inventory remains visible as an older observation.
  // A reset or backend lifecycle change makes that earlier inventory invalid.
  useEffect(() => {
    if (adminAvailable === true && (differentDeployment || changedGeneration)) void refetchFleet();
  }, [adminAvailable, differentDeployment, changedGeneration, deployment.data?.installation_id, deployment.data?.owner_epoch, deployment.data?.generation, deployment.data?.provider, deployment.data?.mode, deployment.data?.reset?.requested_at, refetchFleet]);

  let state: FleetState;
  if (configFailed) state = config.isFetching ? { status: "checking" } : { status: "failed", error: config.error };
  else if (adminAvailable === null) state = { status: "checking" };
  else if (!adminAvailable) state = { status: "unconfigured" };
  else if (differentDeployment) state = fleet.isError && !fleet.isFetching ? { status: "failed", error: fleet.error } : { status: "loading" };
  else if (fleet.data) state = { status: "ready", snapshot: fleet.data, targetGeneration: deployment.data?.generation ?? fleet.data.deployment.generation, refreshing: fleet.isFetching, error: fleet.isError ? fleet.error : null };
  else if (fleet.isError && !fleet.isFetching) state = { status: "failed", error: fleet.error };
  else state = { status: "loading" };

  const { refetch: refetchConfig } = config;
  // Without sandbox administration a refresh asks the console again whether it has it.
  const refresh = useCallback(() => {
    void (adminAvailable === true ? refetchFleet() : refetchConfig());
  }, [adminAvailable, refetchConfig, refetchFleet]);
  return { state, refresh, deployment };
}

export function fleetSnapshot(state: FleetState): FleetSnapshot | null {
  return state.status === "ready" ? state.snapshot : null;
}

/** Online configuration updates preserve ownership; reset/backend lifecycle changes do not. */
function fleetMatchesLifecycle(snapshot: FleetSnapshot, deployment: SandboxDeployment): boolean {
  const previous = snapshot.deployment;
  return previous.installation_id === deployment.installation_id && previous.owner_epoch === deployment.owner_epoch &&
    previous.provider === deployment.provider && previous.mode === deployment.mode &&
    previous.reset?.requested_at === deployment.reset?.requested_at;
}
