import type { CoreHarnessKind, ModelProviderProtocol } from "./harness-catalog";
import type {
  AgentsCore, CreateVaultCredentialAuthParamStaticBearer, CreateVaultCredentialParams, CreateVaultParams,
  DeletedAgentResource, DeletedEnvironmentTemplateResource, DeletedSessionResource, DeletedSkillResource,
  DeletedSkillVersionResource, DeletedVaultCredentialResource, DeletedVaultResource, DeleteFileResponse,
  EnvironmentFileListResource, EnvironmentFileResource, EnvironmentPackagesResource, EnvironmentParamNone,
  EnvironmentParamSelfHosted, EnvironmentResourceSelfHosted, EnvironmentStatusResource, FunctionCallStatusResource,
  HostedEnvironmentFileParam, InputContentParam, InputContentParamInputText, InputMessageParam, ListOrderParam,
  McpOauthRefreshResource, MultiAgentConfigCurrentParam, MultiAgentConfigResource, NetworkAccessResource,
  NetworkPolicyResource, PersistedAgentToolConfigParamFunction, PersistedAgentToolConfigParamProgrammaticToolCalling,
  PersistedAgentToolConfigParamToolSearch, PersistedAgentToolConfigParamWebSearch, ReasoningEffortResource,
  ReasoningParam, ReasoningResource, ReasoningSummaryResource, RotateVaultCredentialAuthParamStaticBearer,
  RotateVaultCredentialParams, ServiceTierResource, SessionEnvironmentStateResource, SessionEnvironmentStatusResource,
  SessionErrorResource, SessionInputParam, SessionInputParamAgentSessionInputCancel,
  SessionInputParamAgentSessionInputMessage, SessionInputParamAgentSessionInputToolResult,
  SessionRequiredActionResource, SessionRequiredActionResourceEnvironmentConnection,
  SessionRequiredActionResourceFunctionCall, SessionStatusResource, SkillResource, SkillVersionResource,
  TextFormatResource, TextParam, TextResource, TokenUsageResource, TurnResource, TurnStatusResource,
  VaultCredentialAuthResourceMcpOauth, VaultCredentialAuthResourceStaticBearer, VaultCredentialResource,
  VaultResource, VaultStatusParam, WebSearchLocationParam,
} from "./generated/public-api";

// Generated types keep their schema names in ./generated/public-api; these are the client's names for them.
export type PageOrder = ListOrderParam;

/** The one cursor-list envelope; the schema repeats it for each resource. */
export interface ListPage<T> {
  object: "list";
  data: T[];
  first_id: string | null;
  last_id: string | null;
  has_more: boolean;
}

// Call options carry query parameters and a cancellation signal; they are not wire types.
export interface PageOptions extends ReadOptions {
  after?: string;
  limit?: number;
  order?: PageOrder;
}

export interface ReadOptions {
  signal?: AbortSignal;
}

export type AgentTextFormat = TextFormatResource;
export type AgentTextConfig = TextResource;
export type AgentTextInput = TextParam;
export type AgentServiceTier = ServiceTierResource;
export type AgentReasoningEffort = ReasoningEffortResource;
export type AgentReasoningSummary = ReasoningSummaryResource;
export type AgentReasoning = ReasoningParam;
export type MultiAgentInput = MultiAgentConfigCurrentParam;

export type FunctionToolInput = PersistedAgentToolConfigParamFunction;

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

export type ToolSearchInput = PersistedAgentToolConfigParamToolSearch;

export type ProgrammaticToolCallingInput = PersistedAgentToolConfigParamProgrammaticToolCalling;

export type WebSearchLocationInput = WebSearchLocationParam;

/**
 * Saved Agents keep every pinned mode; omitted or null `mode` is saved as `live`
 * and omitted or null `context_size` as `medium`. Session admission executes only
 * `disabled` and rejects the other modes unless the Session replaces its tools.
 */
export type WebSearchToolInput = PersistedAgentToolConfigParamWebSearch;

// The tool profiles Core saves, narrowed from the schema's tool params.
export type SavedAgentToolInput =
  | FunctionToolInput
  | ServiceHttpMcpToolInput
  | ToolSearchInput
  | ProgrammaticToolCallingInput
  | WebSearchToolInput;
