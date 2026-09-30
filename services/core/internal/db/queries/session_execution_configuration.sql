-- name: SaveSessionExecutionConfiguration :exec
INSERT INTO session_execution_configuration(session_id, configuration, deployment_provider_revision)
VALUES (@session_id, @configuration, sqlc.narg(deployment_provider_revision));

-- name: GetSessionExecutionConfiguration :one
SELECT s.id, s.engine, s.configuration AS session_configuration,
       p.configuration AS execution_configuration
FROM sessions s
LEFT JOIN session_execution_configuration p ON p.session_id = s.id
WHERE s.tenant_id = @tenant_id AND s.id = @session_id AND s.deleted_at IS NULL;
