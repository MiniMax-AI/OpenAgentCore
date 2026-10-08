-- name: ExecutorProjectScopeExists :one
SELECT EXISTS (
    SELECT 1 FROM execution_project_scopes
    WHERE tenant_id = sqlc.arg(tenant_id) AND organization_id = sqlc.arg(organization_id) AND project_id = sqlc.arg(project_id)
);

-- name: IssueExecutorCredential :one
WITH issued AS (SELECT clock_timestamp() AS at)
INSERT INTO environment_executor_credentials
    (key_id, tenant_id, subject_kind, subject_id, environment_id, token_sha256, created_at, issued_at)
SELECT sqlc.arg(key_id), p.tenant_id, sqlc.arg(subject_kind), sqlc.arg(subject_id),
    sqlc.narg(environment_id)::uuid, sqlc.arg(token_sha256), issued.at, issued.at
FROM execution_project_scopes p CROSS JOIN issued
WHERE p.tenant_id = sqlc.arg(tenant_id) AND p.organization_id = sqlc.arg(organization_id)
    AND p.project_id = sqlc.arg(project_id)
    AND (sqlc.narg(environment_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM environments e JOIN sessions s ON s.id = e.session_id
        WHERE e.id = sqlc.narg(environment_id) AND s.tenant_id = p.tenant_id AND s.deleted_at IS NULL
            AND s.creator_kind = sqlc.arg(subject_kind) AND s.creator_id = sqlc.arg(subject_id)
    ))
ON CONFLICT (key_id) DO NOTHING
RETURNING key_id, environment_id;

-- name: GetExecutorCredentialForPrincipal :one
SELECT c.environment_id
FROM environment_executor_credentials c
JOIN execution_project_scopes p ON p.tenant_id = c.tenant_id
WHERE c.key_id = sqlc.arg(key_id) AND c.tenant_id = sqlc.arg(tenant_id)
    AND c.subject_kind = sqlc.arg(subject_kind) AND c.subject_id = sqlc.arg(subject_id)
    AND p.organization_id = sqlc.arg(organization_id) AND p.project_id = sqlc.arg(project_id);

-- name: RotateExecutorCredential :one
-- Rotation advances the generation of the key's enrollments, so the Link
-- authority refuses what the old secret served, and the epoch of the bound
-- assignments of their Sessions, so the next Bind carries the new
-- generation.
WITH rotated AS (
    UPDATE environment_executor_credentials
    SET token_sha256 = sqlc.arg(token_sha256), issued_at = clock_timestamp(), revoked_at = NULL
    WHERE key_id = sqlc.arg(key_id) AND tenant_id = sqlc.arg(tenant_id)
        AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
    RETURNING key_id, environment_id
), advanced AS (
    UPDATE sandbox_enrollments n SET generation = n.generation + 1
    FROM rotated r WHERE n.executor_key_id = r.key_id
    RETURNING n.environment_id
), rebound AS (
    UPDATE session_runtime_assignments b SET epoch = b.epoch + 1
    FROM advanced a JOIN environments e ON e.id = a.environment_id
    WHERE b.session_id = e.session_id AND b.desired_state = 'bound'
)
SELECT key_id, environment_id FROM rotated;

-- name: RevokeExecutorCredential :execrows
UPDATE environment_executor_credentials SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE key_id = sqlc.arg(key_id) AND tenant_id = sqlc.arg(tenant_id)
    AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id);

-- name: AuthenticateEnvironmentExecutor :one
SELECT s.tenant_id
FROM environment_executor_credentials c
JOIN execution_project_scopes p ON p.tenant_id = c.tenant_id
JOIN environments e ON e.id = sqlc.arg(environment_id)
JOIN sessions s ON s.id = e.session_id
WHERE c.token_sha256 = sqlc.arg(token_sha256) AND c.revoked_at IS NULL
    AND c.tenant_id = s.tenant_id AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
    AND (c.environment_id IS NULL OR c.environment_id = e.id) AND s.deleted_at IS NULL;

-- name: ListEnvironmentExecutorCredentials :many
SELECT key_id, created_at, revoked_at
FROM environment_executor_credentials
WHERE tenant_id = sqlc.arg(tenant_id) AND environment_id = sqlc.arg(environment_id)
    AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
ORDER BY created_at, key_id;

-- name: GetEnvironmentExecutorConnection :one
-- The Environment's enrollment, with the credential that may Serve it while
-- its Link resource is live.
SELECT n.id AS enrollment_id, n.executor_key_id, n.created_at AS enrolled_at, r.credential_hash
FROM environments e
JOIN sessions s ON s.id = e.session_id
LEFT JOIN sandbox_enrollments n ON n.environment_id = e.id
LEFT JOIN sandbox_resources r ON r.kind = 'enrollment' AND r.id = n.id AND r.live
WHERE e.id = sqlc.arg(environment_id) AND s.tenant_id = sqlc.arg(tenant_id)
    AND s.deleted_at IS NULL AND s.configuration->'environment'->>'type' = 'self_hosted';
