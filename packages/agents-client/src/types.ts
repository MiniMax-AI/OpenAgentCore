export type PageOrder = "asc" | "desc";

export interface ListPage<T> {
  data: T[];
  has_more: boolean;
  object?: "list";
  first_id?: string | null;
  last_id?: string | null;
}

export interface PageOptions extends ReadOptions {
  after?: string;
  limit?: number;
  order?: PageOrder;
}

export interface ReadOptions {
  signal?: AbortSignal;
}

export type AgentTextFormat =
  | { type: "text" }
  | { type: "json_schema"; schema: Record<string, unknown> };

export interface AgentTextConfig {
  format: AgentTextFormat;
  verbosity: "low" | "medium" | "high";
}

export interface AgentTextInput {
  format?: AgentTextConfig["format"] | null;
  verbosity?: AgentTextConfig["verbosity"] | null;
}

export type AgentServiceTier = "auto" | "default" | "flex" | "priority" | "fast";
export type AgentReasoningEffort = "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
export type AgentReasoningSummary = "concise" | "detailed" | "auto";

export interface AgentReasoning {
  effort?: AgentReasoningEffort | null;
  summary?: AgentReasoningSummary | null;
}

export interface MultiAgentInput {
  enabled: boolean;
  max_concurrent_subagents?: number | null;
}

export interface FunctionToolInput {
  type: "function";
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  defer_loading?: boolean;
}

/** The service-origin HTTP MCP profile accepted by current Core. */
export interface ServiceHttpMcpToolInput {
  type: "mcp";
  server_label: string;
  transport: {
    type: "http";
    server_url: string;
  };
  /** null or omitted permits every advertised tool; [] permits none. */
  allowed_tools?: string[] | null;
  /** null or omitted is saved as "service", as the official service does. */
  connection_origin?: "service" | null;
  /** Saving a reference does not authorize it; Session vault_ids must attach its owner. */
  credential_id?: string | null;
  required?: boolean;
}

/** Anonymous is an explicit subset that cannot name a stored Credential. */
export type AnonymousHttpMcpToolInput = Omit<ServiceHttpMcpToolInput, "credential_id"> & {
  credential_id?: null;
};

export interface ToolSearchInput {
  type: "tool_search";
}

export interface ProgrammaticToolCallingInput {
  type: "programmatic_tool_calling";
  enabled?: boolean;
}

export interface WebSearchLocationInput {
  city?: string | null;
  country?: string | null;
  region?: string | null;
  timezone?: string | null;
}

/**
 * Saved Agents keep every pinned mode; omitted or null `mode` is saved as `live`
 * and omitted or null `context_size` as `medium`. Session admission executes only
 * `disabled` and rejects the other modes unless the Session replaces its tools.
 */
export interface WebSearchToolInput {
  type: "web_search";
  mode?: "disabled" | "cached" | "live" | null;
  context_size?: "low" | "medium" | "high" | null;
  /** null and [] are saved distinctly. */
  allowed_domains?: string[] | null;
  /** A saved location includes all four keys, with null for omitted ones. */
  location?: WebSearchLocationInput | null;
}

export type SavedAgentToolInput =
  | FunctionToolInput
  | ServiceHttpMcpToolInput
  | ToolSearchInput
  | ProgrammaticToolCallingInput
  | WebSearchToolInput;
export type SessionFunctionToolInput = Omit<FunctionToolInput, "defer_loading"> & { defer_loading?: false };
export type ConfigurableAgentToolInput = SessionFunctionToolInput | ServiceHttpMcpToolInput;

export type VaultStatus = "active" | "archived";

export interface VaultListOptions extends PageOptions {
  status?: VaultStatus | VaultStatus[];
}

export interface Vault {
  id: string;
  object: "vault";
  created_at: number;
  name: string | null;
  metadata: Record<string, string>;
}

export interface VaultList extends ListPage<Vault> {
  object: "list";
  first_id: string | null;
  last_id: string | null;
}

export interface CreateVaultInput {
  name?: string;
  metadata?: Record<string, string> | null;
}

export interface VaultDeleted {
  id: string;
  object: "vault.deleted";
  deleted: true;
}

export interface StaticBearerCredentialAuth {
  type: "static_bearer";
  mcp_server_url: string;
}

