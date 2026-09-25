import { Server } from "lucide-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  EmptyState,
  HelpTip,
  Meter,
  PageBody,
  PageHeader,
  RefreshButton,
  Section,
  SegmentedControl,
  StatusDot,
  type Tone,
} from "../../components/console-ui";
import { TableSkeleton } from "../../components/Skeleton";
import { ListToolbar, listSummary, NameCell, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { formatBytes, formatClock, formatCompact, formatCores, formatDuration, formatInteger, formatRelative, MISSING } from "../../lib/format";
import { projectClient, ProjectName, useProjects } from "../../lib/projects";
import { loadRuntimeDurableSnapshot, RUNTIME_DURABLE_RANGES, type RuntimeDurableRange } from "../dashboard/runtime-history";
import type { RuntimeDashboardSnapshot } from "../dashboard/runtime-snapshot";
import { RUNTIME_SNAPSHOT_REFRESH_MS } from "../dashboard/runtime-snapshot";
import { capacitySummary, nodeHealth, type NodeHealth } from "../fleet/fleet-model";
import { fleetSnapshot, useSandboxFleet, type FleetState } from "../fleet/use-sandbox-fleet";
import {
  hostedRuntimeRows,
  hostedRuntimeUsage,
  matchesRuntime,
  runtimeSnapshot,
  sessionTitle,
  type HostedRuntimeLoad,
  type HostedRuntimeRow,
} from "./sandbox-runtime";
import "./MetricsView.css";
import { RuntimeCharts } from "./RuntimeCharts";
import { hostedRuntimesQuery } from "./metrics-queries";

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
  if (query.isError && !query.isFetching) state = { status: "failed", load, error: query.error instanceof Error ? query.error.message : String(query.error) };
  else if (load && !query.isFetching) state = { status: "ready", load };
  else state = { status: "loading", load };
  const { refetch } = query;
  const refresh = useCallback(() => { void refetch(); }, [refetch]);
  return { state, refresh };
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
  const { state: runtimeState, refresh: refreshRuntime } = useHostedRuntimes();
  const fleet = fleetSnapshot(fleetState);
  const capacity = fleet ? capacitySummary(fleet.nodes) : null;
  const now = Math.floor(Date.now() / 1000);
  const refreshing = runtimeState.status === "loading" || (fleetState.status === "ready" && fleetState.refreshing);
  const updatedAt = runtimeState.load?.loadedAt ?? fleet?.loadedAt ?? null;
  const message = fleetMessage(fleetState, t);
  const [range, setRange] = useState<RuntimeDurableRange>(RUNTIME_DURABLE_RANGES[0].milliseconds);

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
        <Section
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
                    <th scope="col" className="numeric">{t("sandbox.cpus")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeMemory")}</th>
                    <th scope="col" className="numeric">{t("sandbox.freeDiskColumn")}</th>
                    <th scope="col" className="numeric">{t("sandbox.cleanupPending")}</th>
                    <th scope="col" className="numeric">{t("sandbox.lastSeen")}</th>
                  </tr>
                </thead>
                <tbody>
                  {fleet.nodes.map((node) => {
                    const health = nodeHealth(node);
                    return (
                      <tr key={node.id}>
                        <th scope="row"><NameCell name={node.name} id={node.id} onOpen={() => navigate("nodes", { id: node.id })} /></th>
                        <td><StatusDot tone={healthTone[health]} label={t(`sandbox.health.${health}`)} /></td>
                        <td>
                          <span className="table-meter">
                            <Meter value={node.active} limit={node.max_active} label={t("sandbox.slotsOf", { name: node.name })} />
                            <span>{node.active} / {node.max_active}</span>
                          </span>
                        </td>
                        <td className="numeric">{node.online ? node.cpu_count ?? MISSING : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_memory_bytes) : MISSING}</td>
                        <td className="numeric">{node.online ? formatBytes(node.available_disk_bytes) : MISSING}</td>
                        <td className={node.cleanup_pending > 0 ? "numeric numeric-warning" : "numeric"}>{node.cleanup_pending}</td>
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
          ) : fleetState.status === "checking" || fleetState.status === "loading"
            ? <TableSkeleton label={message} rows={3} columns={8} />
            : <p className="page-status" role={fleetState.status === "failed" ? "alert" : "status"}>{message}</p>}
        </Section>

        <HostedRuntimeSection state={runtimeState} fleet={fleet} range={range} />
      </PageBody>
    </section>
  );
}

/** Durable Runtime history of every hosted Session over a range, read through each Session's project. */
function useRuntimeHistory(load: HostedRuntimeLoad | null, range: RuntimeDurableRange) {
  const snapshot = useMemo<RuntimeDashboardSnapshot | null>(() => (load ? runtimeSnapshot(load, "") : null), [load]);
  const targets = useMemo(() => snapshot?.observations
    .filter((observation) => observation.mode === "openai_hosted" && observation.environment_id !== null)
    .map((observation) => observation.session_id)
    .sort()
    .join("|") ?? "", [snapshot]);
  return useQuery({
    queryKey: ["runtime-history", range, targets],
    queryFn: ({ signal }) => loadHistory(snapshot!, range, signal),
    enabled: snapshot !== null,
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
}

function HostedRuntimeSection({ state, fleet, range }: { state: RuntimeState; fleet: ReturnType<typeof fleetSnapshot>; range: RuntimeDurableRange }) {
  const { t, i18n } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const load = state.load;
  const usage = useMemo(() => (load ? hostedRuntimeUsage(load.observations) : null), [load]);
  const history = useRuntimeHistory(load, range);
  const partial = load && (load.unread || load.failed) ? t("sandbox.runtimePartial", { unread: load.unread, failed: load.failed }) : null;

  let body;
  if (!load || !usage) {
    body = state.status === "failed"
      ? <p className="page-status" role="alert">{t("sandbox.runtimeUnavailable", { reason: state.error })}</p>
      : <TableSkeleton label={t("sandbox.runtimeLoading")} rows={4} columns={8} />;
  } else if (!usage.hosted) {
    body = <EmptyState title={t("sandbox.noRuntimeTitle")} description={t("sandbox.noRuntimeDescription")} />;
  } else {
    const durable = history.data ?? null;
    body = (
      <>
        {state.status === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("sandbox.runtimeStale", { reason: state.error })}</p> : null}
        {durable ? <RuntimeCharts samples={durable.samples} resolutionSeconds={durable.resolutionSeconds} />
          : history.isError ? <p className="page-status" role="alert">{t("sandbox.charts.historyFailed", { reason: history.error instanceof Error ? history.error.message : "" })}</p>
            : history.isFetched ? <p className="page-status">{t("sandbox.charts.historyUnavailable")}</p> : null}
        <RuntimeTable rows={hostedRuntimeRows(load, "", fleet)} showProject />
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
            {t("sandbox.runtimeMeta", {
              n: formatInteger(usage.hosted, locale),
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
