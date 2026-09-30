import {
  Bot,
  FileJson,
  Search,
  Settings2,
  UserRound,
  Wrench,
  X,
} from "lucide-react";
import {
  Fragment,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";

import type {
  AgentSession,
  AgentTurn,
  SessionItem,
} from "@oac/agents-client";

import { TraceTimingPanel } from "./TraceTimingPanel";
import { TurnFailure } from "../session-diagnostics";
import { HelpTip } from "../../../components/console-ui";
import { MessageMarkdown } from "../../../components/MessageMarkdown";
import { StatusIcon, type StatusKind } from "../../../components/StatusIcon";
import { ApplyPatchDiffViewer } from "../items/ApplyPatchDiffViewer";
import { parseParsarApplyPatch } from "../items/apply-patch";
import {
  buildTraceModel,
  filterTraceModel,
  type TraceGroup,
  type TraceRow,
  type TraceValue,
} from "./trace-model";

/** Load state of one history part (Items or Turns). */
export type HistoryLoadState = "idle" | "loading" | "ready" | "failed";

interface TraceViewProps {
  id: string;
  labelledBy: string;
  session: AgentSession;
  turns: AgentTurn[];
  items: SessionItem[];
  detailState: HistoryLoadState;
  detailError: string | null;
  turnState: HistoryLoadState;
  turnError: string | null;
}

type DetailTab = "summary" | "preview" | "payload" | "result" | "schema" | "timing" | "raw";

interface DetailTabDefinition {
  id: DetailTab;
  label: string;
}

function pretty(value: unknown, unavailable = "Unavailable"): string {
  if (typeof value === "string") return value;
  if (value === undefined) return unavailable;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return unavailable;
  }
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

function formatDuration(milliseconds: number, locale = "en-US"): string {
  const value = Math.max(0, milliseconds);
  if (value < 1_000) return `${value.toLocaleString(locale)} ms`;
  const seconds = value / 1_000;
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 && !Number.isInteger(seconds) ? 1 : 0)} s`;
  const minutes = Math.floor(seconds / 60);
  const remainder = Math.floor(seconds % 60);
  return `${minutes}m ${String(remainder).padStart(2, "0")}s`;
}

function formatTimestamp(seconds: number | null | undefined, locale: string, unknown: string): string {
  if (typeof seconds !== "number" || !Number.isSafeInteger(seconds) || seconds < 0) return unknown;
  const timestamp = new Date(seconds * 1_000);
  return Number.isNaN(timestamp.getTime())
    ? unknown
    : new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(timestamp);
}

function itemStatusKind(status: TraceRow["status"]): StatusKind {
  if (status === "in_progress") return "running";
  if (status === "failed") return "failed";
  if (status === "incomplete") return "interrupted";
  return status === "completed" ? "completed" : "queued";
}

function turnStatusKind(status: unknown): StatusKind | null {
  if (status === "in_progress" || status === "waiting") return "running";
  if (status === "failed") return "failed";
  if (status === "cancelled") return "cancelled";
  if (status === "completed") return "completed";
  return status === "queued" ? "queued" : null;
}

function rowIcon(row: TraceRow): ReactNode {
  if (row.kind === "configured_instructions") return <Settings2 size={14} strokeWidth={1.5} aria-hidden="true" />;
  if (row.kind === "user_message") return <UserRound size={14} strokeWidth={1.5} aria-hidden="true" />;
  if (row.kind === "assistant_message") return <Bot size={14} strokeWidth={1.5} aria-hidden="true" />;
  if (row.kind === "tool_call" || row.kind === "tool_result") return <Wrench size={14} strokeWidth={1.5} aria-hidden="true" />;
  return <FileJson size={14} strokeWidth={1.5} aria-hidden="true" />;
}

function valueLabel<T>(value: TraceValue<T>, t: Translate, format: (available: T) => string = String): string {
  if (value.state === "available" && value.value !== null) return format(value.value);
  return value.state === "unknown" ? t("common.unknown") : t("common.unavailable");
}

function rowDurationPresentation(value: TraceValue<number>, t: Translate, locale: string): {
  visible: string;
  assistive: string;
  title: string;
} {
  if (value.state === "available" && value.value !== null) {
    const duration = formatDuration(value.value, locale);
    return {
      visible: duration,
      assistive: t("trace.toolDurationSentence", { duration }),
      title: t("trace.toolDuration", { duration }),
    };
  }
  if (value.state === "unknown") {
    return {
      visible: t("common.unknown"),
      assistive: t("trace.durationUnknown"),
      title: t("trace.durationInvalid"),
    };
  }
  return {
    visible: "—",
    assistive: t("trace.perItemNotProvided"),
    title: t("trace.perItemTimingMissing"),
  };
}

function durationDetailLabel(value: TraceValue<number>, t: Translate, locale: string): string {
  if (value.state === "available" && value.value !== null) return formatDuration(value.value, locale);
  return value.state === "unknown" ? t("common.unknown") : t("trace.notProvidedByCore");
}

function turnDurationLabel(value: TraceValue<number>, t: Translate, locale: string): string {
  if (value.state === "available" && value.value !== null) return t("trace.turnDuration", { duration: formatDuration(value.value, locale) });
  return value.state === "unknown" ? t("trace.turnTimeUnknown") : t("trace.turnTimeNotProvided");
}

function rowExcerpt(row: TraceRow, t: Translate): string {
  if (row.text.state === "available" && row.text.value) return row.text.value;
  if (row.tool?.payload.state === "available") return t("trace.payloadAvailable");
  if (row.tool?.result.state === "available") return t("trace.resultAvailable");
  return row.text.state === "unknown" ? t("common.unknown") : t("trace.noPreview");
}

function localizedRowTitle(row: TraceRow, t: Translate): string {
  if (row.kind === "configured_instructions") return t("trace.configuredInstructions");
  if (row.kind === "user_message") return t("trace.userMessage");
  if (row.kind === "assistant_message") return t("trace.assistantMessage");
  if (row.kind === "unknown_item") return t("trace.unsupportedItem");
  return row.title;
}

function localizedGroupTitle(group: TraceGroup, t: Translate): string {
  if (group.kind === "configuration") return t("trace.agentConfiguration");
  const number = group.title.match(/\d+/u)?.[0];
  return group.kind === "turn"
    ? t("trace.turnNumber", { number })
    : t("trace.unassociatedItemsNumber", { number });
}

function applyPatchItem(row: TraceRow): SessionItem | null {
  if (row.kind !== "tool_call" || row.tool?.type !== "function_call") return null;
  return row.sourceItems.find((candidate) => candidate.type === "function_call" && candidate.name === "apply_patch") ?? null;
}

function traceTabs(row: TraceRow): DetailTabDefinition[] {
  const tabs: DetailTabDefinition[] = [{ id: "summary", label: "Summary" }];
  if (row.text.state === "available" || applyPatchItem(row)) tabs.push({ id: "preview", label: "Preview" });
  if (row.tool) {
    tabs.push({ id: "payload", label: "Payload" }, { id: "result", label: "Result" });
    if (row.tool.configuredFunction.state === "available") tabs.push({ id: "schema", label: "Schema" });
  }
  if (row.kind !== "configured_instructions") tabs.push({ id: "timing", label: "Timing" });
  tabs.push({ id: "raw", label: "Raw" });
  return tabs;
}

function onTabKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
  const tabs = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>("[role=tab]") ?? []);
  const index = tabs.indexOf(event.currentTarget);
  if (index < 0) return;
  const target = event.key === "ArrowRight"
    ? tabs[(index + 1) % tabs.length]
    : event.key === "ArrowLeft"
      ? tabs[(index - 1 + tabs.length) % tabs.length]
      : event.key === "Home"
        ? tabs[0]
        : event.key === "End"
          ? tabs[tabs.length - 1]
          : null;
  if (!target) return;
  event.preventDefault();
  target.click();
  target.focus();
}

function ValuePanel({ value }: { value: TraceValue<unknown> }) {
  const { t } = useTranslation("sessions");
  if (value.state !== "available") {
    return <p className="trace-detail-unavailable">{value.state === "unknown" ? t("common.unknown") : t("trace.contractUnavailable")}</p>;
  }
  return <pre className="trace-detail-code">{pretty(value.value, t("common.unavailable"))}</pre>;
}

function TraceSummaryPanel({ row, group, session }: { row: TraceRow; group: TraceGroup; session: AgentSession }) {
  const { t, i18n } = useTranslation("sessions");
  const translate = t as Translate;
  const locale = i18n.resolvedLanguage || "en";
  const rawUsage = group.turn?.usage as unknown;
  const turnUsage = rawUsage !== null && typeof rawUsage === "object"
    && Number.isSafeInteger((rawUsage as Record<string, unknown>).input_tokens)
    && Number.isSafeInteger((rawUsage as Record<string, unknown>).output_tokens)
    && Number.isSafeInteger((rawUsage as Record<string, unknown>).total_tokens)
    ? rawUsage as AgentTurn["usage"]
    : null;
  const configuredModel = typeof session.agent.model === "string" ? session.agent.model : t("common.unknown");
  return (
    <div className="trace-detail-summary">
      <dl>
        <div><dt className="trace-dt-help">{t("trace.source")}<HelpTip>{t("trace.scopeBoundary")}</HelpTip></dt><dd>{row.kind === "configured_instructions" ? t("trace.agentSnapshot") : t("trace.itemSnapshot")}</dd></div>
        <div><dt>{t("common.status")}</dt><dd>{row.status ? t(`status.${row.status}` as never) : t("trace.notApplicable")}</dd></div>
        <div><dt>{t("trace.group")}</dt><dd>{localizedGroupTitle(group, translate)}</dd></div>
        <div><dt>{t("trace.configuredModel")}</dt><dd><code>{configuredModel}</code></dd></div>
        <div><dt>{t("trace.itemTiming")}</dt><dd>{t("trace.notProvidedByCore")}</dd></div>
        {row.durationMs.state === "available" && row.durationMs.value !== null ? (
          <div><dt>{t("trace.toolReportedDuration")}</dt><dd>{formatDuration(row.durationMs.value, locale)}</dd></div>
        ) : null}
      </dl>
      {turnUsage ? (
        <section className="trace-detail-usage" aria-label={t("trace.turnScopedUsage")}>
          <strong>{t("trace.turnScopedTokens")}</strong>
          <dl>
            <div><dt>{t("usage.input")}</dt><dd>{turnUsage.input_tokens.toLocaleString(locale)}</dd></div>
            <div><dt>{t("usage.output")}</dt><dd>{turnUsage.output_tokens.toLocaleString(locale)}</dd></div>
            <div><dt>{t("usage.total")}</dt><dd>{turnUsage.total_tokens.toLocaleString(locale)}</dd></div>
          </dl>
        </section>
      ) : null}
    </div>
  );
}

function TracePreviewPanel({ row }: { row: TraceRow }) {
  const { t } = useTranslation("sessions");
  if (applyPatchItem(row)) return <ApplyPatchPreview row={row} />;
  if (row.text.state !== "available" || row.text.value === null) return <p className="trace-detail-unavailable">{t("trace.contractUnavailable")}</p>;
  if (row.kind === "assistant_message") return <div className="trace-detail-preview"><MessageMarkdown content={row.text.value} /></div>;
  return <pre className="trace-detail-text">{row.text.value}</pre>;
}


function ApplyPatchPreview({ row }: { row: TraceRow }) {
  const item = applyPatchItem(row);
  const patch = item ? parseParsarApplyPatch(item.arguments) : null;
  if (!item || !patch) return null;
  const result = row.tool?.result.state === "available" ? row.tool.result.value : undefined;
  const effectiveItem = row.status ? { ...item, status: row.status } : item;
  return <ApplyPatchDiffViewer item={effectiveItem} patch={patch} result={result} />;
}

function TraceDetailPanel({
  row,
  group,
  session,
  activeTab,
}: {
  row: TraceRow;
  group: TraceGroup;
  session: AgentSession;
  activeTab: DetailTab;
}) {
  const { t } = useTranslation("sessions");
  if (activeTab === "summary") return <TraceSummaryPanel row={row} group={group} session={session} />;
  if (activeTab === "preview") return <TracePreviewPanel row={row} />;
  if (activeTab === "payload") return <ValuePanel value={row.tool?.payload ?? { state: "unavailable", value: null }} />;
  if (activeTab === "result") return <ValuePanel value={row.tool?.result ?? { state: "unavailable", value: null }} />;
  if (activeTab === "schema") return <ValuePanel value={row.tool?.configuredFunction ?? { state: "unavailable", value: null }} />;
  if (activeTab === "timing") return <TraceTimingPanel row={row} group={group} />;
  return <pre className="trace-detail-code">{pretty(row.safeRaw.length === 1 ? row.safeRaw[0] : row.safeRaw, t("common.unavailable"))}</pre>;
}

export function TraceView({
  id,
  labelledBy,
  session,
  turns,
  items,
  detailState,
  detailError,
  turnState,
  turnError,
}: TraceViewProps) {
  const { t, i18n } = useTranslation("sessions");
  const translate = t as Translate;
  const locale = i18n.resolvedLanguage || "en";
  const [query, setQuery] = useState("");
  const [selectedRowId, setSelectedRowId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<DetailTab>("summary");
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const lastTriggerRef = useRef<HTMLButtonElement | null>(null);
  const model = useMemo(() => buildTraceModel({ turns, items, agent: session.agent }), [items, session.agent, turns]);
  const filtered = useMemo(() => filterTraceModel(model, query), [model, query]);
  const selectedRow = selectedRowId ? model.rows.find((row) => row.id === selectedRowId) ?? null : null;
  const selectedGroup = selectedRow ? model.groups.find((group) => group.id === selectedRow.groupId) ?? null : null;
  const tabs = selectedRow ? traceTabs(selectedRow) : [];

  useEffect(() => {
    setQuery("");
    setSelectedRowId(null);
    setActiveTab("summary");
  }, [session.id]);

  useEffect(() => {
    if (!selectedRow) return;
    setActiveTab((current) => traceTabs(selectedRow).some((tab) => tab.id === current) ? current : "summary");
  }, [selectedRow]);

  useEffect(() => {
    if (!selectedRow) return;
    const frame = window.requestAnimationFrame(() => closeButtonRef.current?.focus({ preventScroll: true }));
    return () => window.cancelAnimationFrame(frame);
  }, [selectedRow?.id]);

  const selectRow = (row: TraceRow, trigger: HTMLButtonElement) => {
    lastTriggerRef.current = trigger;
    setSelectedRowId(row.id);
    setActiveTab("summary");
  };

  const closeDetails = () => {
    setSelectedRowId(null);
    const trigger = lastTriggerRef.current;
    window.requestAnimationFrame(() => {
      const fallback = searchInputRef.current?.isConnected
        ? searchInputRef.current
        : document.getElementById(labelledBy);
      const target = trigger?.isConnected ? trigger : fallback;
      target?.focus({ preventScroll: true });
    });
  };

  const orderedRows = filtered.rows.filter((row) => row.kind !== "configured_instructions");
  const sequenceColumns = Math.max(orderedRows.length, 1);
  const sequenceStyle = {
    gridTemplateColumns: `50px repeat(${sequenceColumns}, minmax(18px, 1fr))`,
    minWidth: `${50 + sequenceColumns * 20}px`,
  } satisfies CSSProperties;
  const detailPanelId = `${id}-detail-panel`;
  const detailContentId = `${id}-detail-content`;
  const activeDetailTabId = `${id}-detail-tab-${activeTab}`;
  const knownTime = model.summary.turnsWithKnownWallClock
    ? `${formatDuration(model.summary.knownTurnWallClockDurationMs, locale)}${model.summary.turnsWithUnknownWallClock ? ` · ${t("trace.unknownCount", { count: model.summary.turnsWithUnknownWallClock })}` : ""}`
    : model.summary.turnCount ? t("common.unknown") : t("common.unavailable");

  return (
    <section
      className={`trace-workbench ${selectedRow ? "has-detail" : ""}`}
      id={id}
      role="tabpanel"
      aria-labelledby={labelledBy}
      onKeyDown={(event) => {
        if (event.key === "Escape" && selectedRow) {
          event.stopPropagation();
          closeDetails();
        }
      }}
    >
      <header className="trace-toolbar">
        <dl className="trace-metrics">
          <div><dt>{t("trace.knownTurnTime")}</dt><dd>{knownTime}</dd></div>
          <div><dt>{t("trace.turns")}</dt><dd>{model.summary.turnCount.toLocaleString(locale)}</dd></div>
          <div><dt>{t("trace.toolCalls")}</dt><dd>{model.summary.toolCallCount.toLocaleString(locale)}</dd></div>
        </dl>
        <label className="trace-search">
          <Search size={14} strokeWidth={1.5} aria-hidden="true" />
          <span className="sr-only">{t("trace.search")}</span>
          <input ref={searchInputRef} type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("trace.search")} />
        </label>
      </header>

      <section className="trace-order-overview" aria-label={t("trace.durableExecutionOrder")}>
        <header>
          <strong>{t("trace.durableOrder")}</strong>
          <HelpTip>{t("trace.equalWidth")}. {t("trace.contractNote")}</HelpTip>
        </header>
        <div className="trace-order-scroll">
          <div className="trace-order-grid" style={sequenceStyle}>
            {(["input", "model", "tools"] as const).map((lane, laneIndex) => (
              <Fragment key={lane}>
                <span style={{ gridColumn: 1, gridRow: laneIndex + 1 }}>{t(`trace.lane.${lane}` as never)}</span>
                {orderedRows.map((row, index) => row.lane === lane ? (
                <button
                  type="button"
                  className={`trace-order-item trace-order-item-${lane}`}
                  style={{ gridColumn: index + 2, gridRow: laneIndex + 1 }}
                  aria-label={t("trace.openItem", { label: t(`trace.label.${row.label.toLowerCase()}` as never), title: localizedRowTitle(row, translate) })}
                  aria-pressed={selectedRow?.id === row.id}
                  aria-expanded={selectedRow?.id === row.id}
                  aria-controls={detailPanelId}
                  title={`${t(`trace.label.${row.label.toLowerCase()}` as never)} · ${localizedRowTitle(row, translate)}`}
                  key={row.id}
                  onClick={(event) => selectRow(row, event.currentTarget)}
                />
                ) : null)}
              </Fragment>
            ))}
          </div>
        </div>
      </section>

      {turnState === "loading" || detailState === "loading" ? (
        <p className="trace-load-state" role="status">{t("trace.loadingHistory")}</p>
      ) : null}
      {turnState === "failed" ? (
        <div className="trace-load-error" role="alert"><strong>{t("trace.turnHistoryIncomplete")}</strong><span>{turnError || t("turns.coreReadFailed")}</span></div>
      ) : null}
      {detailState === "failed" ? (
        <div className="trace-load-error" role="alert"><strong>{t("trace.itemHistoryIncomplete")}</strong><span>{detailError || t("trace.itemReadFailed")}</span></div>
      ) : null}

      <div className="trace-workspace">
        <div className="trace-ledger" aria-label={t("trace.items")}>
          {filtered.groups.length ? filtered.groups.map((group) => {
            const groupStatusKind = group.turn ? turnStatusKind(group.turn.status) : null;
            return (
            <section className={`trace-group trace-group-${group.kind}`} key={group.id} aria-labelledby={`${group.id}-title`}>
              <header className="trace-group-header">
                <strong id={`${group.id}-title`}>{localizedGroupTitle(group, translate)}</strong>
                {group.turn && groupStatusKind ? <StatusIcon status={groupStatusKind} title={t("turns.statusTitle", { status: t(`status.${group.turn.status}` as never) })} /> : null}
                {group.turn ? <span>{t(`status.${group.turn.status}` as never)}</span> : null}
                {group.turn ? (
                  <span className="trace-group-duration" title={t("trace.turnWallClockDuration")}>{turnDurationLabel(group.turnWallClockDurationMs, translate, locale)}</span>
                ) : null}
              </header>
              {group.turn ? <TurnFailure turn={group.turn} /> : null}
              {group.rows.length ? (
                <ol>
                  {group.rows.map((row) => {
                    const duration = rowDurationPresentation(row.durationMs, translate, locale);
                    return (
                    <li key={row.id}>
                      <button
                        type="button"
                        className={`trace-ledger-row trace-ledger-row-${row.lane}`}
                        aria-pressed={selectedRow?.id === row.id}
                        aria-expanded={selectedRow?.id === row.id}
                        aria-controls={detailPanelId}
                        onClick={(event) => selectRow(row, event.currentTarget)}
                      >
                        <span className={`trace-row-icon trace-row-icon-${row.lane}`}>{rowIcon(row)}</span>
                        <span className="trace-row-label">{t(`trace.label.${row.label.toLowerCase()}` as never)}</span>
                        <span className="trace-row-copy">
                          <strong>{localizedRowTitle(row, translate)}</strong>
                          <small>{rowExcerpt(row, translate)}</small>
                        </span>
                        {row.status ? <StatusIcon status={itemStatusKind(row.status)} title={t("trace.itemStatus", { status: t(`status.${row.status}` as never) })} /> : null}
                        <span className="trace-row-duration" data-duration-state={row.durationMs.state} title={duration.title}>
                          <span aria-hidden="true">{duration.visible}</span>
                          <span className="trace-visually-hidden">{duration.assistive}</span>
                        </span>
                      </button>
                    </li>
                    );
                  })}
                </ol>
              ) : <p className="trace-group-empty">{t("trace.noItemsForTurn")}</p>}
            </section>
            );
          }) : (
            <div className="trace-empty"><Search size={18} strokeWidth={1.5} aria-hidden="true" /><p>{t("trace.noMatches")}</p></div>
          )}
        </div>

        {selectedRow && selectedGroup ? (
          <aside className="trace-detail" id={detailPanelId} aria-label={t("trace.itemDetails")}>
            <header className="trace-detail-header">
              <span className={`trace-row-label trace-row-label-${selectedRow.lane}`}>{t(`trace.label.${selectedRow.label.toLowerCase()}` as never)}</span>
              <div><strong>{localizedRowTitle(selectedRow, translate)}</strong><small>{localizedGroupTitle(selectedGroup, translate)}</small></div>
              <button ref={closeButtonRef} className="icon-button ghost" type="button" aria-label={t("trace.closeDetails")} onClick={closeDetails}>
                <X size={16} strokeWidth={1.5} aria-hidden="true" />
              </button>
            </header>
            <div className="trace-detail-tabs" role="tablist" aria-label={t("trace.detailView")}>
              {tabs.map((tab) => (
                <button
                  id={`${id}-detail-tab-${tab.id}`}
                  type="button"
                  role="tab"
                  aria-controls={detailContentId}
                  aria-selected={activeTab === tab.id}
                  tabIndex={activeTab === tab.id ? 0 : -1}
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  onKeyDown={onTabKeyDown}
                >
                  {t(`trace.tabs.${tab.id}` as never)}
                </button>
              ))}
            </div>
            <div
              className="trace-detail-content"
              id={detailContentId}
              role="tabpanel"
              aria-labelledby={activeDetailTabId}
              tabIndex={0}
            >
              <TraceDetailPanel row={selectedRow} group={selectedGroup} session={session} activeTab={activeTab} />
            </div>
          </aside>
        ) : null}
      </div>
    </section>
  );
}
