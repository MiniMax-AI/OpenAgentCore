-- +goose Up
ALTER TABLE turns ADD COLUMN settings jsonb;
ALTER TABLE environment_input_reservations ADD COLUMN settings jsonb;

UPDATE turns SET settings = (
    SELECT jsonb_build_object(
        'model', s.configuration #>> '{agent,model}',
        'reasoning_effort', s.configuration #> '{agent,reasoning,effort}',
        'service_tier', s.configuration #>> '{agent,service_tier}',
        'review', COALESCE(s.configuration ->> 'review', 'none'))
    FROM sessions s WHERE s.id = turns.session_id);
UPDATE environment_input_reservations SET settings = (
    SELECT jsonb_build_object(
        'model', s.configuration #>> '{agent,model}',
        'reasoning_effort', s.configuration #> '{agent,reasoning,effort}',
        'service_tier', s.configuration #>> '{agent,service_tier}',
        'review', COALESCE(s.configuration ->> 'review', 'none'))
    FROM sessions s WHERE s.id = environment_input_reservations.session_id);

ALTER TABLE turns
    ALTER COLUMN settings SET NOT NULL,
    ADD CONSTRAINT turns_settings_check CHECK (jsonb_typeof(settings) = 'object');
ALTER TABLE environment_input_reservations
    ALTER COLUMN settings SET NOT NULL,
    ADD CONSTRAINT environment_input_reservations_settings_check CHECK (jsonb_typeof(settings) = 'object');
