-- +goose Up
-- Core derives the address nodes and sandboxes use from AGENTS_API_PUBLIC_URL,
-- so the deployment no longer stores one. Each node keeps the address it
-- enrolled with, which tells an administrator which nodes a change affects.
ALTER TABLE runtime_nodes ADD COLUMN core_url text NOT NULL DEFAULT '';
UPDATE runtime_nodes n SET core_url = d.core_url FROM runtime_deployment d WHERE d.singleton AND n.removed_at IS NULL;
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND mode='' AND generation=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);
ALTER TABLE runtime_deployment DROP COLUMN core_url;

-- +goose Down
ALTER TABLE runtime_deployment ADD COLUMN core_url text NOT NULL DEFAULT '';
UPDATE runtime_deployment d SET core_url = COALESCE((SELECT n.core_url FROM runtime_nodes n
    WHERE n.removed_at IS NULL AND n.core_url <> '' ORDER BY n.created_at DESC LIMIT 1), '')
WHERE d.web_managed AND d.provider_kind <> '';

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_deployment WHERE web_managed AND provider_kind <> '' AND core_url = '') THEN
        RAISE EXCEPTION 'Cannot restore the sandbox deployment Core address: no enrolled node records it';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_setup_check;
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_setup_check CHECK (
    NOT web_managed OR (local_node_id IS NULL AND (
        (provider_kind='' AND core_url='' AND mode='' AND generation=0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='docker' AND core_url<>'' AND mode='nodes' AND generation>0 AND idle_seconds=0 AND retention_seconds=0) OR
        (provider_kind='microsandbox' AND core_url<>'' AND mode='nodes' AND generation>0 AND idle_seconds>0 AND retention_seconds>0) OR
        (provider_kind='e2b' AND core_url<>'' AND mode='direct' AND generation>0 AND idle_seconds=0 AND retention_seconds=0)
    ))
);
ALTER TABLE runtime_nodes DROP COLUMN core_url;
