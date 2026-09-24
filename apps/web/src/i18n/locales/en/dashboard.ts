export const dashboard = {
  trends: {
    title: "Resource trends", retained: "Retained samples · durable history", local: "Browser-local samples · reset on reload", source: "Runtime metric source", live: "Live", history: "History",
    historyStale: "History stale", historyLoading: "History · loading", durableResolution: "Durable · {{seconds}}s", liveRetrying: "Live · history retrying", liveUnavailable: "Live · history unavailable", liveLoading: "Live · loading history", staleRetrying: "Stale · retrying", liveInterval: "Live · {{seconds}}s",
    durableStatus: "{{status}}; {{count}} Runtime targets", retainedFailure: "Runtime sampling refresh failed; showing retained samples", samplingInterval: "Live Runtime sampling every {{seconds}} seconds",
    bucket: "bucket", buckets: "buckets", sample: "sample", samples: "samples", observations: "{{actual}}/{{expected}} observations", durableRange: "Runtime durable range", liveRange: "Runtime live range",
    refreshFailed: "Durable history refresh failed: {{error}}", notConfigured: "Durable history is not configured; Live samples remain available.", requestFailed: "Durable Runtime history request failed.",
  },
  charts: {
    collecting: "Collecting live samples", durableHistory: "durable history", live: "live", retainedBuckets: "retained buckets", liveSamples: "live samples", chartLabel: "{{title}} {{source}} chart",
    hideSeries: "Hide {{series}} series", showSeries: "Show {{series}} series", hide: "Hide {{series}}", show: "Show {{series}}", resetZoom: "Reset zoom",
    instructions: "Move the pointer over the plot for exact values. Drag horizontally to select and zoom a time range. Double-click or use Reset zoom to restore the full range. Click to pin a time. Use Left and Right arrows to move the pinned selection, and Escape to clear it.",
    pinned: "Pinned", hover: "Hover", unavailable: "Unavailable", allHidden: "All series hidden", sparse: "Sparse samples", showLegend: "Use the legend to show a series", sparseDetail: "{{count}} valid points · a line requires consecutive buckets", emptyDetail: "{{valid}}/2 valid points · {{count}} snapshots · no history is synthesized",
    table: { series: "Series", latest: "Latest value", missing: "Missing samples" }, runtime: "Runtime", usage: "usage", used: "used", configuredLimit: "configured limit", input: "input", output: "output", gridDurable: "Runtime durable-history charts", gridLive: "Runtime live-window charts",
    cpu: { title: "CPU usage", withoutData: "{{count}} runtime targets have no CPU data in this range", durable: "bucketed cumulative-delta utilization · durable history", live: "reported or cumulative-delta utilization · live window", empty: "No retained CPU samples" },
    memory: { title: "Memory usage", durable: "observed Sandbox aggregate / configured limit · durable history", live: "observed Sandbox working set / configured limit · live window", empty: "No retained observed memory samples" },
    active: { series: "active", sandboxTitle: "Active sandboxes", runtimeTitle: "Runtime active", sumDurable: "observed allocations per retained bucket · durable history", sumLive: "lifecycle state active allocations per snapshot · live window", binaryDurable: "observed allocation in retained bucket · 1 active / 0 inactive", binaryLive: "lifecycle state active · 1 active / 0 inactive", active: "Active", inactive: "Inactive", empty: "No retained active Sandbox samples" },
    tokens: { title: "Token throughput", durable: "canonical Session Usage deltas · durable history", live: "Session Usage deltas · missing usage excluded", empty: "No retained token samples", perMinute: "{{value}}/min" },
  },
} as const;
