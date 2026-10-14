import { Server } from "lucide-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  EmptyState,
  HelpTip,
  Kpi,
  KpiStrip,
  Meter,
  PageBody,
  PageHeader,
  RefreshButton,
  Section,
  SegmentedControl,
  StatusDot,
  type Tone,
} from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { TableSkeleton } from "../../components/Skeleton";
import { useFailureToast } from "../../components/Toast";
import { ListToolbar, listSummary, NameCell, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatBytes, formatClock, formatCompact, formatCores, formatDuration, formatInteger, formatPercent, MISSING } from "../../lib/format";
import { projectClient, ProjectName, useProjects } from "../../lib/projects";
import { readError } from "../../lib/read-error";
import { sandboxStateLabel } from "../../lib/sandbox-labels";
import { loadRuntimeDurableSnapshot, RUNTIME_DURABLE_RANGES, RuntimeDurableHistoryIncompleteError, type RuntimeDurableRange } from "../dashboard/runtime-history";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { RUNTIME_SNAPSHOT_REFRESH_MS } from "../dashboard/runtime-snapshot";
import { FleetReadNotice } from "../fleet/FleetReadNotice";
import { SandboxResetNotice } from "../fleet/SandboxResetNotice";
import { fleetSnapshot, useSandboxFleet } from "../fleet/use-sandbox-fleet";
import { sandboxSize, templateBuildStatus } from "../sandbox/deployment-specification";
import {
  hostedRuntimeRows,
  hostedRuntimeUsage,
  matchesRuntime,
  runtimeSnapshot,
  type HostedRuntimeLoad,
  type HostedRuntimeRow,
} from "./sandbox-runtime";
import "./MetricsView.css";
import { RuntimeCharts } from "./RuntimeCharts";
import { hostedRuntimesQuery } from "./metrics-queries";
import { SessionRuntimeSection } from "../sessions/SessionRuntimeSection";
import { sessionTitle } from "../../lib/session-title";
import type { SandboxDeployment } from "@oac/agents-client";

type RuntimeState =
  | { status: "loading"; load: HostedRuntimeLoad | null }
  | { status: "ready"; load: HostedRuntimeLoad }
  | { status: "failed"; load: HostedRuntimeLoad | null; error: string };

/** Hosted Runtimes through the query cache, polled while the page is visible; a refresh keeps the last load on screen. */
function useHostedRuntimes() {
  const query = useQuery({
    ...hostedRuntimesQuery,
    refetchInterval: RUNTIME_SNAPSHOT_REFRESH_MS,
    refetchIntervalInBackground: false,
  });
  const load = query.data ?? null;
  let state: RuntimeState;
  const error = query.isError ? (query.error instanceof Error ? query.error.message : String(query.error)) : null;
  if (error !== null && !query.isFetching) state = { status: "failed", load, error };
  else if (load && !query.isFetching) state = { status: "ready", load };
  else state = { status: "loading", load };
  const { refetch } = query;
  const refresh = useCallback(() => { void refetch(); }, [refetch]);
  // The failure lasts through the polls that retry it, so its toast shows once.
  return { state, refresh, stale: load ? error : null };
}

/** Durable history of each hosted Session, read through the Session's project. */
const loadHistory = (snapshot: RuntimeDashboardSnapshot, range: RuntimeDurableRange, signal: AbortSignal) => loadRuntimeDurableSnapshot((sessionId) => {
  const projectId = snapshot.owners?.get(sessionId);
  return projectId ? projectClient(projectId) : null;
}, snapshot, range, signal);

