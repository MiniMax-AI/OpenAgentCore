-- +goose Up
-- Core owns the default idle policy; the singleton row keeps its stored value.
ALTER TABLE runtime_deployment ALTER COLUMN idle_seconds DROP DEFAULT;
