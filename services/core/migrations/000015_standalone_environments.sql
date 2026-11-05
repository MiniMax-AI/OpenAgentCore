-- +goose Up
-- A creation key deduplicates within the Project and the creator.
ALTER TABLE environments
    DROP CONSTRAINT environments_tenant_id_idempotency_key_key,
    ADD CONSTRAINT environments_creation_key UNIQUE (tenant_id, creator_kind, creator_id, idempotency_key);

-- An Environment whose external workspace may be deleted: no live Session holds it, and it is either Session-owned or ended.
CREATE VIEW deletable_environments AS
SELECT e.id FROM environments e
WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.environment_id = e.id AND s.deleted_at IS NULL)
  AND (e.session_owned OR e.status IN ('failed', 'expired'));
