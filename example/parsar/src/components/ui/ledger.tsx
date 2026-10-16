import {
  createContext,
  useContext,
  type CSSProperties,
  type HTMLAttributes,
  type ReactNode,
} from "react";
import { cn } from "../../lib/utils";
const Columns = createContext("minmax(200px,1fr)");
export function Ledger({
  columns,
  children,
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { columns: string }) {
  return (
    <Columns.Provider value={columns}>
      <div className={cn("min-h-0 flex-1 overflow-auto", className)} {...props}>
        {children}
      </div>
    </Columns.Provider>
  );
}
export function LedgerHeader({ children }: { children: ReactNode }) {
  return (
    <div
      style={{ gridTemplateColumns: useContext(Columns) }}
      className="sticky top-0 z-10 grid min-w-max items-center gap-3 border-b border-line bg-surface px-6 h-7 text-xs text-fg-muted"
    >
      {children}
    </div>
  );
}
export function LedgerRow({
  className,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  const style: CSSProperties = { gridTemplateColumns: useContext(Columns) };
  return (
    <div
      style={style}
      className={cn(
        "group grid min-w-max min-h-9 items-center gap-3 border-b border-line-muted px-6 text-sm hover:bg-surface-subtle [&>span]:min-w-0 [&>span]:truncate",
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}
