-- name: LockWorkspaceConfigurations :exec
LOCK TABLE workspace_fs_configurations IN SHARE ROW EXCLUSIVE MODE;

-- name: InsertWorkspaceConfiguration :exec
INSERT INTO workspace_fs_configurations (id, adapter, parameters, credential) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO NOTHING;

-- name: MatchWorkspaceConfiguration :one
SELECT (adapter = sqlc.arg(adapter) AND parameters = sqlc.arg(parameters)::jsonb)::boolean AS matches, credential
FROM workspace_fs_configurations WHERE id = sqlc.arg(id);

-- name: GetWorkspaceConfiguration :one
SELECT * FROM workspace_fs_configurations WHERE id = $1;

-- name: SetWorkspaceTenantCapacity :exec
UPDATE workspace_storage_admission SET tenant_capacity_mib = $1;

-- name: GetWorkspaceSelection :one
SELECT sqlc.embed(c), a.tenant_capacity_mib
FROM workspace_storage_admission a JOIN workspace_fs_configurations c ON c.active;

-- name: LockWorkspaceAdmission :one
SELECT tenant_capacity_mib FROM workspace_storage_admission FOR SHARE;

-- name: LockWorkspaceTenant :exec
SELECT pg_advisory_xact_lock(hashtext('oac.workspace-tenant-capacity'), hashtext(sqlc.arg(tenant_id)::uuid::text));

-- name: TenantWorkspaceCapacity :one
SELECT COALESCE(SUM(w.capacity_mib), 0)::bigint AS committed_mib,
       COALESCE(bool_or(w.capacity_mib = 0), false)::boolean AS unbounded
FROM environment_workspaces w
JOIN environments e ON e.id = w.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND w.state <> 'deleted';

-- name: ClearActiveWorkspaceConfiguration :exec
UPDATE workspace_fs_configurations SET active = false WHERE active;

-- name: ActivateWorkspaceConfiguration :exec
UPDATE workspace_fs_configurations SET active = true WHERE id = $1;

-- name: GetActiveWorkspaceConfiguration :one
SELECT * FROM workspace_fs_configurations WHERE active;

-- name: LockWorkspaceSession :one
SELECT s.id FROM sessions s JOIN environments e ON e.session_id = s.id
WHERE s.tenant_id = $1 AND e.id = $2 FOR UPDATE OF s;

-- name: WorkspaceSessionDeleted :one
SELECT (deleted_at IS NOT NULL)::boolean AS deleted FROM sessions WHERE id = $1;

-- name: GetEnvironmentWorkspace :one
SELECT sqlc.embed(w), sqlc.embed(c), s.tenant_id
FROM environment_workspaces w
JOIN workspace_fs_configurations c ON c.id = w.configuration_id
JOIN environments e ON e.id = w.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE s.tenant_id = $1 AND e.id = $2;

-- name: InsertEnvironmentWorkspace :exec
INSERT INTO environment_workspaces (object_id, environment_id, configuration_id, capacity_mib)
VALUES ($1, $2, $3, $4);

-- name: MarkWorkspaceReady :execrows
UPDATE environment_workspaces SET state = 'ready', attachment = $2
WHERE object_id = $1 AND state = 'creating';

-- name: WorkspaceComputeReleased :one
SELECT NOT EXISTS (
    SELECT 1 FROM runtime_allocations WHERE environment_id = $1 AND state <> 'released'
) AS released;

-- name: BeginWorkspaceDeletion :execrows
UPDATE environment_workspaces SET state = 'deleting'
WHERE object_id = $1 AND state IN ('creating', 'ready');

-- name: MarkWorkspaceDeleted :execrows
UPDATE environment_workspaces SET state = 'deleted'
WHERE object_id = $1 AND state = 'deleting';

-- name: ListWorkspaceDeletionCandidates :many
SELECT sqlc.embed(w), sqlc.embed(c), s.tenant_id
FROM environment_workspaces w
JOIN workspace_fs_configurations c ON c.id = w.configuration_id
JOIN environments e ON e.id = w.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE w.object_id > $1 AND w.state <> 'deleted'
  AND (s.deleted_at IS NOT NULL OR w.state = 'deleting')
  AND NOT EXISTS (
      SELECT 1 FROM runtime_allocations a WHERE a.environment_id = e.id AND a.state <> 'released'
  )
ORDER BY w.object_id LIMIT 32;
