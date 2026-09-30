-- name: SaveAgentModelExecution :exec
INSERT INTO agent_model_execution(agent_id, encrypted_config) VALUES (@agent_id, @encrypted_config)
ON CONFLICT (agent_id) DO UPDATE SET encrypted_config = EXCLUDED.encrypted_config;

-- name: DeleteAgentModelExecution :exec
DELETE FROM agent_model_execution WHERE agent_id = @agent_id;

-- name: GetAgentWithModelExecution :one
SELECT a.*, e.encrypted_config FROM agents a
LEFT JOIN agent_model_execution e ON e.agent_id = a.id
WHERE a.tenant_id = @tenant_id AND a.id = @agent_id;
