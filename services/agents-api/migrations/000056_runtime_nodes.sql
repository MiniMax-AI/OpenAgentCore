-- +goose Up
ALTER TABLE runtime_deployment ADD COLUMN provider_kind text NOT NULL DEFAULT '' CHECK (provider_kind IN ('', 'docker', 'microsandbox'));
ALTER TABLE runtime_deployment ADD COLUMN local_node_id uuid;
ALTER TABLE runtime_deployment ADD COLUMN owner_epoch bigint NOT NULL DEFAULT 0 CHECK (owner_epoch >= 0);
CREATE TABLE runtime_nodes (
    id uuid PRIMARY KEY,
    installation_id uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[0-9a-f]{64}$'),
    credential_sha256 text NOT NULL CHECK (credential_sha256 ~ '^[0-9a-f]{64}$'),
    max_active integer NOT NULL CHECK (max_active > 0),
    max_retained integer NOT NULL CHECK (max_retained >= max_active),
    connection_id uuid,
    provider_ready boolean NOT NULL DEFAULT false,
    health jsonb NOT NULL DEFAULT '{}'::jsonb,
    connected_epoch bigint NOT NULL DEFAULT 0,
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    removed_at timestamptz
);
CREATE TABLE runtime_placements (
    environment_id uuid PRIMARY KEY REFERENCES environments(id),
    node_id uuid NOT NULL REFERENCES runtime_nodes(id),
    reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    released_at timestamptz,
    UNIQUE(environment_id, node_id)
);
ALTER TABLE runtime_allocations ADD COLUMN node_id uuid;
ALTER TABLE runtime_allocations ADD COLUMN observation_error text NOT NULL DEFAULT '' CHECK (observation_error IN ('','node_unavailable','resource_missing','compute_unconfirmed','ownership_mismatch','provider_unavailable'));
ALTER TABLE runtime_allocations ADD CONSTRAINT runtime_allocation_placement_fk FOREIGN KEY (environment_id,node_id) REFERENCES runtime_placements(environment_id,node_id);
CREATE INDEX runtime_placements_node_idx ON runtime_placements(node_id) WHERE released_at IS NULL;
CREATE TABLE runtime_node_enrollments (
    token_sha256 text PRIMARY KEY CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    installation_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    node_id uuid REFERENCES runtime_nodes(id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM runtime_nodes) THEN
    RAISE EXCEPTION 'Cannot discard sandbox node and placement identities';
END IF;
END $$;
-- +goose StatementEnd
DROP TABLE runtime_node_enrollments;
ALTER TABLE runtime_allocations DROP CONSTRAINT runtime_allocation_placement_fk;
ALTER TABLE runtime_allocations DROP COLUMN node_id;
ALTER TABLE runtime_allocations DROP COLUMN observation_error;
DROP TABLE runtime_placements;
DROP TABLE runtime_nodes;
ALTER TABLE runtime_deployment DROP COLUMN owner_epoch;
ALTER TABLE runtime_deployment DROP COLUMN local_node_id;
ALTER TABLE runtime_deployment DROP COLUMN provider_kind;
