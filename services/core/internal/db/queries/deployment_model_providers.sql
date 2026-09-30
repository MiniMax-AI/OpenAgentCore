-- name: ListDeploymentModelProviders :many
SELECT harness, protocol, base_url, context_window, max_output_tokens, model, harness_config, updated_at, last_used_at, last_error_code, last_error_at
FROM deployment_model_providers ORDER BY harness;

-- name: GetDeploymentModelProviderSecret :one
SELECT encrypted_config, revision FROM deployment_model_providers WHERE harness = @harness;

-- name: UpsertDeploymentModelProvider :one
INSERT INTO deployment_model_providers (harness, protocol, base_url, context_window, max_output_tokens, model, harness_config, encrypted_config, revision, updated_at)
VALUES (@harness, @protocol, @base_url, @context_window, @max_output_tokens, @model, @harness_config, @encrypted_config, @revision, clock_timestamp())
ON CONFLICT (harness) DO UPDATE SET protocol = EXCLUDED.protocol, base_url = EXCLUDED.base_url,
    context_window = EXCLUDED.context_window, max_output_tokens = EXCLUDED.max_output_tokens,
    model = EXCLUDED.model, harness_config = EXCLUDED.harness_config, encrypted_config = EXCLUDED.encrypted_config, revision = EXCLUDED.revision, updated_at = EXCLUDED.updated_at,
    last_used_at = NULL, last_error_code = NULL, last_error_at = NULL, recovery_pending = false
RETURNING harness, protocol, base_url, context_window, max_output_tokens, model, harness_config, updated_at, last_used_at, last_error_code, last_error_at;

-- name: DeleteDeploymentModelProvider :execrows
DELETE FROM deployment_model_providers WHERE harness = @harness;
