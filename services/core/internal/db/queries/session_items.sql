-- name: GetSessionItem :one
SELECT * FROM session_items WHERE session_id = $1 AND id = $2;

-- name: PutSessionItem :one
INSERT INTO session_items(id, session_id, turn_id, created_at, payload, position, output_index, settled_at)
VALUES (sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(turn_id), sqlc.arg(created_at), sqlc.arg(payload),
    (SELECT COALESCE(max(position), -1) + 1 FROM session_items WHERE session_id = sqlc.arg(session_id)),
    CASE WHEN sqlc.arg(is_output)::boolean THEN
        (SELECT COALESCE(max(output_index), -1) + 1 FROM session_items WHERE turn_id = sqlc.arg(turn_id))
    END,
    CASE WHEN sqlc.arg(payload)::jsonb->>'status' IN ('completed', 'incomplete', 'failed') THEN sqlc.arg(created_at)::timestamptz END)
ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload,
    settled_at = CASE WHEN session_items.payload->>'status' = 'in_progress'
        AND EXCLUDED.payload->>'status' IN ('completed', 'incomplete', 'failed')
        THEN COALESCE(session_items.settled_at, EXCLUDED.created_at) ELSE session_items.settled_at END
RETURNING *;

-- name: ListUnfinishedSessionItems :many
SELECT id, position, output_index, payload FROM session_items
WHERE session_id = $1 AND turn_id = $2 AND payload->>'status' = 'in_progress';

-- name: FinishSessionItems :exec
-- The finished Items share one settlement time; an Item keeps a settlement
-- time it already has.
WITH observed AS MATERIALIZED (SELECT clock_timestamp() AS at)
UPDATE session_items SET payload = jsonb_set(payload, '{status}', to_jsonb(sqlc.arg(status)::text)),
    settled_at = COALESCE(session_items.settled_at, observed.at)
FROM observed
WHERE session_id = sqlc.arg(session_id) AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListSessionItems :many
SELECT i.id, i.created_at,
    (CASE WHEN i.payload->>'status' = 'in_progress' AND t.status IN ('completed', 'failed', 'cancelled')
         THEN jsonb_set(i.payload, '{status}', '"incomplete"') ELSE i.payload END)::jsonb AS payload
FROM session_items i JOIN turns t ON t.id = i.turn_id
WHERE i.session_id = sqlc.arg(session_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (sqlc.arg(ascending)::boolean AND (i.created_at, i.position, i.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid))
       OR (NOT sqlc.arg(ascending)::boolean AND (i.created_at, i.position, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.position END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.position END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: ItemInputSource :one
SELECT * FROM turn_inputs WHERE session_id = $1 AND sequence = $2;

-- name: ItemEventSources :many
SELECT * FROM turn_events WHERE session_id = $1 AND turn_id = $2 AND ordinal >= $3 ORDER BY ordinal;

-- name: HasNativeMessageItem :one
SELECT EXISTS(SELECT 1 FROM session_items WHERE turn_id = $1
    AND payload->>'role' = 'assistant' AND id <> $2);

-- name: ListTurnItemDiagnostics :many
SELECT id, created_at, settled_at FROM session_items
WHERE session_id = $1 AND turn_id = $2
ORDER BY created_at, position, id
LIMIT 1001;
