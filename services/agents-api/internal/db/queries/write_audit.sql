-- name: InsertWriteAuditOperation :one
INSERT INTO write_audit_operations (id, tenant_id, key_id, key_name, key_prefix, key_kind, action, resource_type, resource_id, parent_id, request_id, trace_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, request_id) DO NOTHING
RETURNING id;

-- name: InsertWriteAuditOwner :exec
INSERT INTO write_audit_owners (tenant_id, resource_type, resource_id, parent_id, operation_id)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, resource_type, resource_id) DO NOTHING;

-- name: GetResourceOwners :many
SELECT o.resource_id, a.key_id, a.key_name, a.key_prefix, a.key_kind, k.revoked_at
FROM write_audit_owners o
JOIN write_audit_operations a ON a.tenant_id = o.tenant_id AND a.id = o.operation_id
LEFT JOIN projects p ON p.tenant_id=a.tenant_id
LEFT JOIN project_api_keys k ON k.project_id=p.id AND k.id = CASE WHEN a.key_kind = 'issued' THEN a.key_id::uuid END
WHERE o.tenant_id = $1 AND o.resource_type = $2 AND o.resource_id = ANY($3::text[]);

-- name: ListWriteOperations :many
SELECT a.*, k.revoked_at
FROM write_audit_operations a
LEFT JOIN projects p ON p.tenant_id=a.tenant_id
LEFT JOIN project_api_keys k ON k.project_id=p.id AND k.id = CASE WHEN a.key_kind = 'issued' THEN a.key_id::uuid END
WHERE a.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(key_id)::text = '' OR a.key_id = sqlc.arg(key_id))
  AND (sqlc.arg(resource_type)::text = '' OR a.resource_type = sqlc.arg(resource_type))
  AND (sqlc.arg(resource_id)::text = '' OR a.resource_id = sqlc.arg(resource_id))
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR a.created_at >= sqlc.narg(created_after))
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR a.created_at < sqlc.narg(created_before))
  AND (sqlc.narg(after_time)::timestamptz IS NULL OR (a.created_at, a.id) < (sqlc.narg(after_time), sqlc.arg(after_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(page_limit);

-- name: GetWriteAuditCursor :one
SELECT created_at FROM write_audit_operations WHERE tenant_id = $1 AND id = $2;

-- name: DeleteExpiredWriteOperations :execrows
DELETE FROM write_audit_operations WHERE id IN (
  SELECT a.id FROM write_audit_operations a
  WHERE a.created_at < $1 AND NOT EXISTS (SELECT 1 FROM write_audit_owners o WHERE o.operation_id = a.id)
  ORDER BY a.created_at, a.id LIMIT $2
  FOR UPDATE SKIP LOCKED
);
