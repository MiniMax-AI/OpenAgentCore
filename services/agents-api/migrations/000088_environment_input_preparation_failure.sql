-- +goose Up
ALTER TABLE environment_input_reservations
  DROP CONSTRAINT environment_input_reservations_check1,
  ADD CONSTRAINT environment_input_reservations_check1
    CHECK (failure_code IS NULL OR (state = 'failed' AND failure_code IN ('model_provider_required', 'runtime_preparation_failed')));

-- +goose Down
ALTER TABLE environment_input_reservations
  DROP CONSTRAINT environment_input_reservations_check1,
  ADD CONSTRAINT environment_input_reservations_check1
    CHECK (failure_code IS NULL OR (state = 'failed' AND failure_code = 'model_provider_required'));
