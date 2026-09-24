import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import type { RuntimeDashboardSnapshot } from "./runtime-snapshot";
import { RUNTIME_SNAPSHOT_REFRESH_MS } from "./runtime-snapshot";
import { RuntimeTrendCharts, type RuntimeTrendSource } from "./RuntimeTrendCharts";
import {
  RUNTIME_DURABLE_RANGES,
  type RuntimeDurableRange,
  type RuntimeDurableSnapshot,
} from "./runtime-history";
import {
  appendRuntimeTrendSample,
  RUNTIME_TREND_RANGES,
  RUNTIME_TREND_WINDOW_MS,
  runtimeTrendRange,
  type RuntimeTrendRange,
  type RuntimeTrendSample,
} from "./runtime-trends";
import { HelpTip, StatusDot } from "../../components/console-ui";
import "./runtime-observability.css";

export type RuntimeHistoryLoader = (
  snapshot: RuntimeDashboardSnapshot,
  range: RuntimeDurableRange,
  signal: AbortSignal,
) => Promise<RuntimeDurableSnapshot | null>;

export function RuntimeTrendPanel({
  snapshot,
  stale,
  loadRuntimeHistory,
  headingId = "dashboard-runtime-live-heading",
  title,
  allowSourceSelection = false,
  activeDisplay = "sum",
}: {
  snapshot: RuntimeDashboardSnapshot;
  stale: boolean;
  loadRuntimeHistory: RuntimeHistoryLoader;
  headingId?: string;
  title?: string;
  allowSourceSelection?: boolean;
  activeDisplay?: "sum" | "binary";
}) {
  const { t, i18n } = useTranslation("dashboard");
  const { t: tMetrics } = useTranslation("metrics");
  const locale = i18n.resolvedLanguage;
  const displayTitle = title ?? t("trends.title");
  const [trendSamples, setTrendSamples] = useState<RuntimeTrendSample[]>(() => appendRuntimeTrendSample([], snapshot));
  const [selectedTrendRange, setSelectedTrendRange] = useState<RuntimeTrendRange>(RUNTIME_TREND_WINDOW_MS);
  const [selectedDurableRange, setSelectedDurableRange] = useState<RuntimeDurableRange>(RUNTIME_DURABLE_RANGES[0].milliseconds);
  const [durableSnapshot, setDurableSnapshot] = useState<RuntimeDurableSnapshot | null>(null);
  const [durableState, setDurableState] = useState<"connecting" | "ready" | "unavailable" | "failed">("connecting");
  const [durableError, setDurableError] = useState<string | null>(null);
  const [durableRefresh, setDurableRefresh] = useState(0);
  const [sourcePreference, setSourcePreference] = useState<RuntimeTrendSource>("durable");
  const latestSnapshotRef = useRef(snapshot);
  latestSnapshotRef.current = snapshot;
  const durableTargetKey = useMemo(() => snapshot.observations
    .filter((observation) => observation.mode === "openai_hosted" && observation.environment_id !== null)
    .map((observation) => observation.session_id)
    .sort()
    .join("|"), [snapshot.observations]);
  const visibleTrendSamples = useMemo(
    () => runtimeTrendRange(trendSamples, selectedTrendRange),
    [selectedTrendRange, trendSamples],
  );
  const durableAvailable = durableState !== "unavailable" || durableSnapshot !== null;
  const source: RuntimeTrendSource = allowSourceSelection && sourcePreference === "live"
    ? "live"
    : durableSnapshot !== null
      ? "durable"
      : "live";
  const selectedSamples = source === "durable"
    ? durableSnapshot?.samples ?? []
    : visibleTrendSamples;
  const latestTrendSample = selectedSamples.at(-1);
  const selectedRange = source === "durable" ? selectedDurableRange : selectedTrendRange;
  const rangeEnd = source === "durable" && durableSnapshot !== null
    ? durableSnapshot.rangeEnd
    : latestTrendSample?.sampledAt ?? snapshot.loadedAt;
  const rangeStart = source === "durable" && durableSnapshot !== null
    ? durableSnapshot.rangeStart
    : rangeEnd - selectedTrendRange;

  useEffect(() => {
    setTrendSamples((current) => appendRuntimeTrendSample(current, snapshot));
  }, [snapshot]);

  useEffect(() => {
    const timer = window.setInterval(() => setDurableRefresh((current) => current + 1), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setDurableState("connecting");
    setDurableError(null);
    void loadRuntimeHistory(latestSnapshotRef.current, selectedDurableRange, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      if (result === null) {
        setDurableSnapshot(null);
        setDurableState("unavailable");
        return;
      }
      setDurableSnapshot(result);
      setDurableState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setDurableState("failed");
      setDurableError(error instanceof Error ? error.message : t("trends.requestFailed"));
    });
    return () => controller.abort();
  }, [durableRefresh, durableTargetKey, loadRuntimeHistory, selectedDurableRange]);

  const rangeOptions = source === "durable" ? RUNTIME_DURABLE_RANGES : RUNTIME_TREND_RANGES;
  const waitingForHistory = source === "live" && (!allowSourceSelection || sourcePreference === "durable");
  const sourceStatus = source === "durable"
    ? durableState === "failed"
      ? t("trends.historyStale")
      : durableState === "connecting"
        ? t("trends.historyLoading")
        : t("trends.durableResolution", { seconds: durableSnapshot?.resolutionSeconds ?? 0 })
    : waitingForHistory
      ? durableState === "failed"
        ? t("trends.liveRetrying")
        : durableState === "unavailable"
          ? t("trends.liveUnavailable")
          : t("trends.liveLoading")
    : stale
      ? t("trends.staleRetrying")
      : t("trends.liveInterval", { seconds: RUNTIME_SNAPSHOT_REFRESH_MS / 1_000 });
  const sourceStatusStale = source === "durable" ? durableState === "failed" : stale || waitingForHistory && durableState === "failed";

  return (
    <section className="dashboard-runtime-live" aria-labelledby={headingId}>
      <header className="dashboard-runtime-live-toolbar">
        <div className="console-section-title">
          <h3 id={headingId}>{displayTitle}</h3>
          {/* Source, sampling and provenance live behind the title's help tip instead of small print. */}
          <HelpTip>
            {t(source === "durable" ? "trends.retained" : "trends.local")}
            <br />
            {sourceStatusStale ? null : <>{sourceStatus}<br /></>}
            {selectedSamples.length.toLocaleString(locale)} {t(source === "durable" ? selectedSamples.length === 1 ? "trends.bucket" : "trends.buckets" : selectedSamples.length === 1 ? "trends.sample" : "trends.samples")}
            {source === "durable" && durableSnapshot ? <> · {t("trends.observations", { actual: durableSnapshot.sampleCount, expected: durableSnapshot.expectedSampleCount })}</> : null}
            {latestTrendSample ? <> · <time dateTime={new Date(latestTrendSample.sampledAt).toISOString()}>{new Date(latestTrendSample.sampledAt).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time></> : null}
          </HelpTip>
        </div>
        <div className="dashboard-runtime-live-controls">
          {allowSourceSelection ? (
            <div className="dashboard-runtime-source" role="group" aria-label={t("trends.source")}>
              <button type="button" aria-pressed={source === "live"} onClick={() => setSourcePreference("live")}>{t("trends.live")}</button>
              <button type="button" aria-pressed={source === "durable"} disabled={!durableAvailable} onClick={() => setSourcePreference("durable")}>{t("trends.history")}</button>
            </div>
          ) : null}
          {sourceStatusStale ? <StatusDot tone="warning" label={sourceStatus} /> : null}
          <div className="dashboard-runtime-range segmented" role="group" aria-label={t(source === "durable" ? "trends.durableRange" : "trends.liveRange")}>
            {rangeOptions.map((range) => (
              <button
                key={range.label}
                type="button"
                aria-pressed={selectedRange === range.milliseconds}
                onClick={() => {
                  if (source === "durable") setSelectedDurableRange(range.milliseconds as RuntimeDurableRange);
                  else setSelectedTrendRange(range.milliseconds as RuntimeTrendRange);
                }}
              >
                {tMetrics(`range.${range.label}`)}
              </button>
            ))}
          </div>
        </div>
      </header>
      {durableState === "failed" && durableError ? <p className="dashboard-runtime-history-error" role="status">{t("trends.refreshFailed", { error: durableError })}</p> : null}
      {durableState === "unavailable" ? <p className="dashboard-runtime-history-note">{t("trends.notConfigured")}</p> : null}
      <RuntimeTrendCharts samples={selectedSamples} source={source} rangeStart={rangeStart} rangeEnd={rangeEnd} activeDisplay={activeDisplay} />
    </section>
  );
}
