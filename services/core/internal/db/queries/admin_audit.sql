-- name: InsertAdminAudit :one
INSERT INTO admin_audit_log (id,tenant_id,admin_credential_id,actor_label,action,project_id,resource_type,resource_id,result_ids,request_id,trace_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id;
