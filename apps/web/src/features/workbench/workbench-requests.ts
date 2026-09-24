import {
  isSkillId,
  type CoreHarnessKind,
  type CreateAgentInput,
  type CreateSessionInput,
  type EnvironmentTemplateResource,
  type SavedAgent,
  type SavedAgentToolInput,
} from "@agents-core-web/agents-client";

import { serializeAgentToolDrafts, validateAgentForm, type AgentFormValues, type AgentToolDraft } from "../agents/agent-form";
import { isCoreWhitespaceOnly, sessionAdmissionBlocker, sessionEnvironmentAdmissionBlocker } from "../agents/session-admission";
import { validateSessionMetadata } from "../sessions/actions/session-actions";
import { sessionCreateRequestPayload } from "../sessions/create/session-create-attempt";
import { sessionEnvironmentInput, type HostedNetworkChoice, type SessionEnvironmentType } from "../sessions/create/session-environment";
import { sessionInitialInputError } from "../sessions/create/session-initial-input";
import { optionalInitialSessionInput } from "../sessions/create/session-start-draft";
import { hostedNetworkNarrowingBlocker } from "../sessions/environment/environment-templates";
import { deriveSessionVaultPlan, type VaultCatalog } from "../vaults/vault-catalog";

/** Objects created from the workbench carry this tag so they can be told apart from callers' objects. */
export const WORKBENCH_METADATA = { created_by: "console-playground" } as const;
const RESERVED_METADATA_KEY = "created_by";

export type WorkbenchKind = "agent.create" | "session.create" | "session.message" | "object.retrieve";
export type LookupKind =
  | "agent"
  | "session"
  | "turn"
  | "execution_configuration"
  | "environment"
  | "environment_template"
  | "vault"
  | "credential"
  | "skill"
  | "file";

export const LOOKUP_KINDS: readonly LookupKind[] = [
  "agent", "session", "turn", "execution_configuration", "environment", "environment_template", "vault", "credential", "skill", "file",
];

export interface MetadataRow {
  key: string;
  value: string;
}

export interface WorkbenchForm {
  agent: {
    name: string;
    model: string;
    harness: CoreHarnessKind | "";
    instructions: string;
    tools: AgentToolDraft[];
    metadata: MetadataRow[];
  };
  session: {
    agentId: string;
    title: string;
    input: string;
    environment: SessionEnvironmentType;
    workspaceDirectory: string;
    templateId: string;
    network: HostedNetworkChoice;
    sandboxNodeId: string;
    /** Vaults chosen by the operator; Vaults a tool Credential requires are added by the plan. */
    vaultIds: string[];
    metadata: MetadataRow[];
  };
  message: { sessionId: string; text: string };
  lookup: { kind: LookupKind; id: string; parentId: string };
}

/** What the console knows beyond the form; the request builder stays pure over it. */
export interface WorkbenchContext {
  agents: readonly SavedAgent[];
  vaultCatalog: VaultCatalog | null;
  templates: readonly EnvironmentTemplateResource[];
  /** Environment types this console build may request. */
  environments: { self_hosted: boolean; openai_hosted: boolean };
}

export const emptyWorkbenchForm = (model: string): WorkbenchForm => ({
  agent: { name: "Playground Agent", model, harness: "", instructions: "Be helpful and concise.", tools: [], metadata: [] },
  session: {
    agentId: "",
    title: "",
    input: "Hello",
    environment: "none",
    workspaceDirectory: "/workspace",
    templateId: "",
    network: "default",
    sandboxNodeId: "",
    vaultIds: [],
    metadata: [],
  },
  message: { sessionId: "", text: "" },
  lookup: { kind: "session", id: "", parentId: "" },
});

/** The first field that blocks sending. `message` is already localized by a shared validator. */
export type WorkbenchProblemCode =
  | "model" | "name" | "tools" | "metadata" | "metadataKey" | "metadataDuplicate" | "metadataReserved"
  | "agent" | "environment" | "vaults" | "input"
  | "session" | "text"
  | "id" | "parent" | "fileId" | "skillId";

export interface WorkbenchProblem {
  code: WorkbenchProblemCode;
  message?: string;
}

/** A missing required value is an unfinished form, not an error. */
export const MISSING_VALUE_PROBLEMS: ReadonlySet<WorkbenchProblemCode> = new Set(["agent", "session", "text", "id", "parent"]);

export type WorkbenchBody = CreateAgentInput | Omit<CreateSessionInput, "stream"> | { events: WorkbenchMessageEvent[] };
interface WorkbenchMessageEvent {
  type: "agent.session.input.message";
  input: Array<{ role: "user"; content: Array<{ type: "input_text"; text: string }> }>;
}