// Session function tools load eagerly.
export type SessionFunctionToolInput = Omit<FunctionToolInput, "defer_loading"> & { defer_loading?: false };
export type ConfigurableAgentToolInput = SessionFunctionToolInput | ServiceHttpMcpToolInput;

export type VaultStatus = VaultStatusParam;

export interface VaultListOptions extends PageOptions {
  status?: VaultStatus | VaultStatus[];
}

export type Vault = VaultResource;

export type VaultList = ListPage<Vault>;

export type CreateVaultInput = CreateVaultParams;

export type VaultDeleted = DeletedVaultResource;

export type StaticBearerCredentialAuth = VaultCredentialAuthResourceStaticBearer;

export type McpOAuthRefreshMetadata = McpOauthRefreshResource;

export type McpOAuthCredentialAuth = VaultCredentialAuthResourceMcpOauth;

export type VaultCredential = VaultCredentialResource;

export type VaultCredentialList = ListPage<VaultCredential>;

// The client writes static bearer Credentials only; it never writes MCP OAuth Credentials.
export type CreateVaultCredentialInput = Omit<CreateVaultCredentialParams, "auth"> & {
  auth: CreateVaultCredentialAuthParamStaticBearer;
};

export type ReplaceVaultCredentialTokenInput = Omit<RotateVaultCredentialParams, "auth"> & {
  auth: RotateVaultCredentialAuthParamStaticBearer;
};

export type VaultCredentialDeleted = DeletedVaultCredentialResource;

/** The Agent resource with the client's SavedAgentCore view; Agent responses are not checked, so tools stay opaque. */
export interface SavedAgent {
  id: string;
  object: "agent";
  x_agents_core?: SavedAgentCore | null;
  model: string;
  name: string | null;
  instructions: string | null;
  metadata: Record<string, string>;
  multi_agent: MultiAgentConfigResource;
  reasoning: ReasoningResource;
  service_tier: AgentServiceTier;
  text: AgentTextConfig;
  tools: unknown[];
  created_at: number;
  updated_at: number;
}

/** Agent params narrowed to the tool profiles and extension inputs the client sends. */
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

/** Session Agent params narrowed to the tools a Session may configure. */
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

/** The Session's frozen Agent: the saved Agent without its resource metadata. */
export type AgentSnapshot = Omit<SavedAgent, "object" | "metadata" | "created_at" | "updated_at" | "x_agents_core"> & {
  x_agents_core?: AgentsCoreSelection | null;
};

declare const unknownEnvironmentType: unique symbol;
declare const unknownItemType: unique symbol;
declare const unknownSessionEventType: unique symbol;

export type NoneAgentEnvironment = EnvironmentParamNone;

export type SelfHostedAgentEnvironmentInput = EnvironmentParamSelfHosted;

export type OpenAIHostedNetworkAccess = "enabled" | "disabled";

/** Narrowed to the network mode and Template reference of the schema's hosted fields. */
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

export type SelfHostedAgentEnvironment = EnvironmentResourceSelfHosted;

export type AgentEnvironmentPackages = EnvironmentPackagesResource;

/**
 * Network policy of a hosted Environment or Template. `restricted` carries the
 * exact hostnames in Core's spelling and order; the other modes carry none.
 */
export type EnvironmentNetworkAccess = NetworkAccessResource;

export type EnvironmentNetworkPolicy = NetworkPolicyResource;

/**
 * Safe output of a managed Session Environment. A basic profile has only empty
 * lists; a Session created from an advanced Template also carries its frozen
 * installation metadata, kept as Core returned it (never env, setup commands or
 * file content).
 */
export interface OpenAIHostedAgentEnvironment {
  type: "openai_hosted";
  id: string;
  capability_directories: string[];
  network: EnvironmentNetworkPolicy;
  packages: AgentEnvironmentPackages;
  files: Record<string, unknown>[];
  plugins: Record<string, unknown>[];
  skills: Record<string, unknown>[];
}

// A Session Environment type this client does not know stays readable instead of failing the Session.
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

export type EnvironmentResourceStatus = EnvironmentStatusResource;

// The Environment resource split by type; hosted lists are always empty.
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

