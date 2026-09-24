import { ChevronDown, ChevronUp } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { HelpTip, Kpi, KpiStrip, Section, SegmentedControl, StatusDot, type Tone } from "../../components/console-ui";
import { formatCompact, formatDateTime, formatInteger, formatPercent, formatRelative, MISSING } from "../../lib/format";
import {
  AGENT_USAGE_RANGES,
  type AgentUsageReport,
  type AgentUsageTotals,
  LARGE_SESSION_COUNT,
  type OtherAgentUsage,
  type UsageSessionStatus,
  usageCoverage,
} from "./agent-usage";
import type { AgentUsageController } from "./use-agent-usage";

import "./AgentUsage.css";

export interface AgentUsageModel {
  controller: AgentUsageController;
  /** Present only once the selected range is completely read. */
  report: AgentUsageReport | null;
}

/** Placeholder while Sessions are still being read; distinct from a missing value. */
const PENDING = "…";

const OTHER_PREVIEW_ROWS = 8;

const STATUS_ORDER: ReadonlyArray<{ status: UsageSessionStatus; tone: Tone }> = [
  { status: "in_progress", tone: "pending" },
  { status: "requires_action", tone: "warning" },
  { status: "failed", tone: "danger" },
  { status: "idle", tone: "neutral" },
];

const TOKEN_KINDS = ["input", "output", "total", "cached", "reasoning"] as const;

const nowSeconds = () => Math.floor(Date.now() / 1_000);

/**
 * Coverage never rounds to a misleading 0% or 100%: any reported Session shows
 * at least 0.1%, and any unreported Session keeps it below 100%.
 */
export function formatUsageCoverage(totals: AgentUsageTotals, noData: string, locale?: string): string {
  const coverage = usageCoverage(totals);
  if (coverage === null) return MISSING;
  if (totals.reported === 0) return noData;
  if (totals.reported === totals.sessions) return formatPercent(1, locale);
  return formatPercent(Math.min(0.99, Math.max(0.001, coverage)), locale);
}

function tokenValue(
  totals: AgentUsageTotals,
  value: number | null,
  noData: string,
  format: (value: number | null) => string,
): string {
  if (totals.sessions === 0) return MISSING;
  if (totals.reported === 0) return noData;
  return format(value);
}

function statusLabel(status: UsageSessionStatus, t: TFunction<"agents">): string {
  return t(`usage.statuses.${status}`);
}

function isPending(usage: AgentUsageModel): boolean {
  return usage.controller.status === "loading" || usage.controller.status === "idle";
}

function unavailableValue(usage: AgentUsageModel): string {
  return isPending(usage) ? PENDING : MISSING;
}

function LastActive({ value, locale }: { value: number | null; locale?: string }) {
  if (value === null) return <>{MISSING}</>;
  return (
    <time dateTime={new Date(value * 1_000).toISOString()} title={formatDateTime(value, locale)}>
      {formatRelative(value, nowSeconds(), locale)}
    </time>
  );
}

export function AgentUsageRangeControl({ usage }: { usage: AgentUsageModel }) {
  const { t } = useTranslation("agents");
  return (
    <div className="agent-usage-range">
      <span className="agent-usage-range-label" aria-hidden="true">{t("usage.rangeLabel")}</span>
      <SegmentedControl
        label={t("usage.rangeLabel")}
        value={usage.controller.range}
        options={AGENT_USAGE_RANGES.map((range) => ({ value: range, label: t(`usage.ranges.${range}`) }))}
        onChange={usage.controller.setRange}
      />
      <HelpTip>{t("usage.help")} {t("usage.caveat")}</HelpTip>
    </div>
  );
}

