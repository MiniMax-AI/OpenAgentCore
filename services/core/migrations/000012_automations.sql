-- +goose Up
CREATE TABLE automations (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    type text NOT NULL CHECK (type IN ('periodic', 'once')),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 256),
    metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata) = 'object'),
    status text NOT NULL CHECK (status IN ('active', 'paused', 'completed')),
    run_at timestamptz,
    expression text,
    timezone text,
    next_run_at timestamptz,
    target jsonb NOT NULL,
    target_contents bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    CHECK ((type = 'once') = (run_at IS NOT NULL)),
    CHECK ((type = 'periodic') = (expression IS NOT NULL AND timezone IS NOT NULL)),
    CHECK (status <> 'completed' OR type = 'once')
);
CREATE INDEX automations_page ON automations (tenant_id, created_at, id);
CREATE INDEX automations_target_session ON automations (tenant_id, (target->>'session_id')) WHERE target->>'type' = 'existing_session';
CREATE INDEX automations_due ON automations (next_run_at) WHERE status = 'active';

CREATE TABLE automation_invocations (
    id uuid PRIMARY KEY,
    automation_id uuid NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    scheduled_at timestamptz,
    automation_snapshot jsonb,
    status text NOT NULL CHECK (status IN ('queued', 'completed', 'failed', 'skipped')),
    skip_reason text CHECK (skip_reason = 'pending_delivery'),
    session_id uuid,
    error_code text,
    error_message text,
    delivery bytea,
    UNIQUE (automation_id, scheduled_at),
    CHECK ((scheduled_at IS NULL) = (automation_snapshot IS NULL)),
    CHECK ((status = 'skipped') = (skip_reason IS NOT NULL)),
    CHECK ((status = 'failed') = (error_code IS NOT NULL AND error_message IS NOT NULL)),
    CHECK ((status = 'queued') = (delivery IS NOT NULL))
);
CREATE INDEX automation_invocations_page ON automation_invocations (automation_id, created_at, id);
CREATE INDEX automation_invocations_queued ON automation_invocations (automation_id, created_at) WHERE status = 'queued';
