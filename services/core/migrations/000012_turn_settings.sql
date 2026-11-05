-- +goose Up
-- Existing Turns and reservations bind their Session's current settings, as sessions.TurnSettingsOf projects them.
CREATE TEMPORARY TABLE bound_settings AS
SELECT id AS session_id, jsonb_build_object(
    'model', coalesce(configuration #>> '{agent,model}', ''),
    'reasoning_effort', configuration #> '{agent,reasoning,effort}',
    'service_tier', coalesce(configuration #>> '{agent,service_tier}', ''),
    'review', coalesce(configuration ->> 'review', 'none')) AS settings
FROM sessions;
ALTER TABLE turns ADD COLUMN settings jsonb;
UPDATE turns SET settings = b.settings FROM bound_settings b WHERE b.session_id = turns.session_id;
ALTER TABLE turns ALTER COLUMN settings SET NOT NULL, ADD CONSTRAINT turns_settings_check CHECK (jsonb_typeof(settings) = 'object');
ALTER TABLE environment_input_reservations ADD COLUMN settings jsonb;
UPDATE environment_input_reservations SET settings = b.settings FROM bound_settings b WHERE b.session_id = environment_input_reservations.session_id;
ALTER TABLE environment_input_reservations ALTER COLUMN settings SET NOT NULL, ADD CONSTRAINT environment_input_reservations_settings_check CHECK (jsonb_typeof(settings) = 'object');
DROP TABLE bound_settings;
