import type { CoreHarnessKind, CreateAgentInput, CreateSessionInput } from "@agents-core-web/agents-client";

/** Objects created from the workbench carry this tag so they can be told apart from callers' objects. */
export const WORKBENCH_METADATA = { created_by: "console-playground" } as const;

export type WorkbenchKind = "agent.create" | "session.create" | "session.message" | "object.retrieve";
export type LookupKind = "agent" | "session" | "vault" | "environment_template" | "environment" | "file";
export type SessionEnvironmentKind = "none" | "openai_hosted";

export interface WorkbenchForm {
  agent: { name: string; model: string; instructions: string; harness: CoreHarnessKind | "" };
  session: { agentId: string; environment: SessionEnvironmentKind; templateId: string; input: string };
  message: { sessionId: string; text: string };
  lookup: { kind: LookupKind; id: string };
}

export const emptyWorkbenchForm = (model: string): WorkbenchForm => ({
  agent: { name: "Playground Agent", model, instructions: "Be helpful and concise.", harness: "" },
  session: { agentId: "", environment: "none", templateId: "", input: "Hello" },
  message: { sessionId: "", text: "" },
  lookup: { kind: "session", id: "" },
});

export type WorkbenchProblem = "model" | "agent" | "input" | "session" | "text" | "id";

export interface WorkbenchRequest {
  method: "GET" | "POST";
  /** Path below the Agents API base URL, for example `/agents`. */
  path: string;
  /** `/v1/files*` and `/v1/skills*` are sent without the Agents Beta header. */
  beta: boolean;
  idempotencyKey?: string;
  body?: CreateAgentInput | CreateSessionInput | { events: unknown[] };
  bodyFile?: string;
  /** The first missing or invalid field; the request cannot be sent while set. */
  problem: WorkbenchProblem | null;
}

const lookupPaths: Record<LookupKind, { path: (id: string) => string; beta: boolean }> = {
  agent: { path: (id) => `/agents/${id}`, beta: true },
  session: { path: (id) => `/agents/sessions/${id}`, beta: true },
  vault: { path: (id) => `/vaults/${id}`, beta: true },
  environment_template: { path: (id) => `/agents/environments/templates/${id}`, beta: true },
  environment: { path: (id) => `/agents/environments/${id}`, beta: true },
  file: { path: (id) => `/files/${id}`, beta: false },
};

const placeholder = (name: string) => `{${name}}`;
const segment = (value: string, name: string) => (value.trim() ? encodeURIComponent(value.trim()) : placeholder(name));

export function buildWorkbenchRequest(kind: WorkbenchKind, form: WorkbenchForm, idempotencyKey: string): WorkbenchRequest {
  if (kind === "agent.create") {
    const { name, model, instructions, harness } = form.agent;
    const body: CreateAgentInput = {
      model: model.trim(),
      ...(name.trim() ? { name: name.trim() } : {}),
      ...(instructions.trim() ? { instructions: instructions.trim() } : {}),
      metadata: { ...WORKBENCH_METADATA },
      ...(harness ? { x_agents_core: { harness } } : {}),
    };
    return { method: "POST", path: "/agents", beta: true, body, bodyFile: "agent.json", problem: model.trim() ? null : "model" };
  }
  if (kind === "session.create") {
    const { agentId, environment, templateId, input } = form.session;
    const body: CreateSessionInput = {
      agent_id: agentId.trim(),
      environment: environment === "openai_hosted"
        ? { type: "openai_hosted", ...(templateId.trim() ? { environment_template_id: templateId.trim() } : {}) }
        : { type: "none" },
      ...(input.trim() ? { input: input.trim() } : {}),
      metadata: { ...WORKBENCH_METADATA },
    };
    // A Session without an environment starts from its first input.
    const problem = !agentId.trim() ? "agent" : environment === "none" && !input.trim() ? "input" : null;
    return { method: "POST", path: "/agents/sessions", beta: true, idempotencyKey, body, bodyFile: "session.json", problem };
  }
  if (kind === "session.message") {
    const { sessionId, text } = form.message;
    const body = { events: [{ type: "agent.session.input.message", input: [{ role: "user", content: [{ type: "input_text", text }] }] }] };
    const problem = !sessionId.trim() ? "session" : !text.trim() ? "text" : null;
    return { method: "POST", path: `/agents/sessions/${segment(sessionId, "session_id")}/events`, beta: true, idempotencyKey, body, bodyFile: "events.json", problem };
  }
  const target = lookupPaths[form.lookup.kind];
  return { method: "GET", path: target.path(segment(form.lookup.id, "id")), beta: target.beta, problem: form.lookup.id.trim() ? null : "id" };
}

/** Shell-safe single-quoted literal. */
function quote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

/**
 * The request as a copyable curl command. Credentials are always placeholders;
 * a paired console (`/v1`) does not know the caller's Core URL either.
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
  const lines = [
    `curl --request ${request.method} ${url}`,
    '  --header "Authorization: Bearer ${AGENTS_CORE_API_KEY}"',
    ...(request.body ? ['  --header "Content-Type: application/json"'] : []),
    ...(request.beta ? ['  --header "OpenAI-Beta: agents=v1"'] : []),
    ...(request.idempotencyKey ? [`  --header "Idempotency-Key: ${request.idempotencyKey}"`] : []),
    ...(request.body && request.bodyFile ? [`  --data @${request.bodyFile}`] : []),
  ];
  return lines.join(" \\\n");
}
