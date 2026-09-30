-- name: AppendSessionEvent :exec
WITH next AS (
    UPDATE sessions SET event_sequence = event_sequence + 1 WHERE id = $1
    RETURNING id, event_sequence
)
INSERT INTO session_events(session_id, sequence, payload)
SELECT id, event_sequence, $2 FROM next;

-- name: SessionEventCursor :one
SELECT event_sequence FROM sessions WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: ListSessionEvents :many
SELECT sequence, payload FROM (
    SELECT e.sequence, e.payload,
        row_number() OVER (ORDER BY e.sequence) AS n,
        sum(e.payload_bytes) OVER (ORDER BY e.sequence) AS bytes
    FROM session_events e JOIN sessions s ON s.id = e.session_id
    WHERE s.tenant_id = $1 AND s.deleted_at IS NULL AND e.session_id = $2 AND e.sequence > $3
) AS pending WHERE n = 1 OR bytes <= 1048576
ORDER BY sequence LIMIT 32;

-- name: PruneSessionEvents :exec
WITH retained AS (
    SELECT sequence, row_number() OVER (ORDER BY sequence DESC) AS n,
        sum(payload_bytes) OVER (ORDER BY sequence DESC) AS bytes
    FROM session_events e WHERE e.session_id = sqlc.arg(session_id)
)
DELETE FROM session_events e WHERE e.session_id = sqlc.arg(session_id) AND e.sequence IN (
    SELECT sequence FROM retained
    WHERE n > sqlc.arg(retained_events)::bigint OR (bytes > sqlc.arg(retained_bytes)::bigint AND n > 1)
);

-- name: SessionEventTurn :one
SELECT * FROM turns WHERE session_id = $1 AND id = $2;
