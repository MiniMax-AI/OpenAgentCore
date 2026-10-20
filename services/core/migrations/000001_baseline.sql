-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION immutable_runtime_deployment_generation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deployment_generation IS DISTINCT FROM OLD.deployment_generation THEN
        RAISE EXCEPTION 'Sandbox ownership generation is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION immutable_sandbox_specification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Retained sandbox specifications are immutable';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION immutable_workspace_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.adapter, NEW.parameters, NEW.credential) IS DISTINCT FROM (OLD.id, OLD.adapter, OLD.parameters, OLD.credential) THEN
        RAISE EXCEPTION 'Workspace filesystem configuration is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION immutable_workspace_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.object_id, NEW.environment_id, NEW.configuration_id, NEW.capacity_mib) IS DISTINCT FROM (OLD.object_id, OLD.environment_id, OLD.configuration_id, OLD.capacity_mib) THEN
        RAISE EXCEPTION 'Environment workspace identity is immutable';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TABLE admin_audit_log (
    id uuid NOT NULL,
    tenant_id uuid,
    project_id uuid,
    admin_credential_id text NOT NULL,
    actor_label text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    request_id text NOT NULL,
    trace_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT admin_audit_log_scope_check CHECK (((tenant_id IS NULL) = (project_id IS NULL)))
);

CREATE TABLE agent_model_execution (
    agent_id uuid NOT NULL,
    encrypted_config bytea NOT NULL
);

CREATE TABLE agents (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    metadata jsonb NOT NULL,
    configuration jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    CONSTRAINT agents_configuration_check CHECK ((jsonb_typeof(configuration) = 'object'::text)),
    CONSTRAINT agents_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text))
);

CREATE TABLE deployment_model_providers (
    harness text NOT NULL,
    protocol text NOT NULL,
    base_url text NOT NULL,
    context_window integer DEFAULT 0 NOT NULL,
    max_output_tokens integer DEFAULT 0 NOT NULL,
    encrypted_config bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    revision uuid DEFAULT gen_random_uuid() NOT NULL,
    last_used_at timestamp with time zone,
    last_error_code text,
    last_error_at timestamp with time zone,
    recovery_pending boolean DEFAULT false NOT NULL,
    model text DEFAULT ''::text NOT NULL,
    harness_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT deployment_model_providers_context_window_check CHECK ((context_window >= 0)),
    CONSTRAINT deployment_model_providers_max_output_tokens_check CHECK ((max_output_tokens >= 0)),
    CONSTRAINT deployment_provider_error_code CHECK ((last_error_code = ANY (ARRAY['authentication_error'::text, 'connection_failed'::text, 'rate_limit_exceeded'::text, 'usage_limit_exceeded'::text, 'server_overloaded'::text, 'server_error'::text, 'resource_not_found'::text, 'request_timeout'::text, 'invalid_request'::text]))),
    CONSTRAINT deployment_provider_error_pair CHECK (((last_error_code IS NULL) = (last_error_at IS NULL))),
    CONSTRAINT deployment_provider_recovery CHECK (((NOT recovery_pending) OR (last_error_at IS NOT NULL)))
);

CREATE TABLE devices (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    credential_hash text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    last_seen_at timestamp with time zone,
    revoked_at timestamp with time zone,
    environment_id uuid,
    executor_key_id uuid,
    archive_cancel_turn_id uuid,
    supported_agent_kinds jsonb DEFAULT '[]'::jsonb NOT NULL,
    CONSTRAINT device_credential_source CHECK ((((executor_key_id IS NULL) AND (credential_hash IS NOT NULL)) OR ((executor_key_id IS NOT NULL) AND (credential_hash IS NULL) AND (environment_id IS NOT NULL)))),
    CONSTRAINT devices_credential_hash_check CHECK ((credential_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT devices_supported_agent_kinds_check CHECK ((jsonb_typeof(supported_agent_kinds) = 'array'::text))
);

CREATE TABLE environment_connections (
    environment_id uuid NOT NULL,
    generation uuid NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    CONSTRAINT environment_connections_revision_check CHECK ((revision >= 0))
);

CREATE TABLE environment_executor_credentials (
    environment_id uuid,
    token_sha256 text NOT NULL,
    issued_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    revoked_at timestamp with time zone,
    key_id uuid NOT NULL,
    tenant_id uuid,
    subject_kind text,
    subject_id text,
    created_at timestamp with time zone,
    CONSTRAINT environment_executor_credentials_token_sha256_check CHECK ((token_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT executor_principal_complete CHECK ((((tenant_id IS NULL) AND (subject_kind IS NULL) AND (subject_id IS NULL) AND (environment_id IS NOT NULL) AND (revoked_at IS NOT NULL) AND (created_at IS NULL)) OR ((tenant_id IS NOT NULL) AND (subject_kind IS NOT NULL) AND (subject_id IS NOT NULL) AND (subject_kind = ANY (ARRAY['user'::text, 'service_account'::text])) AND (subject_id <> ''::text) AND (created_at IS NOT NULL))))
);

CREATE TABLE environment_file_writes (
    id uuid NOT NULL,
    environment_id uuid NOT NULL,
    device_id uuid NOT NULL,
    request_sha256 text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    settled_at timestamp with time zone,
    audit_source jsonb,
    CONSTRAINT environment_file_write_audit_source_object CHECK (((audit_source IS NULL) OR (jsonb_typeof(audit_source) = 'object'::text))),
    CONSTRAINT environment_file_writes_check CHECK (((state = 'pending'::text) = (settled_at IS NULL))),
    CONSTRAINT environment_file_writes_request_sha256_check CHECK ((request_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT environment_file_writes_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'committed'::text, 'rejected'::text])))
);

CREATE TABLE environment_input_reservations (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    batch jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    deadline timestamp with time zone NOT NULL,
    settled_at timestamp with time zone,
    is_initial boolean DEFAULT false NOT NULL,
    failure_code text,
    CONSTRAINT environment_input_reservations_batch_check CHECK (
CASE
    WHEN (jsonb_typeof(batch) = 'array'::text) THEN ((jsonb_array_length(batch) >= 1) AND (jsonb_array_length(batch) <= 64))
    ELSE false
END),
    CONSTRAINT environment_input_reservations_check CHECK (((settled_at IS NOT NULL) = (state <> 'pending'::text))),
    CONSTRAINT environment_input_reservations_check1 CHECK (((failure_code IS NULL) OR ((state = 'failed'::text) AND (failure_code = ANY (ARRAY['model_provider_required'::text, 'runtime_preparation_failed'::text]))))),
    CONSTRAINT environment_input_reservations_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'admitted'::text, 'expired'::text, 'cancelled'::text, 'failed'::text])))
);

CREATE TABLE environment_setups (
    session_id uuid NOT NULL,
    contents bytea NOT NULL
);

CREATE TABLE environment_templates (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    name text,
    network_access text NOT NULL,
    created_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    files jsonb DEFAULT '[]'::jsonb NOT NULL,
    file_contents bytea,
    packages jsonb DEFAULT '{"npm": [], "python": [], "system": []}'::jsonb NOT NULL,
    env_contents bytea,
    setup_contents bytea,
    skills jsonb DEFAULT '[]'::jsonb NOT NULL,
    skill_contents bytea,
    network_allowed_domains text[] DEFAULT '{}'::text[] NOT NULL,
    plugins jsonb DEFAULT '[]'::jsonb NOT NULL,
    plugin_contents bytea,
    capability_directories text[] DEFAULT '{}'::text[] NOT NULL,
    CONSTRAINT environment_templates_files_check CHECK ((jsonb_typeof(files) = 'array'::text)),
    CONSTRAINT environment_templates_name_check CHECK (((name IS NULL) OR ((char_length(name) >= 1) AND (char_length(name) <= 256)))),
    CONSTRAINT environment_templates_network_access_check CHECK ((network_access = ANY (ARRAY['enabled'::text, 'disabled'::text, 'restricted'::text]))),
    CONSTRAINT environment_templates_network_domains_check CHECK ((((network_access = 'restricted'::text) AND ((cardinality(network_allowed_domains) >= 1) AND (cardinality(network_allowed_domains) <= 100))) OR ((network_access = ANY (ARRAY['enabled'::text, 'disabled'::text])) AND (cardinality(network_allowed_domains) = 0)))),
    CONSTRAINT environment_templates_packages_check CHECK ((jsonb_typeof(packages) = 'object'::text))
);

