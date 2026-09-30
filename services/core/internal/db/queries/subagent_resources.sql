-- name: GetPublicSubagent :one
SELECT i.*, COALESCE(p.id::text, s.configuration->'agent'->>'id')::text AS parent_agent_id
FROM subagent_identities i JOIN sessions s ON s.id = i.session_id
LEFT JOIN subagent_identities p ON p.session_id = i.session_id AND p.native_id = i.parent_native_id
WHERE i.session_id = $1 AND i.id = $2 AND i.public_visible;

-- name: ListPublicSubagents :many
SELECT i.id FROM subagent_identities i
WHERE i.session_id = sqlc.arg(session_id) AND i.public_visible
 AND (sqlc.narg(after_opened)::bigint IS NULL
 OR (sqlc.arg(ascending)::boolean AND (i.native_created_at, i.id) > (sqlc.narg(after_opened)::bigint, sqlc.arg(after_id)::uuid))
 OR (NOT sqlc.arg(ascending)::boolean AND (i.native_created_at, i.id) < (sqlc.narg(after_opened)::bigint, sqlc.arg(after_id)::uuid)))
ORDER BY CASE WHEN sqlc.arg(ascending)::boolean THEN i.native_created_at END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN i.id END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.native_created_at END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: GetNativeSubagent :one
SELECT * FROM subagent_identities WHERE session_id = $1 AND native_id = $2;

-- name: PublishSubagent :exec
UPDATE subagent_identities SET public_visible = true, name = $3, instructions = $4 WHERE session_id = $1 AND id = $2;

-- name: PutSubagentEffect :one
INSERT INTO subagent_effects (session_id, effect_id, payload) VALUES ($1, $2, $3)
ON CONFLICT (session_id, effect_id) DO UPDATE SET effect_id = subagent_effects.effect_id
WHERE subagent_effects.payload = EXCLUDED.payload
RETURNING (xmax = 0)::boolean AS inserted;

-- name: ApplySubagentLifecycle :exec
UPDATE subagent_identities SET status = $3, closed_at_ms = $4, lifecycle_at_ms = $5
WHERE session_id = $1 AND id = $2;

-- name: GetChildTurn :one
SELECT * FROM subagent_turns WHERE session_id = $1 AND id = $2;

-- name: PutChildTurn :one
INSERT INTO subagent_turns (id, session_id, subagent_id, native_id, status, created_at, started_at, completed_at, token_usage)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, started_at = EXCLUDED.started_at,
 completed_at = EXCLUDED.completed_at, token_usage = EXCLUDED.token_usage
WHERE subagent_turns.session_id = EXCLUDED.session_id AND subagent_turns.subagent_id = EXCLUDED.subagent_id
 AND subagent_turns.native_id = EXCLUDED.native_id AND subagent_turns.created_at = EXCLUDED.created_at
RETURNING *;

-- name: ListChildTurns :many
SELECT * FROM subagent_turns t
WHERE t.session_id = sqlc.arg(session_id) AND t.subagent_id = sqlc.arg(subagent_id)
 AND (sqlc.narg(after_created)::timestamptz IS NULL
 OR (sqlc.arg(ascending)::boolean AND (t.created_at, t.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 OR (NOT sqlc.arg(ascending)::boolean AND (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY CASE WHEN sqlc.arg(ascending)::boolean THEN t.created_at END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN t.id END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.created_at END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: GetChildItem :one
SELECT i.*, t.created_at AS turn_created_at,
 COALESCE(i.payload = sqlc.narg(candidate)::jsonb, false)::boolean AS payload_equal
FROM subagent_items i JOIN subagent_turns t ON t.id = i.turn_id
WHERE i.session_id = $1 AND i.subagent_id = $2 AND i.id = $3;

-- name: PutChildItem :one
INSERT INTO subagent_items (id, session_id, subagent_id, turn_id, position, payload, output_index)
VALUES (sqlc.arg(id),sqlc.arg(session_id),sqlc.arg(subagent_id),sqlc.arg(turn_id),sqlc.arg(position),sqlc.arg(payload),
 CASE WHEN sqlc.arg(is_output)::boolean THEN
 (SELECT COALESCE(max(output_index),-1)+1 FROM subagent_items WHERE turn_id=sqlc.arg(turn_id)) END)
ON CONFLICT (id) DO UPDATE SET payload = EXCLUDED.payload
RETURNING output_index;

-- name: ListChildItems :many
SELECT i.payload FROM subagent_items i JOIN subagent_turns t ON t.id = i.turn_id
WHERE i.session_id = sqlc.arg(session_id) AND i.subagent_id = sqlc.arg(subagent_id)
 AND (sqlc.narg(turn_id)::uuid IS NULL OR i.turn_id = sqlc.narg(turn_id))
 AND (sqlc.narg(after_created)::timestamptz IS NULL
 OR (sqlc.arg(ascending)::boolean AND (t.created_at, t.id, i.position, i.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_turn)::uuid, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid))
 OR (NOT sqlc.arg(ascending)::boolean AND (t.created_at, t.id, i.position, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_turn)::uuid, sqlc.arg(after_position)::integer, sqlc.arg(after_id)::uuid)))
ORDER BY CASE WHEN sqlc.arg(ascending)::boolean THEN t.created_at END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN t.id END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN i.position END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN i.id END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.created_at END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN t.id END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.position END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: SubagentRootAgent :one
SELECT (configuration->'agent'->>'id')::text AS agent_id FROM sessions WHERE id = $1;