/** An initial Template file. Inline content is never returned, only its decoded size. */
export type EnvironmentTemplateFile =
  | { type: "inline"; path: string; size_bytes: number }
  | { type: "file_id"; path: string; file_id: string };

/**
 * A Template Skill. A reference keeps its unresolved selector: `null` selects the
 * Skill's default version when a Session is created, `"latest"` the latest
 * version, and a positive integer string that exact version.
 */
export type EnvironmentTemplateSkill =
  | { type: "skill_reference"; skill_id: string; version: string | null }
  | { type: "inline"; name: string; description: string };

/** A Template Plugin; only its descriptive metadata is returned. */
export interface EnvironmentTemplatePlugin {
  type: "inline";
  name: string;
  description: string;
}

/** The configuration sections of a Template response. */
export type EnvironmentTemplateSection =
  | "network"
  | "capability_directories"
  | "packages"
  | "files"
  | "plugins"
  | "skills";

export interface EnvironmentTemplateConfiguration {
  network: EnvironmentNetworkPolicy;
  /** Absolute directories within /workspace. */
  capability_directories: string[];
  packages: AgentEnvironmentPackages;
  files: EnvironmentTemplateFile[];
  plugins: EnvironmentTemplatePlugin[];
  skills: EnvironmentTemplateSkill[];
}

interface EnvironmentTemplateIdentity {
  id: string;
  object: "agent.environment.template";
  name: string | null;
  created_at: number;
  updated_at: number;
}

/**
 * Reusable hosted configuration whose complete safe metadata was recognized. A
 * Template never contains a running Workspace and never selects a provider
 * image. Core never returns `env`, `setup_commands` or inline file content, so
 * none of them can appear here.
 */
export interface EnvironmentTemplate extends EnvironmentTemplateIdentity, EnvironmentTemplateConfiguration {
  unrecognized?: undefined;
}

/**
 * A Template whose response contains configuration this client does not
 * recognize, for example a new file or Skill type. Only this Template is
 * affected: recognized sections are still projected, while an unrecognized
 * section is omitted instead of being guessed.
 */
export interface UnrecognizedEnvironmentTemplate
  extends EnvironmentTemplateIdentity, Partial<EnvironmentTemplateConfiguration> {
  /** Unrecognized sections, plus the names (never the values) of unexpected fields. */
  unrecognized: string[];
}

export type EnvironmentTemplateResource = EnvironmentTemplate | UnrecognizedEnvironmentTemplate;

export type EnvironmentTemplateList = ListPage<EnvironmentTemplateResource>;

export type EnvironmentTemplateDeleted = DeletedEnvironmentTemplateResource;

/** A Template network write. Core validates restricted hostnames. */
export type EnvironmentTemplateNetworkInput =
  | { access: OpenAIHostedNetworkAccess }
  | { access: "restricted"; allowed_domains: string[] };

/** Supported create fields. Omitted network stores the pinned enabled default. */
export interface CreateEnvironmentTemplateInput {
  name?: string | null;
  network?: EnvironmentTemplateNetworkInput | null;
}

/**
 * Supplied fields replace; omitted fields stay unchanged, so every other
 * configuration section is preserved. Null name clears the name and null
 * network resets the pinned enabled default.
 */
export type UpdateEnvironmentTemplateInput = CreateEnvironmentTemplateInput;

export type EnvironmentFile = EnvironmentFileResource;

/** Official token page: has_more is true exactly when next carries a token. */
export type EnvironmentFileList = EnvironmentFileListResource;

export interface EnvironmentFileListOptions extends ReadOptions {
  limit?: number;
  order?: PageOrder;
  page?: string;
  /** Absolute Environment path. Omission selects /workspace; self_hosted callers pass its exact workspace_directory. */
  path?: string;
}

export type EnvironmentFileCreateInput = HostedEnvironmentFileParam;

/** Narrowed to user_data Files; the pinned File schema types expires_at and status_details as optional, but Core sends null. */
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
  unrecognized?: undefined;
}

export type SourceFileDeleted = DeleteFileResponse;

/** Files list query. Core accepts limits of 1–10000 (default 10000) and orders newest first by default. */
export interface SourceFileListOptions extends ReadOptions {
  after?: string;
  limit?: number;
  order?: PageOrder;
  purpose?: "user_data";
}

