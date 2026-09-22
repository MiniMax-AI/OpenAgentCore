-- +goose Up
CREATE TABLE runtime_deployment (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    installation_id uuid,
    backend_fingerprint text NOT NULL DEFAULT '',
    maintenance boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((installation_id IS NULL AND backend_fingerprint = '') OR
           (installation_id IS NOT NULL AND backend_fingerprint ~ '^[0-9a-f]{64}$'))
);
INSERT INTO runtime_deployment (singleton) VALUES (true);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE installation_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot discard persisted sandbox installation identity';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE runtime_deployment;
