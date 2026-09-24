import { CircleHelp, RefreshCw, type LucideIcon } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import * as m from "motion/react-m";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

/**
 * Shared page grammar for the administrator console. Every page uses the same
 * header, body padding, section heading, metric strip, status and meter
 * vocabulary so that screens read as one product.
 */

const TIP_WIDTH = 288;

/**
 * Explanations live behind a small circled question mark instead of lines of
 * small print. The tip opens on hover, keyboard focus or click and renders in a
 * portal so framed containers cannot clip it.
 */
export function HelpTip({ children, label, id: fixedId }: { children: ReactNode; label?: string; id?: string }) {
  const { t } = useTranslation();
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [position, setPosition] = useState<{ top: number; left: number; above: boolean } | null>(null);
  const [pinned, setPinned] = useState(false);
  const generatedId = useId();
  // A fixed id lets a form control keep pointing at the explanation with aria-describedby.
  const id = fixedId ?? generatedId;

  const show = useCallback(() => {
    const rect = buttonRef.current?.getBoundingClientRect();
    if (!rect) return;
    const left = Math.min(Math.max(8, rect.left - 12), window.innerWidth - TIP_WIDTH - 8);
    const above = rect.bottom + 160 > window.innerHeight && rect.top > 180;
    setPosition({ top: above ? rect.top - 8 : rect.bottom + 8, left, above });
  }, []);
  const hide = useCallback(() => {
    setPosition(null);
    setPinned(false);
  }, []);

  useEffect(() => {
    if (!position) return;
    const close = () => hide();
    window.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    return () => {
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
    };
  }, [hide, position]);

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className="help-tip"
        aria-label={label ?? t("actions.help")}
        aria-describedby={id}
        aria-expanded={position !== null}
        onMouseEnter={show}
        onMouseLeave={() => { if (!pinned) hide(); }}
        onFocus={show}
        onBlur={hide}
        onClick={() => { if (position && pinned) hide(); else { show(); setPinned(true); } }}
        onKeyDown={(event) => { if (event.key === "Escape") hide(); }}
      >
        <CircleHelp size={13} strokeWidth={1.7} aria-hidden="true" />
      </button>
      {/* The explanation stays in the document for assistive technology; the popover is its visual copy. */}
      <span id={id} className="visually-hidden">{children}</span>
      {position && typeof document !== "undefined" ? createPortal(
        <div
          role="tooltip"
          aria-hidden="true"
          className={position.above ? "help-tip-popover above" : "help-tip-popover"}
          style={{ top: position.top, left: position.left, maxWidth: TIP_WIDTH }}
        >
          {children}
        </div>,
        document.body,
      ) : null}
    </>
  );
}

