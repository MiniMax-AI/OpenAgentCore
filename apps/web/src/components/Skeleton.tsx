const widths = [38, 22, 18, 26, 14, 20, 16];

/**
 * A first-load placeholder in the shape of the table it stands for. It is shown
 * only when nothing is cached yet; refreshes keep the real rows on screen.
 */
export function TableSkeleton({ label, rows = 6, columns = 5 }: { label: string; rows?: number; columns?: number }) {
  return (
    <div className="table-frame skeleton-table" role="status" aria-label={label} aria-busy="true">
      <TableSkeletonRows rows={rows} columns={columns} />
    </div>
  );
}

function TableSkeletonRows({ rows, columns }: { rows: number; columns: number }) {
  return (
    <>
      <div className="skeleton-row skeleton-head">
        {Array.from({ length: columns }, (_, column) => <span key={column} className="skeleton-bar" style={{ width: `${Math.max(8, (widths[column % widths.length] ?? 16) - 8)}%` }} />)}
      </div>
      {Array.from({ length: rows }, (_, row) => (
        <div key={row} className="skeleton-row">
          {Array.from({ length: columns }, (_, column) => <span key={column} className="skeleton-bar" style={{ width: `${widths[(column + row) % widths.length]}%` }} />)}
        </div>
      ))}
    </>
  );
}

/**
 * A monitor page before its first figures: the headline strip and chart
 * panels in their final places, so the data lands without a layout shift.
 */
export function DashboardSkeleton({ label, figures = 6, charts = 2 }: { label: string; figures?: number; charts?: number }) {
  return (
    <div className="skeleton-stack" role="status" aria-label={label} aria-busy="true">
      {figures ? <div className="kpi-strip">
        {Array.from({ length: figures }, (_, index) => (
          <div key={index} className="kpi skeleton-kpi">
            <span className="skeleton-bar skeleton-label" style={{ width: `${40 + (index * 13) % 24}%` }} />
            <span className="skeleton-bar skeleton-figure" style={{ width: `${28 + (index * 11) % 20}%` }} />
          </div>
        ))}
      </div> : null}
      <div className="chart-grid">
        {Array.from({ length: charts }, (_, index) => (
          <div key={index} className="chart-panel skeleton-chart">
            <span className="skeleton-bar skeleton-label" style={{ width: "32%" }} />
            <span className="skeleton-plot" />
          </div>
        ))}
      </div>
    </div>
  );
}

/** A detail page before its resource arrives: the facts card, then a table. */
export function DetailSkeleton({ label, facts = 8 }: { label: string; facts?: number }) {
  return (
    <div className="skeleton-stack" role="status" aria-label={label} aria-busy="true">
      <div className="resource-facts">
        {Array.from({ length: facts }, (_, index) => (
          <div key={index}>
            <span className="skeleton-bar skeleton-label" style={{ width: `${36 + (index * 7) % 20}%` }} />
            <span className="skeleton-bar" style={{ width: `${52 + (index * 17) % 36}%` }} />
          </div>
        ))}
      </div>
      <div className="table-frame skeleton-table">
        <TableSkeletonRows rows={4} columns={4} />
      </div>
    </div>
  );
}