export interface WorkbenchRequest {
  method: "GET" | "POST";
  /** Path below the Agents API base URL, for example `/agents`. */
  path: string;
  /** `/v1/files*` and `/v1/skills*` are sent without the Agents Beta header. */
  beta: boolean;
  idempotencyKey?: string;
  body?: WorkbenchBody;
  /** The first missing or invalid field; the request cannot be sent while set. */
  problem: WorkbenchProblem | null;
}

const problem = (code: WorkbenchProblemCode, message?: string): WorkbenchProblem => (message ? { code, message } : { code });
const placeholder = (name: string) => `{${name}}`;
const segment = (value: string, name: string) => (value.trim() ? encodeURIComponent(value.trim()) : placeholder(name));
const sourceFileIdPattern = /^file-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/**
 * Metadata rows as an object. Empty rows are ignored, keys are trimmed and the
 * workbench tag cannot be overridden; count and length limits are left to the
 * shared Agent and Session validators.
 */
export function metadataFromRows(rows: readonly MetadataRow[]): { metadata: Record<string, string>; problem: null } | { metadata: null; problem: WorkbenchProblem } {
  const metadata: Record<string, string> = { ...WORKBENCH_METADATA };
  const seen = new Set<string>();
  for (const row of rows) {
    const key = row.key.trim();
    if (!key && !row.value) continue;
    if (!key) return { metadata: null, problem: problem("metadataKey") };
    if (key === RESERVED_METADATA_KEY) return { metadata: null, problem: problem("metadataReserved") };
    if (seen.has(key)) return { metadata: null, problem: problem("metadataDuplicate") };
    seen.add(key);
    metadata[key] = row.value;
  }
  return { metadata, problem: null };
}

/** The Agent page's create rules, with the workbench tag in metadata. */
function agentRequest(form: WorkbenchForm["agent"], context: WorkbenchContext): { body: CreateAgentInput; problem: WorkbenchProblem | null } {
  const rows = metadataFromRows(form.metadata);
  const values: AgentFormValues = {
    name: form.name,
    harness: form.harness,
    harnessModified: false,
    model: form.model,
    instructions: form.instructions,
    metadata: JSON.stringify(rows.metadata ?? WORKBENCH_METADATA),
    reasoningEffort: "",
    reasoningSummary: "",
    serviceTier: "auto",
    textFormat: { type: "text" },
    textVerbosity: "medium",
    tools: form.tools,
    toolsModified: true,
  };
  const result = validateAgentForm(values, "create", context.vaultCatalog);
  // While a field is invalid nothing can be sent, but the preview keeps the
  // same shape, with the tools that did serialize.
  const fallbackTools = form.tools.length ? previewTools(form.tools, context.vaultCatalog) : undefined;
  const body = orderAgentBody(result.input as CreateAgentInput | undefined ?? {
    model: form.model.trim(),
    name: form.name.trim() || null,
    instructions: form.instructions.trim() || null,
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    metadata: rows.metadata ?? { ...WORKBENCH_METADATA },
    ...(fallbackTools ? { tools: fallbackTools } : {}),
    ...(form.harness ? { x_agents_core: { harness: form.harness } } : {}),
  });
  const failure = result.modelError ? problem("model", result.modelError)
    : result.nameError ? problem("name", result.nameError)
      : rows.problem ?? (result.metadataError ? problem("metadata", result.metadataError)
        : result.toolsError ? problem("tools", result.toolsError)
          : result.configurationError ? problem("tools", result.configurationError)
            : null);
  return { body, problem: failure };
}

/**
 * Tools as typed, for the preview while one of them does not validate: each
 * valid draft serializes exactly as it will be sent; an invalid one keeps its
 * shape with the values entered so far.
 */
function previewTools(drafts: readonly AgentToolDraft[], catalog: VaultCatalog | null): SavedAgentToolInput[] {
  return drafts.flatMap((draft): SavedAgentToolInput[] => {
    if (draft.kind === "read-only") return [];
    const single = serializeAgentToolDrafts([draft], catalog);
    if (!single.error) return single.tools;
    if (draft.kind === "function") {
      let parameters: unknown = draft.parameters;
      try {
        parameters = JSON.parse(draft.parameters);
      } catch {
        // Keep the text as typed; Send stays disabled until it parses.
      }
      return [{ type: "function", name: draft.name, description: draft.description, parameters: parameters as Record<string, unknown>, defer_loading: false }];
    }
    return [{
      type: "mcp",
      server_label: draft.serverLabel,
      transport: { type: "http", server_url: draft.serverUrl },
      connection_origin: "service",
      ...(draft.credentialId ? { credential_id: draft.credentialId } : {}),
      ...(draft.allowedToolsMode === "list" ? { allowed_tools: draft.allowedTools.split("\n").map((name) => name.trim()).filter(Boolean) } : {}),
      ...(draft.required === undefined ? {} : { required: draft.required }),
    }];
  });
}

