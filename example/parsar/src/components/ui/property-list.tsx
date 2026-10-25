import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// Shared tracks align independent property groups without letting labels crowd values.
export function PropertyList({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <dl
      className={cn(
        "m-0 grid grid-cols-[min(9rem,40%)_minmax(0,1fr)] gap-x-4 gap-y-1.5",
        className,
      )}
    >
      {children}
    </dl>
  );
}

export function Property({
  label,
  children,
  mono,
  className,
  title,
}: {
  label: ReactNode;
  children: ReactNode;
  mono?: boolean;
  className?: string;
  /** Overrides the tooltip; by default a string value becomes its own. */
  title?: string;
}) {
  const plainText =
    typeof children === "string" || typeof children === "number";
  return (
    <>
      <dt
        className="min-h-8 min-w-0 py-1.5 text-sm leading-5 text-fg-muted [overflow-wrap:anywhere]"
        title={typeof label === "string" ? label : undefined}
      >
        {label}
      </dt>
      <dd
        title={title ?? (typeof children === "string" ? children : undefined)}
        className={cn(
          "m-0 min-w-0 text-sm text-fg",
          plainText
            ? "min-h-8 py-1.5 leading-5 [overflow-wrap:anywhere]"
            : "flex min-h-8 items-center gap-2 truncate",
          mono && "font-mono text-sm",
          className,
        )}
      >
        {children}
      </dd>
    </>
  );
}