CREATE TABLE environment_workspaces (
    object_id uuid NOT NULL,
    environment_id uuid NOT NULL,
    configuration_id uuid NOT NULL,
    state text DEFAULT 'creating'::text NOT NULL,
    attachment jsonb,
    capacity_mib bigint DEFAULT 0 NOT NULL,
    CONSTRAINT environment_workspaces_attachment_check CHECK (((attachment IS NULL) OR (jsonb_typeof(attachment) = 'object'::text))),
    CONSTRAINT environment_workspaces_capacity_mib_check CHECK (((capacity_mib >= 0) AND (capacity_mib <= '4294967295'::bigint))),
    CONSTRAINT environment_workspaces_check CHECK (((state <> 'ready'::text) OR (attachment IS NOT NULL))),
    CONSTRAINT environment_workspaces_state_check CHECK ((state = ANY (ARRAY['creating'::text, 'ready'::text, 'deleting'::text, 'deleted'::text])))
);

CREATE TABLE environments (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    failure_reason text,
    failed_at timestamp with time zone,
    failure_detail jsonb,
    initialization text DEFAULT 'complete'::text NOT NULL,
    CONSTRAINT environment_failure_recorded CHECK ((((failure_reason IS NULL) = (failed_at IS NULL)) AND ((failed_at IS NULL) OR (status = 'failed'::text)) AND (char_length(failure_reason) <= 256))),
    CONSTRAINT environments_failure_detail_check CHECK (((failure_detail IS NULL) OR (jsonb_typeof(failure_detail) = 'object'::text))),
    CONSTRAINT environments_initialization_check CHECK ((initialization = ANY (ARRAY['pending'::text, 'running'::text, 'complete'::text, 'failed'::text]))),
    CONSTRAINT environments_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'connected'::text, 'disconnected'::text, 'expired'::text, 'failed'::text])))
);

CREATE TABLE execution_project_scopes (
    tenant_id uuid NOT NULL,
    organization_id text NOT NULL,
    project_id text NOT NULL,
    CONSTRAINT execution_project_scopes_organization_id_check CHECK ((organization_id <> ''::text)),
    CONSTRAINT execution_project_scopes_project_id_check CHECK ((project_id <> ''::text)),
    CONSTRAINT execution_project_scopes_tenant_id_check CHECK ((tenant_id <> '00000000-0000-0000-0000-000000000000'::uuid))
);

CREATE TABLE function_calls (
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    call_id text NOT NULL,
    executor_call_id text NOT NULL,
    name text NOT NULL,
    arguments jsonb NOT NULL,
    result jsonb,
    applied boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    withdrawn boolean DEFAULT false NOT NULL,
    CONSTRAINT function_calls_check CHECK (((NOT applied) OR (result IS NOT NULL))),
    CONSTRAINT function_calls_not_applied_and_withdrawn CHECK ((NOT (applied AND withdrawn))),
    CONSTRAINT function_calls_result_check CHECK (((result IS NULL) OR (jsonb_typeof(result) = 'object'::text)))
);

CREATE TABLE initial_environment_files (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    "position" integer NOT NULL,
    path text NOT NULL,
    size_bytes bigint NOT NULL,
    contents bytea NOT NULL,
    CONSTRAINT initial_environment_files_position_check CHECK ((("position" >= 0) AND ("position" <= 49))),
    CONSTRAINT initial_environment_files_size_bytes_check CHECK (((size_bytes >= 0) AND (size_bytes <= 52428800)))
);

CREATE TABLE node_host_history_samples (
    node_id uuid NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    cpu_utilization double precision,
    memory_used_bytes bigint,
    available_disk_bytes bigint,
    CONSTRAINT node_host_history_samples_available_disk_bytes_check CHECK ((available_disk_bytes >= 0)),
    CONSTRAINT node_host_history_samples_cpu_utilization_check CHECK (((cpu_utilization >= (0)::double precision) AND (cpu_utilization <= (1)::double precision))),
    CONSTRAINT node_host_history_samples_memory_used_bytes_check CHECK ((memory_used_bytes >= 0))
);

CREATE TABLE project_api_keys (
    id uuid NOT NULL,
    name text NOT NULL,
    prefix text NOT NULL,
    token_sha256 text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    project_id uuid NOT NULL,
    CONSTRAINT project_api_keys_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 80))),
    CONSTRAINT project_api_keys_token_sha256_check CHECK ((token_sha256 ~ '^[0-9a-f]{64}$'::text))
);

CREATE TABLE projects (
    id uuid NOT NULL,
    name text NOT NULL,
    tenant_id uuid NOT NULL,
    subject_kind text NOT NULL,
    subject_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone,
    CONSTRAINT projects_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 128))),
    CONSTRAINT projects_subject_kind_check CHECK ((subject_kind = 'service_account'::text))
);

CREATE TABLE subagent_turns (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    subagent_id uuid NOT NULL,
    native_id text NOT NULL,
    status text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    token_usage jsonb,
    CONSTRAINT subagent_turns_check CHECK (((completed_at IS NOT NULL) = (status = ANY (ARRAY['completed'::text, 'failed'::text, 'cancelled'::text])))),
    CONSTRAINT subagent_turns_native_id_check CHECK ((native_id <> ''::text)),
    CONSTRAINT subagent_turns_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'in_progress'::text, 'waiting'::text, 'completed'::text, 'failed'::text, 'cancelled'::text])))
);

CREATE TABLE turns (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    cancel_requested_at timestamp with time zone,
    outcome jsonb DEFAULT '{}'::jsonb NOT NULL,
    event_count integer DEFAULT 0 NOT NULL,
    event_bytes bigint DEFAULT 0 NOT NULL,
    token_usage jsonb,
    artifact_capture_started boolean DEFAULT false NOT NULL,
    CONSTRAINT turns_check CHECK (((completed_at IS NOT NULL) = (status = ANY (ARRAY['completed'::text, 'failed'::text, 'cancelled'::text])))),
    CONSTRAINT turns_outcome_check CHECK ((jsonb_typeof(outcome) = 'object'::text)),
    CONSTRAINT turns_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'in_progress'::text, 'waiting'::text, 'completed'::text, 'failed'::text, 'cancelled'::text])))
);

CREATE VIEW public_execution_turns AS
 SELECT turns.id,
    turns.session_id,
    turns.status,
    turns.created_at,
    turns.started_at,
    turns.completed_at,
    turns.cancel_requested_at,
    turns.outcome,
    turns.token_usage,
    turns.artifact_capture_started,
    NULL::uuid AS subagent_id
   FROM turns
UNION ALL
 SELECT subagent_turns.id,
    subagent_turns.session_id,
    subagent_turns.status,
    subagent_turns.created_at,
    subagent_turns.started_at,
    subagent_turns.completed_at,
    NULL::timestamp with time zone AS cancel_requested_at,
    '{}'::jsonb AS outcome,
    subagent_turns.token_usage,
    false AS artifact_capture_started,
    subagent_turns.subagent_id
   FROM subagent_turns;

