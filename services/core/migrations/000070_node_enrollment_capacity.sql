-- +goose Up
ALTER TABLE runtime_node_enrollments ADD COLUMN max_active integer NOT NULL DEFAULT 2;
ALTER TABLE runtime_node_enrollments ADD COLUMN max_retained integer NOT NULL DEFAULT 8;
ALTER TABLE runtime_node_enrollments ADD CONSTRAINT runtime_enrollment_capacity CHECK (max_active >= 1 AND max_retained >= max_active AND max_retained <= 1000000);

-- +goose Down
ALTER TABLE runtime_node_enrollments DROP COLUMN max_retained;
ALTER TABLE runtime_node_enrollments DROP COLUMN max_active;
