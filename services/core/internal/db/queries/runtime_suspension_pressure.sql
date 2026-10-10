-- name: ListRuntimeSuspensionInFlightNodes :many
SELECT DISTINCT a.node_id
FROM runtime_allocations a
WHERE a.node_id IS NOT NULL AND a.state <> 'released'
  AND (a.compute_phase IN ('quiescing', 'suspending')
       OR (a.compute_retained_until <= clock_timestamp() AND a.compute_phase <> 'disabled'));

-- name: ListWaitingRuntimeRestoreGenerations :many
SELECT DISTINCT a.deployment_generation
FROM runtime_allocations a
JOIN environments e ON e.id = a.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE a.node_id = $1 AND a.state = 'running' AND a.compute_phase = 'suspended'
  AND a.compute_retained_until > clock_timestamp()
  AND s.deleted_at IS NULL AND e.status NOT IN ('failed', 'expired')
  AND (a.compute_wake_requested OR EXISTS (
      SELECT 1 FROM environment_input_reservations r
      WHERE r.session_id = s.id AND r.state = 'pending' AND r.deadline > clock_timestamp()
  ));
