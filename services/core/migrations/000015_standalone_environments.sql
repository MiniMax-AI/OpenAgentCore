-- +goose Up
-- A creation key deduplicates within the Project and the creator.
ALTER TABLE environments
    DROP CONSTRAINT environments_tenant_id_idempotency_key_key,
    ADD CONSTRAINT environments_creation_key UNIQUE (tenant_id, creator_kind, creator_id, idempotency_key);

-- The two facts every lifecycle path reads, derived here and nowhere else.
-- ended: no live Session holds the Environment and it is Session-owned or failed/expired, so nothing can run on it again.
-- active: not ended and not failed/expired, so it can still run work.
CREATE VIEW environment_liveness AS
SELECT x.id, x.ended, (NOT x.ended AND x.status NOT IN ('failed', 'expired')) AS active
FROM (
    SELECT e.id, e.status,
           (NOT EXISTS (SELECT 1 FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL)
            AND (e.session_owned OR e.status IN ('failed', 'expired'))) AS ended
    FROM environments e) x;

-- The reset retention row of an active Environment (the view of migration 000014, reading the liveness view).
CREATE OR REPLACE VIEW runtime_reset_retained_environments AS
SELECT e.id AS environment_id, s.id AS session_id, e.tenant_id, a.deployment_generation
FROM environments e
JOIN environment_liveness l ON l.id = e.id AND l.active
LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
JOIN environment_workspaces w ON w.environment_id = e.id AND w.state = 'ready'
JOIN LATERAL (
    SELECT allocation.state, allocation.provider_key, allocation.created_at, allocation.deployment_generation
    FROM runtime_allocations allocation
    WHERE allocation.environment_id = e.id
    ORDER BY allocation.created_at DESC, allocation.id DESC
    LIMIT 1) a ON true
CROSS JOIN runtime_deployment d
WHERE d.reset_clear IS NOT NULL AND d.reset_requested_at IS NOT NULL
  AND e.created_at <= d.reset_requested_at
  AND e.initialization = 'complete'
  AND e.configuration->>'type' = 'openai_hosted'
  AND a.state = 'released' AND a.provider_key = d.installation_id
  AND a.deployment_generation <= d.generation AND a.created_at <= d.reset_requested_at
  AND NOT EXISTS (SELECT 1 FROM runtime_placements p WHERE p.environment_id = e.id AND p.released_at IS NULL);