export interface McpOAuthRefreshMetadata {
  client_id: string;
  token_endpoint: string;
  token_endpoint_auth: { type: "none" | "client_secret_basic" | "client_secret_post" };
  resource: string | null;
  scope: string | null;
}

export interface McpOAuthCredentialAuth {
  type: "mcp_oauth";
  mcp_server_url: string;
  expires_at: string | null;
  refresh: McpOAuthRefreshMetadata | null;
}

export interface VaultCredential {
  id: string;
  vault_id: string;
  name: string;
  object: "vault.credential";
  auth: StaticBearerCredentialAuth | McpOAuthCredentialAuth;
  created_at: number;
  updated_at: number;
}

export interface VaultCredentialList extends ListPage<VaultCredential> {
  object: "list";
  first_id: string | null;
  last_id: string | null;
}

export interface CreateVaultCredentialInput {
  name: string;
  auth: StaticBearerCredentialAuth & { token: string };
}

export interface ReplaceVaultCredentialTokenInput {
  auth: { type: "static_bearer"; token: string };
}

export interface VaultCredentialDeleted {
  id: string;
  object: "vault.credential.deleted";
  deleted: true;
}

export interface SavedAgent {
  id: string;
  object: "agent";
  x_agents_core?: SavedAgentCore | null;
  model: string;
  name: string | null;
  instructions: string | null;
  metadata: Record<string, string>;
  multi_agent: {
    enabled: boolean;
    max_concurrent_subagents: number | null;
  };
  reasoning: AgentReasoning;
  service_tier: AgentServiceTier;
  text: AgentTextConfig;
  tools: unknown[];
  created_at: number;
  updated_at: number;
}

export interface CreateAgentInput {
  x_agents_core?: SavedAgentCoreInput | null;
  model: string;
  name?: string | null;
  instructions?: string | null;
  metadata?: Record<string, string> | null;
  multi_agent?: MultiAgentInput | null;
  reasoning?: AgentReasoning | null;
  service_tier?: AgentServiceTier | null;
  text?: AgentTextInput | null;
  tools?: SavedAgentToolInput[] | null;
}

export type UpdateAgentInput = Partial<CreateAgentInput>;

export interface InlineAgentInput {
  x_agents_core?: AgentsCoreSelection | null;
  model?: string;
  instructions?: string | null;
  tools?: ConfigurableAgentToolInput[] | null;
  text?: AgentTextInput | null;
  reasoning?: AgentReasoning | null;
  service_tier?: AgentServiceTier | null;
  multi_agent?: MultiAgentInput | null;
}

export type AgentSnapshot = Omit<SavedAgent, "object" | "metadata" | "created_at" | "updated_at" | "x_agents_core"> & {
  x_agents_core?: AgentsCoreSelection | null;
};

declare const unknownEnvironmentType: unique symbol;
declare const unknownItemType: unique symbol;
declare const unknownSessionEventType: unique symbol;

export interface NoneAgentEnvironment {
  type: "none";
}

export interface SelfHostedAgentEnvironmentInput {
  type: "self_hosted";
  workspace_directory: string;
  capability_directories?: string[] | null;
}

export type OpenAIHostedNetworkAccess = "enabled" | "disabled";

export interface OpenAIHostedAgentEnvironmentInput {
  type: "openai_hosted";
  /** Omitted or null defaults to enabled in the pinned basic Core profile. */
  network?: { access: OpenAIHostedNetworkAccess } | null;
  /**
   * Optional tenant-owned reusable configuration. Omitted network inherits the
   * template policy, and an explicitly enabled Session cannot widen a disabled
   * template. The resolved configuration is frozen without echoing this ID, so
   * the created Session carries no template reference.
   */
  environment_template_id?: string;
}

export type AgentEnvironmentInput =
  | NoneAgentEnvironment
  | SelfHostedAgentEnvironmentInput
  | OpenAIHostedAgentEnvironmentInput;

export interface SelfHostedAgentEnvironment {
  type: "self_hosted";
  id: string;
  remote_url: string;
  workspace_directory: string;
  capability_directories: string[];
}

export interface AgentEnvironmentPackages {
  npm: string[];
  python: string[];
  system: string[];
}

/** Exact safe output of Parsar's operator-gated basic managed profile. */
export interface OpenAIHostedAgentEnvironment {
  type: "openai_hosted";
  id: string;
  capability_directories: [];
  network: {
    access: OpenAIHostedNetworkAccess;
    allowed_domains: [];
  };
  packages: { npm: []; python: []; system: [] };
  files: [];
  plugins: [];
  skills: [];
}

