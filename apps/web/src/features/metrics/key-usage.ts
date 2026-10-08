import { type ProjectSummary } from "../../lib/admin-view";

/**
 * Rows of `/summary?group_by=key`: Sessions created in the range, grouped by
 * the API key that created them. A null key collects Sessions without a
 * creation record ("unknown"); those rows are listed last.
 */
export interface KeyUsageRow {
  /** Stable row identity: the key ID, or the project's unknown bucket. */
  id: string;
  summary: ProjectSummary;
}

export function keyUsageRows(rows: readonly ProjectSummary[]): KeyUsageRow[] {
  return rows
    .filter((row) => row.agent_id === null && row.sessions.total > 0)
    .map((summary) => ({ id: `${summary.project_id}:${summary.key_id ?? "unknown"}`, summary }))
    .sort((a, b) => (
      Number(a.summary.key_id === null) - Number(b.summary.key_id === null)
      || (b.summary.usage?.total_tokens ?? -1) - (a.summary.usage?.total_tokens ?? -1)
      || b.summary.sessions.total - a.summary.sessions.total
      || a.id.localeCompare(b.id)
    ));
}