CREATE TABLE runtime_allocations (
    id uuid NOT NULL,
    environment_id uuid NOT NULL,
    device_id uuid NOT NULL,
    provider_key uuid NOT NULL,
    state text DEFAULT 'creating'::text NOT NULL,
    create_settled boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    released_at timestamp with time zone,
    compute_phase text DEFAULT 'disabled'::text NOT NULL,
    compute_revision bigint DEFAULT 0 NOT NULL,
    compute_state jsonb DEFAULT '{}'::jsonb NOT NULL,
    compute_activity_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    compute_wake_requested boolean DEFAULT false NOT NULL,
    compute_retained_until timestamp with time zone,
    node_id uuid,
    observation_error text DEFAULT ''::text NOT NULL,
    compute_phase_changed_at timestamp with time zone DEFAULT clock_timestamp(),
    deployment_generation bigint,
    CONSTRAINT runtime_allocations_check CHECK (((state = 'released'::text) = (released_at IS NOT NULL))),
    CONSTRAINT runtime_allocations_check1 CHECK (((state <> 'released'::text) OR create_settled)),
    CONSTRAINT runtime_allocations_check2 CHECK (((state = 'released'::text) OR (deployment_generation IS NOT NULL))),
    CONSTRAINT runtime_allocations_compute_phase_check CHECK ((compute_phase = ANY (ARRAY['disabled'::text, 'running'::text, 'quiescing'::text, 'suspending'::text, 'suspended'::text, 'restoring'::text, 'waking'::text]))),
    CONSTRAINT runtime_allocations_compute_revision_check CHECK ((compute_revision >= 0)),
    CONSTRAINT runtime_allocations_compute_state_check CHECK ((jsonb_typeof(compute_state) = 'object'::text)),
    CONSTRAINT runtime_allocations_deployment_generation_check CHECK ((deployment_generation >= 0)),
    CONSTRAINT runtime_allocations_observation_error_check CHECK ((observation_error = ANY (ARRAY[''::text, 'node_unavailable'::text, 'resource_missing'::text, 'compute_unconfirmed'::text, 'ownership_mismatch'::text, 'provider_unavailable'::text]))),
    CONSTRAINT runtime_allocations_state_check CHECK ((state = ANY (ARRAY['creating'::text, 'running'::text, 'cleanup_pending'::text, 'released'::text])))
);

CREATE TABLE runtime_deployment (
    singleton boolean DEFAULT true NOT NULL,
    installation_id uuid,
    backend_fingerprint text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    provider_kind text DEFAULT ''::text NOT NULL,
    owner_epoch bigint DEFAULT 0 NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    mode text DEFAULT ''::text NOT NULL,
    provider_credential bytea,
    specification jsonb DEFAULT '{}'::jsonb NOT NULL,
    reset_clear text,
    reset_requested_at timestamp with time zone,
    reset_deadline_at timestamp with time zone,
    reset_forced_at timestamp with time zone,
    reset_audit jsonb,
    provider_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    provider_metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    idle_seconds bigint DEFAULT 300 NOT NULL,
    CONSTRAINT runtime_deployment_generation_check CHECK ((generation >= 0)),
    CONSTRAINT runtime_deployment_identity_check CHECK ((((installation_id IS NULL) AND (backend_fingerprint = ''::text)) OR ((installation_id IS NOT NULL) AND (backend_fingerprint ~ '^[0-9a-f]{64}$'::text)) OR ((installation_id IS NOT NULL) AND (provider_kind = ''::text) AND (backend_fingerprint = ''::text)))),
    CONSTRAINT runtime_deployment_idle_seconds_check CHECK (((idle_seconds >= 30) AND (idle_seconds <= 86400))),
    CONSTRAINT runtime_deployment_mode_check CHECK ((mode = ANY (ARRAY[''::text, 'nodes'::text, 'direct'::text]))),
    CONSTRAINT runtime_deployment_owner_epoch_check CHECK ((owner_epoch >= 0)),
    CONSTRAINT runtime_deployment_provider_config_check CHECK ((jsonb_typeof(provider_config) = 'object'::text)),
    CONSTRAINT runtime_deployment_provider_kind_check CHECK (((provider_kind = ''::text) OR (provider_kind ~ '^[a-z][a-z0-9_-]{0,63}$'::text))),
    CONSTRAINT runtime_deployment_provider_metadata_check CHECK ((jsonb_typeof(provider_metadata) = 'object'::text)),
    CONSTRAINT runtime_deployment_reset_check CHECK ((((reset_clear IS NULL) AND (reset_requested_at IS NULL) AND (reset_deadline_at IS NULL) AND (reset_forced_at IS NULL) AND (reset_audit IS NULL)) OR ((reset_clear IS NOT NULL) AND (installation_id IS NOT NULL) AND (provider_kind <> ''::text) AND (reset_requested_at IS NOT NULL) AND (reset_audit IS NOT NULL) AND (jsonb_typeof(reset_audit) = 'object'::text) AND (((reset_clear = 'auto'::text) AND (reset_deadline_at IS NOT NULL) AND (reset_forced_at IS NULL)) OR ((reset_clear = 'force'::text) AND (reset_forced_at IS NOT NULL)))))),
    CONSTRAINT runtime_deployment_setup_check CHECK (((installation_id IS NULL) OR ((provider_kind = ''::text) AND (mode = ''::text) AND (generation >= 0)) OR ((provider_kind <> ''::text) AND (mode = ANY (ARRAY['nodes'::text, 'direct'::text])) AND (generation > 0)))),
    CONSTRAINT runtime_deployment_singleton_check CHECK (singleton)
);

CREATE TABLE runtime_deployment_generations (
    generation bigint NOT NULL,
    provider_kind text NOT NULL,
    specification jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    provider_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    provider_metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT runtime_deployment_generations_generation_check CHECK ((generation >= 0)),
    CONSTRAINT runtime_deployment_generations_provider_config_check CHECK ((jsonb_typeof(provider_config) = 'object'::text)),
    CONSTRAINT runtime_deployment_generations_provider_kind_check CHECK ((provider_kind ~ '^[a-z][a-z0-9_-]{0,63}$'::text)),
    CONSTRAINT runtime_deployment_generations_provider_metadata_check CHECK ((jsonb_typeof(provider_metadata) = 'object'::text))
);

CREATE TABLE sessions (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    engine text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    configuration jsonb DEFAULT '{}'::jsonb NOT NULL,
    event_sequence bigint DEFAULT 0 NOT NULL,
    creation_request_hash text,
    deleted_at timestamp with time zone,
    creator_kind text,
    creator_id text,
    CONSTRAINT sessions_configuration_check CHECK ((jsonb_typeof(configuration) = 'object'::text)),
    CONSTRAINT sessions_creation_request_hash_check CHECK ((creation_request_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT sessions_creator_complete CHECK ((((creator_kind IS NULL) AND (creator_id IS NULL)) OR ((creator_kind IS NOT NULL) AND (creator_id IS NOT NULL) AND (creator_kind = ANY (ARRAY['user'::text, 'service_account'::text])) AND (creator_id <> ''::text)))),
    CONSTRAINT sessions_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text))
);

CREATE VIEW runtime_device_authority AS
 SELECT d.id,
    d.tenant_id,
    d.name,
    d.environment_id,
    COALESCE(c.token_sha256, d.credential_hash) AS credential_hash
   FROM (((devices d
     LEFT JOIN environments e ON ((e.id = d.environment_id)))
     LEFT JOIN sessions s ON (((s.id = e.session_id) AND (s.tenant_id = d.tenant_id))))
     LEFT JOIN environment_executor_credentials c ON ((c.key_id = d.executor_key_id)))
  WHERE ((d.revoked_at IS NULL) AND ((d.environment_id IS NULL) OR ((s.id IS NOT NULL) AND (s.deleted_at IS NULL))) AND ((d.executor_key_id IS NULL) OR ((c.revoked_at IS NULL) AND (c.tenant_id = s.tenant_id) AND (c.subject_kind = s.creator_kind) AND (c.subject_id = s.creator_id) AND ((c.environment_id IS NULL) OR (c.environment_id = e.id)) AND (((s.configuration -> 'environment'::text) ->> 'type'::text) = 'self_hosted'::text) AND (e.status <> ALL (ARRAY['failed'::text, 'expired'::text])) AND (EXISTS ( SELECT 1
           FROM execution_project_scopes p
          WHERE (p.tenant_id = c.tenant_id))))));