export type UnknownEnvironmentType = string & { readonly [unknownEnvironmentType]: true };

export interface UnknownAgentEnvironment {
  type: UnknownEnvironmentType;
  [key: string]: unknown;
}

export type AgentEnvironment =
  | NoneAgentEnvironment
  | SelfHostedAgentEnvironment
  | OpenAIHostedAgentEnvironment
  | UnknownAgentEnvironment;

export type EnvironmentResourceStatus = "pending" | "connected" | "disconnected" | "expired" | "failed";

export interface SelfHostedAgentEnvironmentResource {
  id: string;
  object: "agent.environment";
  type: "self_hosted";
  status: EnvironmentResourceStatus;
  files: unknown[];
  plugins: unknown[];
  skills: unknown[];
}

/**
 * A hosted Environment is writable only after this exact durable resource has
 * been retrieved. Session shape, health and file-list success are not a
 * substitute for this projection.
 */
export interface OpenAIHostedAgentEnvironmentResource {
  id: string;
  object: "agent.environment";
  type: "openai_hosted";
  status: EnvironmentResourceStatus;
  files: [];
  plugins: [];
  skills: [];
}

export type AgentEnvironmentResource =
  | SelfHostedAgentEnvironmentResource
  | OpenAIHostedAgentEnvironmentResource;

/**
 * Exact safe projection of Parsar's basic reusable hosted configuration. A
 * Template never contains a running Workspace, never exposes `env` or
 * `setup_commands`, and never selects a provider image: Docker or E2B packaging
 * stays operator-owned behind the same `openai_hosted` discriminator.
 */
export interface EnvironmentTemplate {
  id: string;
  object: "agent.environment.template";
  name: string | null;
  network: {
    access: OpenAIHostedNetworkAccess;
    allowed_domains: [];
  };
  capability_directories: [];
  packages: { npm: []; python: []; system: [] };
  files: [];
  plugins: [];
  skills: [];
  created_at: number;
  updated_at: number;
}

export interface EnvironmentTemplateList {
  object: "list";
  data: EnvironmentTemplate[];
  has_more: boolean;
  first_id: string | null;
  last_id: string | null;
}

export interface EnvironmentTemplateDeleted {
  id: string;
  object: "agent.environment.template.deleted";
  deleted: true;
}

/** Supported create fields. Omitted network stores the pinned enabled default. */
export interface CreateEnvironmentTemplateInput {
  name?: string | null;
  network?: { access: OpenAIHostedNetworkAccess } | null;
}

/**
 * Supplied fields replace; omitted fields stay unchanged. Null name clears the
 * name and null network resets the pinned enabled default.
 */
export type UpdateEnvironmentTemplateInput = CreateEnvironmentTemplateInput;

export interface EnvironmentFile {
  environment_id: string;
  object: "agent.environment.file";
  path: string;
  size_bytes: number;
}

/** Official token page: has_more is true exactly when next carries a token. */
export interface EnvironmentFileList {
  object: "page";
  data: EnvironmentFile[];
  next: string | null;
  has_more: boolean;
}

export interface EnvironmentFileListOptions extends ReadOptions {
  limit?: number;
  order?: PageOrder;
  page?: string;
  /** Absolute Environment path. Omission selects /workspace; self_hosted callers pass its exact workspace_directory. */
  path?: string;
}

export type EnvironmentFileCreateInput =
  | { type: "inline"; data: string; path: string }
  | { type: "file_id"; file_id: string; path: string };

export interface SourceFile {
  id: string;
  object: "file";
  bytes: number;
  created_at: number;
  filename: string;
  purpose: "user_data";
  status: "processed";
  expires_at: null;
  status_details: null;
}

export interface SourceFileDeleted {
  id: string;
  object: "file";
  deleted: true;
}

export interface SourceFileContent {
  data: Uint8Array;
  bytes: number;
  content_type: "application/octet-stream";
  content_disposition: string;
}

export interface SourceFileUploadInput {
  file: Blob;
  filename: string;
}

export type SessionStatus = "idle" | "in_progress" | "requires_action" | "failed";

export interface FunctionCallAction {
  type: "function_call";
  call_id: string;
  turn_id: string;
  name: string;
  arguments: unknown;
}

export interface EnvironmentConnectionAction {
  type: "environment_connection";
  environment_id: string;
}

