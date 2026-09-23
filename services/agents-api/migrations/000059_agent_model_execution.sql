-- +goose Up
CREATE TABLE agent_model_execution (
  agent_id uuid PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
  encrypted_config bytea NOT NULL
);

-- +goose Down
DROP TABLE agent_model_execution;
