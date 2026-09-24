import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  ChevronUp,
  Cpu,
  Gauge,
  MemoryStick,
  Search,
  Server,
} from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type FilterFn,
  type SortingState,
} from "@tanstack/react-table";

import {
  buildRuntimeDashboardModel,
  buildSandboxInsights,
  formatDashboardBytes,
  formatDashboardDuration,
  formatDashboardTimestamp,
  formatDashboardTokens,
  reportedSessionTokens,
  type RuntimeDashboardRow,
} from "./dashboard-model";
import { holdLastReported } from "./held-usage";
import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import { RuntimeTrendPanel, type RuntimeHistoryLoader } from "./RuntimeTrendPanel";

const PAGE_SIZE = 10;

export type { RuntimeHistoryLoader } from "./RuntimeTrendPanel";

function RuntimeMetric({
  icon,
  label,
  value,
  detail,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  detail: string;
}) {
  return (
    <div className="dashboard-runtime-metric">
      <span className="dashboard-runtime-metric-icon" aria-hidden="true">{icon}</span>
      <span>
        <small>{label}</small>
        <strong>{value}</strong>
        <span>{detail}</span>
      </span>
    </div>
  );
}

function percent(usage: number | null | undefined, limit: number | null | undefined): number | null {
  if (typeof usage !== "number" || typeof limit !== "number" || limit <= 0) return null;
  return Math.min(100, Math.max(0, usage / limit * 100));
}

function runtimeModeLabel(row: RuntimeDashboardRow, label: (key: string, options?: Record<string, unknown>) => string): string {
  if (row.observation.mode === "openai_hosted") {
    const provider = row.observation.provider_type;
    return provider ? label("runtime.managedProvider", { provider: provider === "docker" ? "Docker" : provider }) : label("runtime.managed");
  }
  return label(`runtime.filters.${row.session.environmentProfile === "openai_hosted" ? "managed" : row.session.environmentProfile === "self_hosted" ? "selfHosted" : "none"}`);
}

function SortHeader({
  label,
  sorted,
  onClick,
}: {
  label: string;
  sorted: false | "asc" | "desc";
  onClick: (event: unknown) => void;
}) {
  const { t } = useTranslation("dashboard");
  const Icon = sorted === "asc" ? ChevronUp : sorted === "desc" ? ChevronDown : ChevronsUpDown;
  return (
    <button type="button" onClick={onClick} aria-label={t("runtime.sortBy", { label })}>
      {label}<Icon size={12} aria-hidden="true" />
    </button>
  );
}

const runtimeGlobalFilter: FilterFn<RuntimeDashboardRow> = (row, _columnId, value) => {
  const query = String(value).trim().toLocaleLowerCase();
  if (!query) return true;
  const item = row.original;
  return [
    item.session.title,
    item.session.agentLabel,
    item.observation.session_id,
    item.observation.environment_id,
    item.observation.instance.allocation_id,
    item.observation.provider_type,
    item.observation.status,
    item.observation.reason,
    item.observation.mode,
  ].some((candidate) => typeof candidate === "string" && candidate.toLocaleLowerCase().includes(query));
};

