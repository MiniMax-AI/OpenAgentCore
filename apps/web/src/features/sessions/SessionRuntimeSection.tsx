import type { AgentSession, OpenAIAgentsClient, RuntimeObservation } from "@agents-core-web/agents-client";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { Kpi, KpiStrip, Section, SegmentedControl, type Tone } from "../../components/console-ui";
import { formatBytes, formatCores, formatDateTime, formatPercent, formatRelative, MISSING } from "../../lib/format";
import { RuntimeTrendCharts } from "../dashboard/RuntimeTrendCharts";
import "../dashboard/runtime-observability.css";
import { loadSessionRuntimeHistory, SESSION_RUNTIME_RANGES, type SessionRuntimeHistory, type SessionRuntimeRange } from "./session-runtime";

const HISTORY_REFRESH_MS = 30_000;
type RangeLabel = (typeof SESSION_RUNTIME_RANGES)[number]["label"];

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * CPU, memory and retained samples of one hosted Session. The observation is
 * read again with every history read (`revision`); the retained history on
 * range changes, on manual refresh (`refreshToken`) and every 30 s while the
 * Session is active.
 */
export function SessionRuntimeSection({
  client,
  session,
  active,
  revision,
  refreshToken,
}: {
  client: OpenAIAgentsClient;
  session: AgentSession;
  active: boolean;
  revision: number;
  refreshToken: number;
}) {
  const { t, i18n } = useTranslation("sessions");
  const locale = i18n.resolvedLanguage;
  const [rangeLabel, setRangeLabel] = useState<RangeLabel>(SESSION_RUNTIME_RANGES[0].label);
  const range = SESSION_RUNTIME_RANGES.find((entry) => entry.label === rangeLabel)!.milliseconds as SessionRuntimeRange;
  const [observation, setObservation] = useState<RuntimeObservation | null>(null);
  const [observationError, setObservationError] = useState<string | null>(null);
  const [history, setHistory] = useState<{ state: "loading" | "ready" | "unavailable" | "failed"; value: SessionRuntimeHistory | null; error: string | null }>({ state: "loading", value: null, error: null });
  const [tick, setTick] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    client.retrieveRuntimeObservation(session.id, { signal: controller.signal }).then(
      (value) => { setObservation(value); setObservationError(null); },
      (error: unknown) => { if (!controller.signal.aborted) setObservationError(message(error)); },
    );
    return () => controller.abort();
  }, [client, session.id, revision]);

  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setTick((value) => value + 1), HISTORY_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [active]);

  useEffect(() => {
    const controller = new AbortController();
    setHistory((current) => ({ ...current, state: current.value ? current.state : "loading", error: null }));
    loadSessionRuntimeHistory(client, session, range, controller.signal).then(
      (value) => setHistory(value ? { state: "ready", value, error: null } : { state: "unavailable", value: null, error: null }),
      (error: unknown) => { if (!controller.signal.aborted) setHistory((current) => ({ ...current, state: "failed", error: message(error) })); },
    );
    return () => controller.abort();
    // The Session object changes on every poll; its ID and the range decide what to read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, session.id, range, refreshToken, tick]);

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
      {observationError ? <p className="coverage-note coverage-note-error" role="alert">{t("runtime.observationFailed", { reason: observationError })}</p> : null}
      {history.state === "failed" ? <p className="coverage-note coverage-note-error" role="alert">{t("runtime.historyFailed", { reason: history.error ?? "" })}</p> : null}
      {history.state === "unavailable" ? <p className="coverage-note">{t("runtime.historyUnavailable")}</p> : null}
      {history.state === "loading" && !history.value ? <p className="page-status" role="status">{t("runtime.historyLoading")}</p> : null}
      {history.value ? (
        <div className="session-runtime-charts">
          <RuntimeTrendCharts samples={history.value.samples} source="durable" rangeStart={history.value.rangeStart} rangeEnd={history.value.rangeEnd} activeDisplay="binary" />
        </div>
      ) : null}
    </Section>
  );
}
