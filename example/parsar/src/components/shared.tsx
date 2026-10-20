import { tr } from "../lib/i18n";
import type { ReactNode } from "react";
import { AlertCircle } from "lucide-react";
import { errorText } from "../lib/api";
import { cn } from "../lib/utils";
export function PageHeader({
  title,
  back,
  narrow,
  children,
}: {
  title: string;
  back?: ReactNode;
  /** Match the 3xl column of form pages. */
  narrow?: boolean;
  children?: ReactNode;
}) {
  return (
    <header className="shrink-0">
      <div
        className={cn(
          "mx-auto flex w-full flex-wrap items-center justify-between gap-4 px-6 pt-8 pb-5 sm:px-10",
          narrow ? "max-w-3xl" : "max-w-6xl",
        )}
      >
        <div className="flex min-w-0 items-center gap-2">
          {back}
          <h1 className="min-w-0 truncate text-xl font-semibold tracking-tight text-fg">
            {title}
          </h1>
        </div>
        {children && (
          <div className="flex flex-wrap items-center gap-2.5">{children}</div>
        )}
      </div>
    </header>
  );
}
export function ErrorNotice({
  error,
  retry,
  className,
  page,
}: {
  error: unknown;
  retry?: () => void;
  className?: string;
  /** Align with the page column instead of the surrounding container. */
  page?: boolean;
}) {
  if (!error) return null;
  const notice = (
    <div
      role="alert"
      className={cn(
        "my-3 flex items-start gap-3 rounded-xl border border-danger-border bg-danger-subtle px-4 py-3 text-sm",
        !page && className,
      )}
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
      <div className="min-w-0 break-words">
        <p>{errorText(error)}</p>
        {retry && (
          <button
            className="mt-1.5 font-medium underline underline-offset-4"
            onClick={retry}
          >
            {tr("重新加载")}
          </button>
        )}
      </div>
    </div>
  );
  return page ? (
    <div className={cn("mx-auto w-full max-w-6xl px-6 sm:px-10", className)}>
      {notice}
    </div>
  ) : (
    notice
  );
}
export function Field({
  label,
  id,
  hint,
  children,
}: {
  label: string;
  id?: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-2">
      <label htmlFor={id} className="block text-sm font-medium text-fg">
        {label}
      </label>
      {children}
      {hint && <p className="text-sm text-fg-muted">{hint}</p>}
    </div>
  );
}
export function PageTabs({
  label,
  active,
  items,
}: {
  label: string;
  active: string;
  items: { key: string; label: string; href: string }[];
}) {
  return (
    <div
      className="mx-auto w-full max-w-6xl shrink-0 px-6 sm:px-10"
      role="tablist"
      aria-label={label}
    >
      <div className="flex items-center gap-6 overflow-x-auto border-b border-line">
        {items.map((item) => (
          <button
            key={item.key}
            role="tab"
            aria-selected={active === item.key}
            className={cn(
              "-mb-px h-10 shrink-0 border-b-2 text-sm transition-colors",
              active === item.key
                ? "border-fg font-medium text-fg"
                : "border-transparent text-fg-muted hover:text-fg",
            )}
            onClick={() => {
              location.hash = item.href;
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
    </div>
  );
}
export function RecordTabs({
  active,
}: {
  active: "conversations" | "runs" | "usage" | "audit";
}) {
  return (
    <PageTabs
      label={tr("对话与记录")}
      active={active}
      items={[
        { key: "conversations", label: tr("对话"), href: "/conversations" },
        { key: "runs", label: tr("运行记录"), href: "/runs" },
        { key: "usage", label: tr("用量"), href: "/usage" },
        { key: "audit", label: tr("审计日志"), href: "/audit" },
      ]}
    />
  );
}
export function Section({
  title,
  action,
  children,
  className,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={cn(
        "space-y-5 rounded-2xl border border-line bg-surface p-6 app-shadow-card sm:p-7",
        className,
      )}
    >
      <div className="flex items-center justify-between gap-4">
        <h2 className="text-lg font-semibold tracking-tight text-fg">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  );
}
export function Toggle({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-center justify-between gap-6 py-1 text-sm text-fg">
      {label}
      <span className="relative inline-flex shrink-0">
        <input
          type="checkbox"
          role="switch"
          className="peer sr-only"
          checked={checked}
          onChange={(event) => onChange(event.target.checked)}
        />
        <span className="h-6 w-10 rounded-full bg-line-strong transition-colors peer-checked:bg-surface-emphasis peer-focus-visible:ring-2 peer-focus-visible:ring-accent/40" />
        <span className="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-surface app-shadow-control transition-transform peer-checked:translate-x-4" />
      </span>
    </label>
  );
}
