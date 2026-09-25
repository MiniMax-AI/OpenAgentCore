import { OpenAIAgentsClient } from "./client";
import type { ReadOptions } from "./types";

export type OperatorMetricsRange = "1h" | "6h" | "24h";

export interface RequestMetricBucket {
  start: string;
  route_family: string;
  outcome: "success" | "client_error" | "server_error";
  count: number;
  latency_sum_ms: number;
  latency_bucket_counts: number[];
}

export interface CollectorMetricBucket {
  start: string;
  source: string;
  attempted_count: number;
  observed_count: number;
  unavailable_count: number;
  timeout_count: number;
  dropped_count: number;
  export_failed_count: number;
}

export interface OperatorMetricsSummary {
  generated_at: string;
  start: string;
  end: string;
  step_seconds: number;
  requests: RequestMetricBucket[];
  collector: CollectorMetricBucket[];
  turns: Array<{ start: string; status: "completed" | "failed" | "cancelled"; count: number; queue_p95_ms: number | null; execution_p95_ms: number | null }>;
  tools: Array<{ start: string; category: string; outcome: "success" | "error" | "cancelled" | "unknown"; count: number; timed_count: number; duration_p95_ms: number | null }>;
}

/** Deployment administrator extension; never uses a project credential. */
export class ObservabilityAdminClient extends OpenAIAgentsClient {
  retrieveSummary(range: OperatorMetricsRange, options?: ReadOptions): Promise<OperatorMetricsSummary> {
    return this.request(`/summary?range=${range}`, { signal: options?.signal }, undefined, false);
  }
}
