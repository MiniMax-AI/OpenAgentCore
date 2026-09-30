-- +goose Up
ALTER TABLE subagent_identities
    ADD COLUMN name text,
    ADD COLUMN instructions text,
    ADD COLUMN public_visible boolean NOT NULL DEFAULT false,
    ADD COLUMN status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    ADD COLUMN closed_at_ms bigint,
    ADD COLUMN lifecycle_at_ms bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT subagent_closed_state CHECK ((status = 'closed') = (closed_at_ms IS NOT NULL)),
    ADD CONSTRAINT subagent_session_identity UNIQUE (session_id, id);

-- Child work is observed from the native owner; it must never enter the Core queue.
CREATE TABLE subagent_turns (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL,
    subagent_id uuid NOT NULL,
    native_id text NOT NULL CHECK (native_id <> ''),
    status text NOT NULL CHECK (status IN ('queued', 'in_progress', 'waiting', 'completed', 'failed', 'cancelled')),
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    token_usage jsonb,
    FOREIGN KEY (session_id, subagent_id) REFERENCES subagent_identities(session_id, id),
    UNIQUE (subagent_id, native_id),
    UNIQUE (session_id, subagent_id, id),
    CHECK ((completed_at IS NOT NULL) = (status IN ('completed', 'failed', 'cancelled')))
);
CREATE TABLE subagent_items (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL,
    subagent_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    output_index integer,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    FOREIGN KEY (session_id, subagent_id, turn_id) REFERENCES subagent_turns(session_id, subagent_id, id),
    UNIQUE (turn_id, position)
);
CREATE TABLE subagent_effects (
    session_id uuid NOT NULL REFERENCES sessions(id),
    effect_id text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    PRIMARY KEY (session_id, effect_id)
);

CREATE VIEW public_execution_turns AS
 SELECT id, session_id, status, created_at, started_at, completed_at, cancel_requested_at, outcome,
        token_usage, artifact_capture_started, NULL::uuid AS subagent_id FROM turns
 UNION ALL
 SELECT id, session_id, status, created_at, started_at, completed_at, NULL::timestamptz,
        '{}'::jsonb, token_usage, false, subagent_id FROM subagent_turns;

-- +goose Down
DROP VIEW public_execution_turns;
DROP TABLE subagent_effects;
DROP TABLE subagent_items;
DROP TABLE subagent_turns;
ALTER TABLE subagent_identities
    DROP CONSTRAINT subagent_session_identity,
    DROP CONSTRAINT subagent_closed_state,
    DROP COLUMN lifecycle_at_ms,
    DROP COLUMN closed_at_ms,
    DROP COLUMN status,
    DROP COLUMN public_visible,
    DROP COLUMN instructions,
    DROP COLUMN name;
