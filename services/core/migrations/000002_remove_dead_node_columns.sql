-- +goose Up
ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_protocol_version_check;
ALTER TABLE runtime_nodes DROP COLUMN protocol_version;
ALTER TABLE runtime_nodes DROP COLUMN provider_ready;
