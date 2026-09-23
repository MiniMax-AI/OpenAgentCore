-- +goose Up
CREATE TABLE session_execution_configuration (
  session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  configuration jsonb NOT NULL CHECK (jsonb_typeof(configuration) = 'object')
);

-- +goose Down
DROP TABLE session_execution_configuration;