export function SandboxMetricsPage() {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const { state: fleetState, refresh: refreshFleet, deployment } = useSandboxFleet({ allocations: true });
  const { state: runtimeState, refresh: refreshRuntime, stale: runtimeStale } = useHostedRuntimes();
  const fleet = fleetSnapshot(fleetState);
  const refreshing = runtimeState.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = runtimeState.load?.loadedAt ?? fleet?.loadedAt ?? null;
  const [range, setRange] = useState<RuntimeDurableRange>(RUNTIME_DURABLE_RANGES[0]);
  // A sandbox opens in a dialog with its own figures, instead of leaving the page.
  const [openRuntime, setOpenRuntime] = useState<string | null>(null);
  const rows = useMemo(() => (runtimeState.load ? hostedRuntimeRows(runtimeState.load, "", fleet) : []), [fleet, runtimeState.load]);
  // A direct Provider runs sandboxes in its cloud: no machines, so no node column.
  const cloud = fleet?.deployment.mode === "direct";
  // Only a Provider that declares checkpoint support records a sandbox's compute phase.
  const suspends = Boolean(fleet?.deployment.suspension);

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="sandbox-metrics-heading">
      <PageHeader
        headingId="sandbox-metrics-heading"
        title={t("sandbox.title")}
        help={t(cloud ? "sandbox.cloudDescription" : "sandbox.description")}
        actions={<>
          <SegmentedControl
            label={t("range.label")}
            value={range}
            options={RUNTIME_DURABLE_RANGES.map((entry) => ({ value: entry, label: t(`range.${entry}`) }))}
            onChange={setRange}
          />
          <RefreshButton refreshing={refreshing} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { refreshRuntime(); refreshFleet(); }} />
        </>}
      />
      <PageBody>
        <SandboxResetNotice deployment={deployment.data} failed={deployment.isError} onRetry={() => void deployment.refetch()} />
        <FleetReadNotice state={fleetState} onRetry={refreshFleet} />
        {fleet && !fleet.deployment.provider ? (
          <EmptyState
            icon={Server}
            title={t("sandbox.notSetUpTitle")}
            action={<button className="button primary" type="button" onClick={() => navigate("settings", { id: "sandbox" })}>{t("sandbox.setUp")}</button>}
          />
        ) : <>
          {cloud && fleet ? <CloudSection deployment={fleet.deployment} /> : null}
          <HostedRuntimeSection state={runtimeState} stale={runtimeStale} fleet={fleet} range={range} onOpen={setOpenRuntime} />
        </>}
      </PageBody>
      <RuntimeDialog row={rows.find((row) => row.observation.session_id === openRuntime) ?? null} showNode={!cloud} showCompute={suspends} onClose={() => setOpenRuntime(null)} />
    </section>
  );
}

/** An E2B deployment: what runs in its cloud now, and with what. */
function CloudSection({ deployment }: { deployment: SandboxDeployment }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  // An E2B selection may adopt its template build's size instead of saving one.
  const resources = sandboxSize(deployment);
  const build = deployment.metadata?.template_build;
  const disk = build?.resources.root_disk_mib ?? null;
  const status = templateBuildStatus(build);
  const template = deployment.configuration?.template;
  return (
    <Section
      headingId="cloud-heading"
      title={t("sandbox.cloud.title")}
      help={t("sandbox.cloud.help")}
      actions={<button className="button outline" type="button" onClick={() => navigate("settings", { id: "sandbox" })}>{t("sandbox.cloud.manage")}</button>}
    >
      <p className="detail-note">{t("sandbox.cloud.counts")}</p>
      <KpiStrip label={t("sandbox.cloud.title")}>
        <Kpi label={t("sandbox.cloud.running")} help={t("sandbox.cloud.runningHelp")} value={formatInteger(deployment.resources.allocations, locale)} />
        <Kpi label={t("sandbox.cloud.pending")} value={formatInteger(deployment.resources.pending, locale)} />
        <Kpi
          label={t("sandbox.cloud.size")}
          value={resources ? <>
            {t("sandbox.cores", { value: formatInteger(resources.cpus, locale) })} · {formatBytes(resources.memory_mib * 2 ** 20)}
            {disk !== null ? <span className="kpi-unit">{t("sandbox.cloud.disk", { disk: formatBytes(disk * 2 ** 20) })}</span> : null}
          </> : MISSING}
        />
        <Kpi
          label={t("sandbox.cloud.template")}
          help={t("sandbox.cloud.templateHelp")}
          tone={status === "notReady" ? "warning" : undefined}
          value={<>
            {t(`sandbox.cloud.build.${status}`)}
            {template ? <code className="kpi-unit cloud-template" title={template}>{template}</code> : null}
          </>}
        />
      </KpiStrip>
    </Section>
  );
}