/**
 * A listed File whose metadata this client does not recognize, for example a
 * purpose other than user_data. Only its ID is kept; nothing is guessed.
 */
export interface UnrecognizedSourceFile {
  id: string;
  object: "file";
  unrecognized: true;
}

export type SourceFileListEntry = SourceFile | UnrecognizedSourceFile;

export type SourceFileList = ListPage<SourceFileListEntry>;

// Binary and multipart shapes the JSON schema does not describe.
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

/**
 * A project-owned Skill. Top-level name and description follow the default
 * version; version numbers are positive integer strings that Core never reuses.
 */
export type Skill = SkillResource;

export type SkillList = ListPage<Skill>;

/** One immutable uploaded version of a Skill. */
export type SkillVersion = SkillVersionResource;

export type SkillVersionList = ListPage<SkillVersion>;

export type SkillDeleted = DeletedSkillResource;

/** Deleting the only remaining version also deletes its Skill. */
export type SkillVersionDeleted = DeletedSkillVersionResource;

/** Skill lists accept 0 through 100 entries; `after` is a Skill ID, or a version resource ID for version lists. */
export type SkillListOptions = PageOptions;

// Multipart upload and download shapes the JSON schema does not describe.
export interface SkillDirectoryFile {
  /** Relative path inside one top-level folder, for example `report/SKILL.md`. */
  path: string;
  file: Blob;
}

/** One ZIP in the `files` field, or a folder as repeated `files[]` fields. */
export type SkillUploadInput =
  | { kind: "zip"; file: Blob; filename: string }
  | { kind: "directory"; files: SkillDirectoryFile[] };

export interface SkillVersionUploadOptions extends ReadOptions {
  /** Sends the `default` form field once; omitted leaves the default pointer unchanged. */
  setDefault?: boolean;
}

/** A downloaded Skill bundle. */
export interface SkillContent {
  data: Blob;
  bytes: number;
  content_type: "application/octet-stream";
  content_disposition: string;
}

export type SessionStatus = SessionStatusResource;

export type FunctionCallAction = SessionRequiredActionResourceFunctionCall;

export type EnvironmentConnectionAction = SessionRequiredActionResourceEnvironmentConnection;

export type RequiredAction = SessionRequiredActionResource;

export type TokenUsage = TokenUsageResource;

/** The installation fields the projection guarantees; the extension schema marks them all optional. */
export interface EnvironmentInstallation {
  status: "available" | "unavailable";
  version: string;
  expires_at?: number;
  commands?: { posix: string; powershell: string };
  message?: string;
}

