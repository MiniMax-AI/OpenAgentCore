-- +goose Up
ALTER TABLE runtime_node_enrollments ALTER COLUMN max_active DROP DEFAULT, ALTER COLUMN max_active DROP NOT NULL;
ALTER TABLE runtime_node_enrollments ALTER COLUMN max_retained DROP DEFAULT, ALTER COLUMN max_retained DROP NOT NULL;
