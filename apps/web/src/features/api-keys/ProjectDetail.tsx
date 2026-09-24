import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import type { AdminKey, Project, ProjectSummary } from "@agents-core-web/agents-client";

import { EmptyState, HelpTip, Kpi, KpiStrip, Section, StatusDot } from "../../components/console-ui";
import { CopyableId, RowActions } from "../../components/list-ui";
import { formatCompact, formatDateTime, formatInteger, formatRelative } from "../../lib/format";
import { useConsoleNavigation } from "../../lib/console-navigation";
import type { ConsoleView } from "../../lib/console-routes";
import { admin } from "../../lib/projects";
import { prefixLabel, sortKeys } from "./key-flows";
import { ProjectSource, ProjectStatus } from "./ProjectStatus";
import { WriteOperations } from "./WriteOperations";

export type Loaded<T> = { status: "loading"; value: T | null } | { status: "ready"; value: T } | { status: "failed"; value: T | null };

/** A project's keys, active first; reloads when `revision` changes. */
export function useProjectKeys(projectId: string | null, revision: number): Loaded<AdminKey[]> & { retry: () => void } {
  const [keys, setKeys] = useState<{ projectId: string | null; loaded: Loaded<AdminKey[]> }>({ projectId, loaded: { status: "loading", value: null } });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!projectId) return;
    const controller = new AbortController();
    // Another project's keys never show while this one loads.
    setKeys((current) => ({ projectId, loaded: { status: "loading", value: current.projectId === projectId ? current.loaded.value : null } }));
    admin.listKeys(projectId, { signal: controller.signal }).then(
      (value) => setKeys({ projectId, loaded: { status: "ready", value: sortKeys(value) } }),
      () => { if (!controller.signal.aborted) setKeys((current) => ({ projectId, loaded: { status: "failed", value: current.loaded.value } })); },
    );
    return () => controller.abort();
  }, [projectId, revision, attempt]);
  const loaded: Loaded<AdminKey[]> = keys.projectId === projectId ? keys.loaded : { status: "loading", value: null };
  return { ...loaded, retry: () => setAttempt((value) => value + 1) };
}

interface Summaries {
  project: ProjectSummary | null;
  /** Per creating key (the `null` entry holds Sessions whose key is unknown); null when unavailable. */
  byKey: Map<string | null, ProjectSummary> | null;
}

function useProjectSummaries(projectId: string, revision: number): Loaded<Summaries> {
  const [state, setState] = useState<Loaded<Summaries>>({ status: "loading", value: null });
  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ status: "loading", value: current.value }));
    Promise.all([
      admin.summary({ project_id: projectId, signal: controller.signal }),
      admin.summary({ project_id: projectId, group_by: "key", signal: controller.signal }).catch(() => null),
    ]).then(
      ([projectRows, keyRows]) => {
        const project = projectRows.find((row) => row.project_id === projectId && row.agent_id === null && row.key === null) ?? null;
        let byKey: Summaries["byKey"] = null;
        if (keyRows) {
          byKey = new Map();
          for (const row of keyRows) if (row.project_id === projectId) byKey.set(row.key?.id ?? null, row);
        }
        setState({ status: "ready", value: { project, byKey } });
      },
      () => { if (!controller.signal.aborted) setState((current) => ({ status: "failed", value: current.value })); },
    );
    return () => controller.abort();
  }, [projectId, revision]);
  return state;
}

const assetLinks: ReadonlyArray<{ key: "agents" | "environment_templates" | "skills" | "files" | "vaults"; view: ConsoleView }> = [
  { key: "agents", view: "agents" },
  { key: "environment_templates", view: "templates" },
  { key: "skills", view: "skills" },
  { key: "files", view: "files" },
  { key: "vaults", view: "vaults" },
];

/**
 * One project: facts, asset counts and usage (each count opens the Resources
 * page filtered to this project), its named keys with usage by key, and its
 * write-operation history.
 */
