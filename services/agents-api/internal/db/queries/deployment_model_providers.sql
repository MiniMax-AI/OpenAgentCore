-- name: ListDeploymentModelProviders :many
SELECT harness, protocol, base_url, context_window, max_output_tokens, updated_at
FROM deployment_model_providers ORDER BY harness;

-- name: GetDeploymentModelProviderSecret :one
SELECT encrypted_config FROM deployment_model_providers WHERE harness = @harness;

-- name: UpsertDeploymentModelProvider :one
INSERT INTO deployment_model_providers (harness, protocol, base_url, context_window, max_output_tokens, encrypted_config, updated_at)
VALUES (@harness, @protocol, @base_url, @context_window, @max_output_tokens, @encrypted_config, clock_timestamp())
ON CONFLICT (harness) DO UPDATE SET protocol = EXCLUDED.protocol, base_url = EXCLUDED.base_url,
    context_window = EXCLUDED.context_window, max_output_tokens = EXCLUDED.max_output_tokens,
    encrypted_config = EXCLUDED.encrypted_config, updated_at = EXCLUDED.updated_at
RETURNING harness, protocol, base_url, context_window, max_output_tokens, updated_at;

-- name: DeleteDeploymentModelProvider :execrows
DELETE FROM deployment_model_providers WHERE harness = @harness;