export type RequiredAction = FunctionCallAction | EnvironmentConnectionAction;

export interface TokenUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  input_tokens_details: {
    cached_tokens: number;
  };
  output_tokens_details: {
    reasoning_tokens: number;
  };
}

export interface AgentSession {
  id: string;
  object: "agent.session";
  agent: AgentSnapshot;
  environment: AgentEnvironment;
  status: SessionStatus;
  error: string | null;
  metadata: Record<string, string>;
  required_actions: RequiredAction[];
  vault_ids: string[];
  usage: TokenUsage | null;
  created_at: number;
  last_active_at: number;
}

export interface CreateSessionInput {
  x_agents_core?: { sandbox_node_id?: string; model_provider?: ModelProviderInput | null };
  agent_id?: string;
  agent?: InlineAgentInput;
  environment: AgentEnvironmentInput;
  /** A nonempty initial input is required for environment:none. */
  input?: string | InputMessage[] | null;
  metadata?: Record<string, string> | null;
  /** This JSON-returning method does not support the endpoint's streaming create variant. */
  stream?: false;
  vault_ids?: string[];
}

export interface InputTextContent {
  type: "input_text";
  text: string;
}

export interface InputMessage {
  type?: "message";
  role: "user";
  content: InputTextContent[];
}

export type ItemStatus = "in_progress" | "completed" | "failed" | "incomplete";

export interface ItemContent {
  type: "input_text" | "output_text" | "input_image" | "encrypted_content";
  text?: string | null;
  image_url?: string;
  encrypted_content?: string;
}

export type KnownSessionItemType =
  | "message"
  | "command_execution"
  | "mcp_call"
  | "function_call"
  | "function_call_output"
  | "web_search_call"
  | "reasoning"
  | "agent_message"
  | "create_subagent_call"
  | "send_subagent_input_call"
  | "resume_subagent_call"
  | "wait_for_subagents_call"
  | "interrupt_subagent_call"
  | "close_subagent_call";

export type UnknownSessionItemType = string & { readonly [unknownItemType]: true };

export interface SessionItemBase {
  id: string;
  turn_id: string;
  /** Inter-agent messages have no status; reasoning may have an unknown status. */
  status?: ItemStatus | null;
  role?: "user" | "assistant";
  /** Null on user messages and when the harness reports none; older Cores omit it. */
  phase?: "commentary" | "final_answer" | null;
  content?: ItemContent[];
  command?: string;
  cwd?: string | null;
  duration_ms?: number | null;
  exit_code?: number | null;
  name?: string;
  call_id?: string;
  server_label?: string;
  arguments?: unknown;
  output?: unknown;
  error?: unknown;
  action?: WebSearchAction;
  agent_id?: string;
  sender_agent_id?: string;
  recipient_agent_id?: string;
  recipient_agent_ids?: string[];
  model?: string | null;
  reasoning_effort?: string | null;
  summary?: { type: "summary_text"; text: string }[];
}

export interface KnownSessionItem extends SessionItemBase {
  type: KnownSessionItemType;
}

export interface UnknownSessionItem extends SessionItemBase {
  type: UnknownSessionItemType;
  [key: string]: unknown;
}

export type SessionItem = KnownSessionItem | UnknownSessionItem;

export interface WebSearchAction {
  type: "search" | "open_page" | "find_in_page" | "other";
  query?: string | null;
  queries?: string[];
  url?: string | null;
  pattern?: string | null;
}

export type TurnStatus = "queued" | "in_progress" | "waiting" | "completed" | "failed" | "cancelled";

export interface AgentTurn {
  id: string;
  /**
   * The Session's Agent ID, for root and Subagent Turns alike. Older Core
   * releases sent the Subagent's own ID here for Subagent Turns.
   */
  agent_id: string;
  /** Set on Subagent Turns, which are read through the Subagent routes. */
  subagent_id?: string | null;
  session_id: string;
  object: "agent.session.turn";
  status: TurnStatus;
  created_at: number;
  started_at: number | null;
  completed_at: number | null;
  error: { code: "internal_error"; message: string } | null;
  usage: TokenUsage | null;
}

export interface StreamError {
  code: string;
  type: string;
  message: string;
  /** Present on Session error events (null when unset); Environment state errors and older or interruption frames omit it. */
  param?: string | null;
}

