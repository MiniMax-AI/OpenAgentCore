-- +goose Up
-- The one definition of placement demand: an Environment that needs a node reservation, with the time it began waiting.
-- A never-allocated one waits from its creation, unless its initial input expired and no input is live: then only from the earliest live input.
-- reserved marks one that already holds a placement; the scan skips it and the per-Environment check counts it.
CREATE VIEW runtime_placement_demand AS
SELECT e.id, e.tenant_id, COALESCE(s.engine, '')::text AS engine, (latest.id IS NOT NULL OR EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id=e.id))::boolean AS retained,
       GREATEST(p.released_at, CASE WHEN latest.id IS NULL THEN CASE WHEN i.initial_expired THEN i.live_at ELSE e.created_at END
            ELSE LEAST(i.live_at, CASE WHEN latest.compute_wake_requested THEN latest.compute_activity_at END) END)::timestamptz AS demanded_at,
       (p.environment_id IS NOT NULL AND p.released_at IS NULL) AS reserved
FROM environments e JOIN environment_liveness l ON l.id = e.id AND l.active
LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
LEFT JOIN runtime_placements p ON p.environment_id = e.id
LEFT JOIN LATERAL (SELECT a.id, a.state, a.compute_wake_requested, a.compute_activity_at FROM runtime_allocations a WHERE a.environment_id = e.id ORDER BY a.created_at DESC, a.id DESC LIMIT 1) latest ON true
LEFT JOIN LATERAL (SELECT min(r.created_at) FILTER (WHERE r.state = 'pending' AND r.deadline > clock_timestamp()) AS live_at,
       COALESCE(bool_or(r.is_initial AND (r.state = 'expired' OR (r.state = 'pending' AND r.deadline <= clock_timestamp()))), false)::boolean AS initial_expired
       FROM environment_input_reservations r WHERE r.session_id = s.id) i ON true
WHERE e.configuration->>'type' = 'openai_hosted'
  AND (SELECT reset_clear IS NULL AND mode = 'nodes' FROM runtime_deployment)
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released')
  AND (
    (e.initialization IN ('pending','complete') AND latest.id IS NULL AND (NOT i.initial_expired OR i.live_at IS NOT NULL))
    OR (e.initialization = 'complete'
      AND EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id AND w.state = 'ready')
      AND EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state = 'released')
      AND (latest.compute_wake_requested OR i.live_at IS NOT NULL))
  );
