import { tr, englishTitle } from "../lib/i18n";
import type { ReactNode } from "react";
import { AlertCircle } from "lucide-react";
import { CircleHelp } from "lucide-react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { errorText } from "../lib/api";
export function PageHeader({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <header className="flex min-h-16 shrink-0 flex-wrap items-center justify-between gap-3 border-b border-line px-6 py-3">
      <div className="flex min-w-0 items-baseline gap-2">
        <h1 className="min-w-0 truncate text-xl font-semibold tracking-tight">
          {title}
        </h1>
        {englishTitle(title) && englishTitle(title) !== title && (
          <span className="shrink-0 text-xs text-fg-muted">
            {englishTitle(title)}
          </span>
        )}
      </div>
      <div className="flex items-center gap-2">{children}</div>
    </header>
  );
}
export function Help({
  children,
  label = tr("了解更多"),
}: {
  children: ReactNode;
  label?: string;
}) {
  return (
    <Tooltip.Root delayDuration={150}>
      <Tooltip.Trigger asChild>
        <button
          type="button"
          aria-label={label}
          className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-fg-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-accent"
        >
          <CircleHelp className="h-4 w-4" />
        </button>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          style={{ pointerEvents: "none" }}
          sideOffset={6}
          className="pointer-events-none app-shadow-floating z-[60] max-w-xs rounded-md border border-line bg-surface px-3 py-2 text-base text-fg"
        >
          {children}
          <Tooltip.Arrow className="fill-surface" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  if (!error) return null;
  return (
    <div
      role="alert"
      className="flex items-start gap-2 border-b border-line px-6 py-3 text-sm"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
      <div className="min-w-0 break-words">
        <p>{errorText(error)}</p>
        {retry && (
          <button className="mt-1 underline underline-offset-4" onClick={retry}>
            {tr("重新加载")}
          </button>
        )}
      </div>
    </div>
  );
}
export function Field({
  label,
  id,
  children,
}: {
  label: string;
  id: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="block text-base text-fg-muted">
        {label}
      </label>
      {children}
    </div>
  );
}