export type SessionEnvironmentStatus = "pending" | "ready" | "connected" | "disconnected" | "failed";

export interface SessionEnvironmentState {
  id: string;
  type: string;
  status: SessionEnvironmentStatus;
  error: StreamError | null;
}

export interface SessionEventBase {
  event_id: string;
  session_id?: string;
  turn_id?: string;
  session?: AgentSession;
  turn?: AgentTurn;
  item?: SessionItem;
  item_id?: string;
  /** Null on Item events for input Items; older Cores omit it. */
  output_index?: number | null;
  content_index?: number;
  part?: ItemContent;
  delta?: string;
  text?: string;
  error?: StreamError;
  /**
   * Present on terminal Turn events only. It mirrors that Turn snapshot's usage
   * and is null when unknown; a later Turn read can still report measured usage.
   */
  usage?: TokenUsage | null;
}

export type AgentSessionEnvironmentEvent = {
  [Status in SessionEnvironmentStatus]: SessionEventBase & {
    type: `agent.session.environment.${Status}`;
    environment: SessionEnvironmentState & { status: Status };
  };
}[SessionEnvironmentStatus];

export type KnownSessionEventType =
  | "agent.session.created"
  | "agent.session.in_progress"
  | "agent.session.requires_action"
  | "agent.session.idle"
  | "agent.session.failed"
  | "agent.session.turn.created"
  | "agent.session.turn.in_progress"
  | "agent.session.turn.waiting"
  | "agent.session.turn.completed"
  | "agent.session.turn.failed"
  | "agent.session.turn.cancelled"
  | "agent.session.turn.item.added"
  | "agent.session.turn.item.done"
  | "agent.session.turn.content_part.added"
  | "agent.session.turn.content_part.done"
  | "agent.session.turn.output_text.delta"
  | "agent.session.turn.output_text.done"
  | "agent.output.command_execution_output.delta";

export interface KnownSessionEvent extends SessionEventBase {
  type: KnownSessionEventType;
}

export type UnknownSessionEventType = string & { readonly [unknownSessionEventType]: true };

export interface UnknownSessionEvent extends SessionEventBase {
  type: UnknownSessionEventType;
  [key: string]: unknown;
}

/**
 * A Session failure reported in the event stream, such as a hosted Environment
 * that failed to provision (type environment_error, code sandbox_error). The
 * agent.session.failed snapshot follows it. Core's own stream interruption is
 * raised as an AgentCoreError instead.
 */
export interface AgentSessionErrorEvent extends SessionEventBase {
  type: "error";
  session_id: string;
  error: StreamError;
}

export type SessionEvent = AgentSessionEnvironmentEvent | AgentSessionErrorEvent | KnownSessionEvent | UnknownSessionEvent;

export interface AgentDeleted {
  id: string;
  object: "agent.deleted";
  deleted: true;
}

export interface SessionDeleted {
  id: string;
  object: "agent.session.deleted";
  deleted: true;
}

export interface FunctionResultInput {
  callId: string;
  turnId: string;
  success: boolean;
  output?: string | FunctionResultContent[] | null;
  error?: string | null;
}

export type FunctionResultContent =
  | { type: "input_text"; text: string }
  | { type: "input_image"; image_url: string };

/** Exact public wire shape for an ordered user-message input event. */
export interface SessionMessageInputEvent {
  type: "agent.session.input.message";
  input: InputMessage[];
}

/** Exact public wire shape for a cancellation input event. */
export interface SessionCancelInputEvent {
  type: "agent.session.input.cancel";
}

/** Exact public wire shape for a Function result input event. */
export interface SessionToolResultInputEvent {
  type: "agent.session.input.tool_result";
  call_id: string;
  turn_id: string;
  success: boolean;
  output?: string | FunctionResultContent[] | null;
  error?: string | null;
}

/** The three input event variants accepted by the current public Core endpoint. */
export type SessionInputEvent =
  | SessionMessageInputEvent
  | SessionCancelInputEvent
  | SessionToolResultInputEvent;

export interface StreamOptions {
  signal?: AbortSignal;
  /** Called once the authenticated streaming response has been accepted. */
  onOpen?: () => void;
  onEvent: (event: SessionEvent) => void;
}

export interface CreateSessionStreamOptions extends StreamOptions {
  /** Called exactly once after the leading creation snapshot passes validation. */
  onSession: (session: AgentSession) => void;
}

