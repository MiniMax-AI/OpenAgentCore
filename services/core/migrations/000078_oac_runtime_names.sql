-- +goose Up
-- Admission also locks the deployment first. Keep the drain check and rewrite
-- atomic with respect to allocation creation and cleanup.
LOCK TABLE runtime_deployment, runtime_allocations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations WHERE released_at IS NULL) THEN
        RAISE EXCEPTION 'Drain the sandbox deployment with the previous release before upgrading to OpenAgentCore: sandboxes created before the rename cannot be managed by this release';
    END IF;
END $$;
-- +goose StatementEnd
UPDATE runtime_deployment
   SET specification = jsonb_set(specification, '{runtime,microsandbox_ref}',
       to_jsonb('oac-runtime@' || split_part(specification #>> '{runtime,microsandbox_ref}', '@', 2)), false)
 WHERE specification #>> '{runtime,microsandbox_ref}' LIKE 'parsar-core-runtime@%';

-- +goose Down
LOCK TABLE runtime_deployment, runtime_allocations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations WHERE released_at IS NULL) THEN
        RAISE EXCEPTION 'Drain the sandbox deployment before reverting OpenAgentCore Runtime names: sandboxes created after the rename cannot be managed by the previous release';
    END IF;
END $$;
-- +goose StatementEnd
UPDATE runtime_deployment
   SET specification = jsonb_set(specification, '{runtime,microsandbox_ref}',
       to_jsonb('parsar-core-runtime@' || split_part(specification #>> '{runtime,microsandbox_ref}', '@', 2)), false)
 WHERE specification #>> '{runtime,microsandbox_ref}' LIKE 'oac-runtime@%';
