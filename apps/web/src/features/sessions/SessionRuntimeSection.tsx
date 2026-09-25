import type { AgentSession } from "@agents-core-web/agents-client";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { failedLast, useFailureToast } from "../../components/Toast";
import { DashboardSkeleton } from "../../components/Skeleton";
import { Kpi, KpiStrip, Section, SegmentedControl, type Tone } from "../../components/console-ui";
import { formatBytes, formatCores, formatDateTime, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { RuntimeCharts } from "../metrics/RuntimeCharts";
import type { RuntimeTrendSample } from "../dashboard/runtime-trends";

import { SESSION_RUNTIME_RANGES, type SessionRuntimeHistory, type SessionRuntimeRange } from "./session-runtime";
import { sessionObservationQuery, sessionRuntimeHistoryQuery } from "./session-queries";

/** Seconds between consecutive samples; history is evenly bucketed. */
function sampleSpacing(samples: readonly RuntimeTrendSample[]): number {
  const [first, second] = samples;
  return first && second ? Math.max(1, Math.round((second.sampledAt - first.sampledAt) / 1000)) : 60;
}

const HISTORY_REFRESH_MS = 30_000;
type RangeLabel = (typeof SESSION_RUNTIME_RANGES)[number]["label"];

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Runs `action` whenever `value` changes after the first render. */
function useOnChange(value: unknown, action: () => void) {
  const seen = useRef(value);
  useEffect(() => {
    if (Object.is(seen.current, value)) return;
    seen.current = value;
    action();
  }, [value, action]);
}

/**
 * CPU, memory and retained samples of one hosted Session, through the query
 * cache. The observation is read again with every history read (`revision`);
 * the retained history on range changes, on manual refresh (`refreshToken`)
 * and every 30 s while the Session is active. A refresh keeps the last values
 * on screen.
 */
export function SessionRuntimeSection({
  projectId,
  session,
  active,
  revision,
  refreshToken,
}: {
  projectId: string;
  session: AgentSession;
  active: boolean;
  revision: number;
  refreshToken: number;
}) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const [rangeLabel, setRangeLabel] = useState<RangeLabel>(SESSION_RUNTIME_RANGES[0].label);
  const range = SESSION_RUNTIME_RANGES.find((entry) => entry.label === rangeLabel)!.milliseconds as SessionRuntimeRange;

  const observationRead = useQuery(sessionObservationQuery(projectId, session.id));
  useOnChange(revision, observationRead.refetch);
  const observation = observationRead.data ?? null;
  const observationError = observationRead.isError ? message(observationRead.error) : null;

  // The Session object changes on every poll; its ID and the range decide what to read.
  const historyRead = useQuery({
    ...sessionRuntimeHistoryQuery(projectId, session, range),
    placeholderData: keepPreviousData,
    refetchInterval: active ? HISTORY_REFRESH_MS : false,
  });
  useOnChange(refreshToken, historyRead.refetch);
  const history: { state: "loading" | "ready" | "unavailable" | "failed"; value: SessionRuntimeHistory | null; error: string | null } = {
    state: historyRead.isError ? "failed" : historyRead.data === undefined ? "loading" : historyRead.data === null ? "unavailable" : "ready",
    value: historyRead.data ?? null,
    error: historyRead.isError ? message(historyRead.error) : null,
  };
  // Both reads repeat while the Session runs; a failure is reported once while it lasts.
  useFailureToast(failedLast(observationRead), t("runtime.observationFailed", { reason: observationError ?? "" }), "session-runtime-observation");
  useFailureToast(history.value !== null && failedLast(historyRead), t("runtime.historyFailed", { reason: history.error ?? "" }), "session-runtime-history");

  let stateLabel = MISSING;
  let stateTone: Tone | undefined;
  if (observation?.status === "observed") stateLabel = t(`runtime.lifecycle.${observation.lifecycle_state}`);
  else if (observation?.reason) { stateLabel = t(`runtime.reason.${observation.reason}`); stateTone = observation.status === "unavailable" ? "warning" : undefined; }
  const cpu = observation?.status === "observed" ? observation.cpu : null;
  const memory = observation?.status === "observed" ? observation.memory : null;
  const cpuValue = cpu?.usage_cores != null && cpu.capacity_cores != null
    ? t("runtime.cores", { used: formatCores(cpu.usage_cores, locale), capacity: formatCores(cpu.capacity_cores, locale) })
    : cpu?.utilization_ratio != null ? formatPercent(cpu.utilization_ratio, locale) : MISSING;
  const memoryValue = memory?.usage_bytes != null
    ? memory.limit_bytes != null ? `${formatBytes(memory.usage_bytes)} / ${formatBytes(memory.limit_bytes)}` : formatBytes(memory.usage_bytes)
    : MISSING;
  const observedAt = observation?.status === "observed" ? observation.observed_at : null;

  return (
    <Section
      headingId="session-runtime-heading"
      title={t("runtime.title")}
      help={t("runtime.help")}
      actions={(
        <SegmentedControl
          label={t("runtime.range")}
          value={rangeLabel}
          options={SESSION_RUNTIME_RANGES.map((entry) => ({ value: entry.label, label: t(`runtime.ranges.${entry.label}`) }))}
          onChange={setRangeLabel}
        />
      )}
    >
      <KpiStrip label={t("runtime.title")}>
        <Kpi label={t("runtime.state")} value={stateLabel} tone={stateTone} />
        <Kpi label={t("runtime.cpu")} value={cpuValue} />
        <Kpi label={t("runtime.memory")} value={memoryValue} />
        <Kpi label={t("runtime.observed")} value={observedAt === null ? MISSING : <span title={formatDateTime(observedAt, locale)}>{formatRelative(observedAt, Math.floor(Date.now() / 1000), locale)}</span>} />
      </KpiStrip>
      {history.state === "failed" && !history.value ? <p className="page-status">{t("runtime.historyFailed", { reason: history.error ?? "" })}</p> : null}
      {history.state === "unavailable" ? <p className="coverage-note">{t("runtime.historyUnavailable")}</p> : null}
      {history.state === "loading" && !history.value ? <DashboardSkeleton label={t("runtime.historyLoading")} figures={0} /> : null}
      {history.value ? (
        <RuntimeCharts samples={history.value.samples} resolutionSeconds={sampleSpacing(history.value.samples)} />
      ) : null}
    </Section>
  );
}