export type RuntimeObservationStatus = "observed" | "unsupported" | "unavailable";
export type RuntimeObservationReason =
  | "runtime_mode_not_observable"
  | "allocation_pending"
  | "runtime_not_running"
  | "source_not_configured"
  | "sample_timeout"
  | "sample_unavailable";

export type RuntimeUnavailableReason = Exclude<RuntimeObservationReason, "runtime_mode_not_observable">;

export interface RuntimeCPUObservation {
  usage_seconds_total: number | null;
  capacity_cores: number | null;
  usage_cores: number | null;
  utilization_ratio: number | null;
}

export interface RuntimeMemoryObservation {
  usage_bytes: number | null;
  limit_bytes: number | null;
}

interface RuntimeObservationBase {
  id: string;
  object: "agent.runtime_observation";
  session_id: string;
  resolved_at: number;
}

export interface RuntimeObservedObservation extends RuntimeObservationBase {
  environment_id: string;
  mode: "openai_hosted";
  provider_type: string | null;
  instance: {
    kind: "managed_allocation";
    allocation_id: string;
    device_id: string | null;
    connection_generation: null;
  };
  status: "observed";
  reason: null;
  allocation_created_at: number | null;
  observed_at: number;
  started_at: number | null;
  cpu: RuntimeCPUObservation | null;
  memory: RuntimeMemoryObservation | null;
}

export interface RuntimeUnavailableObservation extends RuntimeObservationBase {
  environment_id: string;
  mode: "openai_hosted";
  provider_type: string | null;
  instance: {
    kind: "managed_allocation";
    allocation_id: string | null;
    device_id: string | null;
    connection_generation: null;
  };
  status: "unavailable";
  reason: RuntimeUnavailableReason;
  allocation_created_at: number | null;
  observed_at: null;
  started_at: null;
  cpu: null;
  memory: null;
}

export interface RuntimeNoneObservation extends RuntimeObservationBase {
  environment_id: null;
  mode: "none";
  provider_type: null;
  instance: { kind: "none"; allocation_id: null; device_id: null; connection_generation: null };
  status: "unsupported";
  reason: "runtime_mode_not_observable";
  allocation_created_at: null;
  observed_at: null;
  started_at: null;
  cpu: null;
  memory: null;
}

export interface RuntimeSelfHostedObservation extends RuntimeObservationBase {
  environment_id: string;
  mode: "self_hosted";
  provider_type: string | null;
  instance: {
    kind: "self_hosted_connection";
    allocation_id: null;
    device_id: string | null;
    connection_generation: string | null;
  };
  status: "unsupported";
  reason: "runtime_mode_not_observable";
  allocation_created_at: null;
  observed_at: null;
  started_at: null;
  cpu: null;
  memory: null;
}

export type RuntimeObservation =
  | RuntimeObservedObservation
  | RuntimeUnavailableObservation
  | RuntimeNoneObservation
  | RuntimeSelfHostedObservation;

export interface RuntimeObservationList extends ListPage<RuntimeObservation> {
  object: "list";
  first_id: string | null;
  last_id: string | null;
}

export type RuntimeHistoryCollectionMode = "on_read" | "periodic";
export type RuntimeHistoryCapabilityReason = "not_configured" | "periodic_collection_required";
export type RuntimeHistoryMetric = "cpu" | "memory" | "tokens";

export interface RuntimeHistoryCapabilities {
  object: "agent.runtime_history_capabilities";
  available: boolean;
  reason: RuntimeHistoryCapabilityReason | null;
  collection_mode: RuntimeHistoryCollectionMode | null;
  sample_interval_seconds: number | null;
  retention_seconds: number | null;
  minimum_step_seconds: number | null;
  maximum_range_seconds: number | null;
  maximum_points: number | null;
  metrics: RuntimeHistoryMetric[];
}

export interface RuntimeHistoryQuery extends ReadOptions {
  /** Inclusive Unix-second boundary. */
  start: number;
  /** Exclusive Unix-second boundary. */
  end: number;
  /** Requested maximum buckets per series. Core selects the effective resolution. */
  maxPoints?: number;
}

export interface RuntimeHistoryRange {
  start: number;
  end: number;
}

export interface RuntimeHistoryCoveragePoint {
  start: number;
  end: number;
  first_observed_at: number | null;
  last_observed_at: number | null;
  observation_count: number;
  observed_count: number;
  unavailable_count: number;
}

