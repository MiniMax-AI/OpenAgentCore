-- name: GetManagedSessionArchive :one
SELECT s.id AS session_id, e.id AS environment_id,
       (s.configuration->'environment'->>'type')::text AS environment_type,
       CASE
           WHEN a.state = 'released' THEN 'released'
           WHEN a.state = 'cleanup_pending' THEN 'cleanup_pending'
           WHEN e.status IN ('failed', 'expired') THEN
               CASE WHEN a.id IS NULL THEN 'released' ELSE 'cleanup_pending' END
           ELSE 'active'
       END::text AS state
FROM sessions s JOIN environments e ON e.session_id = s.id
LEFT JOIN runtime_allocations a ON a.environment_id = e.id
WHERE s.tenant_id = $1 AND s.id = $2 AND s.deleted_at IS NULL;
