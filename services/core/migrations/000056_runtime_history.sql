-- +goose Up
-- Periodic telemetry is a bounded projection, never execution or Usage authority.
CREATE TABLE runtime_history_samples (
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    resolved_at_ns bigint NOT NULL CHECK (resolved_at_ns >= 0),
    allocation_id uuid,
    provider_type text NOT NULL,
    status text NOT NULL CHECK (status IN ('observed', 'unavailable', 'unsupported')),
    observed_at_ns bigint,
    started_at_ns bigint,
    cpu_usage_seconds double precision,
    cpu_capacity_cores double precision,
    memory_usage_bytes bigint,
    memory_limit_bytes bigint,
    input_tokens bigint,
    output_tokens bigint,
    PRIMARY KEY (tenant_id, session_id, environment_id, resolved_at_ns),
    CHECK ((input_tokens IS NULL) = (output_tokens IS NULL)),
    CHECK (input_tokens >= 0 AND input_tokens <= 9007199254740991),
    CHECK (output_tokens >= 0 AND output_tokens <= 9007199254740991),
    CHECK (observed_at_ns >= 0 AND observed_at_ns <= resolved_at_ns),
    CHECK (started_at_ns >= 0 AND started_at_ns <= observed_at_ns),
    CHECK ((status = 'observed') = (observed_at_ns IS NOT NULL)),
    CHECK (started_at_ns IS NULL OR observed_at_ns IS NOT NULL),
    CHECK (cpu_usage_seconds >= 0 AND cpu_usage_seconds < 'Infinity'::double precision),
    CHECK (cpu_capacity_cores > 0 AND cpu_capacity_cores < 'Infinity'::double precision),
    CHECK (memory_usage_bytes >= 0 AND memory_usage_bytes <= 9007199254740991),
    CHECK (memory_limit_bytes > 0 AND memory_limit_bytes <= 9007199254740991),
    CHECK (started_at_ns IS NOT NULL OR (cpu_usage_seconds IS NULL AND cpu_capacity_cores IS NULL AND memory_usage_bytes IS NULL AND memory_limit_bytes IS NULL))
);
CREATE INDEX runtime_history_samples_retention_idx ON runtime_history_samples (resolved_at_ns);

-- +goose Down
DROP TABLE runtime_history_samples;
