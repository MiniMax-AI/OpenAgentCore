import { Server } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  StatusDot,
  type Tone,
} from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatBytes, formatClock, formatCompact, formatCores, formatDuration, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { admin, ProjectFilter, projectClient, ProjectName, useProjects, type ProjectFilterValue } from "../../lib/projects";
import { loadRuntimeDurableSnapshot } from "../dashboard/runtime-history";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { RUNTIME_SNAPSHOT_REFRESH_MS } from "../dashboard/runtime-snapshot";
import { RuntimeTrendPanel, type RuntimeHistoryLoader } from "../dashboard/RuntimeTrendPanel";
import { capacitySummary, nodeHealth, type NodeHealth } from "../fleet/fleet-model";
import { fleetSnapshot, useSandboxFleet, type FleetState } from "../fleet/use-sandbox-fleet";
import { isAbortError } from "./project-sessions";
import {
  hostedRuntimeRows,
  hostedRuntimeUsage,
  loadHostedRuntimes,
  matchesRuntime,
  runtimeSnapshot,
  sessionTitle,
  type HostedRuntimeLoad,
  type HostedRuntimeRow,
} from "./sandbox-runtime";
import "./MetricsView.css";
import { listRuntimeObservations } from "../../lib/admin-view";

const healthTone: Record<NodeHealth, Tone> = { available: "ok", degraded: "warning", offline: "danger" };

type RuntimeState =
  | { status: "loading"; load: HostedRuntimeLoad | null }
  | { status: "ready"; load: HostedRuntimeLoad }
  | { status: "failed"; load: HostedRuntimeLoad | null; error: string };

function useHostedRuntimes() {
  const [state, setState] = useState<RuntimeState>({ status: "loading", load: null });
  const [revision, setRevision] = useState(0);
  const latest = useRef<HostedRuntimeLoad | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading", load: latest.current });
    loadHostedRuntimes({
      observations: (signal) => listRuntimeObservations(signal),
      sessionReader: (projectId) => projectClient(projectId),
    }, controller.signal).then((load) => {
      latest.current = load;
      setState({ status: "ready", load });
    }, (error: unknown) => {
      if (controller.signal.aborted || isAbortError(error)) return;
      setState({ status: "failed", load: latest.current, error: error instanceof Error ? error.message : String(error) });
    });
    return () => controller.abort();
  }, [revision]);
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) setRevision((value) => value + 1);
    }, RUNTIME_SNAPSHOT_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { state, refresh };
}

