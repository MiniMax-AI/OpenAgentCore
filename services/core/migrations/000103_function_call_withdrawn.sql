-- +goose Up
-- A native Harness can abandon a call before it applies a result. A withdrawn call is no longer a required action.
ALTER TABLE function_calls ADD COLUMN withdrawn boolean NOT NULL DEFAULT false;
ALTER TABLE function_calls ADD CONSTRAINT function_calls_not_applied_and_withdrawn CHECK (NOT (applied AND withdrawn));
DROP INDEX function_calls_pending_idx;
CREATE INDEX function_calls_pending_idx ON function_calls(session_id, turn_id) WHERE NOT applied AND NOT withdrawn;

-- +goose Down
SELECT 1;
