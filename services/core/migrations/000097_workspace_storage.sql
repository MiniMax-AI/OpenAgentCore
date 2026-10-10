-- +goose Up
CREATE TABLE workspace_fs_configurations (
    id uuid PRIMARY KEY,
    adapter text NOT NULL CHECK (adapter <> ''),
    parameters jsonb NOT NULL CHECK (jsonb_typeof(parameters) = 'object'),
    active boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX workspace_fs_active_idx ON workspace_fs_configurations (active) WHERE active;

-- +goose StatementBegin
CREATE FUNCTION immutable_workspace_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.adapter, NEW.parameters) IS DISTINCT FROM (OLD.id, OLD.adapter, OLD.parameters) THEN
        RAISE EXCEPTION 'Workspace filesystem configuration is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_workspace_configuration BEFORE UPDATE ON workspace_fs_configurations
FOR EACH ROW EXECUTE FUNCTION immutable_workspace_configuration();

CREATE TABLE environment_workspaces (
    object_id uuid PRIMARY KEY,
    environment_id uuid NOT NULL UNIQUE REFERENCES environments(id),
    configuration_id uuid NOT NULL REFERENCES workspace_fs_configurations(id),
    state text NOT NULL DEFAULT 'creating' CHECK (state IN ('creating', 'ready', 'deleting', 'deleted')),
    attachment jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object'),
    CHECK (state <> 'ready' OR attachment IS NOT NULL)
);

-- +goose StatementBegin
CREATE FUNCTION immutable_workspace_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.object_id, NEW.environment_id, NEW.configuration_id) IS DISTINCT FROM (OLD.object_id, OLD.environment_id, OLD.configuration_id) THEN
        RAISE EXCEPTION 'Environment workspace identity is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER immutable_workspace_identity BEFORE UPDATE ON environment_workspaces
FOR EACH ROW EXECUTE FUNCTION immutable_workspace_identity();

-- +goose Down
LOCK TABLE environment_workspaces, workspace_fs_configurations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_workspaces) THEN
        RAISE EXCEPTION 'Cannot discard durable workspace ownership, including deleted identities';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE environment_workspaces;
DROP TABLE workspace_fs_configurations;
DROP FUNCTION immutable_workspace_identity();
DROP FUNCTION immutable_workspace_configuration();
