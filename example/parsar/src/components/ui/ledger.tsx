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
      <div
        className={cn(
          "min-h-0 flex-1 overflow-auto py-6 sm:py-8",
          className,
        )}
        {...props}
      >
        <div className="mx-auto w-full max-w-6xl px-6 sm:px-10">
          <div className="overflow-x-auto rounded-2xl border border-line bg-surface app-shadow-card">
            {children}
          </div>
        </div>
      </div>
    </Columns.Provider>
  );
}
export function LedgerHeader({ children }: { children: ReactNode }) {
  return (
    <div
      style={{ gridTemplateColumns: useContext(Columns) }}
      className="sticky top-0 z-10 grid min-w-max items-center gap-4 border-b border-line bg-surface/90 px-6 h-11 text-sm text-fg-muted backdrop-blur-xs"
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
        "group grid min-w-max min-h-14 items-center gap-4 border-b border-line-muted px-6 py-3 text-sm transition-colors last:border-b-0 hover:bg-surface-subtle/70 [&>span]:min-w-0 [&>span]:truncate",
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}
