-- name: StartSandboxReset :exec
UPDATE runtime_deployment SET admission_paused = true, reset_clear = sqlc.arg(clear),
    reset_requested_at = clock_timestamp(),
    reset_deadline_at = CASE WHEN sqlc.arg(clear)::text = 'auto'
        THEN clock_timestamp() + make_interval(secs => sqlc.arg(deadline_seconds)::int) END,
    reset_forced_at = CASE WHEN sqlc.arg(clear)::text = 'force' THEN clock_timestamp() END,
    reset_audit = sqlc.arg(audit)::jsonb, updated_at = clock_timestamp()
WHERE singleton = true;

-- name: ForceSandboxReset :exec
UPDATE runtime_deployment SET reset_clear = 'force', reset_forced_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE singleton = true AND reset_clear = 'auto';

-- name: CancelSandboxReset :exec
UPDATE runtime_deployment SET admission_paused = false, reset_clear = NULL,
    reset_requested_at = NULL, reset_deadline_at = NULL, reset_forced_at = NULL,
    reset_audit = NULL, updated_at = clock_timestamp()
WHERE singleton = true;

-- name: CompleteSandboxReset :exec
UPDATE runtime_deployment SET provider_kind = '', backend_fingerprint = '', mode = '',
    specification = '{}', idle_seconds = 0, retention_seconds = 0,
    provider_config = '{}'::jsonb, provider_metadata = '{}'::jsonb, provider_credential = NULL,
    generation = generation + 1, owner_epoch = owner_epoch + 1,
    admission_paused = false, reset_clear = NULL, reset_requested_at = NULL,
    reset_deadline_at = NULL, reset_forced_at = NULL, reset_audit = NULL,
    updated_at = clock_timestamp()
WHERE singleton = true;

-- name: SessionBlocksAutoReset :one
SELECT (
    EXISTS (SELECT 1 FROM turns t WHERE t.session_id = sqlc.arg(session_id) AND status IN ('in_progress', 'waiting'))
    OR EXISTS (SELECT 1 FROM subagent_turns t WHERE t.session_id = sqlc.arg(session_id) AND status IN ('in_progress', 'waiting'))
    OR EXISTS (SELECT 1 FROM environment_file_writes f JOIN environments e ON e.id = f.environment_id
        WHERE e.session_id = sqlc.arg(session_id) AND f.state = 'pending')
)::boolean;

-- name: ListSandboxResetSessions :many
SELECT s.id, s.tenant_id
FROM sessions s JOIN environments e ON e.session_id = s.id
WHERE s.deleted_at IS NULL AND s.configuration->'environment'->>'type' = 'openai_hosted'
    AND e.status NOT IN ('failed', 'expired')
    AND s.id > sqlc.arg(after_id)::uuid
    AND (EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state NOT IN ('released', 'cleanup_pending'))
        OR (e.status = 'pending' AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id)))
    AND (sqlc.arg(force)::boolean OR NOT (
        EXISTS (SELECT 1 FROM turns t WHERE t.session_id = s.id AND t.status IN ('in_progress', 'waiting'))
        OR EXISTS (SELECT 1 FROM subagent_turns t WHERE t.session_id = s.id AND t.status IN ('in_progress', 'waiting'))
        OR EXISTS (SELECT 1 FROM environment_file_writes f WHERE f.environment_id = e.id AND f.state = 'pending')
    ))
ORDER BY s.id LIMIT 32;

-- name: GetSandboxResetProject :one
SELECT id FROM projects WHERE tenant_id = $1;

