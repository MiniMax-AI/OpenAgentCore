-- +goose Up
ALTER TABLE environment_templates
    DROP CONSTRAINT environment_templates_network_access_check,
    ADD COLUMN network_allowed_domains text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT environment_templates_network_access_check
        CHECK (network_access IN ('enabled', 'disabled', 'restricted')),
    ADD CONSTRAINT environment_templates_network_domains_check
        CHECK ((network_access = 'restricted' AND cardinality(network_allowed_domains) BETWEEN 1 AND 100)
            OR (network_access IN ('enabled', 'disabled') AND cardinality(network_allowed_domains) = 0));

-- +goose Down
-- Refuse rollback while restricted templates exist instead of broadening authority.
ALTER TABLE environment_templates
    ADD CONSTRAINT environment_templates_legacy_network_check
        CHECK (network_access IN ('enabled', 'disabled'));
ALTER TABLE environment_templates
    DROP CONSTRAINT environment_templates_network_domains_check,
    DROP CONSTRAINT environment_templates_network_access_check,
    DROP COLUMN network_allowed_domains;
ALTER TABLE environment_templates
    RENAME CONSTRAINT environment_templates_legacy_network_check TO environment_templates_network_access_check;
