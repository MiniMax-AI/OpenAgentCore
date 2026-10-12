import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { installationQuery } from "../../lib/installation";
import { useSandboxManagerState } from "./use-sandbox-manager-state";

/** Shared observations and write admission survive navigation through the query cache. */
export function useSandboxPageState(options: { poll?: boolean } = {}) {
  const state = useSandboxManagerState(options);
  const queryClient = useQueryClient();
  const installation = useQuery(installationQuery);
  const { ownership, deploymentQuery, deployment, query } = state;
  const loading = deploymentQuery.isFetching;
  const busy = ownership.data.phase === "pending";
  const setupNeedsRefresh = ownership.data.phase === "reconcile";
  const confirmed = deployment !== undefined && !deploymentQuery.isError && ownership.data.phase === "idle";
  const fresh = confirmed && !loading;
  const { refetch: refetchDeployment } = deploymentQuery;
  const { refetch: refetchSnapshot } = query;
  const refetch = useCallback(async () => {
    const result = await refetchDeployment();
    if (!result.isError) await refetchSnapshot();
    return result;
  }, [refetchDeployment, refetchSnapshot]);
  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    return refetch();
  }, [queryClient, refetch]);
  const configurationKey = deployment
    ? `${deployment.installation_id}:${deployment.owner_epoch}:${deployment.provider}:${deployment.mode}:${deployment.generation}`
    : undefined;
  return { ...state, installation, loading, busy, setupNeedsRefresh, confirmed, fresh, refetch, refresh, configurationKey };
}