/** Reads top to bottom like the form: basics, tools, generation, metadata. */
function orderAgentBody(input: CreateAgentInput): CreateAgentInput {
  const { model, name, instructions, x_agents_core: core, tools, reasoning, service_tier: tier, text, metadata, ...rest } = input;
  return {
    model,
    name,
    instructions,
    ...(core === undefined ? {} : { x_agents_core: core }),
    ...(tools === undefined ? {} : { tools }),
    ...(reasoning === undefined ? {} : { reasoning }),
    ...(tier === undefined ? {} : { service_tier: tier }),
    ...(text === undefined ? {} : { text }),
    ...(metadata === undefined ? {} : { metadata }),
    ...rest,
  };
}

/** The Session page's create rules: the same admission, environment, Vault, input and metadata checks. */
function sessionRequest(form: WorkbenchForm["session"], context: WorkbenchContext): { body: Omit<CreateSessionInput, "stream">; problem: WorkbenchProblem | null } {
  const agentId = form.agentId.trim();
  const agent = context.agents.find((candidate) => candidate.id === agentId);
  const template = context.templates.find((candidate) => candidate.id === form.templateId) ?? null;
  const environmentEnabled = form.environment === "none" || context.environments[form.environment];
  const environment = sessionEnvironmentInput(form.environment, form.workspaceDirectory, form.network, form.templateId || null);
  const input = optionalInitialSessionInput(form.input);
  const rows = metadataFromRows(form.metadata);
  const metadata = rows.metadata
    ? validateSessionMetadata({ title: form.title, metadata: JSON.stringify(rows.metadata) })
    : { metadata: undefined, metadataError: undefined };
  const plan = agent ? deriveSessionVaultPlan(agent, context.vaultCatalog, form.vaultIds) : null;
  const vaultIds = plan && !plan.blocker ? plan.vaultIds : [...new Set(form.vaultIds)].sort();
  const body = sessionCreateRequestPayload({
    agentId,
    environment: environment.input ?? previewEnvironment(form),
    ...(form.environment === "openai_hosted" && form.sandboxNodeId ? { sandboxNodeId: form.sandboxNodeId } : {}),
    ...(input === undefined ? {} : { input }),
    metadata: metadata.metadata ?? rows.metadata ?? { ...WORKBENCH_METADATA, ...(form.title.trim() ? { title: form.title.trim() } : {}) },
    stream: false,
    vaultIds,
  });

  let failure: WorkbenchProblem | null = null;
  if (!agentId) failure = problem("agent");
  else if (!agent) failure = problem("agent");
  else {
    const blocker = sessionAdmissionBlocker(agent, context.vaultCatalog);
    if (blocker) failure = problem("agent", blocker);
  }
  if (!failure && !environmentEnabled) failure = problem("environment");
  if (!failure && environment.error) failure = problem("environment", environment.error);
  if (!failure && agent && environment.input) {
    const blocker = sessionEnvironmentAdmissionBlocker(agent, environment.input.type)
      ?? hostedNetworkNarrowingBlocker(template, form.network === "default" ? null : form.network);
    if (blocker) failure = problem("environment", blocker);
  }
  if (!failure && plan?.blocker) failure = problem("vaults", plan.blocker);
  if (!failure) {
    const inputError = sessionInitialInputError(input, form.environment);
    if (inputError) failure = problem("input", inputError);
  }
  if (!failure && rows.problem) failure = rows.problem;
  if (!failure && metadata.metadataError) failure = problem("metadata", metadata.metadataError);
  return { body, problem: failure };
}

/** The environment as typed, for the preview while it does not validate yet. */
function previewEnvironment(form: WorkbenchForm["session"]): CreateSessionInput["environment"] {
  if (form.environment === "self_hosted") {
    return { type: "self_hosted", workspace_directory: form.workspaceDirectory, capability_directories: [] };
  }
  if (form.environment === "openai_hosted") {
    return {
      type: "openai_hosted",
      ...(form.templateId ? { environment_template_id: form.templateId } : {}),
      ...(form.network === "default" ? {} : { network: { access: form.network } }),
    };
  }
  return { type: "none" };
}

const lookupTargets: Record<LookupKind, { path: (id: string, parent: string) => string; beta: boolean; parent?: string }> = {
  agent: { path: (id) => `/agents/${id}`, beta: true },
  session: { path: (id) => `/agents/sessions/${id}`, beta: true },
  turn: { path: (id, parent) => `/agents/sessions/${parent}/turns/${id}`, beta: true, parent: "session_id" },
  execution_configuration: { path: (id) => `/agents/sessions/${id}/execution-configuration`, beta: true },
  environment: { path: (id) => `/agents/environments/${id}`, beta: true },
  environment_template: { path: (id) => `/agents/environments/templates/${id}`, beta: true },
  vault: { path: (id) => `/vaults/${id}`, beta: true },
  credential: { path: (id, parent) => `/vaults/${parent}/credentials/${id}`, beta: true, parent: "vault_id" },
  skill: { path: (id) => `/skills/${id}`, beta: false },
  file: { path: (id) => `/files/${id}`, beta: false },
};