CREATE TABLE runtime_history_samples (
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL,
    environment_id uuid NOT NULL,
    resolved_at_ns bigint NOT NULL,
    allocation_id uuid,
    provider_type text NOT NULL,
    status text NOT NULL,
    observed_at_ns bigint,
    started_at_ns bigint,
    cpu_usage_seconds double precision,
    cpu_capacity_cores double precision,
    memory_usage_bytes bigint,
    memory_limit_bytes bigint,
    input_tokens bigint,
    output_tokens bigint,
    cpu_utilization_ratio double precision,
    CONSTRAINT runtime_history_samples_check CHECK (((input_tokens IS NULL) = (output_tokens IS NULL))),
    CONSTRAINT runtime_history_samples_check1 CHECK (((observed_at_ns >= 0) AND (observed_at_ns <= resolved_at_ns))),
    CONSTRAINT runtime_history_samples_check2 CHECK (((started_at_ns >= 0) AND (started_at_ns <= observed_at_ns))),
    CONSTRAINT runtime_history_samples_check3 CHECK (((status = 'observed'::text) = (observed_at_ns IS NOT NULL))),
    CONSTRAINT runtime_history_samples_check4 CHECK (((started_at_ns IS NULL) OR (observed_at_ns IS NOT NULL))),
    CONSTRAINT runtime_history_samples_check5 CHECK (((started_at_ns IS NOT NULL) OR ((cpu_usage_seconds IS NULL) AND (cpu_capacity_cores IS NULL) AND (memory_usage_bytes IS NULL) AND (memory_limit_bytes IS NULL)))),
    CONSTRAINT runtime_history_samples_cpu_capacity_cores_check CHECK (((cpu_capacity_cores > (0)::double precision) AND (cpu_capacity_cores < 'Infinity'::double precision))),
    CONSTRAINT runtime_history_samples_cpu_usage_seconds_check CHECK (((cpu_usage_seconds >= (0)::double precision) AND (cpu_usage_seconds < 'Infinity'::double precision))),
    CONSTRAINT runtime_history_samples_cpu_utilization_incarnation CHECK (((started_at_ns IS NOT NULL) OR (cpu_utilization_ratio IS NULL))),
    CONSTRAINT runtime_history_samples_cpu_utilization_ratio_check CHECK (((cpu_utilization_ratio >= (0)::double precision) AND (cpu_utilization_ratio < 'Infinity'::double precision))),
    CONSTRAINT runtime_history_samples_input_tokens_check CHECK (((input_tokens >= 0) AND (input_tokens <= '9007199254740991'::bigint))),
    CONSTRAINT runtime_history_samples_memory_limit_bytes_check CHECK (((memory_limit_bytes > 0) AND (memory_limit_bytes <= '9007199254740991'::bigint))),
    CONSTRAINT runtime_history_samples_memory_usage_bytes_check CHECK (((memory_usage_bytes >= 0) AND (memory_usage_bytes <= '9007199254740991'::bigint))),
    CONSTRAINT runtime_history_samples_output_tokens_check CHECK (((output_tokens >= 0) AND (output_tokens <= '9007199254740991'::bigint))),
    CONSTRAINT runtime_history_samples_resolved_at_ns_check CHECK ((resolved_at_ns >= 0)),
    CONSTRAINT runtime_history_samples_status_check CHECK ((status = ANY (ARRAY['observed'::text, 'unavailable'::text, 'unsupported'::text])))
);

CREATE TABLE runtime_node_enrollments (
    token_sha256 text NOT NULL,
    installation_id uuid NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    node_id uuid,
    max_active integer DEFAULT 2 NOT NULL,
    max_retained integer DEFAULT 8 NOT NULL,
    id uuid,
    CONSTRAINT runtime_enrollment_capacity CHECK (((max_active >= 1) AND (max_retained >= max_active) AND (max_retained <= 1000000))),
    CONSTRAINT runtime_node_enrollments_token_sha256_check CHECK ((token_sha256 ~ '^[0-9a-f]{64}$'::text))
);

