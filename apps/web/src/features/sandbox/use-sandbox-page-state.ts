import { useCallback, useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { installationQuery } from "../../lib/installation";
import { sandboxDeploymentQuery, sandboxResetPollInterval, sandboxSnapshotMatchesDeployment, sandboxSnapshotQuery, type SandboxSnapshot } from "./sandbox-queries";
import { sandboxWriteOwnershipQuery } from "./sandbox-write-ownership";

/** Deployment truth and write admission survive navigation through the query cache. Settings reads only these. */
export function useSandboxDeploymentState() {
  const ownership = useQuery(sandboxWriteOwnershipQuery);
  const deploymentQuery = useQuery({ ...sandboxDeploymentQuery, refetchOnMount: "always" });
  const installation = useQuery(installationQuery);
  const queryClient = useQueryClient();
  const deployment = deploymentQuery.data;
  const loading = deploymentQuery.isFetching;
  const busy = ownership.data.phase === "pending";
  const setupNeedsRefresh = ownership.data.phase === "reconcile";
  const confirmed = deployment !== undefined && !deploymentQuery.isError && ownership.data.phase === "idle";
  const fresh = confirmed && !loading;
  const { refetch } = deploymentQuery;
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    return refetch();
  }, [queryClient, refetch]);
  const configurationKey = deployment
    ? `${deployment.installation_id}:${deployment.owner_epoch}:${deployment.provider}:${deployment.mode}:${deployment.generation}`
    : undefined;
  return { ownership, deploymentQuery, deployment, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refresh, configurationKey };
}

/** Nodes adds this lifecycle's node inventory, supplemental evidence beside the deployment. */
export function useSandboxPageState({ poll = false }: { poll?: boolean } = {}) {
  const state = useSandboxDeploymentState();
  const queryClient = useQueryClient();
  const { deploymentQuery, deployment } = state;
  const query = useQuery({ ...sandboxSnapshotQuery, refetchInterval: sandboxResetPollInterval(deployment) || (poll ? 30_000 : false) });
  const inventory = query.data;
  const compatible = Boolean(inventory && deployment && sandboxSnapshotMatchesDeployment(inventory, deployment));
  // An arrival intent must wait for its supplemental read, rather than treating
  // absent/previous-lifecycle inventory as a confirmed inability to add a node.
  const inventoryLoading = query.isPending || query.isFetching || (!compatible && !query.isError);
  const inventoryOlder = compatible && inventory!.deployment.generation !== deployment!.generation;
  const { refetch: refetchSnapshot } = query;
  useEffect(() => {
    if (inventory && deployment && (!compatible || inventoryOlder || inventory.deployment.rollout.state !== deployment.rollout.state)) void refetchSnapshot();
  }, [compatible, inventoryOlder, deployment?.installation_id, deployment?.owner_epoch, deployment?.generation, deployment?.provider, deployment?.reset?.requested_at, deployment?.reset?.clear, deployment?.reset?.forced_at, deployment?.rollout.state, refetchSnapshot]);
  const snapshot: SandboxSnapshot | null = deployment ? {
    deployment,
    nodes: compatible ? inventory!.nodes : [],
    allocations: compatible ? inventory!.allocations : [],
    nodesError: query.isError ? query.error : compatible ? inventory!.nodesError : null,
    readAt: compatible ? inventory!.readAt : 0,
  } : null;
  const { refetch: refetchDeployment } = deploymentQuery;
  const refetch = useCallback(async () => {
    const result = await refetchDeployment();
    if (!result.isError) await refetchSnapshot();
    return result;
  }, [refetchDeployment, refetchSnapshot]);
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    return refetch();
  }, [queryClient, refetch]);
  return { ...state, query, compatible, inventoryOlder, inventoryLoading, snapshot, refetch, refresh };
}
