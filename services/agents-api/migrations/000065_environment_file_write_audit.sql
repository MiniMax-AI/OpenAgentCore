-- +goose Up
-- Safe request identity survives native execution and request detachment.
ALTER TABLE environment_file_writes ADD COLUMN audit_source jsonb;
ALTER TABLE environment_file_writes ADD CONSTRAINT environment_file_write_audit_source_object
    CHECK (audit_source IS NULL OR jsonb_typeof(audit_source) = 'object');

-- +goose Down
ALTER TABLE environment_file_writes DROP COLUMN audit_source;