CREATE TABLE runtime_node_generation_status (
    node_id uuid NOT NULL,
    generation bigint NOT NULL,
    specification_digest text NOT NULL,
    connection_id uuid NOT NULL,
    owner_epoch bigint NOT NULL,
    state text NOT NULL,
    diagnostic text DEFAULT ''::text NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    checkpoint jsonb,
    CONSTRAINT runtime_node_generation_status_checkpoint_check CHECK (((checkpoint IS NULL) OR (jsonb_typeof(checkpoint) = 'object'::text))),
    CONSTRAINT runtime_node_generation_status_generation_check CHECK ((generation > 0)),
    CONSTRAINT runtime_node_generation_status_owner_epoch_check CHECK ((owner_epoch > 0)),
    CONSTRAINT runtime_node_generation_status_specification_digest_check CHECK ((specification_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT runtime_node_generation_status_state_check CHECK ((state = ANY (ARRAY['ready'::text, 'preparing'::text, 'failed'::text])))
);

CREATE TABLE runtime_nodes (
    id uuid NOT NULL,
    installation_id uuid NOT NULL,
    name text NOT NULL,
    backend_fingerprint text NOT NULL,
    credential_sha256 text NOT NULL,
    max_active integer NOT NULL,
    max_retained integer NOT NULL,
    connection_id uuid,
    provider_ready boolean DEFAULT false NOT NULL,
    health jsonb DEFAULT '{}'::jsonb NOT NULL,
    connected_epoch bigint DEFAULT 0 NOT NULL,
    last_seen_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    removed_at timestamp with time zone,
    specification_digest text DEFAULT ''::text NOT NULL,
    deployment_generation bigint DEFAULT 0 NOT NULL,
    core_url text DEFAULT ''::text NOT NULL,
    enrollment_id uuid,
    ready_generation bigint,
    protocol_version integer DEFAULT 1 NOT NULL,
    CONSTRAINT runtime_node_deployment_generation CHECK ((deployment_generation >= 0)),
    CONSTRAINT runtime_node_specification_digest CHECK (((specification_digest = ''::text) OR (specification_digest ~ '^[0-9a-f]{64}$'::text))),
    CONSTRAINT runtime_nodes_backend_fingerprint_check CHECK ((backend_fingerprint ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT runtime_nodes_check CHECK ((max_retained >= max_active)),
    CONSTRAINT runtime_nodes_credential_sha256_check CHECK ((credential_sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT runtime_nodes_max_active_check CHECK ((max_active > 0)),
    CONSTRAINT runtime_nodes_name_check CHECK (((length(name) >= 1) AND (length(name) <= 128))),
    CONSTRAINT runtime_nodes_protocol_version_check CHECK ((protocol_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT runtime_nodes_ready_generation_check CHECK ((ready_generation >= 0))
);

CREATE TABLE runtime_placements (
    environment_id uuid NOT NULL,
    node_id uuid NOT NULL,
    reserved_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    released_at timestamp with time zone,
    deployment_generation bigint,
    CONSTRAINT runtime_placements_check CHECK (((released_at IS NOT NULL) OR (deployment_generation IS NOT NULL))),
    CONSTRAINT runtime_placements_deployment_generation_check CHECK ((deployment_generation >= 0))
);

CREATE VIEW runtime_reset_retained_environments AS
 SELECT e.id AS environment_id,
    s.id AS session_id,
    s.tenant_id,
    a.deployment_generation
   FROM ((((environments e
     JOIN sessions s ON ((s.id = e.session_id)))
     JOIN environment_workspaces w ON (((w.environment_id = e.id) AND (w.state = 'ready'::text))))
     JOIN LATERAL ( SELECT allocation.id,
            allocation.environment_id,
            allocation.device_id,
            allocation.provider_key,
            allocation.state,
            allocation.create_settled,
            allocation.created_at,
            allocation.released_at,
            allocation.compute_phase,
            allocation.compute_revision,
            allocation.compute_state,
            allocation.compute_activity_at,
            allocation.compute_wake_requested,
            allocation.compute_retained_until,
            allocation.node_id,
            allocation.observation_error,
            allocation.compute_phase_changed_at,
            allocation.deployment_generation
           FROM runtime_allocations allocation
          WHERE (allocation.environment_id = e.id)
          ORDER BY allocation.created_at DESC, allocation.id DESC
         LIMIT 1) a ON (true))
     CROSS JOIN runtime_deployment d)
  WHERE ((d.reset_clear IS NOT NULL) AND (d.reset_requested_at IS NOT NULL) AND (s.deleted_at IS NULL) AND (s.created_at <= d.reset_requested_at) AND (e.status <> ALL (ARRAY['failed'::text, 'expired'::text])) AND (e.initialization = 'complete'::text) AND (((s.configuration -> 'environment'::text) ->> 'type'::text) = 'openai_hosted'::text) AND (a.state = 'released'::text) AND (a.provider_key = d.installation_id) AND (a.deployment_generation <= d.generation) AND (a.created_at <= d.reset_requested_at) AND (NOT (EXISTS ( SELECT 1
           FROM runtime_placements p
          WHERE ((p.environment_id = e.id) AND (p.released_at IS NULL))))));

CREATE TABLE session_artifacts (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    environment_id uuid NOT NULL,
    path text NOT NULL,
    size_bytes bigint NOT NULL,
    body_oid oid NOT NULL,
    sha256 text NOT NULL,
    created_at timestamp with time zone,
    CONSTRAINT session_artifacts_path_check CHECK (((octet_length(path) >= 20) AND (octet_length(path) <= 4110))),
    CONSTRAINT session_artifacts_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT session_artifacts_size_bytes_check CHECK (((size_bytes >= 0) AND (size_bytes <= 209715200)))
);

CREATE TABLE session_devices (
    session_id uuid NOT NULL,
    device_id uuid NOT NULL,
    native_session_id text DEFAULT ''::text NOT NULL
);

CREATE TABLE session_events (
    session_id uuid NOT NULL,
    sequence bigint NOT NULL,
    payload jsonb NOT NULL,
    payload_bytes integer GENERATED ALWAYS AS (octet_length((payload)::text)) STORED
);

CREATE TABLE session_execution_configuration (
    session_id uuid NOT NULL,
    configuration jsonb NOT NULL,
    deployment_provider_revision uuid,
    CONSTRAINT session_execution_configuration_configuration_check CHECK ((jsonb_typeof(configuration) = 'object'::text))
);

CREATE TABLE session_items (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    payload jsonb NOT NULL,
    output_index integer,
    settled_at timestamp with time zone,
    CONSTRAINT session_items_output_index_check CHECK ((output_index >= 0)),
    CONSTRAINT session_items_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text))
);

CREATE TABLE session_model_execution (
    session_id uuid NOT NULL,
    encrypted_config bytea NOT NULL
);

CREATE TABLE skill_versions (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    skill_id uuid NOT NULL,
    version bigint NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    contents bytea NOT NULL,
    CONSTRAINT skill_versions_version_check CHECK ((version > 0))
);

CREATE TABLE skills (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    default_version bigint NOT NULL,
    latest_version bigint NOT NULL,
    next_version bigint NOT NULL,
    CONSTRAINT skills_check CHECK ((next_version > latest_version)),
    CONSTRAINT skills_default_version_check CHECK ((default_version > 0)),
    CONSTRAINT skills_latest_version_check CHECK ((latest_version > 0))
);

CREATE TABLE source_files (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    filename text NOT NULL,
    purpose text NOT NULL,
    body_oid oid NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    created_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    CONSTRAINT source_files_filename_check CHECK (((octet_length(filename) >= 1) AND (octet_length(filename) <= 1024))),
    CONSTRAINT source_files_purpose_check CHECK ((purpose = 'user_data'::text)),
    CONSTRAINT source_files_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT source_files_size_bytes_check CHECK (((size_bytes >= 0) AND (size_bytes <= 536870912)))
);

CREATE TABLE subagent_effects (
    session_id uuid NOT NULL,
    effect_id text NOT NULL,
    payload jsonb NOT NULL,
    CONSTRAINT subagent_effects_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text))
);

CREATE TABLE subagent_identities (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    device_id uuid NOT NULL,
    engine text NOT NULL,
    native_id text NOT NULL,
    parent_native_id text NOT NULL,
    native_created_at bigint NOT NULL,
    first_turn_id uuid NOT NULL,
    first_event_ordinal integer NOT NULL,
    name text,
    instructions text,
    public_visible boolean DEFAULT false NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    closed_at_ms bigint,
    lifecycle_at_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT subagent_closed_state CHECK (((status = 'closed'::text) = (closed_at_ms IS NOT NULL))),
    CONSTRAINT subagent_identities_check CHECK (((parent_native_id <> ''::text) AND (parent_native_id <> native_id))),
    CONSTRAINT subagent_identities_native_created_at_check CHECK ((native_created_at > 0)),
    CONSTRAINT subagent_identities_native_id_check CHECK ((native_id <> ''::text)),
    CONSTRAINT subagent_identities_status_check CHECK ((status = ANY (ARRAY['active'::text, 'closed'::text])))
);

CREATE TABLE subagent_items (
    id uuid NOT NULL,
    session_id uuid NOT NULL,
    subagent_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    "position" integer NOT NULL,
    output_index integer,
    payload jsonb NOT NULL,
    CONSTRAINT subagent_items_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT subagent_items_position_check CHECK (("position" >= 0))
);

CREATE TABLE turn_events (
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    ordinal integer NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT turn_events_ordinal_check CHECK ((ordinal > 0)),
    CONSTRAINT turn_events_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text))
);

CREATE TABLE turn_inputs (
    sequence bigint GENERATED ALWAYS AS IDENTITY NOT NULL,
    session_id uuid NOT NULL,
    turn_id uuid,
    idempotency_key text NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    batch_position integer DEFAULT 0 NOT NULL,
    CONSTRAINT turn_inputs_batch_position_check CHECK ((batch_position >= 0)),
    CONSTRAINT turn_inputs_check CHECK (((kind <> 'message'::text) OR (turn_id IS NOT NULL))),
    CONSTRAINT turn_inputs_function_turn_check CHECK (((kind <> 'tool_result'::text) OR (turn_id IS NOT NULL))),
    CONSTRAINT turn_inputs_kind_check CHECK ((kind = ANY (ARRAY['message'::text, 'cancel'::text, 'tool_result'::text]))),
    CONSTRAINT turn_inputs_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text))
);

CREATE TABLE vault_credentials (
    id uuid NOT NULL,
    vault_id uuid NOT NULL,
    name text NOT NULL,
    auth_type text NOT NULL,
    mcp_server_url text NOT NULL,
    token_ciphertext bytea NOT NULL,
    created_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    oauth_metadata jsonb,
    CONSTRAINT vault_credentials_auth_type_check CHECK ((auth_type = ANY (ARRAY['static_bearer'::text, 'mcp_oauth'::text]))),
    CONSTRAINT vault_credentials_name_check CHECK (((octet_length(name) >= 1) AND (octet_length(name) <= 256))),
    CONSTRAINT vault_credentials_oauth_metadata_check CHECK ((((auth_type = 'static_bearer'::text) AND (oauth_metadata IS NULL)) OR ((auth_type = 'mcp_oauth'::text) AND (oauth_metadata IS NOT NULL) AND (jsonb_typeof(oauth_metadata) = 'object'::text)))),
    CONSTRAINT vault_credentials_status_check CHECK ((status = ANY (ARRAY['active'::text, 'archived'::text]))),
    CONSTRAINT vault_credentials_token_ciphertext_check CHECK ((octet_length(token_ciphertext) > 0))
);

CREATE TABLE vaults (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    name text,
    metadata jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT statement_timestamp() NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    CONSTRAINT vaults_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT vaults_name_check CHECK (((name IS NULL) OR ((octet_length(name) >= 1) AND (octet_length(name) <= 256)))),
    CONSTRAINT vaults_status_check CHECK ((status = ANY (ARRAY['active'::text, 'archived'::text])))
);

CREATE TABLE workspace_fs_configurations (
    id uuid NOT NULL,
    adapter text NOT NULL,
    parameters jsonb NOT NULL,
    active boolean DEFAULT false NOT NULL,
    credential bytea,
    CONSTRAINT workspace_fs_configurations_adapter_check CHECK ((adapter <> ''::text)),
    CONSTRAINT workspace_fs_configurations_credential_check CHECK (((credential IS NULL) OR (length(credential) > 0))),
    CONSTRAINT workspace_fs_configurations_parameters_check CHECK ((jsonb_typeof(parameters) = 'object'::text))
);

