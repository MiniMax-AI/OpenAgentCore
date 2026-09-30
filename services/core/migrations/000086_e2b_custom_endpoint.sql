-- +goose Up
ALTER TABLE runtime_deployment ADD COLUMN IF NOT EXISTS e2b_api_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment ADD COLUMN IF NOT EXISTS e2b_domain text NOT NULL DEFAULT '';
-- +goose StatementBegin
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'runtime_deployment_e2b_endpoint_check') THEN
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_endpoint_check CHECK (
    (provider_kind = 'e2b' AND ((e2b_api_url = '' AND e2b_domain = '') OR
                                 (e2b_api_url <> '' AND e2b_domain <> ''))) OR
    (provider_kind <> 'e2b' AND e2b_api_url = '' AND e2b_domain = '')
);
END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE e2b_api_url <> '' OR e2b_domain <> '') THEN
        RAISE EXCEPTION 'cannot remove E2B endpoint columns while a custom endpoint is configured';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_endpoint_check;
ALTER TABLE runtime_deployment DROP COLUMN e2b_domain;
ALTER TABLE runtime_deployment DROP COLUMN e2b_api_url;