/** Durable Runtime history of the snapshot's hosted Sessions over a range, read through each Session's project. */
function useRuntimeHistory(snapshot: RuntimeDashboardSnapshot | null, range: RuntimeDurableRange) {
  const targets = useMemo(() => snapshot?.observations
    .filter((observation) => observation.mode === "openai_hosted" && observation.environment_id !== null)
    .map((observation) => observation.session_id)
    .sort()
    .join("|") ?? "", [snapshot]);
  return useQuery({
    queryKey: ["runtime-history", range, targets],
    queryFn: ({ signal }) => loadHistory(snapshot!, range, signal),
    enabled: snapshot !== null && targets !== "",
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
    // One read per Session per poll: the 30-second poll is the retry, and a
    // retry here would read every Session again.
    retry: false,
  });
}

/** Core's typed errors and Web's own read bound, in the console's language. */
function historyFailure(error: unknown, t: TFunction<"metrics">, tCommon: TFunction<"common">): string {
  if (!(error instanceof RuntimeDurableHistoryIncompleteError)) return readError(error, tCommon);
  return error.limit === null ? t("sandbox.charts.historyMixed") : t("sandbox.charts.historyBudget", { limit: error.limit });
}

function HostedRuntimeSection({ state, stale, fleet, range, onOpen }: { state: RuntimeState; stale: string | null; fleet: ReturnType<typeof fleetSnapshot>; range: RuntimeDurableRange; onOpen: (sessionId: string) => void }) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const load = state.load;
  const usage = useMemo(() => (load ? hostedRuntimeUsage(load.observations) : null), [load]);
  const snapshot = useMemo<RuntimeDashboardSnapshot | null>(() => (load ? runtimeSnapshot(load, "") : null), [load]);
  const history = useRuntimeHistory(snapshot, range);
  const partial = load && (load.unread || load.failed) ? t("sandbox.runtimePartial", { unread: load.unread, failed: load.failed }) : null;
  useFailureToast(Boolean(usage?.hosted && stale), t("sandbox.runtimeStale", { reason: stale ?? "" }), "sandbox-runtimes-refresh");

  let body;
  if (!load || !usage) {
    body = state.status === "failed"
      ? <p className="page-status" role="alert">{t("sandbox.runtimeUnavailable", { reason: state.error })}</p>
      : <TableSkeleton label={t("sandbox.runtimeLoading")} rows={4} columns={8} />;
  } else if (!usage.hosted) {
    body = <EmptyState title={t("sandbox.noRuntimeTitle")} hint={t("sandbox.noRuntimeDescription")} />;
  } else {
    const durable = history.data ?? null;
    body = (
      <>
        {durable ? <RuntimeCharts samples={durable.samples} resolutionSeconds={durable.resolutionSeconds} />
          : history.isError ? <p className="page-status" role="alert">{t("sandbox.charts.historyFailed", { reason: historyFailure(history.error, t, tCommon) })}</p>
            : history.isFetched ? <p className="page-status">{t("sandbox.charts.historyUnavailable")}</p> : null}
        <RuntimeTable rows={hostedRuntimeRows(load, "", fleet)} showProject showNode={fleet?.deployment.mode !== "direct"} showCompute={Boolean(fleet?.deployment.suspension)} onOpen={onOpen} />
      </>
    );
  }

  return (
    <Section
      headingId="runtime-heading"
      title={<>
        {t("sandbox.runtimeSection")}
        {usage?.hosted ? (
          <span className="section-meta">
            {t(fleet?.deployment.suspension ? "sandbox.runtimeMetaSuspended" : "sandbox.runtimeMeta", {
              active: formatInteger(usage.active, locale),
              n: formatInteger(usage.hosted, locale),
              sleeping: formatInteger(usage.sleeping, locale),
              cpu: usage.cpuUsageCores === null ? MISSING : t("sandbox.cores", { value: formatCores(usage.cpuUsageCores, locale) }),
              memory: usage.memoryUsageBytes === null ? MISSING : formatBytes(usage.memoryUsageBytes),
            })}
          </span>
        ) : null}
      </>}
      help={t("sandbox.runtimeSectionDetail")}
      actions={partial ? (
        <span className="partial-chip">
          <StatusDot tone="warning" label={t("coverage.partial")} />
          <HelpTip>{partial}</HelpTip>
        </span>
      ) : undefined}
    >
      {body}
    </Section>
  );
}

