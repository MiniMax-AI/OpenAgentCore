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
  dashboardEnvironmentLabel,
  dashboardStatusLabel,
  formatDashboardBytes,
  formatDashboardDuration,
  formatDashboardTimestamp,
  formatDashboardTokens,
  reportedSessionTokens,
  runtimeObservationStatusLabel,
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

function runtimeModeLabel(row: RuntimeDashboardRow): string {
  if (row.observation.mode === "openai_hosted") {
    const provider = row.observation.provider_type;
    return provider ? `Managed ${provider === "docker" ? "Docker" : provider}` : "Managed";
  }
  return dashboardEnvironmentLabel(row.session.environmentProfile);
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
  const Icon = sorted === "asc" ? ChevronUp : sorted === "desc" ? ChevronDown : ChevronsUpDown;
  return (
    <button type="button" onClick={onClick} aria-label={`Sort by ${label}`}>
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
    runtimeModeLabel(item),
  ].some((candidate) => typeof candidate === "string" && candidate.toLocaleLowerCase().includes(query));
};

function RuntimeTargets({
  rows,
  onOpenSession,
}: {
  rows: RuntimeDashboardRow[];
  onOpenSession: (sessionId: string) => void;
}) {
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
    header: ({ column }) => <SortHeader label="Session" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const item = row.original;
      return (
        <div className="dashboard-runtime-target-identity">
          <button type="button" onClick={() => onOpenSession(item.observation.session_id)}>{item.session.title}</button>
          <small>{item.session.agentLabel}</small>
          <details>
            <summary>Identity</summary>
            <dl>
              <div><dt>Session</dt><dd>{item.observation.session_id}</dd></div>
              <div><dt>Environment</dt><dd>{item.observation.environment_id ?? "Not applicable"}</dd></div>
              <div><dt>Allocation</dt><dd>{item.observation.instance.allocation_id ?? "Not available"}</dd></div>
              <div><dt>Resolved</dt><dd>{formatDashboardTimestamp(item.observation.resolved_at)}</dd></div>
            </dl>
          </details>
        </div>
      );
    },
  }, {
    id: "mode",
    accessorFn: runtimeModeLabel,
    header: ({ column }) => <SortHeader label="Mode" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span>{runtimeModeLabel(row.original)}</span>,
  }, {
    id: "status",
    accessorFn: (row) => row.observation.status,
    header: ({ column }) => <SortHeader label="Status" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => (
      <span className={`dashboard-runtime-status dashboard-runtime-status-${row.original.observation.status}`}>
        <span aria-hidden="true" />{runtimeObservationStatusLabel(row.original.observation)}
      </span>
    ),
  }, {
    id: "cpu",
    accessorFn: (row) => row.observation.cpu?.usage_seconds_total ?? -1,
    header: ({ column }) => <SortHeader label="CPU time" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const cpu = row.original.observation.status === "observed" ? row.original.observation.cpu : null;
      return <span className="dashboard-runtime-table-value"><strong>{formatDashboardDuration(cpu?.usage_seconds_total ?? null)}</strong><small>{typeof cpu?.capacity_cores === "number" ? `${cpu.capacity_cores.toLocaleString("en-US")} cores` : "Capacity unknown"}</small></span>;
    },
  }, {
    id: "memory",
    accessorFn: (row) => row.observation.memory?.usage_bytes ?? -1,
    header: ({ column }) => <SortHeader label="Memory" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => {
      const memory = row.original.observation.status === "observed" ? row.original.observation.memory : null;
      const memoryPercent = percent(memory?.usage_bytes, memory?.limit_bytes);
      return (
        <span className="dashboard-runtime-table-value">
          <strong>{formatDashboardBytes(memory?.usage_bytes ?? null)}</strong>
          <small>{memory?.limit_bytes == null ? "Limit unknown" : `of ${formatDashboardBytes(memory.limit_bytes)}`}</small>
          {memoryPercent !== null ? <span className="dashboard-runtime-bar" aria-label={`${memoryPercent.toFixed(1)}% memory used`}><i style={{ width: `${memoryPercent}%` }} /></span> : null}
        </span>
      );
    },
  }, {
    id: "uptime",
    accessorFn: (row) => row.computeUptimeSeconds ?? -1,
    header: ({ column }) => <SortHeader label="Uptime" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span className="dashboard-runtime-table-value"><strong>{formatDashboardDuration(row.original.computeUptimeSeconds)}</strong><small>{row.original.allocationAgeSeconds === null ? "Allocation age unknown" : `${formatDashboardDuration(row.original.allocationAgeSeconds)} allocated`}</small></span>,
  }, {
    id: "sessionStatus",
    accessorFn: (row) => row.session.status,
    header: ({ column }) => <SortHeader label="Session state" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span>{dashboardStatusLabel(row.original.session.status)}</span>,
  }, {
    id: "tokens",
    accessorFn: (row) => row.session.totalTokens ?? -1,
    header: ({ column }) => <SortHeader label="Tokens" sorted={column.getIsSorted()} onClick={column.getToggleSortingHandler() ?? (() => undefined)} />,
    cell: ({ row }) => <span className="dashboard-runtime-table-value"><strong>{formatDashboardTokens(row.original.session.totalTokens)}</strong><small>{row.original.session.totalTokens === null ? "Not reported" : "Session reported"}</small></span>,
  }], [onOpenSession]);
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
    <section className="dashboard-runtime-targets" aria-label="Runtime target explorer">
      <div className="dashboard-runtime-toolbar">
        <label className="dashboard-runtime-search">
          <Search size={14} aria-hidden="true" />
          <span className="sr-only">Search Runtime targets</span>
          <input value={globalFilter} onChange={(event) => setGlobalFilter(event.target.value)} placeholder="Search Session, provider, or identity" />
        </label>
        <label>
          <span>Status</span>
          <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
            <option value="all">All statuses</option>
            <option value="observed">Observed</option>
            <option value="unavailable">Unavailable</option>
            <option value="unsupported">Unsupported</option>
          </select>
        </label>
        <label>
          <span>Mode</span>
          <select value={modeFilter} onChange={(event) => setModeFilter(event.target.value)}>
            <option value="all">All modes</option>
            <option value="openai_hosted">Managed</option>
            <option value="self_hosted">Self-hosted</option>
            <option value="none">None</option>
          </select>
        </label>
        <span className="dashboard-runtime-visible-count">{visibleRows.toLocaleString("en-US")} visible</span>
      </div>
      <div className="dashboard-runtime-table-scroll">
        <table className="dashboard-runtime-table" aria-label="Runtime targets">
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
        {visibleRows === 0 ? <p className="dashboard-runtime-no-results">No Runtime targets match these filters.</p> : null}
      </div>
      {table.getPageCount() > 1 ? (
        <footer className="dashboard-runtime-pagination">
          <span>Page {table.getState().pagination.pageIndex + 1} of {table.getPageCount()}</span>
          <div>
            <button type="button" disabled={!table.getCanPreviousPage()} onClick={() => table.previousPage()}><ChevronLeft size={14} aria-hidden="true" /> Previous</button>
            <button type="button" disabled={!table.getCanNextPage()} onClick={() => table.nextPage()}>Next <ChevronRight size={14} aria-hidden="true" /></button>
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
  return (
    <>
      <div className="dashboard-runtime-summary" aria-label="Runtime resource snapshot">
        <RuntimeMetric icon={<Server size={17} />} label="Active Runtimes" value={summary.observedRuntimeCount.toLocaleString("en-US")} detail={`${summary.managedRuntimeCount} managed · ${summary.unavailableRuntimeCount} unavailable`} />
        <RuntimeMetric icon={<Cpu size={17} />} label="Cumulative CPU / capacity" value={summary.cpuUsageSecondsTotal === null && summary.cpuCapacityCores === null ? "No current sample" : `${formatDashboardDuration(summary.cpuUsageSecondsTotal)} / ${summary.cpuCapacityCores?.toLocaleString("en-US") ?? "—"} cores`} detail={`${summary.cpuCoverageCount}/${summary.observedRuntimeCount} observed Runtimes report CPU time`} />
        <RuntimeMetric icon={<MemoryStick size={17} />} label="Memory now" value={summary.memoryUsageBytes === null && summary.memoryLimitBytes === null ? "No current sample" : `${formatDashboardBytes(summary.memoryUsageBytes)} / ${formatDashboardBytes(summary.memoryLimitBytes)}`} detail={`${summary.memoryCoverageCount}/${summary.observedRuntimeCount} observed Runtimes report usage`} />
        <RuntimeMetric icon={<Gauge size={17} />} label="Reported tokens" value={formatDashboardTokens(summary.totalTokens)} detail={`${summary.tokenCoverageCount}/${summary.sessionCount} Sessions report usage`} />
      </div>

      <RuntimeTrendPanel snapshot={snapshot} stale={stale} loadRuntimeHistory={loadRuntimeHistory} />

      <details className="dashboard-runtime-explorer">
        <summary>
          <span><strong>Runtime targets</strong><small>Search and inspect exact observations · unknown remains unknown, never zero</small></span>
          <span>{model.rows.length.toLocaleString("en-US")} targets · {stale ? "retained snapshot" : "current snapshot"}<ChevronDown size={15} aria-hidden="true" /></span>
        </summary>
        <RuntimeTargets rows={model.rows} onOpenSession={onOpenSession} />
      </details>
    </>
  );
}