/** The Session resource composed with the client's Agent snapshot, Environment and installation projections. */
export interface AgentSession {
  x_agents_core?: { installation: EnvironmentInstallation };
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

/** Session list query. Pages hold 1–100 Sessions (default 20), newest first by default. */
export interface SessionListOptions extends PageOptions {
  /** Root Agent ID whose Sessions to return; omission lists every Agent. */
  agentId?: string;
}

/** Common preparation for both managed and user-owned Runtime locations. */
export interface EnvironmentCapabilityArchiveInput {
  type: "inline";
  name: string;
  description: string;
  source: { type: "base64"; media_type: "application/zip"; data: string };
}

// The extension schema types environment preparation as an open object.
export interface EnvironmentPreparationInput {
  environment_template_id?: string;
  env?: Record<string, string> | null;
  files?: EnvironmentFileCreateInput[] | null;
  packages?: { npm?: string[] | null; python?: string[] | null } | null;
  setup_commands?: { command: string; cwd?: string }[] | null;
  skills?: (EnvironmentCapabilityArchiveInput | { type: "skill_reference"; skill_id: string; version?: string | null })[] | null;
  plugins?: EnvironmentCapabilityArchiveInput[] | null;
  capability_directories?: string[] | null;
}

/** Session params composed with the client's Agent, Environment and extension inputs. */
export interface CreateSessionInput {
  x_agents_core?: { model_provider?: ModelProviderInput | null; harness_config?: HarnessConfig; environment?: EnvironmentPreparationInput };
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

export type InputTextContent = InputContentParamInputText;

export type InputMessage = InputMessageParam;

export type ItemStatus = FunctionCallStatusResource;

/** Every content part an Item can hold, merged so Web reads one shape. */
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

/** Every Item variant's fields merged into one shape, so Web reads them without narrowing on type. */
export interface SessionItemBase {
  id: string;
  turn_id: string;
  /** Inter-agent messages have no status; reasoning may have a null status. */
  status?: ItemStatus | null;
  role?: "user" | "assistant";
  /** Null on user messages and when the harness reports none. */
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
  action?: WebSearchAction | null;
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

/** The web search action variants merged into one shape, as on SessionItem. */
export interface WebSearchAction {
  type: "search" | "open_page" | "find_in_page" | "other";
  query?: string | null;
  queries?: string[] | null;
  url?: string | null;
  pattern?: string | null;
}

export type TurnStatus = TurnStatusResource;

export type AgentTurn = TurnResource;

export type StreamError = SessionErrorResource;

export type SessionEnvironmentStatus = SessionEnvironmentStatusResource;

export type SessionEnvironmentState = SessionEnvironmentStateResource;

/** Every projected event's fields merged into one shape, as on SessionItem. */
export interface SessionEventBase {
  event_id: string;
  session_id?: string;
  turn_id?: string;
  session?: AgentSession;
  turn?: AgentTurn;
  item?: SessionItem;
  item_id?: string;
  /** Null on Item events for input Items. */
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

/** Environment events happen outside any Turn, so their turn_id is null. */
export type AgentSessionEnvironmentEvent = {
  [Status in SessionEnvironmentStatus]: Omit<SessionEventBase, "turn_id"> & {
    type: `agent.session.environment.${Status}`;
    turn_id: null;
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

export type AgentDeleted = DeletedAgentResource;

export type SessionDeleted = DeletedSessionResource;

/** submitFunctionResult's input, which the client encodes as a tool_result event. */
export interface FunctionResultInput {
  callId: string;
  turnId: string;
  success: boolean;
  output?: string | FunctionResultContent[] | null;
  error?: string | null;
}

export type FunctionResultContent = InputContentParam;

export type SessionMessageInputEvent = SessionInputParamAgentSessionInputMessage;

export type SessionCancelInputEvent = SessionInputParamAgentSessionInputCancel;

export type SessionToolResultInputEvent = SessionInputParamAgentSessionInputToolResult;

export type SessionInputEvent = SessionInputParam;

// Callbacks for the event stream readers.
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
export type RuntimeLifecycleState = "active" | "sleeping" | "transitioning" | "pending" | "stopped";
export type RuntimeObservationReason =
  | "runtime_mode_not_observable"
  | "allocation_pending"
  | "runtime_not_running"
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
  lifecycle_state: RuntimeLifecycleState;
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
  lifecycle_state: RuntimeLifecycleState;
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
  lifecycle_state: null;
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
  lifecycle_state: null;
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

export type { CoreHarnessKind, ModelProviderProtocol } from "./harness-catalog";

/** A complete replacement bundle. API keys are write-only. */
export interface ModelProviderInput {
  protocol: ModelProviderProtocol;
  base_url: string;
  api_key: string;
  context_window?: number;
  max_output_tokens?: number;
  api_key_configured?: never;
}

export interface ModelProviderView {
  protocol: ModelProviderProtocol;
  base_url: string;
  context_window?: number;
  max_output_tokens?: number;
  api_key_configured: boolean;
  api_key?: never;
}

/** A harness's deployment default model provider in Core. The key is never returned. */
export type ProviderObservationErrorCode = "authentication_error" | "connection_failed" | "rate_limit_exceeded" | "usage_limit_exceeded" | "server_overloaded" | "server_error" | "resource_not_found" | "request_timeout" | "invalid_request";

/** Native model parameters validated by the selected harness. An empty object clears them. */
export type HarnessConfig = Record<string, unknown>;

export interface ModelConfigurationInput {
  model_provider: ModelProviderInput;
  model: string;
  harness_config?: HarnessConfig;
}

export interface ModelConfigurationView {
  model_provider: ModelProviderView;
  model: string;
  harness_config: HarnessConfig;
}

export interface HarnessModelConfiguration extends ModelConfigurationView {
  last_used_at: string | null;
  last_error_code: ProviderObservationErrorCode | null;
  last_error_at: string | null;
  object: "core.model_configuration";
  harness: CoreHarnessKind;
  updated_at: string;
}

/** Adapter build support, independent of runtime readiness or model availability. */
export interface ModelConfigurationSupport {
  /** Ordered supported native protocols; the first is the default. */
  protocols: ModelProviderInput["protocol"][];
  accepts_harness_config: boolean;
  token_limits_required: boolean;
}

/**
 * A harness this Core build supports. `enabled` and `default` are read-only views of
 * the process configuration; `model_configuration` is the deployment default, or null.
 */
export interface CoreHarness {
  object: "core.harness";
  id: CoreHarnessKind;
  enabled: boolean;
  default: boolean;
  model_configuration: HarnessModelConfiguration | null;
  model_configuration_support: ModelConfigurationSupport;
}

/** Native parameter inheritance follows the extension contract; null provider clears it. */
export interface SavedAgentCoreInput {
  harness?: CoreHarnessKind;
  model_provider?: ModelProviderInput | null;
  harness_config?: HarnessConfig;
}

export interface SavedAgentCore {
  harness?: CoreHarnessKind;
  model_provider?: ModelProviderView;
  harness_config?: HarnessConfig;
}

export type AgentsCoreSelection = AgentsCore;

export type ExecutionConfigurationSource = "session" | "agent" | "deployment" | "unknown";

/** Immutable committed selections, not live execution health. */
export interface SessionExecutionConfiguration {
  object: "agent.session.execution_configuration";
  schema_version: 1;
  session_id: string;
  model: { value: string | null; source: ExecutionConfigurationSource };
  harness: { value: string | null; source: ExecutionConfigurationSource };
  harness_config: { value: HarnessConfig; source: ExecutionConfigurationSource };
  model_provider: {
    source: ExecutionConfigurationSource;
    status: "available" | "redacted" | "unavailable";
    configuration: ModelProviderView | null;
  };
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
  listSessions(options?: SessionListOptions): Promise<ListPage<AgentSession>>;
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
  retrieveEnvironmentTemplate(templateId: string, options?: ReadOptions): Promise<EnvironmentTemplateResource>;
  updateEnvironmentTemplate(templateId: string, input: UpdateEnvironmentTemplateInput, options?: ReadOptions): Promise<EnvironmentTemplateResource>;
  deleteEnvironmentTemplate(templateId: string, options?: ReadOptions): Promise<EnvironmentTemplateDeleted>;
  listEnvironmentFiles(environmentId: string, options: EnvironmentFileListOptions): Promise<EnvironmentFileList>;
  createEnvironmentFile(environmentId: string, input: EnvironmentFileCreateInput, options?: ReadOptions): Promise<EnvironmentFile>;
  listSourceFiles(options?: SourceFileListOptions): Promise<SourceFileList>;
  uploadSourceFile(input: SourceFileUploadInput, options?: ReadOptions): Promise<SourceFile>;
  retrieveSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFile>;
  downloadSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileContent>;
  deleteSourceFile(fileId: string, options?: ReadOptions): Promise<SourceFileDeleted>;
  listSkills(options?: SkillListOptions): Promise<SkillList>;
  retrieveSkill(skillId: string, options?: ReadOptions): Promise<Skill>;
  uploadSkill(input: SkillUploadInput, options?: ReadOptions): Promise<Skill>;
  updateSkillDefaultVersion(skillId: string, version: string, options?: ReadOptions): Promise<Skill>;
  deleteSkill(skillId: string, options?: ReadOptions): Promise<SkillDeleted>;
  downloadSkill(skillId: string, options?: ReadOptions): Promise<SkillContent>;
  listSkillVersions(skillId: string, options?: SkillListOptions): Promise<SkillVersionList>;
  retrieveSkillVersion(skillId: string, version: string, options?: ReadOptions): Promise<SkillVersion>;
  uploadSkillVersion(skillId: string, input: SkillUploadInput, options?: SkillVersionUploadOptions): Promise<SkillVersion>;
  deleteSkillVersion(skillId: string, version: string, options?: ReadOptions): Promise<SkillVersionDeleted>;
  downloadSkillVersion(skillId: string, version: string, options?: ReadOptions): Promise<SkillContent>;
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
