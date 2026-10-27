-- +goose Up
-- The deployment cap defaults to public access with no private domains.
UPDATE runtime_deployment SET specification = specification || '{"network":{"public":true,"domains":[]}}'::jsonb
WHERE specification <> '{}'::jsonb AND NOT specification ? 'network';

ALTER TABLE runtime_deployment_generations DISABLE TRIGGER immutable_sandbox_specification;
UPDATE runtime_deployment_generations SET specification = specification || '{"network":{"public":true,"domains":[]}}'::jsonb
WHERE specification <> '{}'::jsonb AND NOT specification ? 'network';
ALTER TABLE runtime_deployment_generations ENABLE TRIGGER immutable_sandbox_specification;
