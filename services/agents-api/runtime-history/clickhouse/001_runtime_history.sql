-- Parsar Core Runtime history projection for the OpenTelemetry Collector
-- ClickHouse exporter generic metrics_gauge and metrics_sum tables.
-- Apply this file only after those generic tables exist.

CREATE TABLE IF NOT EXISTS runtime_history_metrics
(
    timestamp DateTime64(9) CODEC(ZSTD(1)),
    tenant_id String CODEC(ZSTD(1)),
    session_id String CODEC(ZSTD(1)),
    environment_id String CODEC(ZSTD(1)),
    allocation_id String CODEC(ZSTD(1)),
    started_at_unix_nano Nullable(Int64) CODEC(ZSTD(1)),
    provider_type LowCardinality(String) CODEC(ZSTD(1)),
    status LowCardinality(String) CODEC(ZSTD(1)),
    collection_source LowCardinality(String) CODEC(ZSTD(1)),
    resolved_at_unix_nano Int64 CODEC(ZSTD(1)),
    observed_at_unix_nano Nullable(Int64) CODEC(ZSTD(1)),
    metric_name LowCardinality(String) CODEC(ZSTD(1)),
    value Float64 CODEC(ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
ORDER BY
(
    tenant_id,
    session_id,
    environment_id,
    collection_source,
    resolved_at_unix_nano,
    allocation_id,
    ifNull(started_at_unix_nano, -1),
    metric_name
)
TTL toDateTime(timestamp) + INTERVAL 7 DAY;

CREATE MATERIALIZED VIEW IF NOT EXISTS runtime_history_metrics_gauge_mv
TO runtime_history_metrics AS
SELECT
    fromUnixTimestamp64Nano(toInt64(Attributes['agents.runtime.resolved_at_unix_nano'])) AS timestamp,
    Attributes['agents.tenant.id'] AS tenant_id,
    Attributes['agents.session.id'] AS session_id,
    Attributes['agents.environment.id'] AS environment_id,
    Attributes['agents.runtime.allocation.id'] AS allocation_id,
    toInt64OrNull(Attributes['agents.runtime.compute.started_at_unix_nano']) AS started_at_unix_nano,
    Attributes['agents.runtime.provider.type'] AS provider_type,
    Attributes['agents.runtime.status'] AS status,
    Attributes['agents.runtime.collection.source'] AS collection_source,
    toInt64(Attributes['agents.runtime.resolved_at_unix_nano']) AS resolved_at_unix_nano,
    toInt64OrNull(Attributes['agents.runtime.observed_at_unix_nano']) AS observed_at_unix_nano,
    MetricName AS metric_name,
    toFloat64(Value) AS value
FROM metrics_gauge
WHERE MetricName IN
(
    'agents.runtime.cpu.capacity',
    'agents.runtime.memory.usage',
    'agents.runtime.memory.limit'
)
AND Attributes['agents.tenant.id'] != ''
AND Attributes['agents.session.id'] != ''
AND Attributes['agents.environment.id'] != ''
AND Attributes['agents.runtime.collection.source'] IN ('on_read', 'periodic')
AND toInt64OrNull(Attributes['agents.runtime.resolved_at_unix_nano']) IS NOT NULL;
CREATE MATERIALIZED VIEW IF NOT EXISTS runtime_history_metrics_sum_mv
TO runtime_history_metrics AS
SELECT
    fromUnixTimestamp64Nano(toInt64(Attributes['agents.runtime.resolved_at_unix_nano'])) AS timestamp,
    Attributes['agents.tenant.id'] AS tenant_id,
    Attributes['agents.session.id'] AS session_id,
    Attributes['agents.environment.id'] AS environment_id,
    Attributes['agents.runtime.allocation.id'] AS allocation_id,
    toInt64OrNull(Attributes['agents.runtime.compute.started_at_unix_nano']) AS started_at_unix_nano,
    Attributes['agents.runtime.provider.type'] AS provider_type,
    Attributes['agents.runtime.status'] AS status,
    Attributes['agents.runtime.collection.source'] AS collection_source,
    toInt64(Attributes['agents.runtime.resolved_at_unix_nano']) AS resolved_at_unix_nano,
    toInt64OrNull(Attributes['agents.runtime.observed_at_unix_nano']) AS observed_at_unix_nano,
    MetricName AS metric_name,
    toFloat64(Value) AS value
FROM metrics_sum
WHERE MetricName IN
(
    'agents.runtime.sample',
    'agents.runtime.cpu.usage'
)
AND Attributes['agents.tenant.id'] != ''
AND Attributes['agents.session.id'] != ''
AND Attributes['agents.environment.id'] != ''
AND Attributes['agents.runtime.collection.source'] IN ('on_read', 'periodic')
AND toInt64OrNull(Attributes['agents.runtime.resolved_at_unix_nano']) IS NOT NULL;