/** The parent resource a lookup needs, if any. */
export function lookupParent(kind: LookupKind): "session" | "vault" | null {
  return kind === "turn" ? "session" : kind === "credential" ? "vault" : null;
}

const lookupIdName: Record<LookupKind, string> = {
  agent: "agent_id",
  session: "session_id",
  turn: "turn_id",
  execution_configuration: "session_id",
  environment: "environment_id",
  environment_template: "template_id",
  vault: "vault_id",
  credential: "credential_id",
  skill: "skill_id",
  file: "file_id",
};

function lookupProblem(form: WorkbenchForm["lookup"]): WorkbenchProblem | null {
  const target = lookupTargets[form.kind];
  if (target.parent && !form.parentId.trim()) return problem("parent");
  const id = form.id.trim();
  if (!id) return problem("id");
  // The client rejects these before sending, so the form says so first.
  if (form.kind === "file" && !sourceFileIdPattern.test(id)) return problem("fileId");
  if (form.kind === "skill" && !isSkillId(id)) return problem("skillId");
  return null;
}

export function buildWorkbenchRequest(kind: WorkbenchKind, form: WorkbenchForm, idempotencyKey: string, context: WorkbenchContext): WorkbenchRequest {
  if (kind === "agent.create") {
    const { body, problem: failure } = agentRequest(form.agent, context);
    return { method: "POST", path: "/agents", beta: true, body, problem: failure };
  }
  if (kind === "session.create") {
    const { body, problem: failure } = sessionRequest(form.session, context);
    return { method: "POST", path: "/agents/sessions", beta: true, idempotencyKey, body, problem: failure };
  }
  if (kind === "session.message") {
    const { sessionId, text } = form.message;
    const body = { events: [{ type: "agent.session.input.message" as const, input: [{ role: "user" as const, content: [{ type: "input_text" as const, text }] }] }] };
    const failure = !sessionId.trim() ? problem("session") : isCoreWhitespaceOnly(text) ? problem("text") : null;
    return { method: "POST", path: `/agents/sessions/${segment(sessionId, "session_id")}/events`, beta: true, idempotencyKey, body, problem: failure };
  }
  const target = lookupTargets[form.lookup.kind];
  return {
    method: "GET",
    path: target.path(segment(form.lookup.id, lookupIdName[form.lookup.kind]), segment(form.lookup.parentId, target.parent ?? "")),
    beta: target.beta,
    problem: lookupProblem(form.lookup),
  };
}

/** The exact JSON body Send transmits, pretty-printed. */
export function workbenchJson(request: WorkbenchRequest): string | null {
  return request.body === undefined ? null : JSON.stringify(request.body, null, 2);
}

/** Shell-safe single-quoted literal. */
function quote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

/**
 * The request as a copyable curl command with the body inline. Credentials are
 * always placeholders; a paired console (`/v1`) does not know the caller's Core
 * URL either.
 */
export function workbenchCurl(request: WorkbenchRequest, baseUrl: string): string {
  const base = baseUrl.trim().replace(/\/+$/, "");
  let url = `"\${AGENTS_CORE_BASE_URL}${request.path}"`;
  if (base !== "/v1") {
    try {
      const parsed = new URL(base);
      if (["http:", "https:"].includes(parsed.protocol) && !parsed.username && !parsed.password && !parsed.search && !parsed.hash) {
        url = quote(`${parsed.toString().replace(/\/+$/, "")}${request.path}`);
      }
    } catch {
      // Keep the placeholder for anything that is not a plain HTTP(S) origin.
    }
  }
  const json = workbenchJson(request);
  const lines = [
    `curl --request ${request.method} ${url}`,
    '  --header "Authorization: Bearer ${AGENTS_CORE_API_KEY}"',
    ...(request.beta ? ['  --header "OpenAI-Beta: agents=v1"'] : []),
    ...(json === null ? [] : ['  --header "Content-Type: application/json"']),
    ...(request.idempotencyKey ? [`  --header "Idempotency-Key: ${request.idempotencyKey}"`] : []),
    // JSON whitespace is insignificant, so the body is indented under --data.
    ...(json === null ? [] : [`  --data ${quote(json.replaceAll("\n", "\n  "))}`]),
  ];
  return lines.join(" \\\n");
}
