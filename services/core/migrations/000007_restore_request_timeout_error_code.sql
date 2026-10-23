-- +goose Up
ALTER TABLE deployment_model_providers DROP CONSTRAINT deployment_provider_error_code;
ALTER TABLE deployment_model_providers ADD CONSTRAINT deployment_provider_error_code CHECK ((last_error_code = ANY (ARRAY['authentication_error'::text, 'connection_failed'::text, 'rate_limit_exceeded'::text, 'usage_limit_exceeded'::text, 'server_overloaded'::text, 'server_error'::text, 'resource_not_found'::text, 'request_timeout'::text, 'invalid_request'::text])));
