-- +goose Up
ALTER TABLE vault_credentials DROP CONSTRAINT vault_credentials_auth_type_check;
ALTER TABLE vault_credentials ADD COLUMN oauth_metadata jsonb;
ALTER TABLE vault_credentials ADD CONSTRAINT vault_credentials_auth_type_check
    CHECK (auth_type IN ('static_bearer', 'mcp_oauth'));
ALTER TABLE vault_credentials ADD CONSTRAINT vault_credentials_oauth_metadata_check
    CHECK ((auth_type = 'static_bearer' AND oauth_metadata IS NULL)
        OR (auth_type = 'mcp_oauth' AND oauth_metadata IS NOT NULL
            AND jsonb_typeof(oauth_metadata) = 'object'));

-- +goose Down
-- Refuse to discard grants when rolling back to static-only storage.
ALTER TABLE vault_credentials DROP CONSTRAINT vault_credentials_auth_type_check;
ALTER TABLE vault_credentials ADD CONSTRAINT vault_credentials_auth_type_check
    CHECK (auth_type = 'static_bearer');
ALTER TABLE vault_credentials DROP CONSTRAINT vault_credentials_oauth_metadata_check;
ALTER TABLE vault_credentials DROP COLUMN oauth_metadata;
