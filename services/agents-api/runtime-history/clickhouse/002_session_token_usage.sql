-- Session-scoped cumulative token counters. Apply after 001_runtime_history.sql.
-- A separate materialized view keeps usage independent of provider Runtime
-- incarnation fields while reusing the bounded runtime_history_metrics table.

CREATE MATERIALIZED VIEW IF NOT EXISTS runtime_history_session_usage_gauge_mv
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
    'agents.session.tokens.input',
    'agents.session.tokens.output'
)
AND Attributes['agents.tenant.id'] != ''
AND Attributes['agents.session.id'] != ''
AND Attributes['agents.environment.id'] != ''
AND Attributes['agents.runtime.collection.source'] IN ('on_read', 'periodic')
AND toInt64OrNull(Attributes['agents.runtime.resolved_at_unix_nano']) IS NOT NULL;
