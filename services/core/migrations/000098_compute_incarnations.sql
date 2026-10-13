-- +goose Up
ALTER TABLE runtime_allocations DROP CONSTRAINT runtime_allocations_environment_id_key;
CREATE UNIQUE INDEX runtime_allocations_current_environment ON runtime_allocations(environment_id) WHERE state <> 'released';
CREATE INDEX runtime_allocations_environment_receipts ON runtime_allocations(environment_id, created_at DESC, id DESC);
ALTER TABLE runtime_allocations DROP CONSTRAINT runtime_allocation_placement_fk;
ALTER TABLE runtime_allocations ADD CONSTRAINT runtime_allocation_node_fk FOREIGN KEY(node_id) REFERENCES runtime_nodes(id);
ALTER TABLE devices DROP CONSTRAINT devices_environment_id_key;
CREATE UNIQUE INDEX devices_environment_authority ON devices(environment_id) WHERE revoked_at IS NULL OR executor_key_id IS NOT NULL;

ALTER TABLE devices ADD COLUMN supported_agent_kinds jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(supported_agent_kinds) = 'array');

-- Cold retained Sessions own no compute, but a reset must end their execution lifetime.
CREATE VIEW runtime_reset_retained_environments AS
SELECT e.id AS environment_id, s.id AS session_id, s.tenant_id, a.deployment_generation
FROM environments e JOIN sessions s ON s.id = e.session_id
JOIN environment_workspaces w ON w.environment_id = e.id AND w.state = 'ready'
JOIN LATERAL (
    SELECT allocation.* FROM runtime_allocations allocation
    WHERE allocation.environment_id = e.id
    ORDER BY allocation.created_at DESC, allocation.id DESC LIMIT 1
) a ON true
CROSS JOIN runtime_deployment d
WHERE d.reset_clear IS NOT NULL AND d.reset_requested_at IS NOT NULL
  AND s.deleted_at IS NULL AND s.created_at <= d.reset_requested_at
  AND e.status NOT IN ('failed','expired') AND e.initialization = 'complete'
  AND s.configuration->'environment'->>'type' = 'openai_hosted'
  AND a.state = 'released' AND a.provider_key = d.installation_id
  AND a.deployment_generation <= d.generation AND a.created_at <= d.reset_requested_at
  AND NOT EXISTS (SELECT 1 FROM runtime_placements p WHERE p.environment_id = e.id AND p.released_at IS NULL);

-- +goose Down
DROP VIEW runtime_reset_retained_environments;
ALTER TABLE devices DROP COLUMN supported_agent_kinds;
DROP INDEX devices_environment_authority;
ALTER TABLE devices ADD CONSTRAINT devices_environment_id_key UNIQUE(environment_id);
ALTER TABLE runtime_allocations DROP CONSTRAINT runtime_allocation_node_fk;
ALTER TABLE runtime_allocations ADD CONSTRAINT runtime_allocation_placement_fk FOREIGN KEY(environment_id,node_id) REFERENCES runtime_placements(environment_id,node_id);
DROP INDEX runtime_allocations_environment_receipts;
DROP INDEX runtime_allocations_current_environment;
ALTER TABLE runtime_allocations ADD CONSTRAINT runtime_allocations_environment_id_key UNIQUE(environment_id);
