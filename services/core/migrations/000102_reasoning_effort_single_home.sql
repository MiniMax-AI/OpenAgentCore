-- +goose Up
-- Reasoning effort has one home, reasoning.effort. Move the Harness-native keys there when it is unset, then drop them everywhere they were stored.
UPDATE agents SET configuration = jsonb_set(
    configuration,
    '{reasoning}',
    CASE WHEN jsonb_typeof(configuration -> 'reasoning') = 'object' THEN configuration -> 'reasoning' ELSE '{}'::jsonb END
        || jsonb_build_object('effort', COALESCE(configuration #> '{x_agents_core,harness_config,model_reasoning_effort}', configuration #> '{x_agents_core,harness_config,effort}')))
WHERE COALESCE(configuration #> '{x_agents_core,harness_config,model_reasoning_effort}', configuration #> '{x_agents_core,harness_config,effort}') IS NOT NULL
  AND COALESCE(configuration #> '{reasoning,effort}', 'null'::jsonb) = 'null'::jsonb;

UPDATE agents SET configuration = configuration #- '{x_agents_core,harness_config,model_reasoning_effort}' #- '{x_agents_core,harness_config,effort}'
WHERE configuration #> '{x_agents_core,harness_config}' ?| ARRAY['model_reasoning_effort', 'effort'];

UPDATE sessions SET configuration = jsonb_set(
    configuration,
    '{agent,reasoning}',
    CASE WHEN jsonb_typeof(configuration #> '{agent,reasoning}') = 'object' THEN configuration #> '{agent,reasoning}' ELSE '{}'::jsonb END
        || jsonb_build_object('effort', COALESCE(configuration #> '{agent,x_agents_core,harness_config,model_reasoning_effort}', configuration #> '{agent,x_agents_core,harness_config,effort}')))
WHERE COALESCE(configuration #> '{agent,x_agents_core,harness_config,model_reasoning_effort}', configuration #> '{agent,x_agents_core,harness_config,effort}') IS NOT NULL
  AND COALESCE(configuration #> '{agent,reasoning,effort}', 'null'::jsonb) = 'null'::jsonb;

UPDATE sessions SET configuration = configuration #- '{agent,x_agents_core,harness_config,model_reasoning_effort}' #- '{agent,x_agents_core,harness_config,effort}'
WHERE configuration #> '{agent,x_agents_core,harness_config}' ?| ARRAY['model_reasoning_effort', 'effort'];

UPDATE session_execution_configuration SET configuration = configuration #- '{harness_config,value,model_reasoning_effort}' #- '{harness_config,value,effort}'
WHERE configuration #> '{harness_config,value}' ?| ARRAY['model_reasoning_effort', 'effort'];

UPDATE deployment_model_providers SET harness_config = harness_config - 'model_reasoning_effort' - 'effort'
WHERE harness_config ?| ARRAY['model_reasoning_effort', 'effort'];

-- +goose Down
SELECT 1;
