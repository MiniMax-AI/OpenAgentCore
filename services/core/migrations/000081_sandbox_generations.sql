-- +goose Up
CREATE TABLE runtime_deployment_generations (
    generation bigint PRIMARY KEY CHECK (generation >= 0),
    provider_kind text NOT NULL CHECK (provider_kind IN ('docker','microsandbox','e2b')),
    specification jsonb NOT NULL,
    e2b_template text NOT NULL DEFAULT '',
    e2b_template_build_status text,
    e2b_template_cpus integer,
    e2b_template_memory_mib integer,
    e2b_template_root_disk_mib integer,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE runtime_allocations ADD COLUMN deployment_generation bigint;
ALTER TABLE runtime_placements ADD COLUMN deployment_generation bigint;
UPDATE runtime_allocations SET deployment_generation=(SELECT generation FROM runtime_deployment) WHERE state <> 'released';
UPDATE runtime_placements SET deployment_generation=(SELECT generation FROM runtime_deployment) WHERE released_at IS NULL;
ALTER TABLE runtime_allocations ADD CHECK (deployment_generation >= 0),
    ADD CHECK (state = 'released' OR deployment_generation IS NOT NULL);
ALTER TABLE runtime_placements ADD CHECK (deployment_generation >= 0),
    ADD CHECK (released_at IS NOT NULL OR deployment_generation IS NOT NULL);
ALTER TABLE runtime_nodes ADD COLUMN ready_generation bigint CHECK (ready_generation >= 0);
-- The serving pin is durable, independent of current connection readiness.
UPDATE runtime_nodes SET ready_generation=deployment_generation WHERE removed_at IS NULL;
CREATE INDEX runtime_allocations_generation_live ON runtime_allocations(deployment_generation) WHERE state <> 'released';
CREATE INDEX runtime_placements_generation_live ON runtime_placements(deployment_generation) WHERE released_at IS NULL;
CREATE INDEX runtime_nodes_generation_live ON runtime_nodes(ready_generation) WHERE removed_at IS NULL;
-- +goose StatementBegin
CREATE FUNCTION immutable_runtime_deployment_generation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deployment_generation IS DISTINCT FROM OLD.deployment_generation THEN
        RAISE EXCEPTION 'Sandbox ownership generation is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_runtime_allocation_generation BEFORE UPDATE ON runtime_allocations
FOR EACH ROW EXECUTE FUNCTION immutable_runtime_deployment_generation();
CREATE TRIGGER immutable_runtime_placement_generation BEFORE UPDATE ON runtime_placements
FOR EACH ROW EXECUTE FUNCTION immutable_runtime_deployment_generation();

-- +goose StatementBegin
CREATE FUNCTION immutable_sandbox_specification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Retained sandbox specifications are immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_sandbox_specification BEFORE UPDATE ON runtime_deployment_generations
FOR EACH ROW EXECUTE FUNCTION immutable_sandbox_specification();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations a CROSS JOIN runtime_deployment d WHERE a.state <> 'released' AND a.deployment_generation <> d.generation)
       OR EXISTS (SELECT 1 FROM runtime_placements p CROSS JOIN runtime_deployment d WHERE p.released_at IS NULL AND p.deployment_generation <> d.generation)
       OR EXISTS (SELECT 1 FROM runtime_nodes n CROSS JOIN runtime_deployment d WHERE n.removed_at IS NULL AND n.ready_generation <> d.generation) THEN
        RAISE EXCEPTION 'Cannot downgrade while retained ownership or node serving pins require generation routing';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER immutable_runtime_allocation_generation ON runtime_allocations;
DROP TRIGGER immutable_runtime_placement_generation ON runtime_placements;
DROP FUNCTION immutable_runtime_deployment_generation();
ALTER TABLE runtime_nodes DROP COLUMN ready_generation;
ALTER TABLE runtime_placements DROP COLUMN deployment_generation;
ALTER TABLE runtime_allocations DROP COLUMN deployment_generation;
DROP TABLE runtime_deployment_generations;
DROP FUNCTION immutable_sandbox_specification();