-- name: GetSandboxDeploymentSnapshot :one
WITH deployment AS MATERIALIZED (SELECT * FROM runtime_deployment WHERE singleton = true LIMIT 1),
observed AS MATERIALIZED (SELECT clock_timestamp() AS as_of),
held AS (
    SELECT a.deployment_generation, a.node_id, s.id AS session_id, e.id AS environment_id, false AS pending,
        (a.state = 'cleanup_pending' OR s.deleted_at IS NOT NULL OR e.status IN ('failed', 'expired')
         OR CASE WHEN a.compute_phase NOT IN ('disabled', 'running')
            THEN a.compute_retained_until IS NOT NULL AND a.compute_retained_until <= observed.as_of
            ELSE a.node_id IS NULL AND d.mode <> 'direct' AND a.kept_at <= observed.as_of - interval '1 hour' END) AS cleanup
    FROM runtime_allocations a JOIN environments e ON e.id = a.environment_id
    JOIN sessions s ON s.id = e.session_id CROSS JOIN deployment d CROSS JOIN observed
    WHERE a.state <> 'released'
    UNION ALL
    SELECT p.deployment_generation, p.node_id, s.id, e.id, true, false
    FROM environments e JOIN sessions s ON s.id = e.session_id
    LEFT JOIN runtime_placements p ON p.environment_id = e.id AND p.released_at IS NULL
    WHERE s.deleted_at IS NULL AND e.status = 'pending'
        AND s.configuration->'environment'->>'type' = 'openai_hosted'
        AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id)
), classified AS (
    SELECT h.*, CASE WHEN h.cleanup THEN 'cleanup'
        WHEN EXISTS (SELECT 1 FROM turns t WHERE t.session_id = h.session_id AND t.status IN ('in_progress', 'waiting'))
          OR EXISTS (SELECT 1 FROM subagent_turns t WHERE t.session_id = h.session_id AND t.status IN ('in_progress', 'waiting'))
          OR EXISTS (SELECT 1 FROM environment_file_writes f WHERE f.environment_id = h.environment_id AND f.state = 'pending')
        THEN 'busy' ELSE 'idle' END AS category,
        n.name, (h.node_id IS NOT NULL AND NOT COALESCE(n.removed_at IS NULL AND n.connection_id IS NOT NULL
            AND n.connected_epoch = d.owner_epoch AND n.last_seen_at > observed.as_of - interval '45 seconds', false)) AS offline
    FROM held h LEFT JOIN runtime_nodes n ON n.id = h.node_id CROSS JOIN deployment d CROSS JOIN observed
), rollout_nodes AS (
    SELECT CASE
        WHEN n.connection_id IS NULL OR n.connected_epoch <> d.owner_epoch OR n.last_seen_at IS NULL OR n.last_seen_at <= observed.as_of - interval '45 seconds' THEN 'unknown'
        WHEN n.protocol_version=1 AND n.deployment_generation <> d.generation THEN 'update_required'
        ELSE COALESCE(g.state,'unknown') END AS state
    FROM runtime_nodes n CROSS JOIN deployment d CROSS JOIN observed
    LEFT JOIN runtime_node_generation_status g ON g.node_id=n.id AND g.generation=d.generation AND g.connection_id=n.connection_id AND g.owner_epoch=d.owner_epoch
    WHERE n.removed_at IS NULL AND n.installation_id=d.installation_id
), offline AS (
    SELECT node_id, name, count(*)::bigint AS resources FROM classified WHERE offline GROUP BY node_id, name
)
SELECT sqlc.embed(d),
    (SELECT count(*) FROM classified WHERE NOT pending)::bigint AS allocations,
    (SELECT count(*) FROM classified WHERE pending)::bigint AS pending,
    jsonb_build_object(
        'busy', (SELECT count(*) FROM classified WHERE category = 'busy'),
        'idle', (SELECT count(*) FROM classified WHERE category = 'idle'),
        'cleanup', (SELECT count(*) FROM classified WHERE category = 'cleanup'),
        'on_offline_nodes', (SELECT count(*) FROM classified WHERE offline),
        'offline_nodes', COALESCE((SELECT jsonb_agg(jsonb_build_object('node_id', node_id, 'name', name, 'resources', resources) ORDER BY node_id) FROM offline), '[]'::jsonb)
    )::jsonb AS remaining,
    jsonb_build_object('state',CASE WHEN EXISTS(SELECT 1 FROM rollout_nodes WHERE state='preparing') THEN 'preparing' ELSE 'settled' END,
        'previous_generation_sandboxes',(SELECT count(*) FROM held h WHERE h.deployment_generation <> d.generation),
        'nodes',CASE WHEN d.mode='nodes' THEN jsonb_build_object(
            'ready',(SELECT count(*) FROM rollout_nodes WHERE state='ready'),
            'preparing',(SELECT count(*) FROM rollout_nodes WHERE state='preparing'),
            'failed',(SELECT count(*) FROM rollout_nodes WHERE state='failed'),
            'update_required',(SELECT count(*) FROM rollout_nodes WHERE state='update_required'),
            'unknown',(SELECT count(*) FROM rollout_nodes WHERE state='unknown')) ELSE NULL END
    )::jsonb AS rollout
FROM runtime_deployment d
WHERE d.singleton = true
LIMIT 1;
