-- +goose Up
-- Node readiness diagnostics are Provider-neutral classes. An offline node keeps
-- its last report, so rewrite every stored vendor code to its class.
UPDATE runtime_nodes SET health = jsonb_set(health, '{diagnostic}', to_jsonb(CASE health->>'diagnostic'
        WHEN 'docker_unavailable' THEN 'provider_unavailable'
        WHEN 'microsandbox_artifacts_unavailable' THEN 'artifacts_unavailable'
        ELSE 'host_unsupported' END::text))
WHERE health->>'diagnostic' IN ('docker_unavailable', 'docker_limits_unsupported', 'kvm_unavailable', 'microsandbox_artifacts_unavailable');
UPDATE runtime_node_generation_status SET diagnostic = CASE diagnostic
        WHEN 'docker_unavailable' THEN 'provider_unavailable'
        WHEN 'microsandbox_artifacts_unavailable' THEN 'artifacts_unavailable'
        ELSE 'host_unsupported' END
WHERE diagnostic IN ('docker_unavailable', 'docker_limits_unsupported', 'kvm_unavailable', 'microsandbox_artifacts_unavailable');

-- +goose Down
UPDATE runtime_nodes SET health = jsonb_set(health, '{diagnostic}', '"provider_unavailable"')
WHERE health->>'diagnostic' IN ('host_unsupported', 'artifacts_unavailable');
UPDATE runtime_node_generation_status SET diagnostic = 'provider_unavailable'
WHERE diagnostic IN ('host_unsupported', 'artifacts_unavailable');
