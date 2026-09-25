-- +goose Up
-- Existing rows keep NULL: the time they entered their current phase is unknown.
ALTER TABLE runtime_allocations ADD COLUMN compute_phase_changed_at timestamptz;
ALTER TABLE runtime_allocations ALTER COLUMN compute_phase_changed_at SET DEFAULT clock_timestamp();

-- +goose Down
ALTER TABLE runtime_allocations DROP COLUMN compute_phase_changed_at;
