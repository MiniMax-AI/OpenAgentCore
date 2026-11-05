-- +goose Up
-- A creation key deduplicates within the Project and the creator.
ALTER TABLE environments
    DROP CONSTRAINT environments_tenant_id_idempotency_key_key,
    ADD CONSTRAINT environments_creation_key UNIQUE (tenant_id, creator_kind, creator_id, idempotency_key);
