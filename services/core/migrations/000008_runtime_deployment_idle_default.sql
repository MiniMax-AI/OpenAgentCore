-- +goose Up
-- The column default is the one authored definition of the initial idle policy.
ALTER TABLE runtime_deployment ALTER COLUMN idle_seconds SET DEFAULT 300;
