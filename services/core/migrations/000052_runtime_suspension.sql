-- +goose Up
ALTER TABLE runtime_allocations
    ADD COLUMN compute_phase text NOT NULL DEFAULT 'disabled'
        CHECK (compute_phase IN ('disabled', 'running', 'quiescing', 'suspending', 'suspended', 'restoring', 'waking')),
    ADD COLUMN compute_revision bigint NOT NULL DEFAULT 0 CHECK (compute_revision >= 0),
    ADD COLUMN compute_state jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(compute_state) = 'object'),
    ADD COLUMN compute_activity_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN compute_wake_requested boolean NOT NULL DEFAULT false,
    ADD COLUMN compute_retained_until timestamptz;

-- +goose Down
LOCK TABLE runtime_allocations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations WHERE compute_phase <> 'disabled') THEN
        RAISE EXCEPTION 'Cannot discard retained compute and snapshot ownership';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_allocations
    DROP COLUMN compute_retained_until,
    DROP COLUMN compute_wake_requested,
    DROP COLUMN compute_activity_at,
    DROP COLUMN compute_state,
    DROP COLUMN compute_revision,
    DROP COLUMN compute_phase;
