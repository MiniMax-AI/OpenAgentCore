-- +goose Up
-- Only a claimed deployment with a provider admits hosted work, so every
-- allocation runs on a node or belongs to a direct deployment, and none needs
-- a keepalive lease.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations a CROSS JOIN runtime_deployment d
            WHERE a.state <> 'released' AND a.node_id IS NULL AND d.mode <> 'direct')
        OR EXISTS (SELECT 1 FROM environments e JOIN sessions s ON s.id = e.session_id CROSS JOIN runtime_deployment d
            WHERE d.installation_id IS NULL AND s.deleted_at IS NULL AND e.status = 'pending'
            AND s.configuration->'environment'->>'type' = 'openai_hosted') THEN
        RAISE EXCEPTION 'Cannot upgrade: hosted Sessions hold sandbox work that no configured sandbox deployment admitted. Delete those Sessions and release their runtime allocations, then upgrade';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE runtime_allocations DROP COLUMN kept_at;

-- +goose Down
ALTER TABLE runtime_allocations ADD COLUMN kept_at timestamptz NOT NULL DEFAULT clock_timestamp();
