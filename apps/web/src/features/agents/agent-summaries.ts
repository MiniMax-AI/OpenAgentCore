import { queryOptions, useQueries, type UseQueryResult } from "@tanstack/react-query";

import { loadSummary, type ProjectSummary } from "../../lib/admin-view";
import { queryClient } from "../../lib/queries";

const AGENT_SUMMARY_KEY = ["summary", "agent"] as const;

/** One project's usage summary grouped by Agent. */
export function agentSummaryQuery(projectId: string) {
  return queryOptions({
    queryKey: [...AGENT_SUMMARY_KEY, projectId],
    queryFn: ({ signal }) => loadSummary({ group_by: "agent", project_id: projectId, signal }),
  });
}

/** Marks every cached per-Agent summary stale; mounted lists read theirs again. */
export function refreshAgentSummaries(): Promise<void> {
  return queryClient.invalidateQueries({ queryKey: AGENT_SUMMARY_KEY });
}

function byAgent(results: UseQueryResult<ProjectSummary[]>[]): Map<string, ProjectSummary> {
  const rows = new Map<string, ProjectSummary>();
  for (const result of results) {
    for (const row of result.data ?? []) if (row.agent_id) rows.set(`${row.project_id}:${row.agent_id}`, row);
  }
  return rows;
}

/**
 * Per-Agent usage of the listed projects, from the Web API summary, keyed by
 * `project:agent`. Each project is cached on its own; a project whose summary
 * fails leaves its Agents' figures missing.
 */
export function useAgentSummaries(projectIds: readonly string[]): Map<string, ProjectSummary> {
  return useQueries({
    queries: projectIds.map((projectId) => agentSummaryQuery(projectId)),
    combine: byAgent,
  });
}
