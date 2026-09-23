import { AgentCoreError, type AgentCore, type CreateAgentInput, type SavedAgent } from "@agents-core-web/agents-client";
import { listAllCollectionPages } from "../../lib/collection-pagination";
import { isValidDirectCoreBaseUrl } from "../../lib/connection";
import { validateAgentForm, valuesFromAgent, type AgentFormValues } from "../agents/agent-form";

export const HOME_EXAMPLE_MARKER = "core_home_example";
export type ExampleClient = Pick<AgentCore, "createAgent" | "listAgents">;
export type ExamplePhase = "idle" | "checking" | "creating" | "waiting" | "complete";
export interface ExampleState { phase: ExamplePhase; agent: SavedAgent | null; error: "read" | "rejected" | "uncertain" | null }
export interface ExampleIdentity { marker: string; submitted: boolean }

export function loadExampleIdentity(scope: string): ExampleIdentity {
  try {
    const stored: unknown = JSON.parse(sessionStorage.getItem(`core-home-example:${scope}`) ?? "null");
    if (stored && typeof stored === "object" && "marker" in stored && typeof stored.marker === "string"
      && /^[a-f0-9-]{36}$/.test(stored.marker) && "submitted" in stored && typeof stored.submitted === "boolean") {
      return { marker: stored.marker, submitted: stored.submitted };
    }
  } catch { /* Storage is optional; the current mount still owns one attempt. */ }
  return { marker: crypto.randomUUID(), submitted: false };
}

export function saveExampleIdentity(scope: string, identity: ExampleIdentity) {
  try { sessionStorage.setItem(`core-home-example:${scope}`, JSON.stringify(identity)); }
  catch { /* Never persist credentials or block the current-page example. */ }
}

export function exampleForm(model: string, marker: string): AgentFormValues {
  return { ...valuesFromAgent(), name: "My first Agent", model, instructions: "Be helpful and concise.", metadata: JSON.stringify({ [HOME_EXAMPLE_MARKER]: marker }) };
}

export function exampleInput(values: AgentFormValues): CreateAgentInput | null {
  const input = validateAgentForm(values).input;
  return input?.model ? { ...input, model: input.model } : null;
}

export function findExampleAgent(agents: SavedAgent[], marker: string): SavedAgent | null {
  const matches = agents.filter((agent) => agent.metadata?.[HOME_EXAMPLE_MARKER] === marker);
  if (matches.length > 1) throw new Error("The example matched more than one Agent.");
  return matches[0] ?? null;
}

function pythonLiteral(value: unknown, depth = 0): string {
  if (value === null) return "None";
  if (value === true) return "True";
  if (value === false) return "False";
  if (typeof value !== "object") return JSON.stringify(value);
  const entries = Array.isArray(value) ? value.map((item) => pythonLiteral(item, depth + 1))
    : Object.entries(value).filter(([, item]) => item !== undefined).map(([key, item]) => `${JSON.stringify(key)}: ${pythonLiteral(item, depth + 1)}`);
  const [open, close] = Array.isArray(value) ? ["[", "]"] : ["{", "}"];
  return entries.length ? `${open}\n${entries.map((entry) => `${"    ".repeat(depth + 1)}${entry}`).join(",\n")}\n${"    ".repeat(depth)}${close}` : `${open}${close}`;
}

export function terminalExample(input: CreateAgentInput, baseUrl: string): string | null {
  if (!isValidDirectCoreBaseUrl(baseUrl)) return null;
  const endpoint = `${baseUrl.trim().replace(/\/+$/, "")}/agents`;
  // Always replace a write-only model key before rendering any request code.
  const provider = input.x_agents_core?.model_provider;
  const safeInput = provider ? { ...input, x_agents_core: { ...input.x_agents_core, model_provider: { ...provider, api_key: null } } } : input;
  return [
    "python3 - <<'PY'",
    "import getpass, json, os",
    "from urllib.request import HTTPRedirectHandler, Request, build_opener",
    "",
    "class NoRedirect(HTTPRedirectHandler):",
    "    def redirect_request(self, *args, **kwargs):",
    "        return None",
    "",
    `payload = ${pythonLiteral(safeInput)}`,
    "",
    'core_key = os.environ.get("CORE_API_KEY") or getpass.getpass("Core API key: ")',
    ...(provider ? ['payload["x_agents_core"]["model_provider"]["api_key"] = (', '    os.environ.get("MODEL_API_KEY") or getpass.getpass("Model API key: ")', ')'] : []),
    `request = Request(${JSON.stringify(endpoint)},`,
    '    data=json.dumps(payload).encode(),',
    '    headers={"Authorization": "Bearer " + core_key,',
    '             "Content-Type": "application/json", "OpenAI-Beta": "agents=v1"},',
    '    method="POST")',
    'with build_opener(NoRedirect()).open(request) as response:',
    '    agent = json.load(response)',
    'print("Agent created:", agent["id"])',
    "PY",
  ].join("\n");
}

/** Owns one example write. Reads may repeat; an uncertain write never does. */
export function createExampleRequest(client: ExampleClient, identity: ExampleIdentity, onChange: (state: ExampleState) => void, onSubmitted: (submitted: boolean) => void) {
  let state: ExampleState = { phase: identity.submitted ? "waiting" : "idle", agent: null, error: null };
  let live = true;
  let reading = false;
  const controller = new AbortController();
  const publish = (next: ExampleState) => { if (live) { state = next; onChange(next); } };
  const lookup = async () => findExampleAgent(await listAllCollectionPages((options) => client.listAgents(options), controller.signal), identity.marker);
  const finish = (agent: SavedAgent) => publish({ phase: "complete", agent, error: null });
  return {
    getState: () => state,
    observeExternal() {
      if (!live || state.phase !== "idle") return;
      onSubmitted(true);
      publish({ phase: "waiting", agent: null, error: null });
    },
    async reconcile() {
      if (!live || reading || state.phase === "creating" || state.phase === "checking" || state.phase === "complete") return;
      reading = true;
      try {
        const agent = await lookup();
        if (agent) finish(agent);
        else if (live && state.error === "read") publish({ ...state, error: null });
      } catch { if (live && !state.error) publish({ ...state, error: "read" }); }
      finally { reading = false; }
    },
    async run(input: CreateAgentInput) {
      if (!live || state.phase !== "idle") return;
      publish({ phase: "checking", agent: null, error: null });
      try {
        const existing = await lookup();
        if (!live || state.agent) return;
        if (existing) { finish(existing); return; }
      } catch { publish({ phase: "idle", agent: null, error: "read" }); return; }
      if (!live) return;
      onSubmitted(true);
      publish({ phase: "creating", agent: null, error: null });
      try {
        const agent = await client.createAgent(input);
        if (!agent.id || agent.object !== "agent" || agent.metadata?.[HOME_EXAMPLE_MARKER] !== identity.marker) throw new Error("Unexpected Agent response.");
        finish(agent);
      } catch (error) {
        if (!live) return;
        if (error instanceof AgentCoreError && [400, 401, 403, 404, 422].includes(error.status)) {
          onSubmitted(false);
          publish({ phase: "idle", agent: null, error: "rejected" });
        } else publish({ phase: "waiting", agent: null, error: "uncertain" });
      }
    },
    dispose() { live = false; controller.abort(); },
  };
}