/** Progress, cancellation, retry and the large-collection hint for the current read. */
export function AgentUsageStatus({ usage }: { usage: AgentUsageModel }) {
  const { t, i18n } = useTranslation("agents");
  const locale = i18n.resolvedLanguage;
  const { controller } = usage;
  const count = (value: number) => formatInteger(value, locale);
  if (controller.status === "loading") {
    const large = controller.progress.sessions > LARGE_SESSION_COUNT;
    return (
      <div className="agent-usage-status">
        <span role="status">{t("usage.reading", { formattedCount: count(controller.progress.sessions) })}</span>
        <button className="text-action" type="button" onClick={controller.cancel}>{t("usage.cancel")}</button>
        {large ? (
          <span className="agent-usage-warning" role="note">
            {controller.range === "7d"
              ? t("usage.largeShortest", { formattedCount: count(LARGE_SESSION_COUNT) })
              : t("usage.large", { formattedCount: count(LARGE_SESSION_COUNT) })}
          </span>
        ) : null}
      </div>
    );
  }
  if (controller.status === "failed") {
    return (
      <div className="agent-usage-status agent-usage-status-error">
        <span role="alert">{t("usage.failed")}{controller.error ? ` ${controller.error}` : ""}</span>
        <button className="text-action" type="button" onClick={controller.resume}>{t("usage.retry")}</button>
      </div>
    );
  }
  if (controller.status === "cancelled") {
    return (
      <div className="agent-usage-status">
        <span role="status">{t("usage.cancelled", { count: controller.progress.sessions, formattedCount: count(controller.progress.sessions) })}</span>
        <button className="text-action" type="button" onClick={controller.resume}>{t("usage.continue")}</button>
      </div>
    );
  }
  if (controller.status === "ready" && controller.unrecognized.length) {
    const known = controller.unrecognized.flatMap((entry) => entry.id ? [entry.id] : []);
    const count = controller.unrecognized.length;
    return (
      <div className="agent-usage-status">
        <span role="status">
          {controller.range === "all"
            ? t("usage.unrecognized", { count, formattedCount: formatInteger(count, locale) })
            : t("usage.unrecognizedUpTo", { count, formattedCount: formatInteger(count, locale) })}
        </span>
        <HelpTip>{t("usage.unrecognizedHelp")}{known.length ? ` ${known.join(", ")}` : ""}</HelpTip>
      </div>
    );
  }
  return null;
}

/** The four key figures of one saved Agent as numeric table cells. */
export function AgentUsageCells({ usage, agentId }: { usage: AgentUsageModel; agentId: string }) {
  const { t, i18n } = useTranslation("agents");
  const locale = i18n.resolvedLanguage;
  const totals = usage.report?.byAgent.get(agentId) ?? null;
  if (!totals) {
    const placeholder = unavailableValue(usage);
    return (
      <>
        <td className="numeric agent-usage-pending">{placeholder}</td>
        <td className="numeric agent-usage-pending">{placeholder}</td>
        <td className="numeric agent-usage-pending">{placeholder}</td>
        <td className="numeric agent-usage-pending">{placeholder}</td>
      </>
    );
  }
  const noData = t("usage.noData");
  return (
    <>
      <td className="numeric" title={formatInteger(totals.sessions, locale)}>{formatCompact(totals.sessions, locale)}</td>
      <td className="numeric" title={totals.reported > 0 ? formatInteger(totals.tokens.total, locale) : undefined}>
        {tokenValue(totals, totals.tokens.total, noData, (value) => formatCompact(value, locale))}
      </td>
      <td className="numeric">{formatUsageCoverage(totals, noData, locale)}</td>
      <td className="numeric"><LastActive value={totals.lastActiveAt} locale={locale} /></td>
    </>
  );
}

