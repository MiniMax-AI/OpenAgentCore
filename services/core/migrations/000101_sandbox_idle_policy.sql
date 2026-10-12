-- +goose Up
ALTER TABLE runtime_deployment ADD COLUMN idle_seconds bigint NOT NULL DEFAULT 300 CHECK (idle_seconds BETWEEN 30 AND 86400);

-- +goose Down
ALTER TABLE runtime_deployment DROP COLUMN idle_seconds;
