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
  connection_origin: "service";
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

export type SavedAgentToolInput = FunctionToolInput | ServiceHttpMcpToolInput | ToolSearchInput | ProgrammaticToolCallingInput;
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
  model?: string;
  instructions?: string | null;
  tools?: ConfigurableAgentToolInput[] | null;
  text?: AgentTextInput | null;
  reasoning?: AgentReasoning | null;
  service_tier?: AgentServiceTier | null;
  multi_agent?: MultiAgentInput | null;
}

export type AgentSnapshot = Omit<SavedAgent, "object" | "metadata" | "created_at" | "updated_at">;

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

export interface EnvironmentFileList {
  data: EnvironmentFile[];
  next: string | null;
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
  phase?: "commentary" | "final_answer";
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
  agent_id: string;
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
  output_index?: number;
  content_index?: number;
  part?: ItemContent;
  delta?: string;
  text?: string;
  error?: StreamError;
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

export type SessionEvent = AgentSessionEnvironmentEvent | KnownSessionEvent | UnknownSessionEvent;

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

export interface AgentCore {
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