/** Durable history of each hosted Session, read through the Session's project. */
const loadHistory: RuntimeHistoryLoader = (snapshot, range, signal) => loadRuntimeDurableSnapshot((sessionId) => {
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
  const { state: runtimeState, refresh: refreshRuntime } = useHostedRuntimes();
  const fleet = fleetSnapshot(fleetState);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const now = Math.floor(Date.now() / 1000);
  const refreshing = runtimeState.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = runtimeState.load?.loadedAt ?? fleet?.loadedAt ?? null;
  const message = fleetMessage(fleetState, t);

  return (
    <section className="page-section console-page metrics-page" aria-labelledby="sandbox-metrics-heading">
      <PageHeader
        headingId="sandbox-metrics-heading"
        title={t("sandbox.title")}
        help={t("sandbox.description")}
        actions={<RefreshButton refreshing={refreshing} updatedAt={updatedAt ? formatClock(updatedAt, locale) : null} onClick={() => { refreshRuntime(); refreshFleet(); }} />}
      />
      <PageBody>
        <KpiStrip label={t("sandbox.kpiLabel")}>
          <Kpi
            label={t("sandbox.nodesOnline")}
            value={capacity ? `${capacity.online} / ${capacity.nodes}` : MISSING}
            tone={capacity && capacity.nodes ? capacity.online < capacity.nodes ? "danger" : capacity.available < capacity.online ? "warning" : "ok" : undefined}
            help={capacity ? t("sandbox.nodesAvailable", { count: capacity.available }) : message}
          />
          <Kpi label={t("sandbox.activeSandboxes")} value={capacity ? `${formatInteger(capacity.active, locale)} / ${formatInteger(capacity.maxActive, locale)}` : MISSING} help={t("sandbox.activeDetail")} />
          <Kpi label={t("sandbox.retained")} value={capacity ? `${formatInteger(capacity.retained, locale)} / ${formatInteger(capacity.maxRetained, locale)}` : MISSING} help={t("sandbox.retainedDetail")} />
          <Kpi label={t("sandbox.reserved")} value={capacity ? formatInteger(capacity.reserved, locale) : MISSING} help={t("sandbox.reservedDetail")} />
          <Kpi
            label={t("sandbox.cleanupPending")}
            value={capacity ? formatInteger(capacity.cleanupPending, locale) : MISSING}
            tone={capacity ? capacity.cleanupPending > 0 ? "warning" : "ok" : undefined}
            help={t("sandbox.cleanupDetail")}
          />
          <Kpi label={t("sandbox.freeMemory")} value={capacity ? formatBytes(capacity.availableMemoryBytes) : MISSING} help={capacity ? t("sandbox.freeDisk", { value: formatBytes(capacity.availableDiskBytes) }) : message} />
        </KpiStrip>

        <Section
          headingId="node-capacity-heading"
          title={t("sandbox.nodesSection")}
          help={t("sandbox.nodesSectionDetail")}
          actions={<button className="text-action" type="button" onClick={() => navigate("nodes")}>{t("sandbox.manageNodes")}</button>}
        >
          {fleet ? fleet.nodes.length ? (
            <div className="table-frame">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">{t("sandbox.node")}</th>
                    <th scope="col">{t("sandbox.status")}</th>
                    <th scope="col">{t("sandbox.slots")}</th>
                    <th scope="col" className="numeric">{t("sandbox.retained")}</th>
                    <th scope="col" className="numeric">{t("sandbox.reserved")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cleanupPending")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cpus")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeMemory")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeDiskColumn")}</th>
                    <th scope="col" className="numeric">{t("sandbox.lastSeen")}</th>
                  </tr>
                </thead>
                <tbody>
                  {fleet.nodes.map((node) => {
                    const health = nodeHealth(node);
                    return (
                      <tr key={node.id}>
                        <th scope="row" title={node.id}><span className="table-primary">{node.name || node.id}</span></th>
                        <td><StatusDot tone={healthTone[health]} label={t(`sandbox.health.${health}`)} /></td>
                        <td>
                          <span className="table-meter">
                            <Meter value={node.active} limit={node.max_active} label={t("sandbox.slotsOf", { name: node.name })} />
                            <span>{node.active} / {node.max_active}</span>
                          </span>
                        </td>
                        <td className="numeric">{node.retained} / {node.max_retained}</td>
                        <td className="numeric">{node.reserved}</td>
                        <td className="numeric">{node.cleanup_pending}</td>
                        <td className="numeric">{node.online ? node.cpu_count ?? MISSING : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_memory_bytes) : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_disk_bytes) : MISSING}</td>
                        <td className="numeric">{formatRelative(node.last_seen_at ? Date.parse(node.last_seen_at) / 1000 : null, now, locale)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <EmptyState
              icon={Server}
              title={t("sandbox.noNodesTitle")}
              description={t("sandbox.noNodesDescription")}
              action={<button className="button primary" type="button" onClick={() => navigate("nodes")}>{t("sandbox.addNode")}</button>}
            />
          ) : <p className="page-status" role={fleetState.status === "failed" ? "alert" : "status"}>{message}</p>}
        </Section>

        <HostedRuntimeSection state={runtimeState} fleet={fleet} />
      </PageBody>
    </section>
  );
}

function HostedRuntimeSection({ state, fleet }: { state: RuntimeState; fleet: ReturnType<typeof fleetSnapshot> }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const [project, setProject] = useState<ProjectFilterValue>("");
  const load = state.load;
  const snapshot = useMemo<RuntimeDashboardSnapshot | null>(() => (load ? runtimeSnapshot(load, project) : null), [load, project]);
  const usage = useMemo(() => (load ? hostedRuntimeUsage(load.observations.filter((observation) => !project || observation.project_id === project)) : null), [load, project]);
  const partial = load && (load.unread || load.failed) ? t("sandbox.runtimePartial", { unread: load.unread, failed: load.failed }) : null;

  let body;
  if (!load || !snapshot || !usage) {
    body = (
      <p className="page-status" role={state.status === "failed" ? "alert" : "status"}>
        {state.status === "failed" ? t("sandbox.runtimeUnavailable", { reason: state.error }) : t("sandbox.runtimeLoading")}
      </p>
    );
  } else if (!usage.hosted) {
    body = <EmptyState title={t("sandbox.noRuntimeTitle")} description={t(project ? "sandbox.noRuntimeProject" : "sandbox.noRuntimeDescription")} />;
  } else {
    body = (
      <>
        {state.status === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("sandbox.runtimeStale", { reason: state.error })}</p> : null}
        <KpiStrip label={t("sandbox.runtimeKpiLabel")}>
          <Kpi
            label={t("sandbox.runtimes")}
            value={t("sandbox.runtimeStates", { active: formatInteger(usage.active, locale), sleeping: formatInteger(usage.sleeping, locale) })}
            help={t("sandbox.runtimesDetail", { total: usage.hosted, pending: usage.pending, observed: usage.observed })}
          />
          <Kpi
            label={t("sandbox.cpu")}
            value={usage.cpuUsageCores === null ? MISSING : `${formatCores(usage.cpuUsageCores, locale)} / ${t("sandbox.cores", { value: formatCores(usage.cpuCapacityCores, locale) })}`}
            help={t("sandbox.cpuDetail")}
          />
          <Kpi
            label={t("sandbox.memory")}
            value={usage.memoryUsageBytes === null ? MISSING : `${formatBytes(usage.memoryUsageBytes)} / ${formatBytes(usage.memoryLimitBytes)}`}
            help={t("sandbox.memoryDetail")}
          />
        </KpiStrip>
        <div className="runtime-embed">
          <RuntimeTrendPanel key={project || "all"} snapshot={snapshot} stale={state.status === "failed"} loadRuntimeHistory={loadHistory} />
        </div>
        <RuntimeTable rows={hostedRuntimeRows(load, project, fleet)} showProject={!project} />
      </>
    );
  }

  return (
    <Section
      headingId="runtime-heading"
      title={t("sandbox.runtimeSection")}
      help={t("sandbox.runtimeSectionDetail")}
      actions={<>
        {partial ? (
          <span className="partial-chip">
            <StatusDot tone="warning" label={t("coverage.partial")} />
            <HelpTip>{partial}</HelpTip>
          </span>
        ) : null}
        <ProjectFilter value={project} onChange={setProject} />
      </>}
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

function RuntimeTable({ rows, showProject }: { rows: HostedRuntimeRow[]; showProject: boolean }) {
  const { t, i18n } = useTranslation("metrics");
  const { t: tCommon } = useTranslation("common");
  const locale = i18n.resolvedLanguage;
  const { navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const [query, setQuery] = useState("");
  const visible = rows.filter((row) => matchesRuntime(row, query));
  return (
    <div className="runtime-list">
      <ListToolbar label={t("sandbox.runtimeList")} summary={listSummary(tCommon, visible.length, rows.length, { locale })}>
        <SearchField value={query} onChange={setQuery} placeholder={t("sandbox.searchPlaceholder")} label={t("sandbox.search")} />
      </ListToolbar>
      <div className="table-frame">
        <table className="data-table" aria-label={t("sandbox.runtimeList")}>
          <thead>
            <tr>
              <th scope="col">{t("sandbox.session")}</th>
              {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
              <th scope="col">{t("sandbox.node")}</th>
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
              const open = () => navigate("session", { project: observation.project_id, id: observation.session_id });
              return (
                <tr key={`${observation.project_id}:${observation.session_id}`}>
                  <th scope="row">
                    <NameCell name={sessionTitle(session)} id={observation.session_id} fallback={t("sandbox.untitled")} onOpen={open} />
                  </th>
                  {showProject ? <td><ProjectName project={byId.get(observation.project_id)} /></td> : null}
                  <td>{row.node ? row.node.name || row.node.id : <span className="table-muted">{MISSING}</span>}</td>
                  <td><StatusDot tone={lifecycleTone(row)} label={lifecycleLabel(row, t)} /></td>
                  <td>
                    {cpu?.usage_cores != null ? (
                      <span className="table-meter">
                        <Meter value={cpu.usage_cores} limit={cpu.capacity_cores} label={t("sandbox.cpu")} />
                        <span>{formatCores(cpu.usage_cores, locale)} / {t("sandbox.cores", { value: formatCores(cpu.capacity_cores, locale) })}</span>
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
