-- +goose Up
-- Persisted specifications and node pins belong to their installation release.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM (
      SELECT specification FROM runtime_deployment
      UNION ALL
      SELECT specification FROM runtime_deployment_generations
    ) retained
    WHERE specification->'runtime' IS NOT NULL
      AND specification->'runtime' <> 'null'::jsonb
      AND (jsonb_typeof(specification->'runtime') <> 'object'
        OR (specification->'runtime') - ARRAY['source_commit', 'artifacts'] <> '{}'::jsonb
        OR COALESCE(jsonb_typeof(specification#>'{runtime,artifacts}'), '') <> 'object')
  ) THEN
    RAISE EXCEPTION 'Cannot change persisted Runtime release specifications; follow docs/getting-started/operations.md#installation-version-policy and preserve this installation';
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Earlier code cannot interpret declared artifact identities.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM (
      SELECT specification FROM runtime_deployment
      UNION ALL
      SELECT specification FROM runtime_deployment_generations
    ) retained
    WHERE specification->'runtime' ? 'artifacts'
  ) THEN
    RAISE EXCEPTION 'Cannot downgrade persisted Runtime artifact specifications; follow docs/getting-started/operations.md#installation-version-policy and preserve this installation';
  END IF;
END $$;
-- +goose StatementEnd
