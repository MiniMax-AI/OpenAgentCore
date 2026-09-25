-- +goose Up
ALTER TABLE runtime_nodes
  ADD COLUMN admission_state text NOT NULL DEFAULT 'enabled' CHECK (admission_state IN ('pending_confirmation', 'enabled')),
  ADD COLUMN configuration_version bigint NOT NULL DEFAULT 1,
  ADD COLUMN last_update_revision text NOT NULL DEFAULT '',
  ADD COLUMN last_update_digest text NOT NULL DEFAULT '';
ALTER TABLE runtime_nodes ALTER COLUMN admission_state SET DEFAULT 'pending_confirmation';
ALTER TABLE runtime_node_enrollments ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX runtime_node_enrollments_id ON runtime_node_enrollments(id);

-- +goose Down
DROP INDEX runtime_node_enrollments_id;
ALTER TABLE runtime_node_enrollments DROP COLUMN id;
ALTER TABLE runtime_nodes DROP COLUMN last_update_digest, DROP COLUMN last_update_revision, DROP COLUMN configuration_version, DROP COLUMN admission_state;
