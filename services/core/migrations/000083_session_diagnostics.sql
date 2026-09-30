-- +goose Up
-- Historical terminal Items retain unknown settlement times.
ALTER TABLE session_items ADD COLUMN settled_at timestamptz;
ALTER TABLE environments ADD COLUMN failure_detail jsonb CHECK (failure_detail IS NULL OR jsonb_typeof(failure_detail) = 'object');

-- +goose Down
ALTER TABLE environments DROP COLUMN failure_detail;
ALTER TABLE session_items DROP COLUMN settled_at;
