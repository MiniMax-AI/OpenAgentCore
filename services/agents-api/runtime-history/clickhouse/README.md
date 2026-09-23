# ClickHouse Runtime history reference deployment

This optional deployment turns periodic, provider-neutral Runtime observations
into the Durable history source exposed by Agents Core. ClickHouse remains an
operator-owned read model. It is not required for execution, current observations,
or lifecycle decisions.

## Security and ownership

- The Collector writer may insert into the generic `metrics_gauge` and
  `metrics_sum` tables.
- The Agents API reader should receive `SELECT` on
  `runtime_history_metrics` only. It does not need access to generic telemetry,
  schema mutation, or another tenant selector.
- Every Reader query contains the authenticated tenant plus resolved Session and
  Environment identity, an exclusive time bound, and
  `collection_source = 'periodic'`.
- Web calls only the public Agents API. Do not expose ClickHouse or Collector
  credentials to a browser.
- The reference table has a seven-day TTL. The server advertises that same
  retention and permits at most a 24-hour query range, 1,000 buckets per series,
  64 series, and 10,000 total returned points.

## Install

1. Run an OpenTelemetry Collector with
   [`otel-collector.example.yaml`](otel-collector.example.yaml). Its ClickHouse
   exporter creates the generic metric tables. Keep the OTLP receiver private or
   authenticate it at the network/proxy boundary.
2. After `metrics_gauge` and `metrics_sum` exist, apply
   [`001_runtime_history.sql`](001_runtime_history.sql) to the same database.
   The materialized views retain only the five values needed by the history API;
   provider receipts, native container identities, paths, and raw errors never
   enter the projection.
3. Create a read-only ClickHouse account for Core:

   ```sql
   GRANT SELECT ON runtime_history.runtime_history_metrics TO agents_runtime_reader;
   ```

4. Store the following JSON outside the repository with mode `0600`, then point
   `AGENTS_API_RUNTIME_HISTORY_FILE` at its absolute path:

   ```json
   {
     "transport": "otlp_http",
     "endpoint": "https://collector.example.com/v1/metrics",
     "headers": {"Authorization": "Bearer operator-managed-secret"},
     "queue_capacity": 256,
     "timeout_seconds": 2,
     "sample_interval_seconds": 30,
     "clickhouse": {
       "address": "clickhouse.example.com:9440",
       "database": "runtime_history",
       "username": "agents_runtime_reader",
       "password": "operator-managed-secret",
       "secure": true,
       "dial_timeout_seconds": 5,
       "query_timeout_seconds": 10
     }
   }
   ```

When `clickhouse` is omitted, export and background sampling continue but Durable
history remains unavailable. When `sample_interval_seconds` is omitted, a
configured Reader is advertised as on-read only and Web must not call it Durable.
Startup validates configuration without echoing credentials. The connection is
opened lazily: a ClickHouse outage returns a sanitized `503` for history reads but
does not stop Core execution or current observations.

Exactly one of `secure` or `insecure` is required. Production TCP connections
should use `secure: true`; plaintext local validation must opt in explicitly with
`insecure: true` so omitting TLS never silently transmits credentials or history.

The specialized projection intentionally stores both `on_read` and `periodic`
records for operator inspection. Public Durable queries use only `periodic`
records so Dashboard traffic cannot inflate or fabricate cadence coverage.
