-- name: InsertAutomation :one
INSERT INTO automations (id, tenant_id, type, name, metadata, status, run_at, expression, timezone, next_run_at, target, target_contents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING created_at;

-- name: GetAutomation :one
SELECT a.id, a.tenant_id, a.type, a.name, a.metadata, a.status, a.run_at, a.expression, a.timezone, a.next_run_at, a.target, a.created_at
FROM automations a WHERE a.tenant_id = $1 AND a.id = $2;

-- name: LockAutomation :one
SELECT a.id, a.tenant_id, a.type, a.name, a.metadata, a.status, a.run_at, a.expression, a.timezone, a.next_run_at, a.target, a.created_at
FROM automations a WHERE a.tenant_id = $1 AND a.id = $2 FOR UPDATE;

-- name: AutomationQueue :one
-- A statement after LockAutomation, so it reads what committed while the lock waited.
SELECT EXISTS (SELECT 1 FROM automation_invocations i WHERE i.automation_id = sqlc.arg(id)::uuid AND i.status = 'queued' AND i.scheduled_at IS NOT NULL) AS pending,
    EXISTS (SELECT 1 FROM automation_invocations i WHERE i.automation_id = sqlc.arg(id)::uuid AND i.status = 'queued') AS queued;

-- name: UpdateAutomation :exec
UPDATE automations SET name = $3, metadata = $4, status = $5, run_at = $6, expression = $7, timezone = $8, next_run_at = $9,
    target = CASE WHEN sqlc.arg(set_target)::boolean THEN sqlc.arg(target)::jsonb ELSE target END,
    target_contents = CASE WHEN sqlc.arg(set_target)::boolean THEN sqlc.arg(target_contents)::bytea ELSE target_contents END
WHERE tenant_id = $1 AND id = $2;

-- name: DeleteAutomation :one
DELETE FROM automations WHERE tenant_id = $1 AND id = $2 RETURNING id;

-- name: ListAutomations :many
SELECT a.id, a.tenant_id, a.type, a.name, a.metadata, a.status, a.run_at, a.expression, a.timezone, a.next_run_at, a.target, a.created_at
FROM automations a
WHERE a.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(session_id)::text IS NULL OR (a.target->>'type' = 'existing_session' AND a.target->>'session_id' = sqlc.narg(session_id)::text))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (a.created_at, a.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (a.created_at, a.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN a.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN a.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN a.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN a.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: InsertManualInvocation :one
INSERT INTO automation_invocations (id, automation_id, status, session_id, delivery)
SELECT sqlc.arg(id), a.id, 'queued', (CASE WHEN a.target->>'type' = 'existing_session' THEN a.target->>'session_id' END)::uuid, a.target_contents FROM automations a WHERE a.tenant_id = sqlc.arg(tenant_id) AND a.id = sqlc.arg(automation_id)
RETURNING created_at, session_id;

-- name: GetInvocation :one
SELECT i.id, i.automation_id, i.created_at, i.scheduled_at, i.automation_snapshot, i.status, i.skip_reason, i.session_id, i.error_code, i.error_message
FROM automation_invocations i JOIN automations a ON a.id = i.automation_id
WHERE a.tenant_id = $1 AND i.automation_id = $2 AND i.id = $3;

-- name: ListInvocations :many
SELECT i.id, i.automation_id, i.created_at, i.scheduled_at, i.automation_snapshot, i.status, i.skip_reason, i.session_id, i.error_code, i.error_message
FROM automation_invocations i
WHERE i.automation_id = sqlc.arg(automation_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (i.created_at, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (i.created_at, i.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN i.id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN i.id END DESC
LIMIT sqlc.arg(page_limit);

-- name: DueAutomationIDs :many
SELECT a.tenant_id, a.id FROM automations a
WHERE a.status = 'active' AND a.next_run_at <= sqlc.arg(now)::timestamptz
  AND EXISTS (SELECT 1 FROM projects p WHERE p.tenant_id = a.tenant_id AND p.archived_at IS NULL)
ORDER BY a.next_run_at LIMIT sqlc.arg(page_limit);

-- name: InsertScheduledInvocation :exec
INSERT INTO automation_invocations (id, automation_id, scheduled_at, automation_snapshot, status, skip_reason, session_id, delivery)
SELECT sqlc.arg(id), a.id, sqlc.arg(scheduled_at), sqlc.arg(automation_snapshot), sqlc.arg(status)::text, sqlc.narg(skip_reason)::text,
    (CASE WHEN a.target->>'type' = 'existing_session' THEN a.target->>'session_id' END)::uuid,
    CASE WHEN sqlc.arg(status)::text = 'queued' THEN a.target_contents END
FROM automations a WHERE a.id = sqlc.arg(automation_id)
ON CONFLICT (automation_id, scheduled_at) DO NOTHING;

-- name: AdvanceAutomation :exec
UPDATE automations SET next_run_at = sqlc.narg(next_run_at), status = CASE WHEN sqlc.arg(completed)::boolean THEN 'completed' ELSE status END WHERE id = sqlc.arg(id);

-- name: DeliverableInvocations :many
WITH picked AS (
    SELECT * FROM (
        SELECT DISTINCT ON (i.automation_id) i.id, i.automation_id, a.tenant_id, (a.target->>'type')::text AS target_type, i.delivery, i.created_at, i.attempted_at,
            p.subject_kind, p.subject_id, s.organization_id, s.project_id
        FROM automation_invocations i
        JOIN automations a ON a.id = i.automation_id
        JOIN projects p ON p.tenant_id = a.tenant_id AND p.archived_at IS NULL
        JOIN execution_project_scopes s ON s.tenant_id = a.tenant_id
        WHERE i.status = 'queued' AND (i.scheduled_at IS NULL OR a.status = 'active')
        ORDER BY i.automation_id, i.created_at, i.id
    ) oldest
    ORDER BY attempted_at NULLS FIRST, created_at, id
    LIMIT sqlc.arg(page_limit)
), marked AS (
    UPDATE automation_invocations u SET attempted_at = statement_timestamp() FROM picked WHERE u.id = picked.id
)
SELECT id, automation_id, tenant_id, target_type, delivery, subject_kind, subject_id, organization_id, project_id FROM picked;

-- name: SettleInvocation :one
UPDATE automation_invocations SET status = $2, session_id = COALESCE(sqlc.narg(session_id), session_id), error_code = sqlc.narg(error_code), error_message = sqlc.narg(error_message), delivery = NULL
WHERE id = $1 AND status = 'queued' RETURNING automation_id, scheduled_at;

-- name: CompleteOnceAutomation :exec
UPDATE automations SET status = 'completed', next_run_at = NULL WHERE id = $1 AND type = 'once';
