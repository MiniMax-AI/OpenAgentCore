import { createHash } from "node:crypto";
import { AppError } from "./store.mjs";
import { contributions } from "./capability-kinds.mjs";
import { harnessOptions } from "../shared/harness-options.mjs";

// Parsar authors Agents and environment templates; Core keeps them as saved
// Agents and Environment Templates, which Sessions reference by ID. Each Core
// resource is the projection of a Parsar record and of what it binds, and is
// rewritten whenever that projection's digest changes.

const digest = (value) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const withoutNulls = (value) => Object.fromEntries(Object.entries(value).filter(([, v]) => v !== null));

const askUserTool = {
  type: "function",
  name: "ask_user",
  description:
    "Ask the user a business question when essential information is missing. Never request execution permission or approval; execution is bypass-only.",
  parameters: {
    type: "object",
    properties: { question: { type: "string" }, options: { type: "array", items: { type: "string" } } },
    required: ["question", "options"],
    additionalProperties: false,
  },
};

// The saved Core Agent: model, instructions, tools and Harness. Cleared
// settings are sent as null so an update resets them.
function coreAgent(store, agent) {
  const model = store.get("models", agent.model_id);
  if (!model || model.enabled === false) throw new AppError(400, "Agent 绑定的资源已不可用。");
  if (!harnessOptions[agent.harness]) throw new AppError(400, "请选择执行引擎。");
  const bound = contributions(store, agent);
  const reasoning = agent.reasoning_effort || agent.reasoning_summary
    ? {
        ...(agent.reasoning_effort ? { effort: agent.reasoning_effort } : {}),
        ...(agent.reasoning_summary ? { summary: agent.reasoning_summary } : {}),
      }
    : null;
  return {
    name: agent.name.slice(0, 128),
    model: model.model,
    instructions: [agent.instructions, ...bound.agent.instructions].filter(Boolean).join("\n\n") || null,
    reasoning,
    service_tier: agent.service_tier || null,
    text: { verbosity: agent.verbosity || "medium" },
    tools: [
      { type: "web_search", mode: agent.web_search || "disabled" },
      ...(agent.ask_user ? [askUserTool] : []),
      ...bound.agent.tools,
    ],
    multi_agent: { enabled: false },
    metadata: { application: "parsar-example", parsar_agent_id: agent.id },
    x_agents_core: { harness: agent.harness },
  };
}

export const templateFiles = (runtime) =>
  (runtime.files || []).map(({ path, content }) => ({
    type: "inline",
    path,
    data: Buffer.from(content).toString("base64"),
  }));

// The saved Core Environment Template: packages, setup, variables and files.
function coreTemplate(runtime) {
  if (runtime.environment !== "openai_hosted")
    throw new AppError(400, "Parsar 仅支持托管工作环境，请重新配置环境模板。");
  return {
    name: runtime.name.slice(0, 256),
    packages: { npm: runtime.packages?.npm || [], python: runtime.packages?.python || [] },
    setup_commands: runtime.setup_script ? [{ command: runtime.setup_script, cwd: "/workspace" }] : [],
    env: { ...runtime.env },
    files: templateFiles(runtime),
  };
}

// The saved Agent and Template as an inline Session configuration, frozen when
// a conversation is created. Replacing its Session later uses this copy, so
// edits to the saved resources reach only new conversations.
export function frozenConfiguration(store, agent, runtime) {
  const { name: _agentName, metadata: _metadata, ...inline } = withoutNulls(coreAgent(store, agent));
  const { name: _templateName, ...template } = coreTemplate(runtime);
  return { agent: inline, template };
}

const targets = {
  agents: { field: "core_agent_id", path: "/v1/agents", body: coreAgent },
  runtimes: { field: "core_template_id", path: "/v1/agents/environments/templates", body: (_store, row) => coreTemplate(row) },
};

export function coreResources(store, core) {
  // Writes the Core resource when the record or anything it binds changed.
  // A resource missing in Core, as after moving to a new Project, is created again.
  const ensure = async (kind, id) => {
    const { field, path, body: build } = targets[kind];
    const row = store.get(kind, id);
    if (!row) throw new AppError(400, "请选择有效的资源。");
    const body = build(store, row);
    const print = digest(body);
    if (row[field] && row.core_digest === print) return row;
    let coreId = row[field];
    if (coreId)
      try {
        await core(`${path}/${coreId}`, "POST", body);
      } catch (error) {
        if (!(error instanceof AppError && error.status === 404)) throw error;
        coreId = undefined;
      }
    if (!coreId) coreId = (await core(path, "POST", withoutNulls(body))).id;
    const next = { ...store.get(kind, id), [field]: coreId, core_digest: print };
    store.put(kind, next);
    return next;
  };
  const remove = async (kind, row) => {
    const { field, path } = targets[kind];
    if (!row?.[field]) return;
    await core(`${path}/${row[field]}`, "DELETE").catch((error) => {
      if (!(error instanceof AppError && error.status === 404)) throw error;
    });
  };
  // Forget Core IDs that no longer resolve, so the next use creates them again.
  const verify = async () => {
    for (const kind of Object.keys(targets)) {
      const { field, path } = targets[kind];
      for (const row of store.list(kind).filter((row) => row[field]))
        await core(`${path}/${row[field]}`, "GET").catch((error) => {
          if (error instanceof AppError && error.status === 404) {
            const { [field]: _gone, core_digest: _old, ...rest } = store.get(kind, row.id);
            store.put(kind, rest);
          }
        });
    }
  };
  return { ensure, remove, verify };
}
