-- +goose Up
-- One deployment default model provider per harness. The safe columns serve
-- reads without decryption; encrypted_config holds the complete bundle.
CREATE TABLE deployment_model_providers (
  harness text PRIMARY KEY,
  protocol text NOT NULL,
  base_url text NOT NULL,
  context_window integer NOT NULL DEFAULT 0 CHECK (context_window >= 0),
  max_output_tokens integer NOT NULL DEFAULT 0 CHECK (max_output_tokens >= 0),
  encrypted_config bytea NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Deployment-wide administrator writes have no Project.
ALTER TABLE admin_audit_log ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE admin_audit_log ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE admin_audit_log ADD CONSTRAINT admin_audit_log_scope_check CHECK ((tenant_id IS NULL) = (project_id IS NULL));

-- Input reserved before providers were required fails with this reason instead
-- of waiting for its deadline.
ALTER TABLE environment_input_reservations ADD COLUMN failure_code text
  CHECK (failure_code IS NULL OR (state = 'failed' AND failure_code = 'model_provider_required'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM admin_audit_log WHERE project_id IS NULL) THEN
        RAISE EXCEPTION 'Deployment-wide audit entries prevent this downgrade; restore a complete database backup instead.';
    END IF;
    IF EXISTS (SELECT 1 FROM deployment_model_providers) THEN
        RAISE EXCEPTION 'Deployment model providers prevent this downgrade; remove them or restore a complete database backup instead.';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE environment_input_reservations DROP COLUMN failure_code;
ALTER TABLE admin_audit_log DROP CONSTRAINT admin_audit_log_scope_check;
ALTER TABLE admin_audit_log ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE admin_audit_log ALTER COLUMN tenant_id SET NOT NULL;
DROP TABLE deployment_model_providers;