function lifecycleTone(row: HostedRuntimeRow): Tone {
  if (row.observation.status === "unavailable") return "warning";
  switch (row.observation.lifecycle_state) {
    case "active": return "ok";
    case "transitioning":
    case "pending": return "pending";
    default: return "neutral";
  }
}

function lifecycleLabel(row: HostedRuntimeRow, t: TFunction<"metrics">): string {
  if (row.observation.status === "unavailable") return t(`sandbox.reason.${row.observation.reason}`);
  return t(`sandbox.lifecycle.${row.observation.lifecycle_state ?? "stopped"}`);
}

function RuntimeTable({ rows, showProject, showNode, showCompute, onOpen }: { rows: HostedRuntimeRow[]; showProject: boolean; showNode: boolean; showCompute: boolean; onOpen: (sessionId: string) => void }) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { byId } = useProjects();
  const [query, setQuery] = useState("");
  const visible = rows.filter((row) => matchesRuntime(row, query));
  return (
    <div className="runtime-list">
      <ListToolbar label={t("sandbox.runtimeList")} summary={listSummary(tCommon, visible.length, rows.length, { locale })}>
        <SearchField value={query} onChange={setQuery} placeholder={t(showNode ? "sandbox.searchPlaceholder" : "sandbox.searchPlaceholderCloud")} label={t("sandbox.search")} />
      </ListToolbar>
      <div className="table-frame">
        <table className="data-table" aria-label={t("sandbox.runtimeList")}>
          <thead>
            <tr>
              <th scope="col">{t("sandbox.session")}</th>
              {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
              {showNode ? <th scope="col">{t("sandbox.node")}</th> : null}
              <th scope="col">{t("sandbox.status")}</th>
              {showCompute ? <th scope="col"><span className="column-help">{t("sandbox.recordedCompute")}<HelpTip>{t("sandbox.recordedComputeHelp")}</HelpTip></span></th> : null}
              <th scope="col">{t("sandbox.cpu")}</th>
              <th scope="col">{t("sandbox.memory")}</th>
              <th scope="col" className="numeric">{t("sandbox.uptime")}</th>
              <th scope="col" className="numeric">{t("sandbox.tokens")}</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((row) => {
              const { observation, session } = row;
              const observed = observation.status === "observed";
              const cpu = observed ? observation.cpu : null;
              const memory = observed ? observation.memory : null;
              const open = () => onOpen(observation.session_id);
              return (
                <tr key={`${observation.project_id}:${observation.session_id}`} className="clickable-row" onClick={open}>
                  <th scope="row">
                    <NameCell name={sessionTitle(session)} id={observation.session_id} fallback={tCommon("untitledSession")} onOpen={open} openLabel={t("sandbox.runtimeDialog.openLabel", { name: sessionTitle(session) ?? observation.session_id })} />
                  </th>
                  {showProject ? <td><ProjectName project={byId.get(observation.project_id)} /></td> : null}
                  {showNode ? <td><NodeLink node={row.node} /></td> : null}
                  <td><StatusDot tone={lifecycleTone(row)} label={lifecycleLabel(row, t)} /></td>
                  {showCompute ? <td>{row.allocation ? sandboxStateLabel(row.allocation.compute_phase, locale?.startsWith("zh") ? "zh-CN" : "en") : MISSING}</td> : null}
                  <td>
                    {cpu?.usage_cores != null ? (
                      <span className="table-meter">
                        <Meter value={cpu.usage_cores} limit={cpu.capacity_cores} label={t("sandbox.cpu")} />
                        <span>{formatCores(cpu.usage_cores, locale)} / {t("sandbox.cores", { value: formatCores(cpu.capacity_cores, locale) })}</span>
                      </span>
                    ) : cpu?.utilization_ratio != null ? (
                      // Providers that report only a utilization ratio: show it as a share of the sandbox's CPU.
                      <span className="table-meter">
                        <Meter value={cpu.utilization_ratio} limit={1} label={t("sandbox.cpu")} />
                        <span>{formatPercent(cpu.utilization_ratio, locale)}</span>
                      </span>
                    ) : <span className="table-muted">{MISSING}</span>}
                  </td>
                  <td>
                    {memory?.usage_bytes != null ? (
                      <span className="table-meter">
                        <Meter value={memory.usage_bytes} limit={memory.limit_bytes} label={t("sandbox.memory")} />
                        <span>{formatBytes(memory.usage_bytes)} / {formatBytes(memory.limit_bytes)}</span>
                      </span>
                    ) : <span className="table-muted">{MISSING}</span>}
                  </td>
                  <td className="numeric">{formatDuration(row.uptimeSeconds)}</td>
                  <td className="numeric" title={session?.usage ? t("sandbox.tokenSplit", { input: formatInteger(session.usage.input_tokens, locale), output: formatInteger(session.usage.output_tokens, locale) }) : undefined}>
                    {session?.usage ? formatCompact(session.usage.total_tokens, locale) : MISSING}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {!visible.length ? <p className="runtime-list-empty">{tCommon("list.noMatches")}</p> : null}
      </div>
    </div>
  );
}

/** A Runtime's host, opening its page on Nodes. */
function NodeLink({ node }: { node: HostedRuntimeRow["node"] }) {
  const { t } = useTranslation("metrics");
  const { navigate } = useConsoleNavigation();
  if (!node) return <span className="table-muted">{MISSING}</span>;
  const name = node.name || node.id;
  return <button type="button" className="table-link" aria-label={t("sandbox.openNode", { name })} onClick={(event) => { event.stopPropagation(); navigate("nodes", { id: node.id }); }}><strong>{name}</strong></button>;
}

/** Keeps the last value on screen while a dialog closes. */
function useLast<T>(value: T | null): T | null {
  const last = useRef(value);
  if (value !== null) last.current = value;
  return value ?? last.current;
}

/** A hosted sandbox in a dialog: where it runs, and its Session's Runtime state and history. */
function RuntimeDialog({ row, showNode, showCompute, onClose }: { row: HostedRuntimeRow | null; showNode: boolean; showCompute: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const { navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const shown = useLast(row);
  const observation = shown?.observation ?? null;
  return (
    <Modal
      open={row !== null}
      wide
      title={shown ? sessionTitle(shown.session) ?? shown.observation.session_id : ""}
      onClose={onClose}
      footer={observation ? <button className="button outline" type="button" onClick={() => navigate("session", { project: observation.project_id, id: observation.session_id })}>{t("sandbox.runtimeDialog.openSession")}</button> : undefined}
    >
      {shown && observation ? (
        <div className="metrics-dialog">
          <dl className="resource-facts" aria-label={t("sandbox.runtimeDialog.facts")}>
            <div><dt>{tCommon("project.column")}</dt><dd><ProjectName project={byId.get(observation.project_id)} /></dd></div>
            {showNode ? <div><dt>{t("sandbox.node")}</dt><dd><NodeLink node={shown.node} /></dd></div> : null}
            <div><dt>{t("sandbox.status")}</dt><dd><StatusDot tone={lifecycleTone(shown)} label={lifecycleLabel(shown, t)} /></dd></div>
            {showCompute && shown.allocation ? <div><dt>{t("sandbox.recordedCompute")}</dt><dd>{sandboxStateLabel(shown.allocation.compute_phase, i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en")}</dd></div> : null}
            <div><dt>{t("sandbox.configurationGeneration")}</dt><dd>{shown.deploymentGeneration ?? MISSING}</dd></div>
            <div><dt>{t("sandbox.uptime")}</dt><dd>{formatDuration(shown.uptimeSeconds)}</dd></div>
            {/* Only E2B reports a sandbox's disk. */}
            {observation.disk ? <div><dt>{t("sandbox.disk")}</dt><dd>{formatBytes(observation.disk.usage_bytes)} / {formatBytes(observation.disk.limit_bytes)}</dd></div> : null}
          </dl>
          {shown.session
            ? <SessionRuntimeSection projectId={observation.project_id} session={shown.session} active revision={0} refreshToken={0} />
            : <p className="page-status">{t("sandbox.runtimeDialog.noSession")}</p>}
        </div>
      ) : null}
    </Modal>
  );
}
