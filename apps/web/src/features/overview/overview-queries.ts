import { queryOptions } from "@tanstack/react-query";

import { loadSummary, type Project } from "../../lib/admin-view";
import { projectClient } from "../../lib/projects";
import { loadOverview } from "./overview-loader";

/** The Overview's summary and Session reads for the listed projects, keyed by their IDs. */
export function overviewQuery(projects: readonly Project[]) {
  return queryOptions({
    queryKey: ["overview", projects.map((project) => project.id)],
    queryFn: ({ signal }) => loadOverview(projects, {
      summary: (summarySignal) => loadSummary({ signal: summarySignal }),
      sessions: (project) => projectClient(project.id),
    }, Math.floor(Date.now() / 1000), signal),
  });
}
