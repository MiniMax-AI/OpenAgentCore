-- name: BeginTurnArtifactCapture :execrows
UPDATE turns SET artifact_capture_started = true
WHERE session_id = $1 AND id = $2 AND NOT artifact_capture_started;

-- name: StageSessionArtifact :exec
INSERT INTO session_artifacts (id, session_id, turn_id, environment_id, path, size_bytes, body_oid, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteUnchangedTurnArtifacts :exec
-- A staged path is not republished while the newest remaining published
-- Artifact for that path in the Session has the same bytes. Newest follows the
-- producing Turn's database creation time: Turns in a Session are serialized,
-- while publication time may come from the Runtime's reported completion.
WITH newest AS (
    SELECT DISTINCT ON (published.path) published.path, published.sha256
    FROM session_artifacts published
    JOIN turns producer ON producer.session_id = published.session_id AND producer.id = published.turn_id
    WHERE published.session_id = $1 AND published.created_at IS NOT NULL
    ORDER BY published.path, producer.created_at DESC, producer.id DESC
), removed AS (
    DELETE FROM session_artifacts staged USING newest
    WHERE staged.session_id = $1 AND staged.turn_id = $2 AND staged.created_at IS NULL
      AND staged.path = newest.path AND staged.sha256 = newest.sha256
    RETURNING staged.body_oid
)
SELECT lo_unlink(body_oid) FROM removed;

-- name: PublishTurnArtifacts :exec
UPDATE session_artifacts SET created_at = $3
WHERE session_id = $1 AND turn_id = $2 AND created_at IS NULL;

-- name: DeleteUnpublishedTurnArtifacts :exec
WITH removed AS (
    DELETE FROM session_artifacts WHERE session_id = $1 AND turn_id = $2 AND created_at IS NULL RETURNING body_oid
)
SELECT lo_unlink(body_oid) FROM removed;

-- name: DeleteSessionArtifacts :exec
WITH removed AS (
    DELETE FROM session_artifacts WHERE session_id = $1 RETURNING body_oid
)
SELECT lo_unlink(body_oid) FROM removed;

-- name: GetSessionArtifact :one
SELECT a.* FROM session_artifacts a JOIN sessions s ON s.id = a.session_id
WHERE s.tenant_id = $1 AND s.deleted_at IS NULL AND a.session_id = $2 AND a.id = $3 AND a.created_at IS NOT NULL;

-- name: DeleteSessionArtifact :one
DELETE FROM session_artifacts a USING sessions s
WHERE s.id = a.session_id AND s.tenant_id = $1 AND s.deleted_at IS NULL
AND a.session_id = $2 AND a.id = $3 AND a.created_at IS NOT NULL
RETURNING a.body_oid;

-- name: ListSessionArtifacts :many
SELECT a.* FROM session_artifacts a JOIN sessions s ON s.id = a.session_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND s.deleted_at IS NULL
  AND a.session_id = sqlc.arg(session_id) AND a.created_at IS NOT NULL
  AND (sqlc.narg(environment_id)::uuid IS NULL OR a.environment_id = sqlc.narg(environment_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (a.created_at, a.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (a.created_at, a.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN a.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN a.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN a.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN a.id END DESC
LIMIT sqlc.arg(page_limit);
