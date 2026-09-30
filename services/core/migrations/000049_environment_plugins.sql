-- +goose Up
ALTER TABLE environment_templates
 ADD COLUMN plugins jsonb NOT NULL DEFAULT '[]'::jsonb,
 ADD COLUMN plugin_contents bytea,
 ADD COLUMN capability_directories text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE environment_templates
 DROP COLUMN capability_directories,
 DROP COLUMN plugin_contents,
 DROP COLUMN plugins;
