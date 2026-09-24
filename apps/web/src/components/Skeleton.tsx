/**
 * A first-load placeholder in the shape of the table it stands for. It is shown
 * only when nothing is cached yet; refreshes keep the real rows on screen.
 */
export function TableSkeleton({ label, rows = 6, columns = 5 }: { label: string; rows?: number; columns?: number }) {
  const widths = [38, 22, 18, 26, 14, 20, 16];
  return (
    <div className="table-frame skeleton-table" role="status" aria-label={label} aria-busy="true">
      <div className="skeleton-row skeleton-head">
        {Array.from({ length: columns }, (_, column) => <span key={column} className="skeleton-bar" style={{ width: `${Math.max(8, (widths[column % widths.length] ?? 16) - 8)}%` }} />)}
      </div>
      {Array.from({ length: rows }, (_, row) => (
        <div key={row} className="skeleton-row">
          {Array.from({ length: columns }, (_, column) => <span key={column} className="skeleton-bar" style={{ width: `${widths[(column + row) % widths.length]}%` }} />)}
        </div>
      ))}
    </div>
  );
}
