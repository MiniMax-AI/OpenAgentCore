-- +goose Up
ALTER TABLE turns ADD COLUMN settings jsonb NOT NULL CHECK (jsonb_typeof(settings) = 'object');
ALTER TABLE environment_input_reservations ADD COLUMN settings jsonb NOT NULL CHECK (jsonb_typeof(settings) = 'object');
