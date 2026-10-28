import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
  size?: "default" | "compact";
}

/**
 * Flat empty state: a muted icon, an ink title, optional one-line
 * description and one action. No dashed box, no card.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
  size = "default",
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col items-center justify-center gap-3.5 text-center",
        size === "compact" ? "px-6 py-10" : "px-8 py-20",
        className,
      )}
    >
      {Icon && (
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-surface-muted text-fg-muted">
          <Icon className="h-5 w-5" strokeWidth={1.5} aria-hidden="true" />
        </div>
      )}
      <div className="min-w-0 max-w-full space-y-1.5 [overflow-wrap:anywhere]">
        <p className="text-lg font-semibold tracking-tight text-fg">{title}</p>
        {description && (
          <p className="max-w-md text-base leading-relaxed text-fg-muted">
            {description}
          </p>
        )}
      </div>
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
