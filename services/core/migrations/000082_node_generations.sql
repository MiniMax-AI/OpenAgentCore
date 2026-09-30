-- +goose Up
ALTER TABLE runtime_nodes ADD COLUMN protocol_version integer NOT NULL DEFAULT 1 CHECK (protocol_version IN (1,2));
CREATE TABLE runtime_node_generation_status (
 node_id uuid NOT NULL REFERENCES runtime_nodes(id),
 generation bigint NOT NULL CHECK (generation > 0),
 specification_digest text NOT NULL CHECK (specification_digest ~ '^[0-9a-f]{64}$'),
 connection_id uuid NOT NULL,
 owner_epoch bigint NOT NULL CHECK (owner_epoch > 0),
 state text NOT NULL CHECK (state IN ('ready','preparing','failed')),
 diagnostic text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(node_id,generation)
);
INSERT INTO runtime_node_generation_status(node_id,generation,specification_digest,connection_id,owner_epoch,state)
SELECT id,deployment_generation,specification_digest,connection_id,connected_epoch,
 CASE WHEN provider_ready THEN 'ready' ELSE 'failed' END FROM runtime_nodes
WHERE removed_at IS NULL AND connection_id IS NOT NULL AND connected_epoch>0
AND deployment_generation>0 AND specification_digest ~ '^[0-9a-f]{64}$';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM runtime_nodes n CROSS JOIN runtime_deployment d WHERE n.removed_at IS NULL
   AND (n.protocol_version=2 OR n.deployment_generation<>d.generation OR n.ready_generation<>d.generation)) THEN
  RAISE EXCEPTION 'Cannot downgrade while protocol v2 or retained-generation nodes remain; remove them after releasing their resources first';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE runtime_node_generation_status;
ALTER TABLE runtime_nodes DROP COLUMN protocol_version;
