-- +goose Up
-- Earlier generation rows predate custom endpoints and use the official defaults.
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_api_url text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment_generations ADD COLUMN e2b_domain text NOT NULL DEFAULT '';
ALTER TABLE runtime_deployment_generations ADD CONSTRAINT runtime_deployment_generation_e2b_endpoint_check CHECK (
    (provider_kind = 'e2b' AND ((e2b_api_url = '' AND e2b_domain = '') OR
                                 (e2b_api_url <> '' AND e2b_domain <> ''))) OR
    (provider_kind <> 'e2b' AND e2b_api_url = '' AND e2b_domain = '')
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment_generations WHERE e2b_api_url <> '' OR e2b_domain <> '') THEN
        RAISE EXCEPTION 'cannot remove E2B generation endpoints while a custom endpoint is retained';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_deployment_generations DROP CONSTRAINT runtime_deployment_generation_e2b_endpoint_check;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_domain;
ALTER TABLE runtime_deployment_generations DROP COLUMN e2b_api_url;