/** Sessions whose Agent is not a loaded saved Agent, kept visible under their raw Agent ID. */
export function OtherAgentUsageSection({ usage, query = "" }: { usage: AgentUsageModel; query?: string }) {
  const { t, i18n } = useTranslation("agents");
  const locale = i18n.resolvedLanguage;
  const [expanded, setExpanded] = useState(false);
  const tableId = useId();
  const report = usage.report;
  if (!report || report.other.length === 0) return null;
  const normalized = query.trim().toLowerCase();
  const groups = normalized
    ? report.other.filter((group) => (group.agentId ?? "").toLowerCase().includes(normalized))
    : report.other;
  if (groups.length === 0) return null;
  const visible = expanded || normalized ? groups : groups.slice(0, OTHER_PREVIEW_ROWS);
  const hidden = groups.length - OTHER_PREVIEW_ROWS;
  const noData = t("usage.noData");
  const row = (group: OtherAgentUsage) => (
    <tr key={group.agentId ?? ""}>
      <th scope="row">{group.agentId === null ? <span className="agent-null-value">{t("usage.other.missingId")}</span> : <code>{group.agentId}</code>}</th>
      <td className="numeric">{formatInteger(group.totals.sessions, locale)}</td>
      <td className="numeric">{tokenValue(group.totals, group.totals.tokens.total, noData, (value) => formatInteger(value, locale))}</td>
      <td className="numeric">{formatUsageCoverage(group.totals, noData, locale)}</td>
      <td className="numeric"><LastActive value={group.totals.lastActiveAt} locale={locale} /></td>
    </tr>
  );
  return (
    <Section
      className="agent-usage-other"
      headingId="agent-usage-other-heading"
      title={t("usage.other.title")}
      help={t("usage.other.help")}
    >
      <div className="table-frame">
        <table className="data-table" id={tableId} aria-labelledby="agent-usage-other-heading">
          <thead>
            <tr>
              <th scope="col">{t("usage.other.agentId")}</th>
              <th scope="col" className="numeric">{t("usage.sessions")}</th>
              <th scope="col" className="numeric">{t("usage.tokens")}</th>
              <th scope="col" className="numeric">{t("usage.coverage")}</th>
              <th scope="col" className="numeric">{t("usage.lastActive")}</th>
            </tr>
          </thead>
          <tbody>{visible.map(row)}</tbody>
        </table>
      </div>
      {!normalized && hidden > 0 ? (
        <footer className="table-footer agent-usage-other-footer">
          <button className="button outline" type="button" aria-controls={tableId} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            {expanded ? <ChevronUp size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
            {expanded ? t("usage.other.showLess") : t("usage.other.showAll", { formattedCount: formatInteger(groups.length, locale) })}
          </button>
        </footer>
      ) : null}
    </Section>
  );
}

/** Complete statistics for one saved Agent in its detail view. */
export function AgentUsagePanel({ usage, agentId }: { usage: AgentUsageModel; agentId: string }) {
  const { t, i18n } = useTranslation("agents");
  const locale = i18n.resolvedLanguage;
  const totals = usage.report?.byAgent.get(agentId) ?? null;
  const noData = t("usage.noData");
  const placeholder = unavailableValue(usage);
  const integer = (value: number | null) => formatInteger(value, locale);
  return (
    <section className="agent-usage-panel" aria-labelledby="agent-usage-panel-title" aria-busy={isPending(usage) || undefined}>
      <header>
        <h2 id="agent-usage-panel-title">{t("usage.title")}</h2>
        <AgentUsageRangeControl usage={usage} />
      </header>
      <AgentUsageStatus usage={usage} />
      <KpiStrip label={t("usage.title")}>
        <Kpi label={t("usage.sessions")} value={totals ? integer(totals.sessions) : placeholder} />
        <Kpi
          label={t("usage.tokens")}
          value={totals ? tokenValue(totals, totals.tokens.total, noData, (value) => formatCompact(value, locale)) : placeholder}
        />
        <Kpi label={t("usage.coverage")} value={totals ? formatUsageCoverage(totals, noData, locale) : placeholder} help={t("usage.coverageHelp")} />
        <Kpi label={t("usage.lastActive")} value={totals ? <LastActive value={totals.lastActiveAt} locale={locale} /> : placeholder} />
      </KpiStrip>
      {totals ? (
        <dl className="agent-usage-breakdown">
          <div>
            <dt>{t("usage.status")}</dt>
            <dd className="agent-usage-statuses">
              {STATUS_ORDER.map(({ status, tone }) => (
                <StatusDot key={status} tone={tone} label={<>{statusLabel(status, t)} <strong>{integer(totals.statuses[status])}</strong></>} />
              ))}
              {totals.statuses.unknown > 0 ? (
                <StatusDot tone="neutral" label={<>{statusLabel("unknown", t)} <strong>{integer(totals.statuses.unknown)}</strong></>} />
              ) : null}
            </dd>
          </div>
          <div>
            <dt>{t("usage.tokens")}</dt>
            <dd>
              <dl className="agent-usage-tokens">
                {TOKEN_KINDS.map((kind) => (
                  <div key={kind}>
                    <dt>{t(`usage.tokenKinds.${kind}`)}</dt>
                    <dd>{tokenValue(totals, totals.tokens[kind], noData, integer)}</dd>
                  </div>
                ))}
              </dl>
            </dd>
          </div>
          <div>
            <dt>{t("usage.coverage")}</dt>
            <dd>{totals.sessions > 0
              ? t("usage.coverageDetail", { count: totals.sessions, formattedCount: integer(totals.sessions), reported: integer(totals.reported) })
              : MISSING}</dd>
          </div>
          <div>
            <dt>{t("usage.lastActive")}</dt>
            <dd>{formatDateTime(totals.lastActiveAt, locale)}</dd>
          </div>
        </dl>
      ) : null}
    </section>
  );
}
