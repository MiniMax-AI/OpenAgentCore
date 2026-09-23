-- +goose Up
UPDATE skills s
SET name = v.name, description = v.description
FROM skill_versions v
WHERE v.tenant_id = s.tenant_id
  AND v.skill_id = s.id
  AND v.version = s.default_version
  AND (s.name, s.description) IS DISTINCT FROM (v.name, v.description);

-- +goose Down
-- Keep repaired metadata; restoring stale names and descriptions would corrupt it.
SELECT 1;
