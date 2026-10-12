import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { sandboxDeploymentQuery, sandboxResetPollInterval, sandboxSnapshotMatchesDeployment, sandboxSnapshotQuery, type SandboxSnapshot } from "./sandbox-queries";
import { sandboxWriteOwnershipQuery } from "./sandbox-write-ownership";

/** All pages share deployment truth. Nodes are supplemental evidence from the same lifecycle. */
export function useSandboxManagerState({ poll = false }: { poll?: boolean } = {}) {
  const ownership = useQuery(sandboxWriteOwnershipQuery);
  const deploymentQuery = useQuery({ ...sandboxDeploymentQuery, refetchOnMount: "always" });
  const query = useQuery({ ...sandboxSnapshotQuery, refetchInterval: sandboxResetPollInterval(deploymentQuery.data) || (poll ? 30_000 : false) });
  const inventory = query.data;
  const deployment = deploymentQuery.data;
  const compatible = Boolean(inventory && deployment && sandboxSnapshotMatchesDeployment(inventory, deployment));
  // An arrival intent must wait for its supplemental read, rather than treating
  // absent/previous-lifecycle inventory as a confirmed inability to add a node.
  const inventoryLoading = query.isPending || query.isFetching || (!compatible && !query.isError);
  const inventoryOlder = compatible && inventory!.deployment.generation !== deployment!.generation;
  const { refetch } = query;
  useEffect(() => {
    if (inventory && deployment && (!compatible || inventoryOlder || inventory.deployment.rollout.state !== deployment.rollout.state)) void refetch();
  }, [compatible, inventoryOlder, deployment?.installation_id, deployment?.owner_epoch, deployment?.generation, deployment?.provider, deployment?.reset?.requested_at, deployment?.reset?.clear, deployment?.reset?.forced_at, deployment?.rollout.state, refetch]);
  const snapshot: SandboxSnapshot | null = deployment ? {
    deployment,
    nodes: compatible ? inventory!.nodes : [],
    allocations: compatible ? inventory!.allocations : [],
    nodesError: query.isError ? query.error : compatible ? inventory!.nodesError : null,
    readAt: compatible ? inventory!.readAt : 0,
  } : null;
  return { ownership, deploymentQuery, query, deployment, compatible, inventoryOlder, inventoryLoading, snapshot };
}
