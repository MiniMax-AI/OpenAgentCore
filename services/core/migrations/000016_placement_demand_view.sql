-- +goose Up
-- The one definition of placement demand. A row is an active hosted Environment that could take a node reservation: no live compute, either never allocated or with released compute and a ready workspace.
-- waiting is whether it needs one now. A never-allocated Environment waits from its creation, unless its initial input expired and no input is live: then only from the earliest live input. One with released compute waits on a wake request or a live input.
-- reserved marks one that already holds a placement; the scan skips it, and the preparation check reads it.
CREATE VIEW runtime_placement_demand AS
SELECT e.id, e.tenant_id, COALESCE(s.engine, '')::text AS engine, (latest.id IS NOT NULL OR EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id=e.id))::boolean AS retained,
       GREATEST(p.released_at, CASE WHEN latest.id IS NULL THEN CASE WHEN x.initial_expired THEN i.live_at ELSE e.created_at END
            ELSE LEAST(i.live_at, CASE WHEN latest.compute_wake_requested THEN latest.compute_activity_at END) END)::timestamptz AS demanded_at,
       (p.environment_id IS NOT NULL AND p.released_at IS NULL) AS reserved,
       CASE WHEN latest.id IS NULL THEN NOT x.initial_expired OR i.live_at IS NOT NULL ELSE latest.compute_wake_requested OR i.live_at IS NOT NULL END AS waiting
FROM environments e JOIN environment_liveness l ON l.id = e.id AND l.active
LEFT JOIN sessions s ON s.environment_id = e.id AND s.deleted_at IS NULL
LEFT JOIN runtime_placements p ON p.environment_id = e.id
LEFT JOIN LATERAL (SELECT a.id, a.state, a.compute_wake_requested, a.compute_activity_at FROM runtime_allocations a WHERE a.environment_id = e.id ORDER BY a.created_at DESC, a.id DESC LIMIT 1) latest ON true
-- Input is read through its unique pending row, and the history only for an Environment never allocated, so the cost does not grow with input history.
LEFT JOIN LATERAL (SELECT r.created_at AS live_at FROM environment_input_reservations r WHERE r.session_id = s.id AND r.state = 'pending' AND r.deadline > clock_timestamp()) i ON true
LEFT JOIN LATERAL (SELECT (latest.id IS NULL AND EXISTS (SELECT 1 FROM environment_input_reservations r WHERE r.session_id = s.id AND r.is_initial AND (r.state = 'expired' OR (r.state = 'pending' AND r.deadline <= clock_timestamp())))) AS initial_expired) x ON true
WHERE e.configuration->>'type' = 'openai_hosted'
  AND (SELECT reset_clear IS NULL AND mode = 'nodes' FROM runtime_deployment)
  AND NOT EXISTS (SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released')
  AND (
    (e.initialization IN ('pending','complete') AND latest.id IS NULL)
    OR (e.initialization = 'complete'
      AND EXISTS (SELECT 1 FROM environment_workspaces w WHERE w.environment_id = e.id AND w.state = 'ready')
      AND latest.state = 'released')
  );