export function PageHeader({
  title,
  help,
  actions,
  headingId,
}: {
  title: ReactNode;
  help?: ReactNode;
  actions?: ReactNode;
  headingId?: string;
}) {
  return (
    <header className="page-header console-page-header">
      <div className="console-page-heading">
        <h1 id={headingId}>{title}</h1>
        {help ? <HelpTip>{help}</HelpTip> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

export function RefreshButton({
  onClick,
  refreshing = false,
  disabled = false,
  label,
  updatedAt,
}: {
  onClick: () => void;
  refreshing?: boolean;
  disabled?: boolean;
  label?: string;
  /** Shown as the button's tooltip instead of a visible timestamp. */
  updatedAt?: string | null;
}) {
  const { t } = useTranslation();
  const name = refreshing ? t("actions.refreshing") : label ?? t("actions.refresh");
  return (
    <button
      className="icon-button refresh-button"
      type="button"
      onClick={onClick}
      disabled={disabled || refreshing}
      aria-busy={refreshing}
      aria-label={name}
      title={!refreshing && updatedAt ? `${name}\n${t("actions.updatedAt", { time: updatedAt })}` : name}
    >
      <RefreshCw className={refreshing ? "refresh-spinning" : undefined} size={16} strokeWidth={1.6} aria-hidden="true" />
    </button>
  );
}

export function PageBody({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={["console-page-body", className].filter(Boolean).join(" ")}>{children}</div>;
}

export function Section({
  title,
  help,
  actions,
  headingId,
  children,
  className,
}: {
  title: ReactNode;
  help?: ReactNode;
  actions?: ReactNode;
  headingId: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={["console-section", className].filter(Boolean).join(" ")} aria-labelledby={headingId}>
      <header className="console-section-header">
        <div className="console-section-title">
          <h2 id={headingId}>{title}</h2>
          {help ? <HelpTip>{help}</HelpTip> : null}
        </div>
        {actions ? <div className="console-section-actions">{actions}</div> : null}
      </header>
      {children}
    </section>
  );
}

export type Tone = "ok" | "warning" | "danger" | "neutral" | "pending";

export function StatusDot({ tone, label }: { tone: Tone; label: ReactNode }) {
  return (
    <span className={`status-dot status-dot-${tone}`}>
      <span className="status-dot-mark" aria-hidden="true" />
      <span>{label}</span>
    </span>
  );
}

export function KpiStrip({ children, label }: { children: ReactNode; label: string }) {
  return <dl className="kpi-strip" aria-label={label}>{children}</dl>;
}

export function Kpi({
  label,
  value,
  help,
  tone,
}: {
  label: ReactNode;
  value: ReactNode;
  help?: ReactNode;
  tone?: Tone;
}) {
  return (
    <div className={tone ? `kpi kpi-${tone}` : "kpi"}>
      <dt>
        <span>{label}</span>
        {help ? <HelpTip>{help}</HelpTip> : null}
      </dt>
      <dd>
        {tone ? <span className="kpi-tone" aria-hidden="true" /> : null}
        <span className="kpi-value">{value}</span>
      </dd>
    </div>
  );
}

/** A ratio against a limit. The fill carries severity; the track stays in the same ramp. */
export function Meter({
  value,
  limit,
  label,
  warnAt = 0.9,
  dangerAt = 1,
  color,
}: {
  value: number | null;
  limit: number | null;
  label: string;
  warnAt?: number;
  dangerAt?: number;
  /** Fixed identity colour for a share (for example a status); disables the threshold tones. */
  color?: string;
}) {
  const ratio = value !== null && limit !== null && limit > 0 ? Math.min(1, Math.max(0, value / limit)) : null;
  const tone = ratio === null ? "unknown" : color ? "share" : ratio >= dangerAt ? "danger" : ratio >= warnAt ? "warning" : "ok";
  return (
    <span
      className={`meter meter-${tone}`}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={limit ?? undefined}
      aria-valuenow={value ?? undefined}
    >
      <span className="meter-fill" style={{ width: ratio === null ? 0 : `${Math.max(ratio * 100, ratio > 0 ? 3 : 0)}%`, background: color }} />
    </span>
  );
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
}: {
  icon?: LucideIcon;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="console-empty">
      {Icon ? <Icon size={20} strokeWidth={1.5} aria-hidden="true" /> : null}
      <p className="console-empty-title">{title}</p>
      {description ? <p className="console-empty-description">{description}</p> : null}
      {action ? <div className="console-empty-action">{action}</div> : null}
    </div>
  );
}

export function SegmentedControl<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: ReadonlyArray<{ value: T; label: string; count?: string | number }>;
  onChange: (value: T) => void;
}) {
  const groupRef = useRef<HTMLDivElement>(null);
  const thumbId = useId();
  const move = (delta: number) => {
    const index = options.findIndex((option) => option.value === value);
    const next = options[(index + delta + options.length) % options.length];
    if (!next) return;
    onChange(next.value);
    window.requestAnimationFrame(() => groupRef.current?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus());
  };
  return (
    <div
      ref={groupRef}
      className="segmented"
      role="radiogroup"
      aria-label={label}
      onKeyDown={(event) => {
        if (event.key === "ArrowRight" || event.key === "ArrowDown") { event.preventDefault(); move(1); }
        if (event.key === "ArrowLeft" || event.key === "ArrowUp") { event.preventDefault(); move(-1); }
      }}
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={option.value === value}
          tabIndex={option.value === value ? 0 : -1}
          className={option.value === value ? "active" : undefined}
          onClick={() => onChange(option.value)}
        >
          {option.value === value ? <m.span className="segmented-thumb" layoutId={`segmented-thumb${thumbId}`} aria-hidden="true" /> : null}
          <span className="segmented-label">{option.label}</span>
          {option.count !== undefined ? <span className="segmented-count">{option.count}</span> : null}
        </button>
      ))}
    </div>
  );
}
