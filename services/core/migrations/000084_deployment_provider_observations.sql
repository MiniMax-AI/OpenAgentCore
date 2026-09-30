-- +goose Up
-- Revision identity is private and independent of content and wall-clock time.
ALTER TABLE deployment_model_providers
  ADD COLUMN revision uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN last_used_at timestamptz,
  ADD COLUMN last_error_code text,
  ADD COLUMN last_error_at timestamptz,
  ADD COLUMN recovery_pending boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT deployment_provider_error_pair CHECK ((last_error_code IS NULL) = (last_error_at IS NULL)),
  ADD CONSTRAINT deployment_provider_recovery CHECK (NOT recovery_pending OR last_error_at IS NOT NULL),
  ADD CONSTRAINT deployment_provider_error_code CHECK (last_error_code IN (
    'authentication_error', 'connection_failed', 'rate_limit_exceeded',
    'usage_limit_exceeded', 'server_overloaded', 'server_error',
    'resource_not_found', 'request_timeout', 'invalid_request'));
ALTER TABLE session_execution_configuration ADD COLUMN deployment_provider_revision uuid;

-- +goose Down
ALTER TABLE session_execution_configuration DROP COLUMN deployment_provider_revision;
ALTER TABLE deployment_model_providers
  DROP CONSTRAINT deployment_provider_error_pair,
  DROP CONSTRAINT deployment_provider_recovery,
  DROP CONSTRAINT deployment_provider_error_code,
  DROP COLUMN revision,
  DROP COLUMN last_used_at,
  DROP COLUMN last_error_code,
  DROP COLUMN last_error_at,
  DROP COLUMN recovery_pending;
