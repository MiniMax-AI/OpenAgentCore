import type { ExecutorCredentialList } from "@oac/agents-client";
import { queryOptions } from "@tanstack/react-query";

import { admin } from "../../lib/projects";
import { sessionKey } from "./session-queries";

export const EXECUTOR_CONNECTION_POLL_MS = 5_000;

/** Metadata and Core's connection observation share one read; secrets never enter this cache. */
export function executorConnectionQuery(projectId: string, sessionId: string, environmentId: string) {
  return queryOptions<ExecutorCredentialList>({
    queryKey: [...sessionKey(projectId, sessionId), "executor-connection", environmentId],
    queryFn: async ({ signal }) => {
      const { data, connection } = await admin.listExecutorCredentials(projectId, environmentId, { signal });
      return { connection, data: [...data].sort((a, b) => Number(a.revoked_at !== null) - Number(b.revoked_at !== null) || Date.parse(b.created_at) - Date.parse(a.created_at)) };
    },
    staleTime: EXECUTOR_CONNECTION_POLL_MS,
    refetchInterval: EXECUTOR_CONNECTION_POLL_MS,
    refetchIntervalInBackground: false,
    retry: false,
  });
}
