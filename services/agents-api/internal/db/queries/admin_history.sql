-- name: ListAdminAuditLog :many
SELECT * FROM admin_audit_log
WHERE (sqlc.arg(project_id)::text='' OR project_id::text=sqlc.arg(project_id))
 AND (sqlc.arg(resource_type)::text='' OR resource_type=sqlc.arg(resource_type))
 AND (sqlc.arg(resource_id)::text='' OR resource_id=sqlc.arg(resource_id))
 AND (sqlc.arg(action)::text='' OR action=sqlc.arg(action))
 AND (sqlc.narg(created_after)::timestamptz IS NULL OR created_at>=sqlc.narg(created_after))
 AND (sqlc.narg(created_before)::timestamptz IS NULL OR created_at<sqlc.narg(created_before))
 AND (sqlc.narg(after_time)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_time),sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: AdminAuditCursor :one
SELECT created_at FROM admin_audit_log WHERE id=$1;

-- name: GetAdminResourceOwners :many
SELECT resource_id,audit_id FROM admin_resource_owners
WHERE tenant_id=$1 AND resource_type=$2 AND resource_id=ANY($3::text[]);
