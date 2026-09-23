-- +goose Up
-- A failed hosted Environment keeps the public reason and time of its
-- provisioning failure. Core composes the reason from a fixed step label and an
-- exit status; Runtime output is never stored. Earlier failures keep NULL.
ALTER TABLE environments
    ADD COLUMN failure_reason text,
    ADD COLUMN failed_at timestamptz,
    ADD CONSTRAINT environment_failure_recorded CHECK (
        (failure_reason IS NULL) = (failed_at IS NULL)
        AND (failed_at IS NULL OR status = 'failed')
        AND char_length(failure_reason) <= 256
    );

-- +goose Down
ALTER TABLE environments
    DROP CONSTRAINT environment_failure_recorded,
    DROP COLUMN failed_at,
    DROP COLUMN failure_reason;
