-- +goose Up
ALTER TABLE vault_credentials
    ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT vault_credentials_metadata_check CHECK (jsonb_typeof(metadata) = 'object');
