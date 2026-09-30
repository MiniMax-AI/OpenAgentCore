-- +goose Up
ALTER TABLE deployment_model_providers ADD COLUMN model text NOT NULL DEFAULT '';
ALTER TABLE deployment_model_providers ADD COLUMN harness_config jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE deployment_model_providers DROP COLUMN harness_config;
ALTER TABLE deployment_model_providers DROP COLUMN model;
