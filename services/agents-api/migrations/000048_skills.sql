-- +goose Up
CREATE TABLE skills (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    default_version bigint NOT NULL CHECK (default_version > 0),
    latest_version bigint NOT NULL CHECK (latest_version > 0),
    next_version bigint NOT NULL CHECK (next_version > latest_version),
    UNIQUE (tenant_id, id)
);
CREATE INDEX skills_tenant_created ON skills (tenant_id, created_at, id);

CREATE TABLE skill_versions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    skill_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    name text NOT NULL,
    description text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    contents bytea NOT NULL,
    UNIQUE (skill_id, version),
    FOREIGN KEY (tenant_id, skill_id) REFERENCES skills (tenant_id, id) ON DELETE CASCADE
);
ALTER TABLE skills ADD CONSTRAINT skills_default_version
 FOREIGN KEY (id, default_version) REFERENCES skill_versions (skill_id, version)
 DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE skills ADD CONSTRAINT skills_latest_version
 FOREIGN KEY (id, latest_version) REFERENCES skill_versions (skill_id, version)
 DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE skills DROP CONSTRAINT skills_default_version, DROP CONSTRAINT skills_latest_version;
DROP TABLE skill_versions;
DROP TABLE skills;
