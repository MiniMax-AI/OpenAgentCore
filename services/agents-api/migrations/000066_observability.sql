-- +goose Up
-- Runtime samples remain a bounded telemetry projection, not execution or Usage authority.
ALTER TABLE runtime_history_samples RENAME TO observability_runtime_samples;
ALTER INDEX runtime_history_samples_pkey RENAME TO observability_runtime_samples_pkey;
ALTER INDEX runtime_history_samples_retention_idx RENAME TO observability_runtime_samples_retention_idx;
CREATE INDEX turns_completed_time_idx ON turns (completed_at) WHERE completed_at IS NOT NULL;

-- Histogram bins: <=10, 25, 50, 100, 250, 500, 1000, 2500, 5000,
-- 10000 milliseconds, and >10000 milliseconds. Counts are disjoint.
CREATE TABLE observability_request_minute_buckets (
    bucket_start timestamptz NOT NULL,
    route_family text NOT NULL CHECK (length(route_family) BETWEEN 1 AND 64),
    method text NOT NULL CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OTHER')),
    outcome text NOT NULL CHECK (outcome IN ('success', 'client_error', 'server_error')),
    request_count bigint NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    latency_sum_ms bigint NOT NULL DEFAULT 0 CHECK (latency_sum_ms >= 0),
    latency_bucket_counts bigint[] NOT NULL DEFAULT array_fill(0::bigint, ARRAY[11])
        CHECK (array_length(latency_bucket_counts, 1) = 11),
    PRIMARY KEY (bucket_start, route_family, method, outcome)
);

CREATE TABLE observability_model_attempts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id uuid NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL CHECK (finished_at >= started_at),
    model_family text NOT NULL CHECK (length(model_family) BETWEEN 1 AND 64),
    provider_type text NOT NULL CHECK (length(provider_type) BETWEEN 1 AND 64),
    outcome text NOT NULL CHECK (outcome IN ('success', 'error', 'cancelled', 'unknown')),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    usage_status text NOT NULL CHECK (usage_status IN ('observed', 'unavailable', 'unsupported')),
    CHECK ((input_tokens IS NULL AND output_tokens IS NULL) OR usage_status = 'observed')
);
CREATE INDEX observability_model_attempts_tenant_time_idx ON observability_model_attempts (tenant_id, started_at DESC);
CREATE INDEX observability_model_attempts_session_time_idx ON observability_model_attempts (session_id, started_at DESC);
CREATE INDEX observability_model_attempts_retention_idx ON observability_model_attempts (started_at);

CREATE TABLE observability_tool_attempts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id uuid NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    started_at timestamptz,
    finished_at timestamptz NOT NULL,
    tool_category text NOT NULL CHECK (length(tool_category) BETWEEN 1 AND 64),
    outcome text NOT NULL CHECK (outcome IN ('success', 'error', 'cancelled', 'unknown')),
    duration_ms bigint CHECK (duration_ms >= 0),
    CHECK (started_at IS NULL OR finished_at >= started_at)
);
CREATE INDEX observability_tool_attempts_tenant_time_idx ON observability_tool_attempts (tenant_id, finished_at DESC);
CREATE INDEX observability_tool_attempts_session_time_idx ON observability_tool_attempts (session_id, finished_at DESC);
CREATE INDEX observability_tool_attempts_retention_idx ON observability_tool_attempts (finished_at);

CREATE TABLE observability_collector_minute_buckets (
    bucket_start timestamptz NOT NULL,
    source text NOT NULL CHECK (source IN ('runtime', 'request', 'model', 'tool', 'export')),
    attempted_count bigint NOT NULL DEFAULT 0 CHECK (attempted_count >= 0),
    observed_count bigint NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
    unavailable_count bigint NOT NULL DEFAULT 0 CHECK (unavailable_count >= 0),
    timeout_count bigint NOT NULL DEFAULT 0 CHECK (timeout_count >= 0),
    dropped_count bigint NOT NULL DEFAULT 0 CHECK (dropped_count >= 0),
    export_failed_count bigint NOT NULL DEFAULT 0 CHECK (export_failed_count >= 0),
    PRIMARY KEY (bucket_start, source)
);

-- +goose Down
DROP TABLE observability_collector_minute_buckets;
DROP TABLE observability_tool_attempts;
DROP TABLE observability_model_attempts;
DROP TABLE observability_request_minute_buckets;
DROP INDEX turns_completed_time_idx;
ALTER INDEX observability_runtime_samples_retention_idx RENAME TO runtime_history_samples_retention_idx;
ALTER INDEX observability_runtime_samples_pkey RENAME TO runtime_history_samples_pkey;
ALTER TABLE observability_runtime_samples RENAME TO runtime_history_samples;
