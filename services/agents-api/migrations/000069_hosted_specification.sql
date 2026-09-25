-- +goose Up
ALTER TABLE runtime_deployment ADD COLUMN specification jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE runtime_nodes ADD COLUMN specification_digest text NOT NULL DEFAULT '';
ALTER TABLE runtime_nodes ADD COLUMN deployment_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE runtime_nodes ADD CONSTRAINT runtime_node_specification_digest CHECK (specification_digest = '' OR specification_digest ~ '^[0-9a-f]{64}$');
ALTER TABLE runtime_nodes ADD CONSTRAINT runtime_node_deployment_generation CHECK (deployment_generation >= 0);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE specification <> '{}'::jsonb) THEN
        RAISE EXCEPTION 'Cannot discard an active sandbox specification';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_nodes DROP COLUMN deployment_generation;
ALTER TABLE runtime_nodes DROP COLUMN specification_digest;
ALTER TABLE runtime_deployment DROP COLUMN specification;