CREATE TABLE workspace_storage_admission (
    singleton boolean DEFAULT true NOT NULL,
    tenant_capacity_mib bigint DEFAULT 0 NOT NULL,
    CONSTRAINT workspace_storage_admission_singleton_check CHECK (singleton),
    CONSTRAINT workspace_storage_admission_tenant_capacity_mib_check CHECK (((tenant_capacity_mib >= 0) AND (tenant_capacity_mib <= '4294967295'::bigint)))
);

CREATE TABLE write_audit_operations (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    key_id text NOT NULL,
    key_name text NOT NULL,
    key_prefix text NOT NULL,
    key_kind text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    parent_id text DEFAULT ''::text NOT NULL,
    request_id text NOT NULL,
    trace_id text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT write_audit_operations_key_kind_check CHECK ((key_kind = ANY (ARRAY['static'::text, 'issued'::text, 'console'::text])))
);

CREATE TABLE write_audit_owners (
    tenant_id uuid NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    parent_id text DEFAULT ''::text NOT NULL,
    operation_id uuid NOT NULL
);

ALTER TABLE admin_audit_log
    ADD CONSTRAINT admin_audit_log_pkey PRIMARY KEY (id);

ALTER TABLE admin_audit_log
    ADD CONSTRAINT admin_audit_log_tenant_id_request_id_action_resource_type_r_key UNIQUE (tenant_id, request_id, action, resource_type, resource_id);

ALTER TABLE agent_model_execution
    ADD CONSTRAINT agent_model_execution_pkey PRIMARY KEY (agent_id);

ALTER TABLE agents
    ADD CONSTRAINT agents_pkey PRIMARY KEY (id);

ALTER TABLE deployment_model_providers
    ADD CONSTRAINT deployment_model_providers_pkey PRIMARY KEY (harness);

ALTER TABLE devices
    ADD CONSTRAINT devices_pkey PRIMARY KEY (id);

ALTER TABLE environment_connections
    ADD CONSTRAINT environment_connections_pkey PRIMARY KEY (environment_id);

ALTER TABLE environment_executor_credentials
    ADD CONSTRAINT environment_executor_credentials_pkey PRIMARY KEY (key_id);

ALTER TABLE environment_executor_credentials
    ADD CONSTRAINT environment_executor_credentials_token_sha256_key UNIQUE (token_sha256);

ALTER TABLE environment_file_writes
    ADD CONSTRAINT environment_file_writes_pkey PRIMARY KEY (id);

ALTER TABLE environment_input_reservations
    ADD CONSTRAINT environment_input_reservations_pkey PRIMARY KEY (id);

ALTER TABLE environment_input_reservations
    ADD CONSTRAINT environment_input_reservations_session_id_idempotency_key_key UNIQUE (session_id, idempotency_key);

ALTER TABLE environment_setups
    ADD CONSTRAINT environment_setups_pkey PRIMARY KEY (session_id);

ALTER TABLE environment_templates
    ADD CONSTRAINT environment_templates_pkey PRIMARY KEY (id);

ALTER TABLE environment_workspaces
    ADD CONSTRAINT environment_workspaces_environment_id_key UNIQUE (environment_id);

ALTER TABLE environment_workspaces
    ADD CONSTRAINT environment_workspaces_pkey PRIMARY KEY (object_id);

ALTER TABLE environments
    ADD CONSTRAINT environments_pkey PRIMARY KEY (id);

ALTER TABLE environments
    ADD CONSTRAINT environments_session_id_key UNIQUE (session_id);

ALTER TABLE execution_project_scopes
    ADD CONSTRAINT execution_project_scopes_organization_id_project_id_key UNIQUE (organization_id, project_id);

ALTER TABLE execution_project_scopes
    ADD CONSTRAINT execution_project_scopes_pkey PRIMARY KEY (tenant_id);

ALTER TABLE function_calls
    ADD CONSTRAINT function_calls_pkey PRIMARY KEY (session_id, turn_id, call_id);

ALTER TABLE function_calls
    ADD CONSTRAINT function_calls_session_id_turn_id_executor_call_id_key UNIQUE (session_id, turn_id, executor_call_id);

ALTER TABLE initial_environment_files
    ADD CONSTRAINT initial_environment_files_pkey PRIMARY KEY (id);

ALTER TABLE initial_environment_files
    ADD CONSTRAINT initial_environment_files_session_id_path_key UNIQUE (session_id, path);

ALTER TABLE initial_environment_files
    ADD CONSTRAINT initial_environment_files_session_id_position_key UNIQUE (session_id, "position");

ALTER TABLE node_host_history_samples
    ADD CONSTRAINT node_host_history_samples_pkey PRIMARY KEY (node_id, observed_at);

ALTER TABLE project_api_keys
    ADD CONSTRAINT project_api_keys_pkey PRIMARY KEY (id);

ALTER TABLE project_api_keys
    ADD CONSTRAINT project_api_keys_token_sha256_key UNIQUE (token_sha256);

ALTER TABLE projects
    ADD CONSTRAINT projects_id_tenant_id_key UNIQUE (id, tenant_id);

ALTER TABLE projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);

ALTER TABLE projects
    ADD CONSTRAINT projects_tenant_id_key UNIQUE (tenant_id);

ALTER TABLE runtime_allocations
    ADD CONSTRAINT runtime_allocations_device_id_key UNIQUE (device_id);

ALTER TABLE runtime_allocations
    ADD CONSTRAINT runtime_allocations_pkey PRIMARY KEY (id);

ALTER TABLE runtime_deployment_generations
    ADD CONSTRAINT runtime_deployment_generations_pkey PRIMARY KEY (generation);

ALTER TABLE runtime_deployment
    ADD CONSTRAINT runtime_deployment_pkey PRIMARY KEY (singleton);

ALTER TABLE runtime_history_samples
    ADD CONSTRAINT runtime_history_samples_pkey PRIMARY KEY (tenant_id, session_id, environment_id, resolved_at_ns);

ALTER TABLE runtime_node_enrollments
    ADD CONSTRAINT runtime_node_enrollments_id_key UNIQUE (id);

ALTER TABLE runtime_node_enrollments
    ADD CONSTRAINT runtime_node_enrollments_pkey PRIMARY KEY (token_sha256);

ALTER TABLE runtime_node_generation_status
    ADD CONSTRAINT runtime_node_generation_status_pkey PRIMARY KEY (node_id, generation);

ALTER TABLE runtime_nodes
    ADD CONSTRAINT runtime_nodes_pkey PRIMARY KEY (id);

ALTER TABLE runtime_placements
    ADD CONSTRAINT runtime_placements_environment_id_node_id_key UNIQUE (environment_id, node_id);

ALTER TABLE runtime_placements
    ADD CONSTRAINT runtime_placements_pkey PRIMARY KEY (environment_id);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_body_oid_key UNIQUE (body_oid);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_pkey PRIMARY KEY (id);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_turn_id_path_key UNIQUE (turn_id, path);

ALTER TABLE session_devices
    ADD CONSTRAINT session_devices_pkey PRIMARY KEY (session_id);

ALTER TABLE session_events
    ADD CONSTRAINT session_events_pkey PRIMARY KEY (session_id, sequence);

ALTER TABLE session_execution_configuration
    ADD CONSTRAINT session_execution_configuration_pkey PRIMARY KEY (session_id);

ALTER TABLE session_items
    ADD CONSTRAINT session_items_pkey PRIMARY KEY (id);

ALTER TABLE session_model_execution
    ADD CONSTRAINT session_model_execution_pkey PRIMARY KEY (session_id);

ALTER TABLE sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);

ALTER TABLE sessions
    ADD CONSTRAINT sessions_tenant_id_idempotency_key_key UNIQUE (tenant_id, idempotency_key);

ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_pkey PRIMARY KEY (id);

ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_skill_id_version_key UNIQUE (skill_id, version);

ALTER TABLE skills
    ADD CONSTRAINT skills_pkey PRIMARY KEY (id);

ALTER TABLE skills
    ADD CONSTRAINT skills_tenant_id_id_key UNIQUE (tenant_id, id);

