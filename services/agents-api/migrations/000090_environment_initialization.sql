-- +goose Up
ALTER TABLE environments ADD COLUMN initialization text NOT NULL DEFAULT 'complete'
    CHECK (initialization IN ('pending', 'running', 'complete', 'failed'));
ALTER TABLE runtime_allocations DROP COLUMN initialization;

-- +goose Down
ALTER TABLE runtime_allocations ADD COLUMN initialization text NOT NULL DEFAULT 'complete'
    CHECK (initialization IN ('pending', 'running', 'complete'));
ALTER TABLE environments DROP COLUMN initialization;