function RuntimeTargets({
  rows,
  onOpenSession,
}: {
  rows: RuntimeDashboardRow[];
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("dashboard");
  const locale = i18n.resolvedLanguage;
  const label = (key: string, options?: Record<string, unknown>) => String(t(key as never, options as never));
  const unavailable = t("runtime.filters.unavailable");
  const duration = (value: number | null) => value === null ? unavailable : formatDashboardDuration(value);
  const bytes = (value: number | null) => value === null ? unavailable : formatDashboardBytes(value);
  const [sorting, setSorting] = useState<SortingState>([]);
  const [globalFilter, setGlobalFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [modeFilter, setModeFilter] = useState("all");
  const filteredRows = useMemo(() => rows.filter((row) => (
    (statusFilter === "all" || row.observation.status === statusFilter) &&
    (modeFilter === "all" || row.observation.mode === modeFilter)
  )), [modeFilter, rows, statusFilter]);
  const columns = useMemo<ColumnDef<RuntimeDashboardRow>[]>(() => [{
    id: "session",
    accessorFn: (row) => row.session.title,
    header: ({ column }) => <SortHeader label={t("runtime.columns.session")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const item = row.original;
      return (
        <div className="dashboard-runtime-target-identity">
          <button type="button" onClick={() => onOpenSession(item.observation.session_id)}>{item.session.title}</button>
          <small>{item.session.agentLabel}</small>
          <details>
            <summary>{t("runtime.identity")}</summary>
            <dl>
              <div><dt>{t("runtime.columns.session")}</dt><dd>{item.observation.session_id}</dd></div>
              <div><dt>{t("runtime.environment")}</dt><dd>{item.observation.environment_id ?? t("runtime.notApplicable")}</dd></div>
              <div><dt>{t("runtime.allocation")}</dt><dd>{item.observation.instance.allocation_id ?? t("runtime.notAvailable")}</dd></div>
              <div><dt>{t("runtime.resolved")}</dt><dd>{formatDashboardTimestamp(item.observation.resolved_at, locale)}</dd></div>
            </dl>
          </details>
        </div>
      );
    },
  }, {
    id: "mode",
    accessorFn: (row) => runtimeModeLabel(row, label),
    header: ({ column }) => <SortHeader label={t("runtime.columns.mode")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span>{runtimeModeLabel(row.original, label)}</span>,
  }, {
    id: "status",
    accessorFn: (row) => row.observation.status,
    header: ({ column }) => <SortHeader label={t("runtime.columns.status")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => (
      <span className={`dashboard-runtime-status dashboard-runtime-status-${row.original.observation.status}`}>
        <span aria-hidden="true" />{t(`runtime.status.${row.original.observation.status === "unavailable" ? row.original.observation.reason : row.original.observation.status}` as never)}
      </span>
    ),
  }, {
    id: "cpu",
    accessorFn: (row) => row.observation.cpu?.usage_seconds_total ?? -1,
    header: ({ column }) => <SortHeader label={t("runtime.columns.cpu")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const cpu = row.original.observation.status === "observed" ? row.original.observation.cpu : null;
      return <span className="dashboard-runtime-table-value"><strong>{duration(cpu?.usage_seconds_total ?? null)}</strong><small>{typeof cpu?.capacity_cores === "number" ? t("runtime.cores", { value: cpu.capacity_cores.toLocaleString(locale) }) : t("runtime.capacityUnknown")}</small></span>;
    },
  }, {
    id: "memory",
    accessorFn: (row) => row.observation.memory?.usage_bytes ?? -1,
    header: ({ column }) => <SortHeader label={t("runtime.columns.memory")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const memory = row.original.observation.status === "observed" ? row.original.observation.memory : null;
      const memoryPercent = percent(memory?.usage_bytes, memory?.limit_bytes);
      return (
        <span className="dashboard-runtime-table-value">
          <strong>{bytes(memory?.usage_bytes ?? null)}</strong>
          <small>{memory?.limit_bytes == null ? t("runtime.limitUnknown") : t("runtime.ofLimit", { limit: bytes(memory.limit_bytes) })}</small>
          {memoryPercent !== null ? <span className="dashboard-runtime-bar" aria-label={t("runtime.memoryUsed", { percent: memoryPercent.toLocaleString(locale, { maximumFractionDigits: 1 }) })}><i style={{ width: `${memoryPercent}%` }} /></span> : null}
        </span>
      );
    },
  }, {
    id: "uptime",
    accessorFn: (row) => row.computeUptimeSeconds ?? -1,
    header: ({ column }) => <SortHeader label={t("runtime.columns.uptime")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span className="dashboard-runtime-table-value"><strong>{duration(row.original.computeUptimeSeconds)}</strong><small>{row.original.allocationAgeSeconds === null ? t("runtime.allocationUnknown") : t("runtime.allocated", { duration: duration(row.original.allocationAgeSeconds) })}</small></span>,
  }, {
    id: "sessionStatus",
    accessorFn: (row) => row.session.status,
    header: ({ column }) => <SortHeader label={t("runtime.columns.sessionState")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span>{t(`runtime.status.${row.original.session.status}` as never)}</span>,
  }, {
    id: "tokens",
    accessorFn: (row) => row.session.totalTokens ?? -1,
    header: ({ column }) => <SortHeader label={t("runtime.columns.tokens")} sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span className="dashboard-runtime-table-value"><strong>{row.original.session.totalTokens === null ? unavailable : formatDashboardTokens(row.original.session.totalTokens, locale)}</strong><small>{row.original.session.totalTokens === null ? t("runtime.notReported") : t("runtime.sessionReported")}</small></span>,
  }], [locale, onOpenSession, t, unavailable]);
  const table = useReactTable({
    data: filteredRows,
    columns,
    state: { sorting, globalFilter },
    onSortingChange: setSorting,
    onGlobalFilterChange: setGlobalFilter,
    globalFilterFn: runtimeGlobalFilter,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageIndex: 0, pageSize: PAGE_SIZE } },
  });
  const visibleRows = table.getFilteredRowModel().rows.length;

  return (
    <section className="dashboard-runtime-targets" aria-label={t("runtime.explorer")}>
      <div className="dashboard-runtime-toolbar">
        <label className="dashboard-runtime-search">
          <Search size={14} aria-hidden="true" />
          <span className="sr-only">{t("runtime.search")}</span>
          <input value={globalFilter} onChange={(event) => setGlobalFilter(event.target.value)} placeholder={t("runtime.searchPlaceholder")} />
        </label>
        <label>
          <span>{t("runtime.columns.status")}</span>
          <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
            <option value="all">{t("runtime.filters.allStatuses")}</option>
            <option value="observed">{t("runtime.filters.observed")}</option>
            <option value="unavailable">{t("runtime.filters.unavailable")}</option>
            <option value="unsupported">{t("runtime.filters.unsupported")}</option>
          </select>
        </label>
        <label>
          <span>{t("runtime.columns.mode")}</span>
          <select value={modeFilter} onChange={(event) => setModeFilter(event.target.value)}>
            <option value="all">{t("runtime.filters.allModes")}</option>
            <option value="openai_hosted">{t("runtime.filters.managed")}</option>
            <option value="self_hosted">{t("runtime.filters.selfHosted")}</option>
            <option value="none">{t("runtime.filters.none")}</option>
          </select>
        </label>
        <span className="dashboard-runtime-visible-count">{t("runtime.visible", { value: visibleRows.toLocaleString(locale) })}</span>
      </div>
      <div className="dashboard-runtime-table-scroll">
        <table className="dashboard-runtime-table" aria-label={t("runtime.targets")}>
          <thead>
            {table.getHeaderGroups().map((headerGroup) => (
              <tr key={headerGroup.id}>
                {headerGroup.headers.map((header) => <th key={header.id} aria-sort={header.column.getIsSorted() === "asc" ? "ascending" : header.column.getIsSorted() === "desc" ? "descending" : "none"}>{flexRender(header.column.columnDef.header, header.getContext())}</th>)}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.map((row) => (
              <tr key={row.id}>
                {row.getVisibleCells().map((cell) => <td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
        {visibleRows === 0 ? <p className="dashboard-runtime-no-results">{t("runtime.noResults")}</p> : null}
      </div>
      {table.getPageCount() > 1 ? (
        <footer className="dashboard-runtime-pagination">
          <span>{t("runtime.page", { page: table.getState().pagination.pageIndex + 1, pages: table.getPageCount() })}</span>
          <div>
            <button type="button" disabled={!table.getCanPreviousPage()} onClick={() => table.previousPage()}><ChevronLeft size={14} aria-hidden="true" /> {t("runtime.previous")}</button>
            <button type="button" disabled={!table.getCanNextPage()} onClick={() => table.nextPage()}>{t("runtime.next")} <ChevronRight size={14} aria-hidden="true" /></button>
          </div>
        </footer>
      ) : null}
    </section>
  );
}

export function RuntimeObservabilityContent({
  snapshot,
  stale,
  loadRuntimeHistory,
  onOpenSession,
}: {
  snapshot: RuntimeDashboardSnapshot;
  stale: boolean;
  loadRuntimeHistory: RuntimeHistoryLoader;
  onOpenSession: (sessionId: string) => void;
}) {
  const { t, i18n } = useTranslation("dashboard");
  const locale = i18n.resolvedLanguage;
  // Hold each Session's last reported tokens across snapshots, so the summary
  // total does not drop while a Turn withholds public Session usage.
  const [heldTokens, setHeldTokens] = useState(() => ({
    snapshot,
    totals: holdLastReported(new Map<string, number>(), reportedSessionTokens(snapshot.sessions)),
  }));
  let tokenTotals = heldTokens.totals;
  if (heldTokens.snapshot !== snapshot) {
    tokenTotals = holdLastReported(heldTokens.totals, reportedSessionTokens(snapshot.sessions));
    setHeldTokens({ snapshot, totals: tokenTotals });
  }
  const model = useMemo(
    () => buildRuntimeDashboardModel(snapshot.sessions, snapshot.observations, tokenTotals),
    [snapshot, tokenTotals],
  );
  const summary = model.summary;
  const sandbox = useMemo(() => buildSandboxInsights(model.rows), [model.rows]);
  const unavailableReasons = Object.entries(sandbox.unavailableReasons)
    .sort((left, right) => right[1] - left[1]);
  return (
    <>
      <div className="dashboard-runtime-summary" aria-label={t("runtime.resourceSnapshot")}>
        <RuntimeMetric icon={<Server size={17} />} label={t("runtime.metrics.sandboxState")} value={t("runtime.metrics.sandboxStateValue", { active: summary.activeSandboxCount.toLocaleString(locale), sleeping: summary.sleepingSandboxCount.toLocaleString(locale) })} detail={t("runtime.metrics.sandboxStateDetail", { total: summary.sandboxTotalCount.toLocaleString(locale), transitioning: (summary.transitioningSandboxCount + summary.pendingSandboxCount).toLocaleString(locale) })} />
        <RuntimeMetric icon={<Cpu size={17} />} label={t("runtime.metrics.cpu")} value={summary.cpuUsageSecondsTotal === null && summary.cpuCapacityCores === null ? t("runtime.metrics.noSample") : `${summary.cpuUsageSecondsTotal === null ? t("runtime.filters.unavailable") : formatDashboardDuration(summary.cpuUsageSecondsTotal)} / ${summary.cpuCapacityCores === null ? "—" : t("runtime.cores", { value: summary.cpuCapacityCores.toLocaleString(locale) })}`} detail={t("runtime.metrics.cpuDetail", { covered: summary.cpuCoverageCount, total: summary.observedRuntimeCount })} />
        <RuntimeMetric icon={<MemoryStick size={17} />} label={t("runtime.metrics.memory")} value={summary.memoryUsageBytes === null && summary.memoryLimitBytes === null ? t("runtime.metrics.noSample") : `${summary.memoryUsageBytes === null ? t("runtime.filters.unavailable") : formatDashboardBytes(summary.memoryUsageBytes)} / ${summary.memoryLimitBytes === null ? t("runtime.filters.unavailable") : formatDashboardBytes(summary.memoryLimitBytes)}`} detail={t("runtime.metrics.memoryDetail", { covered: summary.memoryCoverageCount, total: summary.observedRuntimeCount })} />
        <RuntimeMetric icon={<Gauge size={17} />} label={t("runtime.metrics.tokens")} value={summary.totalTokens === null ? t("runtime.filters.unavailable") : formatDashboardTokens(summary.totalTokens, locale)} detail={t("runtime.metrics.tokenDetail", { covered: summary.tokenCoverageCount, total: summary.sessionCount })} />
      </div>

      <section className="dashboard-sandbox-insights" aria-labelledby="dashboard-sandbox-insights-title">
        <header>
          <div>
            <h3 id="dashboard-sandbox-insights-title">{t("sandbox.title")}</h3>
            <p>{t("sandbox.subtitle")}</p>
          </div>
          <small>{t(stale ? "sandbox.retained" : "sandbox.current")}</small>
        </header>
        <div className="dashboard-sandbox-insight-grid">
          <div><small>{t("sandbox.observed")}</small><strong>{sandbox.observed.toLocaleString(locale)}</strong><span>{t("sandbox.observedDetail", { total: summary.managedRuntimeCount })}</span></div>
          <div><small>{t("sandbox.unavailable")}</small><strong>{sandbox.unavailable.toLocaleString(locale)}</strong><span>{t("sandbox.unavailableDetail")}</span></div>
          <div><small>{t("sandbox.highMemory")}</small><strong>{sandbox.measuredMemory === 0 ? t("runtime.filters.unavailable") : sandbox.highMemory.toLocaleString(locale)}</strong><span>{t("sandbox.highMemoryDetail", { total: sandbox.measuredMemory })}</span></div>
          <div><small>{t("sandbox.parked")}</small><strong>{(sandbox.sleeping + sandbox.pending).toLocaleString(locale)}</strong><span>{t("sandbox.parkedDetail", { sleeping: sandbox.sleeping, pending: sandbox.pending })}</span></div>
        </div>
        {unavailableReasons.length > 0 || sandbox.highestMemory.length > 0 ? (
          <div className="dashboard-sandbox-diagnostics">
            <div>
              <h4>{t("sandbox.sampleGaps")}</h4>
              {unavailableReasons.length ? <ul>{unavailableReasons.map(([reason, count]) => <li key={reason}><span>{t(`runtime.status.${reason}` as never)}</span><strong>{count}</strong></li>)}</ul> : <p>{t("sandbox.noGaps")}</p>}
            </div>
            <div>
              <h4>{t("sandbox.memoryLeaders")}</h4>
              {sandbox.highestMemory.length ? <ul>{sandbox.highestMemory.map((entry) => <li key={entry.allocationId}><button type="button" onClick={() => onOpenSession(entry.sessionId)}>{entry.title}</button><span title={`${formatDashboardBytes(entry.memoryUsageBytes)} / ${formatDashboardBytes(entry.memoryLimitBytes)}`}>{entry.memoryPercent.toLocaleString(locale, { maximumFractionDigits: 1 })}%</span></li>)}</ul> : <p>{t("sandbox.noMemory")}</p>}
            </div>
          </div>
        ) : null}
      </section>

      <RuntimeTrendPanel snapshot={snapshot} stale={stale} loadRuntimeHistory={loadRuntimeHistory} />

      <details className="dashboard-runtime-explorer">
        <summary>
          <span><strong>{t("runtime.targets")}</strong><small>{t("runtime.explorerHint")}</small></span>
          <span>{t("runtime.targetCount", { value: model.rows.length.toLocaleString(locale), snapshot: t(stale ? "runtime.retainedSnapshot" : "runtime.currentSnapshot") })}<ChevronDown size={15} aria-hidden="true" /></span>
        </summary>
        <RuntimeTargets rows={model.rows} onOpenSession={onOpenSession} />
      </details>
    </>
  );
}
