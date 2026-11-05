-- name: GetEnvironmentInputActivity :one
SELECT r.state, r.is_initial, r.created_at, r.settled_at, r.failure_code, e.id AS environment_id, e.status AS connection_status,
       COALESCE(e.configuration->>'type', '')::text AS environment_type
FROM sessions s
JOIN environments e ON e.id = s.environment_id
JOIN LATERAL (
    SELECT * FROM environment_input_reservations
    WHERE session_id = s.id
    ORDER BY created_at DESC, id DESC LIMIT 1
) r ON true
WHERE s.id = $1 AND r.state <> 'admitted'
  AND NOT EXISTS (
      SELECT 1 FROM turns t WHERE t.session_id = s.id
        AND (t.created_at >= r.created_at OR t.status IN ('queued', 'in_progress', 'waiting'))
  );
