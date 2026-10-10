-- +goose Up
ALTER TABLE runtime_node_generation_status ADD COLUMN checkpoint jsonb
    CHECK (checkpoint IS NULL OR jsonb_typeof(checkpoint) = 'object');

-- +goose Down
ALTER TABLE runtime_node_generation_status DROP COLUMN checkpoint;
