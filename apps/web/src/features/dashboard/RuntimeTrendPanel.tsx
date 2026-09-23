import { useEffect, useMemo, useState } from "react";

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
  title = "Resource trends",
}: {
  snapshot: RuntimeDashboardSnapshot;
  stale: boolean;
  loadRuntimeHistory: RuntimeHistoryLoader;
  headingId?: string;
  title?: string;
}) {
  const [trendSamples, setTrendSamples] = useState<RuntimeTrendSample[]>(() => appendRuntimeTrendSample([], snapshot));
  const [selectedTrendRange, setSelectedTrendRange] = useState<RuntimeTrendRange>(RUNTIME_TREND_WINDOW_MS);
  const [selectedDurableRange, setSelectedDurableRange] = useState<RuntimeDurableRange>(RUNTIME_DURABLE_RANGES[0].milliseconds);
  const [durableSnapshot, setDurableSnapshot] = useState<RuntimeDurableSnapshot | null>(null);
  const [durableState, setDurableState] = useState<"connecting" | "ready" | "unavailable" | "failed">("connecting");
  const [durableError, setDurableError] = useState<string | null>(null);
  const visibleTrendSamples = useMemo(
    () => runtimeTrendRange(trendSamples, selectedTrendRange),
    [selectedTrendRange, trendSamples],
  );
  const source: RuntimeTrendSource = durableState === "unavailable" && durableSnapshot === null
    ? "live"
    : "durable";
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
    const controller = new AbortController();
    setDurableState("connecting");
    setDurableError(null);
    void loadRuntimeHistory(snapshot, selectedDurableRange, controller.signal).then((result) => {
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
      setDurableError(error instanceof Error ? error.message : "Durable Runtime history request failed.");
    });
    return () => controller.abort();
  }, [loadRuntimeHistory, selectedDurableRange, snapshot]);

  const rangeOptions = source === "durable" ? RUNTIME_DURABLE_RANGES : RUNTIME_TREND_RANGES;
  const sourceStatus = source === "durable"
    ? durableState === "failed"
      ? "History stale"
      : durableState === "connecting"
        ? "History · loading"
        : `Durable · ${durableSnapshot?.resolutionSeconds ?? 0}s`
    : stale
      ? "Stale · retrying"
      : `Live · ${RUNTIME_SNAPSHOT_REFRESH_MS / 1_000}s`;
  const sourceStatusStale = source === "durable" ? durableState === "failed" : stale;

  return (
    <section className="dashboard-runtime-live" aria-labelledby={headingId}>
      <header className="dashboard-runtime-live-toolbar">
        <div>
          <h3 id={headingId}>{title}</h3>
          <p>{source === "durable" ? "ClickHouse-backed retained samples · explicit history source" : "Browser-local samples · reset on reload"}</p>
        </div>
        <div className="dashboard-runtime-live-controls">
          <span
            className={`${sourceStatusStale ? "dashboard-runtime-live-status dashboard-runtime-live-status-stale" : "dashboard-runtime-live-status"}${source === "durable" ? " dashboard-runtime-live-status-durable" : ""}`}
            aria-label={source === "durable"
              ? `${sourceStatus}; ${durableSnapshot?.targetCount ?? 0} Runtime targets`
              : stale
                ? "Runtime sampling refresh failed; showing retained samples"
                : `Live Runtime sampling every ${RUNTIME_SNAPSHOT_REFRESH_MS / 1_000} seconds`}
          >
            <i aria-hidden="true" />{sourceStatus}
          </span>
          <span className="dashboard-runtime-sample-count">
            {selectedSamples.length} {source === "durable" ? selectedSamples.length === 1 ? "bucket" : "buckets" : selectedSamples.length === 1 ? "sample" : "samples"}
            {source === "durable" && durableSnapshot ? <> · {durableSnapshot.sampleCount}/{durableSnapshot.expectedSampleCount} observations</> : null}
            {latestTrendSample ? <> · <time dateTime={new Date(latestTrendSample.sampledAt).toISOString()}>{new Date(latestTrendSample.sampledAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time></> : null}
          </span>
          <div className="dashboard-runtime-range" role="group" aria-label={source === "durable" ? "Runtime durable range" : "Runtime live range"}>
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
                {range.label}
              </button>
            ))}
          </div>
        </div>
      </header>
      {durableState === "failed" && durableError ? <p className="dashboard-runtime-history-error" role="status">Durable history refresh failed: {durableError}</p> : null}
      {durableState === "unavailable" ? <p className="dashboard-runtime-history-note">Durable history is not configured; Live samples remain available.</p> : null}
      <RuntimeTrendCharts samples={selectedSamples} source={source} rangeStart={rangeStart} rangeEnd={rangeEnd} />
    </section>
  );
}
