import { Check, Copy, Search } from "lucide-react";
import { useEffect, useState, type ButtonHTMLAttributes, type MouseEvent, type ReactNode } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { shortId } from "../lib/format";

/**
 * One list grammar for every resource page: a toolbar (search first, then
 * filters, the count on the right), a framed table whose first column carries
 * the name with its ID underneath, and row actions aligned at the end.
 */

export function ListToolbar({ label, children, summary }: { label: string; children?: ReactNode; summary?: ReactNode }) {
  return (
    <div className="list-toolbar" role="group" aria-label={label}>
      <div className="list-toolbar-controls">{children}</div>
      {summary ? <p className="list-toolbar-summary" aria-live="polite">{summary}</p> : null}
    </div>
  );
}

export function SearchField({
  value,
  onChange,
  placeholder,
  label,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  label?: string;
}) {
  const { t } = useTranslation();
  return (
    <label className="search-control list-search">
      <Search size={14} strokeWidth={1.75} aria-hidden="true" />
      <input type="search" value={value} placeholder={placeholder} aria-label={label ?? t("list.search")} onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

/** "12 total", "3 of 12" while filtering, or "40 loaded" when more pages exist. */
export function listSummary(
  t: TFunction<"common">,
  shown: number,
  total: number,
  options: { hasMore?: boolean; locale?: string } = {},
): string {
  const format = (value: number) => value.toLocaleString(options.locale);
  // With more pages on Core the filter covers loaded rows only, and the count says so.
  if (shown !== total) return t(options.hasMore ? "list.filteredLoaded" : "list.filtered", { shown: format(shown), total: format(total) });
  if (options.hasMore) return t("list.loadedMore", { n: format(total) });
  return t("list.count", { n: format(total) });
}

/** A one-click copy button for an ID. */
export function CopyIdButton({ id, label }: { id: string; label?: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = (event: MouseEvent) => {
    event.stopPropagation();
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (!clipboard) return;
    void clipboard.writeText(id).then(() => setCopied(true), () => undefined);
  };
  const name = copied ? t("actions.copied") : label ?? t("actions.copyId");
  return (
    <button type="button" className="icon-button ghost copyable-id-button" aria-label={name} title={name} onClick={copy}>
      {copied ? <Check size={13} strokeWidth={1.75} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.75} aria-hidden="true" />}
    </button>
  );
}

/** A resource ID with its copy button; compact IDs keep the full value in the tooltip. */
export function CopyableId({ id, compact = false, label }: { id: string; compact?: boolean; label?: string }) {
  return (
    <span className="copyable-id">
      <code title={id}>{compact ? shortId(id) : id}</code>
      <CopyIdButton id={id} label={label} />
    </span>
  );
}

/**
 * The first column of every resource table: the name (or a muted fallback)
 * with the compact ID and its copy button underneath. When `onOpen` is given
 * the name opens the resource.
 */
export function NameCell({
  name,
  id,
  fallback,
  onOpen,
  openLabel,
  openProps,
  idLabel,
  children,
}: {
  name: string | null | undefined;
  id: string;
  fallback?: string;
  onOpen?: () => void;
  openLabel?: string;
  /** Extra attributes for the open button (data hooks, disabled). */
  openProps?: ButtonHTMLAttributes<HTMLButtonElement> & Record<`data-${string}`, string>;
  /** Accessible name of the copy button when a resource-specific one reads better ("Copy File ID"). */
  idLabel?: string;
  /** Optional inline marker after the name, for example a status. */
  children?: ReactNode;
}) {
  const label = name?.trim() ? name : null;
  const title = label ?? fallback ?? id;
  return (
    <span className="name-cell">
      <span className="name-cell-title">
        {onOpen ? (
          <button
            type="button"
            className={label ? "name-cell-link" : "name-cell-link muted"}
            onClick={(event) => { event.stopPropagation(); onOpen(); }}
            aria-label={openLabel}
            {...openProps}
          >
            {title}
          </button>
        ) : (
          <span className={label ? undefined : "muted"}>{title}</span>
        )}
        {children}
      </span>
      <CopyableId id={id} compact label={idLabel} />
    </span>
  );
}

export function RowActions({ children }: { children: ReactNode }) {
  return <span className="row-actions-group">{children}</span>;
}
