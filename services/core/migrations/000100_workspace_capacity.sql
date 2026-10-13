-- +goose Up
-- Adapter-encoded private credential, sealed with the installation and
-- configuration ID. Existing configurations require none.
ALTER TABLE workspace_fs_configurations ADD COLUMN credential bytea
    CHECK (credential IS NULL OR length(credential) > 0);
-- The admitted per-workspace capacity. Existing workspaces were admitted without one.
ALTER TABLE environment_workspaces ADD COLUMN capacity_mib bigint NOT NULL DEFAULT 0
    CHECK (capacity_mib BETWEEN 0 AND 4294967295);
-- The installation's single tenant capacity policy; zero disables admission.
CREATE TABLE workspace_storage_admission (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    tenant_capacity_mib bigint NOT NULL DEFAULT 0 CHECK (tenant_capacity_mib BETWEEN 0 AND 4294967295)
);
INSERT INTO workspace_storage_admission DEFAULT VALUES;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_workspace_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.adapter, NEW.parameters, NEW.credential) IS DISTINCT FROM (OLD.id, OLD.adapter, OLD.parameters, OLD.credential) THEN
        RAISE EXCEPTION 'Workspace filesystem configuration is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_workspace_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.object_id, NEW.environment_id, NEW.configuration_id, NEW.capacity_mib) IS DISTINCT FROM (OLD.object_id, OLD.environment_id, OLD.configuration_id, OLD.capacity_mib) THEN
        RAISE EXCEPTION 'Environment workspace identity is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
LOCK TABLE environment_workspaces, workspace_fs_configurations, workspace_storage_admission IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workspace_fs_configurations WHERE credential IS NOT NULL)
        OR EXISTS (SELECT 1 FROM environment_workspaces WHERE capacity_mib <> 0)
        OR EXISTS (SELECT 1 FROM workspace_storage_admission WHERE tenant_capacity_mib <> 0) THEN
        RAISE EXCEPTION 'Cannot discard workspace credentials, admitted capacities or tenant capacity policy';
    END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_workspace_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.adapter, NEW.parameters) IS DISTINCT FROM (OLD.id, OLD.adapter, OLD.parameters) THEN
        RAISE EXCEPTION 'Workspace filesystem configuration is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_workspace_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.object_id, NEW.environment_id, NEW.configuration_id) IS DISTINCT FROM (OLD.object_id, OLD.environment_id, OLD.configuration_id) THEN
        RAISE EXCEPTION 'Environment workspace identity is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TABLE workspace_storage_admission;
ALTER TABLE environment_workspaces DROP COLUMN capacity_mib;
ALTER TABLE workspace_fs_configurations DROP COLUMN credential;
