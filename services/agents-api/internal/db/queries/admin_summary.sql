-- name: AdminAssetCounts :one
SELECT
 (SELECT count(*) FROM agents count_source WHERE count_source.tenant_id=sqlc.arg(tenant_id))::bigint AS agents,
 (SELECT count(*) FROM skills count_source WHERE count_source.tenant_id=sqlc.arg(tenant_id))::bigint AS skills,
 (SELECT count(*) FROM environment_templates count_source WHERE count_source.tenant_id=sqlc.arg(tenant_id))::bigint AS environment_templates,
 (SELECT count(*) FROM source_files count_source WHERE count_source.tenant_id=sqlc.arg(tenant_id))::bigint AS files,
 (SELECT count(*) FROM vaults count_source WHERE count_source.tenant_id=sqlc.arg(tenant_id))::bigint AS vaults,
 (SELECT count(*) FROM vault_credentials c JOIN vaults v ON v.id=c.vault_id WHERE v.tenant_id=sqlc.arg(tenant_id))::bigint AS credentials;

-- name: AdminSummarySessions :many
SELECT * FROM sessions
WHERE tenant_id=sqlc.arg(tenant_id) AND deleted_at IS NULL
 AND (sqlc.narg(created_after)::timestamptz IS NULL OR created_at >= sqlc.narg(created_after)::timestamptz)
 AND (sqlc.narg(created_before)::timestamptz IS NULL OR created_at < sqlc.narg(created_before)::timestamptz)
 AND id > sqlc.arg(after_id)::uuid
ORDER BY id LIMIT 100;

-- name: AdminRuntimeTargets :many
SELECT id,tenant_id,created_at FROM sessions
WHERE tenant_id=ANY(sqlc.arg(tenant_ids)::uuid[]) AND deleted_at IS NULL
 AND (sqlc.narg(after_time)::timestamptz IS NULL
 OR (sqlc.arg(ascending)::boolean AND (created_at,id)>(sqlc.narg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
 OR (NOT sqlc.arg(ascending)::boolean AND (created_at,id)<(sqlc.narg(after_time)::timestamptz,sqlc.arg(after_id)::uuid)))
ORDER BY
 CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);

-- name: AdminRuntimeCursor :one
SELECT created_at FROM sessions WHERE id=sqlc.arg(id) AND tenant_id=ANY(sqlc.arg(tenant_ids)::uuid[]) AND deleted_at IS NULL;