export function ProjectDetail({ project, keys, revision, busy, onIssue, onRevoke }: {
  project: Project;
  keys: ReturnType<typeof useProjectKeys>;
  /** Changes whenever the project or its keys may have changed. */
  revision: number;
  busy: boolean;
  onIssue: () => void;
  onRevoke: (key: AdminKey, activeCount: number) => void;
}) {
  const { t, i18n } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const summaries = useProjectSummaries(project.id, revision);
  const now = Math.floor(Date.now() / 1000);
  const summary = summaries.value?.project ?? null;
  const pending = summaries.status === "loading" && !summaries.value;
  const figure = (value: number | null | undefined, compact = false) => (pending ? "—" : compact ? formatCompact(value, locale) : formatInteger(value, locale));
  const activeCount = (keys.value ?? []).filter((key) => key.revoked_at === null).length;
  const byKey = summaries.value?.byKey ?? null;
  const unknownUsage = byKey?.get(null) ?? null;
  const usage = summary?.usage ?? null;

  const keyUsage = (row: ProjectSummary | null | undefined) => (
    <>
      {/* With the key summary loaded, a key without a row created no Sessions. */}
      <td className="numeric">{figure(byKey ? row?.sessions.total ?? 0 : null)}</td>
      <td className="numeric">{figure(row?.usage?.total_tokens, true)}</td>
      <td className="key-nowrap" title={row?.last_active_at != null ? formatDateTime(row.last_active_at, locale) : undefined}>{formatRelative(row?.last_active_at, now, locale)}</td>
    </>
  );

  return (
    <>
      <dl className="resource-facts" aria-label={t("detail.facts")}>
        <div><dt>{t("detail.id")}</dt><dd><CopyableId id={project.id} /></dd></div>
        <div><dt>{t("detail.source")}</dt><dd><ProjectSource project={project} /></dd></div>
        <div><dt>{t("detail.status")}</dt><dd><ProjectStatus project={project} /></dd></div>
        <div><dt>{t("detail.created")}</dt><dd>{formatDateTime(project.created_at, locale)}</dd></div>
        {project.archived_at !== null ? <div><dt>{t("detail.archived")}</dt><dd>{formatDateTime(project.archived_at, locale)}</dd></div> : null}
        <div>
          <dt>{t("detail.lastActive")}</dt>
          <dd title={summary?.last_active_at != null ? formatDateTime(summary.last_active_at, locale) : undefined}>{formatRelative(summary?.last_active_at, now, locale)}</dd>
        </div>
      </dl>

      <Section headingId="project-assets-heading" title={t("detail.assets")} help={t("detail.assetsHelp")}>
        {summaries.status === "failed" && !summaries.value ? <p className="page-status" role="status">{t("detail.summaryFailed")}</p> : null}
        <KpiStrip label={t("detail.assets")}>
          {assetLinks.map(({ key, view }) => {
            const label = t(`detail.assetTypes.${key}`);
            return (
              <Kpi
                key={key}
                label={label}
                value={(
                  <button type="button" className="asset-link" aria-label={t("detail.openAssets", { type: label, name: project.name })} onClick={() => navigate(view, { project: project.id })}>
                    {figure(summary?.assets ? summary.assets[key] : null)}
                  </button>
                )}
              />
            );
          })}
        </KpiStrip>
      </Section>

      <Section headingId="project-usage-heading" title={t("detail.usage")} help={t("detail.usageHelp")}>
        <KpiStrip label={t("detail.usage")}>
          <Kpi
            label={t("detail.usageFigures.sessions")}
            value={(
              <button type="button" className="asset-link" aria-label={t("detail.openAssets", { type: t("detail.usageFigures.sessions"), name: project.name })} onClick={() => navigate("sessions", { project: project.id })}>
                {figure(summary?.sessions.total)}
              </button>
            )}
          />
          <Kpi label={t("detail.usageFigures.total")} value={figure(usage?.total_tokens, true)} />
          <Kpi label={t("detail.usageFigures.input")} value={figure(usage?.input_tokens, true)} />
          <Kpi label={t("detail.usageFigures.output")} value={figure(usage?.output_tokens, true)} />
          <Kpi
            label={t("detail.usageFigures.coverage")}
            help={t("detail.usageFigures.coverageHelp")}
            value={summary && !pending ? t("detail.coverageValue", { reported: formatInteger(summary.coverage.reported, locale), total: formatInteger(summary.coverage.sessions, locale) }) : "—"}
          />
        </KpiStrip>
      </Section>

      <Section
        headingId="project-keys-heading"
        title={<>{t("detail.keys")} {keys.value ? <span className="heading-count">{formatInteger(keys.value.length, locale)}</span> : null}</>}
        help={t("detail.keysHelp")}
        actions={project.status === "active" ? (
          <button className="button outline" type="button" onClick={onIssue} disabled={busy}>
            <Plus size={14} aria-hidden="true" />{t("actions.issue")}
          </button>
        ) : undefined}
      >
        {keys.status === "failed" && !keys.value ? (
          <EmptyState title={t("detail.keysFailed")} action={<button className="button outline" type="button" onClick={keys.retry}>{tCommon("actions.retry")}</button>} />
        ) : !keys.value ? (
          <p className="page-status" role="status">{t("detail.keysLoading")}</p>
        ) : !keys.value.length && !unknownUsage ? (
          <EmptyState
            title={t("detail.noKeys")}
            action={project.status === "active" ? <button className="button outline" type="button" onClick={onIssue} disabled={busy}>{t("actions.issue")}</button> : undefined}
          />
        ) : (
          <div className="table-frame">
            <table className="data-table project-keys-table" aria-label={t("detail.keysLabel", { name: project.name })}>
              <thead>
                <tr>
                  <th scope="col">{t("detail.keyColumns.name")}</th>
                  <th scope="col">{t("detail.keyColumns.prefix")}</th>
                  <th scope="col">{t("detail.keyColumns.status")}</th>
                  <th scope="col">{t("detail.keyColumns.created")}</th>
                  <th scope="col" className="numeric">{t("detail.keyColumns.sessions")}</th>
                  <th scope="col" className="numeric"><span className="column-help">{t("detail.keyColumns.tokens")}<HelpTip>{t("detail.keyColumns.usageHelp")}</HelpTip></span></th>
                  <th scope="col">{t("detail.keyColumns.lastActive")}</th>
                  <th scope="col"><span className="visually-hidden">{t("list.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {keys.value.map((key) => {
                  const revoked = key.revoked_at !== null;
                  return (
                    <tr key={key.id} className={revoked ? "key-revoked" : undefined}>
                      <th scope="row">
                        <span className="name-cell">
                          <span className="name-cell-title"><span>{key.name}</span></span>
                          <CopyableId id={key.id} compact label={t("detail.copyKeyId")} />
                        </span>
                      </th>
                      <td><code className="key-prefix">{prefixLabel(key.prefix)}</code></td>
                      <td>
                        <span className="key-status">
                          <StatusDot tone={revoked ? "neutral" : "ok"} label={revoked ? t("detail.keyStatus.revoked") : t("detail.keyStatus.active")} />
                          {revoked ? <span className="key-status-date">{formatDateTime(key.revoked_at, locale)}</span> : null}
                        </span>
                      </td>
                      <td className="key-nowrap">{formatDateTime(key.created_at, locale)}</td>
                      {keyUsage(byKey?.get(key.id))}
                      <td className="actions-cell">
                        {!revoked && project.status === "active" ? (
                          <RowActions>
                            <button className="text-action danger" type="button" aria-label={t("actions.revokeLabel", { name: key.name })} disabled={busy} onClick={() => onRevoke(key, activeCount)}>
                              {t("actions.revoke")}
                            </button>
                          </RowActions>
                        ) : null}
                      </td>
                    </tr>
                  );
                })}
                {unknownUsage && unknownUsage.sessions.total > 0 ? (
                  <tr className="key-unknown">
                    <th scope="row"><span className="column-help">{t("detail.unknownKey")}<HelpTip>{t("detail.unknownKeyHelp")}</HelpTip></span></th>
                    <td><span className="table-muted">—</span></td>
                    <td><span className="table-muted">—</span></td>
                    <td><span className="table-muted">—</span></td>
                    {keyUsage(unknownUsage)}
                    <td className="actions-cell" />
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        )}
      </Section>

      <WriteOperations projectId={project.id} keys={keys.value} refreshToken={revision} />
    </>
  );
}
