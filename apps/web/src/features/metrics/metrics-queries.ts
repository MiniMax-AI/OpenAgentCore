import { queryOptions } from "@tanstack/react-query";
import { CoreMetricsClient, type CoreMetricsRange } from "@agents-core-web/agents-client";

import { listRuntimeObservations, loadSummary, type Project } from "../../lib/admin-view";
import { projectClient } from "../../lib/projects";
import { aggregateAgentMetrics, metricsWindow, type AgentMetrics, type AgentMetricsRange } from "./agent-metrics";
import { loadProjectAgentMetrics } from "./agent-metrics-loader";
import { keyUsageRows } from "./key-usage";
import type { ProjectReadFailure } from "./project-sessions";
import { loadHostedRuntimes } from "./sandbox-runtime";

/** Hosted Runtimes of every project and the Sessions they belong to. */
export const hostedRuntimesQuery = queryOptions({
  queryKey: ["hosted-runtimes"],
  queryFn: ({ signal }) => loadHostedRuntimes({
    observations: (observationSignal) => listRuntimeObservations(observationSignal),
    sessionReader: (projectId) => projectClient(projectId),
  }, signal),
});

export interface LoadedAgentMetrics {
  metrics: AgentMetrics;
  truncatedLists: Project[];
  listFailures: ProjectReadFailure[];
  unrecognizedSessions: number;
  /** Epoch milliseconds. */
  loadedAt: number;
}

/**
 * Agent metrics of the chosen projects over a range. The window is placed at
 * read time; the key holds every input: the project filter, the range and the
 * projects read.
 */
export function agentMetricsQuery(targets: readonly Project[], filter: string, range: AgentMetricsRange) {
  return queryOptions({
    queryKey: ["agent-metrics", { project: filter, range }, targets.map((project) => project.id)],
    queryFn: async ({ signal }): Promise<LoadedAgentMetrics> => {
      const window = metricsWindow(range, Math.floor(Date.now() / 1000));
      // The summary says which projects were active; a failed summary only means every project is read.
      const summary = await loadSummary({ project_id: filter || undefined, signal }).catch((error: unknown) => {
        if (signal.aborted) throw error;
        return null;
      });
      const load = await loadProjectAgentMetrics(targets, window, { clientFor: (project) => projectClient(project.id), summary }, signal, { includeTools: true });
      return {
        metrics: aggregateAgentMetrics(window, load.activities, load.coverage),
        truncatedLists: load.truncatedLists,
        listFailures: load.listFailures,
        unrecognizedSessions: load.unrecognizedSessions,
        loadedAt: Date.now(),
      };
    },
  });
}

/** Usage by creating API key of the Sessions created in the range. */
export function keyUsageQuery(filter: string, range: AgentMetricsRange) {
  return queryOptions({
    queryKey: ["key-usage", { project: filter, range }],
    queryFn: async ({ signal }) => {
      const window = metricsWindow(range, Math.floor(Date.now() / 1000));
      return keyUsageRows(await loadSummary({ group_by: "key", created_after: window.start, project_id: filter || undefined, signal }));
    },
  });
}

const coreMetricsClient = new CoreMetricsClient({ baseUrl: "/core/v1/admin" });

/** Core's own metrics over a range, aggregated by Core. */
export function coreMetricsQuery(range: CoreMetricsRange) {
  return queryOptions({
    queryKey: ["core-metrics", range],
    queryFn: ({ signal }) => coreMetricsClient.retrieveCoreMetrics(range, { signal }),
  });
}