export interface RuntimeHistoryCPU {
  contributor_count: number;
  utilization_ratio: number | null;
  capacity_cores: number | null;
}

export interface RuntimeHistoryMemory {
  contributor_count: number;
  usage_bytes: number | null;
  limit_bytes: number | null;
}

export interface RuntimeHistoryPoint extends RuntimeHistoryCoveragePoint {
  cpu: RuntimeHistoryCPU | null;
  memory: RuntimeHistoryMemory | null;
}

export interface RuntimeHistorySeries {
  environment_id: string;
  allocation_id: string;
  /** Earliest retained provider start estimate for compatible uptime display. */
  started_at: RuntimeHistoryTime;
  provider_type: string;
  points: RuntimeHistoryPoint[];
}

export interface RuntimeHistoryTime {
  seconds: number;
  nanoseconds: number;
}

export interface RuntimeHistoryCoverage {
  retained_start: number;
  first_sample_at: number | null;
  last_sample_at: number | null;
  sample_count: number;
  expected_sample_count: number;
  buckets: RuntimeHistoryCoveragePoint[];
}

export interface RuntimeHistoryTokenUsagePoint {
  start: number;
  end: number;
  sampled_at: number;
  input_tokens: number;
  output_tokens: number;
}

export interface RuntimeHistory {
  object: "agent.runtime_history";
  source: "durable";
  session_id: string;
  requested_range: RuntimeHistoryRange;
  resolution_seconds: number;
  generated_at: number;
  coverage: RuntimeHistoryCoverage;
  series: RuntimeHistorySeries[];
  token_usage: RuntimeHistoryTokenUsagePoint[];
}

export type CoreHarnessKind = "claude_sdk" | "codex" | "mcode";
export type CoreManagedSandboxProvider = "docker" | "microsandbox";

/** A complete replacement bundle. API keys are write-only. */
export interface ModelProviderInput {
  protocol: "anthropic" | "responses";
  base_url: string;
  api_key: string;
  context_window?: number;
  max_output_tokens?: number;
  api_key_configured?: never;
}

export interface ModelProviderView {
  protocol: "anthropic" | "responses";
  base_url: string;
  context_window?: number;
  max_output_tokens?: number;
  api_key_configured: boolean;
  api_key?: never;
}

/** Omitted members preserve saved defaults on update; null provider clears it. */
export interface SavedAgentCoreInput {
  harness?: CoreHarnessKind;
  model_provider?: ModelProviderInput | null;
}

export interface SavedAgentCore {
  harness?: CoreHarnessKind;
  model_provider?: ModelProviderView;
}

export interface AgentsCoreSelection {
  harness: CoreHarnessKind;
}

export type ExecutionConfigurationSource = "session" | "agent" | "deployment" | "unknown";

/** Immutable committed selections, not live execution health. */
export interface SessionExecutionConfiguration {
  object: "agent.session.execution_configuration";
  schema_version: 1;
  session_id: string;
  model: { value: string | null; source: ExecutionConfigurationSource };
  harness: { value: string | null; source: ExecutionConfigurationSource };
  model_provider: {
    source: ExecutionConfigurationSource;
    status: "available" | "redacted" | "unavailable";
    configuration: ModelProviderView | null;
  };
}

export interface CoreStartupConfiguration {
  object: "agents.core.startup_configuration";
  schema_version: 1;
  supported: {
    harnesses: CoreHarnessKind[];
    managed_sandbox_providers: CoreManagedSandboxProvider[];
  };
  configured: {
    default_harness: CoreHarnessKind;
    enabled_harnesses: CoreHarnessKind[];
    daemon_gateway: boolean;
    self_hosted: boolean;
    managed_sandbox: {
      enabled: boolean;
      provider: CoreManagedSandboxProvider | null;
      maintenance: boolean;
    };
    model_providers: Array<{
      harness: CoreHarnessKind;
      endpoint_configured: boolean;
    }>;
  };
}

