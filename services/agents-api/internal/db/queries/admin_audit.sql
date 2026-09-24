-- name: InsertAdminAudit :one
INSERT INTO admin_audit_log (id,tenant_id,admin_credential_id,actor_label,action,target_key_id,resource_type,resource_id,result_ids,request_id,trace_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id;

-- name: AdminResetRequestExists :one
SELECT EXISTS (SELECT 1 FROM admin_audit_log WHERE tenant_id=$1 AND request_id=$2 AND action='reset' AND resource_type='api_key' AND resource_id=$3);
