-- +goose Up
ALTER TABLE environments
    ADD COLUMN initialization_steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT environments_initialization_steps_check CHECK (jsonb_typeof(initialization_steps) = 'array');