export interface AgentCore {
  retrieveSessionExecutionConfiguration(sessionId: string, options?: ReadOptions): Promise<SessionExecutionConfiguration>;
  retrieveStartupConfiguration(options?: ReadOptions): Promise<CoreStartupConfiguration>;
  listAgents(options?: PageOptions): Promise<ListPage<SavedAgent>>;
  createAgent(input: CreateAgentInput): Promise<SavedAgent>;
  retrieveAgent(agentId: string): Promise<SavedAgent>;
  updateAgent(agentId: string, input: UpdateAgentInput): Promise<SavedAgent>;
  deleteAgent(agentId: string): Promise<AgentDeleted>;
  listVaults(options?: VaultListOptions): Promise<VaultList>;
  createVault(input: CreateVaultInput): Promise<Vault>;
  retrieveVault(vaultId: string, options?: ReadOptions): Promise<Vault>;
  deleteVault(vaultId: string): Promise<VaultDeleted>;
  listVaultCredentials(vaultId: string, options?: VaultListOptions): Promise<VaultCredentialList>;
  createVaultCredential(vaultId: string, input: CreateVaultCredentialInput): Promise<VaultCredential>;
  retrieveVaultCredential(vaultId: string, credentialId: string, options?: ReadOptions): Promise<VaultCredential>;
  replaceVaultCredentialToken(vaultId: string, credentialId: string, input: ReplaceVaultCredentialTokenInput): Promise<VaultCredential>;
  deleteVaultCredential(vaultId: string, credentialId: string): Promise<VaultCredentialDeleted>;
  listSessions(options?: PageOptions & { agentId?: string }): Promise<ListPage<AgentSession>>;
  listRuntimeObservations(options?: PageOptions): Promise<RuntimeObservationList>;
  retrieveRuntimeObservation(sessionId: string, options?: ReadOptions): Promise<RuntimeObservation>;
  getRuntimeHistoryCapabilities(options?: ReadOptions): Promise<RuntimeHistoryCapabilities>;
  retrieveRuntimeHistory(sessionId: string, query: RuntimeHistoryQuery): Promise<RuntimeHistory>;
  createSession(input: CreateSessionInput, idempotencyKey?: string): Promise<AgentSession>;
  createSessionStream(
    input: Omit<CreateSessionInput, "stream">,
    idempotencyKey: string | undefined,
    options: CreateSessionStreamOptions,
  ): Promise<void>;
  retrieveSession(sessionId: string, options?: ReadOptions): Promise<AgentSession>;
  retrieveEnvironment(environmentId: string, options?: ReadOptions): Promise<AgentEnvironmentResource>;
  listEnvironmentTemplates(options?: PageOptions & ReadOptions): Promise<EnvironmentTemplateList>;
  createEnvironmentTemplate(input: CreateEnvironmentTemplateInput, options?: ReadOptions): Promise<EnvironmentTemplate>;
  retrieveEnvironmentTemplate(templateId: string, options?: ReadOptions): Promise<EnvironmentTemplate>;
  updateEnvironmentTemplate(templateId: string, input: UpdateEnvironmentTemplateInput, options?: ReadOptions): Promise<EnvironmentTemplate>;
  deleteEnvironmentTemplate(templateId: string, options?: ReadOptions): Promise<EnvironmentTemplateDeleted>;
  listEnvironmentFiles(environmentId: string, options: EnvironmentFileListOptions): Promise<EnvironmentFileList>;
  createEnvironmentFile(environmentId: string, input: EnvironmentFileCreateInput, options?: ReadOptions): Promise<EnvironmentFile>;
  uploadSourceFile(input: SourceFileUploadInput, options?: ReadOptions): Promise<SourceFile>;
  retrieveSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFile>;
  downloadSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileContent>;
  deleteSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileDeleted>;
  updateSession(sessionId: string, metadata: Record<string, string> | null): Promise<AgentSession>;
  deleteSession(sessionId: string): Promise<SessionDeleted>;
  listItems(sessionId: string, options?: PageOptions & ReadOptions): Promise<ListPage<SessionItem>>;
  listTurns(sessionId: string, options?: PageOptions & ReadOptions): Promise<ListPage<AgentTurn>>;
  retrieveTurn(sessionId: string, turnId: string, options?: ReadOptions): Promise<AgentTurn>;
  submitEvents(sessionId: string, events: readonly SessionInputEvent[], idempotencyKey: string): Promise<void>;
  sendMessage(sessionId: string, text: string, idempotencyKey: string): Promise<void>;
  cancelTurn(sessionId: string, idempotencyKey: string): Promise<void>;
  submitFunctionResult(sessionId: string, input: FunctionResultInput, idempotencyKey: string): Promise<void>;
  streamEvents(sessionId: string, options: StreamOptions): Promise<void>;
}
