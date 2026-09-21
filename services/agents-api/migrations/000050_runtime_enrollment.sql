-- +goose Up
ALTER TABLE devices
    ADD COLUMN executor_key_id uuid REFERENCES environment_executor_credentials(key_id),
    ALTER COLUMN credential_hash DROP NOT NULL,
    ADD CONSTRAINT device_credential_source CHECK (
        (executor_key_id IS NULL AND credential_hash IS NOT NULL)
        OR (executor_key_id IS NOT NULL AND credential_hash IS NULL AND environment_id IS NOT NULL)
    );

-- Resolve the current credential once for bootstrap, reconnect and heartbeat.
CREATE VIEW runtime_device_authority AS
SELECT d.id, d.tenant_id, d.name, d.environment_id,
    COALESCE(c.token_sha256, d.credential_hash) AS credential_hash
FROM devices d
LEFT JOIN environments e ON e.id = d.environment_id
LEFT JOIN sessions s ON s.id = e.session_id AND s.tenant_id = d.tenant_id
LEFT JOIN environment_executor_credentials c ON c.key_id = d.executor_key_id
WHERE d.revoked_at IS NULL
    AND (d.environment_id IS NULL OR (s.id IS NOT NULL AND s.deleted_at IS NULL))
    AND (d.executor_key_id IS NULL OR (
        c.revoked_at IS NULL AND c.tenant_id = s.tenant_id
        AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
        AND (c.environment_id IS NULL OR c.environment_id = e.id)
        AND s.configuration->'environment'->>'type' = 'self_hosted'
        AND e.status NOT IN ('failed', 'expired')
        AND EXISTS (SELECT 1 FROM execution_project_scopes p WHERE p.tenant_id = c.tenant_id)
    ));

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM devices WHERE executor_key_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot remove Runtime enrollment while bound devices exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP VIEW runtime_device_authority;
ALTER TABLE devices DROP CONSTRAINT device_credential_source,
    DROP COLUMN executor_key_id, ALTER COLUMN credential_hash SET NOT NULL;
