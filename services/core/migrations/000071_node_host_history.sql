-- +goose Up
CREATE TABLE node_host_history_samples (
    node_id uuid NOT NULL REFERENCES runtime_nodes(id),
    observed_at timestamptz NOT NULL,
    cpu_utilization double precision CHECK (cpu_utilization >= 0 AND cpu_utilization <= 1),
    memory_used_bytes bigint CHECK (memory_used_bytes >= 0),
    available_disk_bytes bigint CHECK (available_disk_bytes >= 0),
    PRIMARY KEY (node_id, observed_at)
);
CREATE INDEX node_host_history_retention ON node_host_history_samples(observed_at);

-- +goose Down
DROP TABLE node_host_history_samples;