ALTER TABLE source_files
    ADD CONSTRAINT source_files_body_oid_key UNIQUE (body_oid);

ALTER TABLE source_files
    ADD CONSTRAINT source_files_pkey PRIMARY KEY (id);

ALTER TABLE subagent_effects
    ADD CONSTRAINT subagent_effects_pkey PRIMARY KEY (session_id, effect_id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_device_id_engine_native_id_key UNIQUE (device_id, engine, native_id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_pkey PRIMARY KEY (id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_session_id_native_id_key UNIQUE (session_id, native_id);

ALTER TABLE subagent_items
    ADD CONSTRAINT subagent_items_pkey PRIMARY KEY (id);

ALTER TABLE subagent_items
    ADD CONSTRAINT subagent_items_turn_id_position_key UNIQUE (turn_id, "position");

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_session_identity UNIQUE (session_id, id);

ALTER TABLE subagent_turns
    ADD CONSTRAINT subagent_turns_pkey PRIMARY KEY (id);

ALTER TABLE subagent_turns
    ADD CONSTRAINT subagent_turns_session_id_subagent_id_id_key UNIQUE (session_id, subagent_id, id);

ALTER TABLE subagent_turns
    ADD CONSTRAINT subagent_turns_subagent_id_native_id_key UNIQUE (subagent_id, native_id);

ALTER TABLE turn_events
    ADD CONSTRAINT turn_events_pkey PRIMARY KEY (turn_id, ordinal);

ALTER TABLE turn_inputs
    ADD CONSTRAINT turn_inputs_pkey PRIMARY KEY (sequence);

ALTER TABLE turn_inputs
    ADD CONSTRAINT turn_inputs_session_id_idempotency_key_batch_position_key UNIQUE (session_id, idempotency_key, batch_position);

ALTER TABLE turns
    ADD CONSTRAINT turns_pkey PRIMARY KEY (id);

ALTER TABLE turns
    ADD CONSTRAINT turns_session_id_id_key UNIQUE (session_id, id);

ALTER TABLE vault_credentials
    ADD CONSTRAINT vault_credentials_pkey PRIMARY KEY (id);

ALTER TABLE vaults
    ADD CONSTRAINT vaults_pkey PRIMARY KEY (id);

ALTER TABLE workspace_fs_configurations
    ADD CONSTRAINT workspace_fs_configurations_pkey PRIMARY KEY (id);

ALTER TABLE workspace_storage_admission
    ADD CONSTRAINT workspace_storage_admission_pkey PRIMARY KEY (singleton);

ALTER TABLE write_audit_operations
    ADD CONSTRAINT write_audit_operations_pkey PRIMARY KEY (id);

ALTER TABLE write_audit_operations
    ADD CONSTRAINT write_audit_operations_tenant_id_id_key UNIQUE (tenant_id, id);

ALTER TABLE write_audit_operations
    ADD CONSTRAINT write_audit_operations_tenant_id_request_id_key UNIQUE (tenant_id, request_id);

ALTER TABLE write_audit_owners
    ADD CONSTRAINT write_audit_owners_pkey PRIMARY KEY (tenant_id, resource_type, resource_id);

CREATE INDEX admin_audit_log_history ON admin_audit_log USING btree (project_id, created_at DESC, id DESC);

CREATE INDEX agents_tenant_created_idx ON agents USING btree (tenant_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX devices_environment_authority ON devices USING btree (environment_id) WHERE ((revoked_at IS NULL) OR (executor_key_id IS NOT NULL));

CREATE INDEX devices_tenant_idx ON devices USING btree (tenant_id);

CREATE UNIQUE INDEX environment_file_writes_pending ON environment_file_writes USING btree (environment_id) WHERE (state = 'pending'::text);

CREATE INDEX environment_input_reservations_latest_idx ON environment_input_reservations USING btree (session_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX environment_input_reservations_one_pending_idx ON environment_input_reservations USING btree (session_id) WHERE (state = 'pending'::text);

CREATE INDEX environment_input_reservations_pending_deadline_idx ON environment_input_reservations USING btree (deadline, id) WHERE (state = 'pending'::text);

CREATE INDEX environment_templates_tenant_order ON environment_templates USING btree (tenant_id, created_at, id);

CREATE INDEX function_calls_pending_idx ON function_calls USING btree (session_id, turn_id) WHERE ((NOT applied) AND (NOT withdrawn));

CREATE INDEX node_host_history_retention ON node_host_history_samples USING btree (observed_at);

CREATE INDEX project_api_keys_project ON project_api_keys USING btree (project_id, id);

CREATE UNIQUE INDEX runtime_allocations_current_environment ON runtime_allocations USING btree (environment_id) WHERE (state <> 'released'::text);

CREATE INDEX runtime_allocations_environment_receipts ON runtime_allocations USING btree (environment_id, created_at DESC, id DESC);

CREATE INDEX runtime_allocations_generation_live ON runtime_allocations USING btree (deployment_generation) WHERE (state <> 'released'::text);

CREATE INDEX runtime_history_samples_retention_idx ON runtime_history_samples USING btree (resolved_at_ns);

CREATE INDEX runtime_nodes_generation_live ON runtime_nodes USING btree (ready_generation) WHERE (removed_at IS NULL);

CREATE INDEX runtime_placements_generation_live ON runtime_placements USING btree (deployment_generation) WHERE (released_at IS NULL);

CREATE INDEX runtime_placements_node_idx ON runtime_placements USING btree (node_id) WHERE (released_at IS NULL);

CREATE INDEX session_artifacts_page_idx ON session_artifacts USING btree (session_id, created_at DESC, id DESC) WHERE (created_at IS NOT NULL);

CREATE UNIQUE INDEX session_items_output_idx ON session_items USING btree (turn_id, output_index);

CREATE INDEX session_items_page_idx ON session_items USING btree (session_id, created_at, "position", id);

CREATE UNIQUE INDEX session_items_position_idx ON session_items USING btree (session_id, "position");

CREATE INDEX session_items_turn_idx ON session_items USING btree (turn_id);

CREATE INDEX sessions_tenant_agent_created_idx ON sessions USING btree (tenant_id, ((configuration #>> '{agent,id}'::text[])), created_at DESC, id DESC);

CREATE INDEX sessions_tenant_created_idx ON sessions USING btree (tenant_id, created_at DESC, id DESC);

CREATE INDEX skills_tenant_created ON skills USING btree (tenant_id, created_at, id);

CREATE INDEX source_files_page_idx ON source_files USING btree (tenant_id, created_at DESC, id DESC);

CREATE INDEX turn_events_item_history_idx ON turn_events USING btree (turn_id, created_at, ordinal);

CREATE INDEX turn_inputs_item_history_idx ON turn_inputs USING btree (turn_id, created_at, sequence) WHERE (kind = 'message'::text);

CREATE INDEX turn_inputs_turn_sequence_idx ON turn_inputs USING btree (turn_id, sequence);

CREATE INDEX turns_execution_queue_idx ON turns USING btree (status, id) WHERE (status = ANY (ARRAY['queued'::text, 'in_progress'::text, 'waiting'::text]));

CREATE INDEX turns_metrics_interrupted_idx ON turns USING btree (completed_at) WHERE ((status = 'failed'::text) AND ((outcome ->> 'error_code'::text) = 'execution_interrupted'::text));

CREATE INDEX turns_metrics_started_idx ON turns USING btree (started_at) INCLUDE (created_at) WHERE (started_at IS NOT NULL);

CREATE UNIQUE INDEX turns_one_active_idx ON turns USING btree (session_id) WHERE (status = ANY (ARRAY['queued'::text, 'in_progress'::text, 'waiting'::text]));

CREATE INDEX vault_credentials_vault_created_id_idx ON vault_credentials USING btree (vault_id, created_at, id);

CREATE INDEX vaults_tenant_created_id_idx ON vaults USING btree (tenant_id, created_at, id);

CREATE UNIQUE INDEX workspace_fs_active_idx ON workspace_fs_configurations USING btree (active) WHERE active;

CREATE INDEX write_audit_operations_expiry ON write_audit_operations USING btree (created_at, id);

CREATE INDEX write_audit_operations_history ON write_audit_operations USING btree (tenant_id, created_at DESC, id DESC);

CREATE INDEX write_audit_operations_key_history ON write_audit_operations USING btree (tenant_id, key_id, created_at DESC, id DESC);

CREATE INDEX write_audit_operations_resource_history ON write_audit_operations USING btree (tenant_id, resource_type, resource_id, created_at DESC, id DESC);

CREATE INDEX write_audit_owners_operation ON write_audit_owners USING btree (operation_id);

CREATE TRIGGER immutable_runtime_allocation_generation BEFORE UPDATE ON runtime_allocations FOR EACH ROW EXECUTE FUNCTION immutable_runtime_deployment_generation();

CREATE TRIGGER immutable_runtime_placement_generation BEFORE UPDATE ON runtime_placements FOR EACH ROW EXECUTE FUNCTION immutable_runtime_deployment_generation();

CREATE TRIGGER immutable_sandbox_specification BEFORE UPDATE ON runtime_deployment_generations FOR EACH ROW EXECUTE FUNCTION immutable_sandbox_specification();

CREATE TRIGGER immutable_workspace_configuration BEFORE UPDATE ON workspace_fs_configurations FOR EACH ROW EXECUTE FUNCTION immutable_workspace_configuration();

CREATE TRIGGER immutable_workspace_identity BEFORE UPDATE ON environment_workspaces FOR EACH ROW EXECUTE FUNCTION immutable_workspace_identity();

ALTER TABLE admin_audit_log
    ADD CONSTRAINT admin_audit_log_project_id_tenant_id_fkey FOREIGN KEY (project_id, tenant_id) REFERENCES projects(id, tenant_id);

ALTER TABLE agent_model_execution
    ADD CONSTRAINT agent_model_execution_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;

ALTER TABLE devices
    ADD CONSTRAINT devices_archive_cancel_turn_id_fkey FOREIGN KEY (archive_cancel_turn_id) REFERENCES turns(id) ON DELETE SET NULL;

ALTER TABLE devices
    ADD CONSTRAINT devices_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE devices
    ADD CONSTRAINT devices_executor_key_id_fkey FOREIGN KEY (executor_key_id) REFERENCES environment_executor_credentials(key_id);

ALTER TABLE environment_connections
    ADD CONSTRAINT environment_connections_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE environment_executor_credentials
    ADD CONSTRAINT environment_executor_credentials_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE environment_executor_credentials
    ADD CONSTRAINT environment_executor_credentials_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES execution_project_scopes(tenant_id);

ALTER TABLE environment_file_writes
    ADD CONSTRAINT environment_file_writes_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);

ALTER TABLE environment_file_writes
    ADD CONSTRAINT environment_file_writes_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE environment_input_reservations
    ADD CONSTRAINT environment_input_reservations_session_id_fkey FOREIGN KEY (session_id) REFERENCES environments(session_id);

ALTER TABLE environment_setups
    ADD CONSTRAINT environment_setups_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;

ALTER TABLE environment_workspaces
    ADD CONSTRAINT environment_workspaces_configuration_id_fkey FOREIGN KEY (configuration_id) REFERENCES workspace_fs_configurations(id);

ALTER TABLE environment_workspaces
    ADD CONSTRAINT environment_workspaces_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE environments
    ADD CONSTRAINT environments_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE function_calls
    ADD CONSTRAINT function_calls_session_id_turn_id_fkey FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id);

ALTER TABLE initial_environment_files
    ADD CONSTRAINT initial_environment_files_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;

ALTER TABLE node_host_history_samples
    ADD CONSTRAINT node_host_history_samples_node_id_fkey FOREIGN KEY (node_id) REFERENCES runtime_nodes(id);

ALTER TABLE project_api_keys
    ADD CONSTRAINT project_api_keys_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE projects
    ADD CONSTRAINT projects_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES execution_project_scopes(tenant_id);

ALTER TABLE runtime_allocations
    ADD CONSTRAINT runtime_allocation_node_fk FOREIGN KEY (node_id) REFERENCES runtime_nodes(id);

ALTER TABLE runtime_allocations
    ADD CONSTRAINT runtime_allocations_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);

ALTER TABLE runtime_allocations
    ADD CONSTRAINT runtime_allocations_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE runtime_history_samples
    ADD CONSTRAINT runtime_history_samples_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE CASCADE;

ALTER TABLE runtime_history_samples
    ADD CONSTRAINT runtime_history_samples_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;

ALTER TABLE runtime_node_enrollments
    ADD CONSTRAINT runtime_node_enrollments_node_id_fkey FOREIGN KEY (node_id) REFERENCES runtime_nodes(id);

ALTER TABLE runtime_node_generation_status
    ADD CONSTRAINT runtime_node_generation_status_node_id_fkey FOREIGN KEY (node_id) REFERENCES runtime_nodes(id);

ALTER TABLE runtime_placements
    ADD CONSTRAINT runtime_placements_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE runtime_placements
    ADD CONSTRAINT runtime_placements_node_id_fkey FOREIGN KEY (node_id) REFERENCES runtime_nodes(id);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_environment_id_fkey FOREIGN KEY (environment_id) REFERENCES environments(id);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE session_artifacts
    ADD CONSTRAINT session_artifacts_session_id_turn_id_fkey FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id);

ALTER TABLE session_devices
    ADD CONSTRAINT session_devices_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);

ALTER TABLE session_devices
    ADD CONSTRAINT session_devices_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE session_events
    ADD CONSTRAINT session_events_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE session_execution_configuration
    ADD CONSTRAINT session_execution_configuration_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;

ALTER TABLE session_items
    ADD CONSTRAINT session_items_session_id_turn_id_fkey FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id);

ALTER TABLE session_model_execution
    ADD CONSTRAINT session_model_execution_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;

ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_tenant_id_skill_id_fkey FOREIGN KEY (tenant_id, skill_id) REFERENCES skills(tenant_id, id) ON DELETE CASCADE;

ALTER TABLE skills
    ADD CONSTRAINT skills_default_version FOREIGN KEY (id, default_version) REFERENCES skill_versions(skill_id, version) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE skills
    ADD CONSTRAINT skills_latest_version FOREIGN KEY (id, latest_version) REFERENCES skill_versions(skill_id, version) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE subagent_effects
    ADD CONSTRAINT subagent_effects_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_first_turn_id_first_event_ordinal_fkey FOREIGN KEY (first_turn_id, first_event_ordinal) REFERENCES turn_events(turn_id, ordinal);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_session_id_first_turn_id_fkey FOREIGN KEY (session_id, first_turn_id) REFERENCES turns(session_id, id);

ALTER TABLE subagent_identities
    ADD CONSTRAINT subagent_identities_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE subagent_items
    ADD CONSTRAINT subagent_items_session_id_subagent_id_turn_id_fkey FOREIGN KEY (session_id, subagent_id, turn_id) REFERENCES subagent_turns(session_id, subagent_id, id);

ALTER TABLE subagent_turns
    ADD CONSTRAINT subagent_turns_session_id_subagent_id_fkey FOREIGN KEY (session_id, subagent_id) REFERENCES subagent_identities(session_id, id);

ALTER TABLE turn_events
    ADD CONSTRAINT turn_events_session_id_turn_id_fkey FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id);

ALTER TABLE turn_inputs
    ADD CONSTRAINT turn_inputs_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE turn_inputs
    ADD CONSTRAINT turn_inputs_session_id_turn_id_fkey FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id);

ALTER TABLE turns
    ADD CONSTRAINT turns_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id);

ALTER TABLE vault_credentials
    ADD CONSTRAINT vault_credentials_vault_id_fkey FOREIGN KEY (vault_id) REFERENCES vaults(id) ON DELETE CASCADE;

ALTER TABLE write_audit_owners
    ADD CONSTRAINT write_audit_owners_tenant_id_operation_id_fkey FOREIGN KEY (tenant_id, operation_id) REFERENCES write_audit_operations(tenant_id, id);

INSERT INTO runtime_deployment (singleton) VALUES (true);
INSERT INTO workspace_storage_admission DEFAULT VALUES;
