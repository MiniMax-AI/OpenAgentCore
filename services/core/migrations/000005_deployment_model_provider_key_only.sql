-- +goose Up
-- The sealed value was the whole configuration and is now the provider key alone. Defaults stored before this change are removed and set again.
DELETE FROM deployment_model_providers;
ALTER TABLE deployment_model_providers RENAME COLUMN encrypted_config TO encrypted_api_key;
