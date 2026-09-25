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
import { formatBytes, formatClock, formatCompact, formatCores, formatDateTime, formatDuration, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { projectClient, ProjectName, useProjects } from "../../lib/projects";
import { nodeProviderDiagnostic } from "../../lib/sandbox-diagnostic";
import { loadRuntimeDurableSnapshot, RUNTIME_DURABLE_RANGES, type RuntimeDurableRange } from "../dashboard/runtime-history";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { RUNTIME_SNAPSHOT_REFRESH_MS } from "../dashboard/runtime-snapshot";
import { DiagnosticTip } from "../fleet/DiagnosticTip";
import { capacitySummary, nodeHealth, suspendedSandboxes, type NodeHealth } from "../fleet/fleet-model";
import { nodeDetailQuery } from "../fleet/fleet-queries";
import { fleetSnapshot, useSandboxFleet, type FleetState } from "../fleet/use-sandbox-fleet";
import { sandboxSize, templateBuildStatus } from "../sandbox/deployment-specification";
import { formatShare, NodeHostCharts } from "./NodeHostCharts";
import {
  hostedRuntimeRows,
  hostedRuntimeUsage,
  matchesRuntime,
  runtimeSnapshot,
  runtimeSnapshotWhere,
  sessionTitle,
  type HostedRuntimeLoad,
  type HostedRuntimeRow,
} from "./sandbox-runtime";
import "./MetricsView.css";
import { RuntimeCharts } from "./RuntimeCharts";
import { hostedRuntimesQuery } from "./metrics-queries";
import { SessionRuntimeSection } from "../sessions/SessionRuntimeSection";
import type { SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";

const healthTone: Record<NodeHealth, Tone> = { available: "ok", degraded: "warning", offline: "danger" };

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

function fleetMessage(state: FleetState, t: TFunction<"metrics">): string {
  if (state.status === "unconfigured") return t("sandbox.fleetUnconfigured");
  if (state.status === "failed") return t("sandbox.fleetFailed");
  return t("sandbox.fleetLoading");
}

export function SandboxMetricsPage() {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const { state: fleetState, refresh: refreshFleet } = useSandboxFleet({ allocations: true });
  const { state: runtimeState, refresh: refreshRuntime, stale: runtimeStale } = useHostedRuntimes();
  const fleet = fleetSnapshot(fleetState);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const now = Math.floor(Date.now() / 1000);
  const refreshing = runtimeState.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = runtimeState.load?.loadedAt ?? fleet?.loadedAt ?? null;
  const message = fleetMessage(fleetState, t);
  const [range, setRange] = useState<RuntimeDurableRange>(RUNTIME_DURABLE_RANGES[0].milliseconds);
  // A node or a sandbox opens in a dialog with its own figures, instead of leaving the page.
  const [openNode, setOpenNode] = useState<string | null>(null);
  const [openRuntime, setOpenRuntime] = useState<string | null>(null);
  const rows = useMemo(() => (runtimeState.load ? hostedRuntimeRows(runtimeState.load, "", fleet) : []), [fleet, runtimeState.load]);
  // E2B runs sandboxes in its cloud: no machines, so no node table, node column or node dialog.
  const cloud = fleet?.deployment.provider === "e2b";
  // Only microsandbox suspends sandboxes into snapshots; its nodes also show how many sleep.
  const suspends = fleet?.deployment.provider === "microsandbox";

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="sandbox-metrics-heading">
      <PageHeader
        headingId="sandbox-metrics-heading"
        title={t("sandbox.title")}
        help={t("sandbox.description")}
        actions={<>
          <SegmentedControl
            label={t("range.label")}
            value={String(range)}
            options={RUNTIME_DURABLE_RANGES.map((entry) => ({ value: String(entry.milliseconds), label: t(`range.${entry.label}`) }))}
            onChange={(value) => setRange(Number(value) as RuntimeDurableRange)}
          />
          <RefreshButton refreshing={refreshing} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { refreshRuntime(); refreshFleet(); }} />
        </>}
      />
      <PageBody>
        {cloud && fleet ? <CloudSection deployment={fleet.deployment} /> : <Section
          headingId="node-capacity-heading"
          title={t("sandbox.node")}
          help={t("sandbox.nodesSectionDetail")}
          actions={<button className="button outline" type="button" onClick={() => navigate("nodes")}>{t("sandbox.manageNodes")}</button>}
        >
          {fleet ? fleet.nodes.length ? (
            <div className="table-frame">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">{t("sandbox.node")}</th>
                    <th scope="col">{t("sandbox.status")}</th>
                    <th scope="col">{t("sandbox.slots")}</th>
                    {suspends ? <th scope="col" className="numeric"><span className="column-help">{t("sandbox.suspended")}<HelpTip>{t("sandbox.suspendedHelp")}</HelpTip></span></th> : null}
                    <th scope="col" className="numeric">{t("sandbox.cpus")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeMemory")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeDiskColumn")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cleanupPending")}</th>
                    <th scope="col" className="numeric">{t("sandbox.lastSeen")}</th>
                  </tr>
                </thead>
                <tbody>
                  {fleet.nodes.map((node) => (
                    <tr key={node.id} className="clickable-row" onClick={() => setOpenNode(node.id)}>
                      <th scope="row"><NameCell name={node.name} id={node.id} onOpen={() => setOpenNode(node.id)} openLabel={t("sandbox.nodeDialog.openLabel", { name: node.name || node.id })} /></th>
                      <td><NodeHealthStatus node={node} /></td>
                      <td>
                        <span className="table-meter">
                          <Meter value={node.active} limit={node.max_active} label={t("sandbox.slotsOf", { name: node.name })} />
                          <span>{node.active} / {node.max_active}</span>
                        </span>
                      </td>
                      {suspends ? <td className="numeric">{suspendedSandboxes(node)}</td> : null}
                      <td className="numeric">{node.online ? node.cpu_count ?? MISSING : MISSING}</td>
                      <td className="numeric">{node.online ? formatBytes(node.available_memory_bytes) : MISSING}</td>
                      <td className="numeric">{node.online ? formatBytes(node.available_disk_bytes) : MISSING}</td>
                      <td className={node.cleanup_pending > 0 ? "numeric numeric-warning" : "numeric"}>{node.cleanup_pending}</td>
                      <td className="numeric">{formatRelative(node.last_seen_at ? Date.parse(node.last_seen_at) / 1000 : null, now, locale)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              icon={Server}
              title={t("sandbox.noNodesTitle")}
              hint={t("sandbox.noNodesDescription")}
              action={<button className="button primary" type="button" onClick={() => navigate("nodes")}>{t("sandbox.addNode")}</button>}
            />
          ) : fleetState.status === "checking" || fleetState.status === "loading"
            ? <TableSkeleton label={message} rows={3} columns={suspends ? 9 : 8} />
            : <p className="page-status" role={fleetState.status === "failed" ? "alert" : "status"}>{message}</p>}
        </Section>}

        <HostedRuntimeSection state={runtimeState} stale={runtimeStale} fleet={fleet} range={range} onOpen={setOpenRuntime} />
      </PageBody>
      <NodeDialog
        node={fleet?.nodes.find((node) => node.id === openNode) ?? null}
        rows={rows}
        load={runtimeState.load}
        range={range}
        onClose={() => setOpenNode(null)}
      />
      <RuntimeDialog row={rows.find((row) => row.observation.session_id === openRuntime) ?? null} showNode={!cloud} onClose={() => setOpenRuntime(null)} />
    </section>
  );
}

/** A node's health; a degraded node names the reason its provider is not ready behind the help tip. */
function NodeHealthStatus({ node }: { node: SandboxNode }) {
  const { t } = useTranslation("metrics");
  const health = nodeHealth(node);
  const diagnostic = health === "degraded" ? nodeProviderDiagnostic(node) : "";
  return (
    <span className="status-with-help">
      <StatusDot tone={healthTone[health]} label={t(`sandbox.health.${health}`)} />
      {/* The tip opens on its own, not the row's node dialog. */}
      {diagnostic ? <span className="status-with-help" onClick={(event) => event.stopPropagation()}><DiagnosticTip code={diagnostic} /></span> : null}
    </span>
  );
}

/** An E2B deployment in place of the node table: what runs in its cloud now, and with what. */
function CloudSection({ deployment }: { deployment: SandboxDeployment }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  // An E2B selection may adopt its template build's size instead of saving one.
  const resources = sandboxSize(deployment);
  const build = deployment.e2b?.template_build;
  const disk = build?.resources.root_disk_mib ?? null;
  const status = templateBuildStatus(build);
  const template = deployment.e2b?.template;
  return (
    <Section
      headingId="cloud-heading"
      title={t("sandbox.cloud.title")}
      help={t("sandbox.cloud.help")}
      actions={<button className="button outline" type="button" onClick={() => navigate("nodes")}>{t("sandbox.cloud.manage")}</button>}
    >
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
  });
}

function HostedRuntimeSection({ state, stale, fleet, range, onOpen }: { state: RuntimeState; stale: string | null; fleet: ReturnType<typeof fleetSnapshot>; range: RuntimeDurableRange; onOpen: (sessionId: string) => void }) {
  const { t, i18n } = useTranslation("metrics");
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
          : history.isError ? <p className="page-status" role="alert">{t("sandbox.charts.historyFailed", { reason: history.error instanceof Error ? history.error.message : "" })}</p>
            : history.isFetched ? <p className="page-status">{t("sandbox.charts.historyUnavailable")}</p> : null}
        <RuntimeTable rows={hostedRuntimeRows(load, "", fleet)} showProject showNode={fleet?.deployment.provider !== "e2b"} onOpen={onOpen} />
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
            {t(fleet?.deployment.provider === "microsandbox" ? "sandbox.runtimeMetaSuspended" : "sandbox.runtimeMeta", {
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

function RuntimeTable({ rows, showProject, showNode, onOpen }: { rows: HostedRuntimeRow[]; showProject: boolean; showNode: boolean; onOpen: (sessionId: string) => void }) {
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
                    <NameCell name={sessionTitle(session)} id={observation.session_id} fallback={t("sandbox.untitled")} onOpen={open} openLabel={t("sandbox.runtimeDialog.openLabel", { name: sessionTitle(session) ?? observation.session_id })} />
                  </th>
                  {showProject ? <td><ProjectName project={byId.get(observation.project_id)} /></td> : null}
                  {showNode ? <td>{row.node ? row.node.name || row.node.id : <span className="table-muted">{MISSING}</span>}</td> : null}
                  <td><StatusDot tone={lifecycleTone(row)} label={lifecycleLabel(row, t)} /></td>
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

/** Keeps the last value on screen while a dialog closes. */
function useLast<T>(value: T | null): T | null {
  const last = useRef(value);
  if (value !== null) last.current = value;
  return value ?? last.current;
}

/**
 * A node in a dialog: the host figures it reports with each heartbeat, and
 * CPU and memory of the hosted sandboxes placed on it over the page's range.
 */
function NodeDialog({ node, rows, load, range, onClose }: {
  node: SandboxNode | null;
  rows: readonly HostedRuntimeRow[];
  load: HostedRuntimeLoad | null;
  range: RuntimeDurableRange;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const shown = useLast(node);
  const sessionIds = useMemo(() => new Set(rows.filter((row) => row.node?.id === node?.id).map((row) => row.observation.session_id)), [node?.id, rows]);
  const snapshot = useMemo<RuntimeDashboardSnapshot | null>(
    () => (load && node && sessionIds.size ? runtimeSnapshotWhere(load, (observation) => sessionIds.has(observation.session_id)) : null),
    [load, node, sessionIds],
  );
  const history = useRuntimeHistory(snapshot, range);
  // The machine itself: its last heartbeat and host history over the page's range.
  const hostRange = RUNTIME_DURABLE_RANGES.find((entry) => entry.milliseconds === range)?.label ?? "1h";
  const detail = useQuery({
    ...nodeDetailQuery(shown?.id ?? "", hostRange),
    enabled: node !== null,
    placeholderData: keepPreviousData,
    refetchInterval: RUNTIME_SNAPSHOT_REFRESH_MS,
    refetchIntervalInBackground: false,
  });
  const host = detail.data?.id === shown?.id ? detail.data?.host ?? null : null;
  const now = Math.floor(Date.now() / 1000);
  const online = shown?.online ?? false;
  const seen = shown?.last_seen_at ? Date.parse(shown.last_seen_at) / 1000 : null;
  const cores = host?.effective_cpu_cores ?? shown?.cpu_count ?? null;
  const coresLabel = cores === null ? null : t("sandbox.cores", { value: formatInteger(cores, locale) });
  const cpu = !online || coresLabel === null ? MISSING
    : host?.cpu_utilization != null ? t("sandbox.nodeDialog.cpuOf", { percent: formatShare(host.cpu_utilization, locale), cores: coresLabel }) : coresLabel;
  const memory = !online ? MISSING
    : host?.total_memory_bytes != null && host.available_memory_bytes != null
      ? t("sandbox.nodeDialog.memoryOf", { used: formatBytes(host.total_memory_bytes - host.available_memory_bytes), total: formatBytes(host.total_memory_bytes) })
      : formatBytes(shown?.available_memory_bytes ?? null);
  return (
    <Modal
      open={node !== null}
      wide
      title={shown ? shown.name || shown.id : ""}
      onClose={onClose}
      footer={shown ? <button className="button outline" type="button" onClick={() => navigate("nodes", { id: shown.id })}>{t("sandbox.nodeDialog.openNode")}</button> : undefined}
    >
      {shown ? (
        <div className="metrics-dialog">
          <dl className="resource-facts" aria-label={t("sandbox.nodeDialog.facts")}>
            <div><dt>{t("sandbox.status")}</dt><dd><NodeHealthStatus node={shown} /></dd></div>
            <div><dt>{t("sandbox.slots")}</dt><dd>{formatInteger(shown.active, locale)} / {formatInteger(shown.max_active, locale)}</dd></div>
            {shown.provider === "microsandbox" ? <div><dt>{t("sandbox.suspended")}</dt><dd>{formatInteger(suspendedSandboxes(shown), locale)}</dd></div> : null}
            {shown.provider === "microsandbox" ? <div><dt>{t("sandbox.nodeDialog.retainedSlots")}</dt><dd>{formatInteger(shown.retained, locale)} / {formatInteger(shown.max_retained, locale)}</dd></div> : null}
            <div><dt>{t("sandbox.cpus")}</dt><dd>{cpu}</dd></div>
            <div><dt>{t("sandbox.memory")}</dt><dd>{memory}</dd></div>
            <div><dt>{t("sandbox.freeDiskColumn")}</dt><dd>{online ? formatBytes(shown.available_disk_bytes) : MISSING}</dd></div>
            <div><dt>{t("sandbox.cleanupPending")}</dt><dd>{formatInteger(shown.cleanup_pending, locale)}</dd></div>
            <div><dt>{t("sandbox.lastSeen")}</dt><dd title={seen === null ? undefined : formatDateTime(seen, locale)}>{formatRelative(seen, now, locale)}</dd></div>
          </dl>
          <Section headingId="node-host-heading" title={t("sandbox.nodeDialog.host")} help={t("sandbox.nodeDialog.hostHelp")}>
            {detail.data?.id === shown.id ? <NodeHostCharts detail={detail.data} />
              : detail.isError ? <p className="page-status">{t("sandbox.nodeDialog.hostFailed", { reason: detail.error instanceof Error ? detail.error.message : "" })}</p>
                : <TableSkeleton label={t("sandbox.runtimeLoading")} rows={3} columns={4} />}
          </Section>
          <Section
            headingId="node-runtimes-heading"
            title={<>{t("sandbox.nodeDialog.runtimes")}<span className="section-meta">{t("sandbox.nodeDialog.runtimesMeta", { n: formatInteger(sessionIds.size, locale) })}</span></>}
            help={t("sandbox.nodeDialog.runtimesHelp")}
          >
            {!sessionIds.size ? <EmptyState title={t("sandbox.nodeDialog.noRuntimes")} />
              : history.data ? <RuntimeCharts samples={history.data.samples} resolutionSeconds={history.data.resolutionSeconds} height={150} />
                : history.isError ? <p className="page-status" role="alert">{t("sandbox.charts.historyFailed", { reason: history.error instanceof Error ? history.error.message : "" })}</p>
                  : history.isFetched ? <p className="page-status">{t("sandbox.charts.historyUnavailable")}</p>
                    : <TableSkeleton label={t("sandbox.runtimeLoading")} rows={3} columns={4} />}
          </Section>
        </div>
      ) : null}
    </Modal>
  );
}

/** A hosted sandbox in a dialog: where it runs, and its Session's Runtime state and history. */
function RuntimeDialog({ row, showNode, onClose }: { row: HostedRuntimeRow | null; showNode: boolean; onClose: () => void }) {
  const { t } = useTranslation("metrics");
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
            {showNode ? <div><dt>{t("sandbox.node")}</dt><dd>{shown.node ? shown.node.name || shown.node.id : MISSING}</dd></div> : null}
            <div><dt>{t("sandbox.status")}</dt><dd><StatusDot tone={lifecycleTone(shown)} label={lifecycleLabel(shown, t)} /></dd></div>
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
