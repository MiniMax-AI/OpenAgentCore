-- name: CreateSkill :one
INSERT INTO skills (id, tenant_id, name, description, default_version, latest_version, next_version)
VALUES ($1, $2, $3, $4, 1, 1, 2)
RETURNING *;

-- name: GetSkill :one
SELECT * FROM skills WHERE tenant_id = $1 AND id = $2;

-- name: LockSkill :one
SELECT * FROM skills WHERE tenant_id = $1 AND id = $2 FOR UPDATE;

-- name: ListSkills :many
SELECT * FROM skills
WHERE tenant_id = sqlc.arg(tenant_id)
 AND (sqlc.narg(after_created)::timestamptz IS NULL
 OR (sqlc.arg(ascending)::boolean AND (created_at, id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 OR (NOT sqlc.arg(ascending)::boolean AND (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
 CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
 CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);

-- name: SetDefaultSkillVersion :one
UPDATE skills SET default_version = $3 WHERE tenant_id = $1 AND id = $2 RETURNING *;

-- name: AdvanceSkillVersion :exec
UPDATE skills SET latest_version = next_version, next_version = next_version + 1,
 default_version = CASE WHEN sqlc.arg(make_default)::boolean THEN next_version ELSE default_version END
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: DeleteSkill :one
DELETE FROM skills WHERE tenant_id = $1 AND id = $2 RETURNING id;

-- name: CreateSkillVersion :one
INSERT INTO skill_versions (id, tenant_id, skill_id, version, name, description, contents)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, tenant_id, skill_id, version, name, description, created_at;

-- name: GetSkillVersion :one
SELECT id, tenant_id, skill_id, version, name, description, created_at
FROM skill_versions WHERE tenant_id = $1 AND skill_id = $2 AND version = $3;

-- name: GetSkillVersionByID :one
SELECT id, tenant_id, skill_id, version, name, description, created_at
FROM skill_versions WHERE tenant_id = $1 AND skill_id = $2 AND id = $3;

-- name: ReadSkillVersion :one
SELECT * FROM skill_versions WHERE tenant_id = $1 AND skill_id = $2 AND version = $3;

-- name: ListSkillVersions :many
SELECT id, tenant_id, skill_id, version, name, description, created_at
FROM skill_versions
WHERE tenant_id = sqlc.arg(tenant_id) AND skill_id = sqlc.arg(skill_id)
 AND (sqlc.narg(after_version)::bigint IS NULL
 OR (sqlc.arg(ascending)::boolean AND version > sqlc.narg(after_version)::bigint)
 OR (NOT sqlc.arg(ascending)::boolean AND version < sqlc.narg(after_version)::bigint))
ORDER BY CASE WHEN sqlc.arg(ascending)::boolean THEN version END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::boolean THEN version END DESC
LIMIT sqlc.arg(page_limit);

-- name: DeleteSkillVersion :one
DELETE FROM skill_versions WHERE tenant_id = $1 AND skill_id = $2 AND version = $3
RETURNING id, tenant_id, skill_id, version, name, description, created_at;

-- name: RefreshLatestSkillVersion :exec
UPDATE skills SET latest_version = (SELECT max(v.version) FROM skill_versions v WHERE v.skill_id = skills.id)
WHERE skills.tenant_id = $1 AND skills.id = $2;

-- name: ReadDefaultSkillVersion :one
SELECT v.* FROM skill_versions v JOIN skills s ON s.id = v.skill_id AND s.default_version = v.version
WHERE s.tenant_id = $1 AND s.id = $2;
