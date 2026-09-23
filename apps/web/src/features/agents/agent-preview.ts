import type { CreateAgentInput, UpdateAgentInput } from "@agents-core-web/agents-client";

import type { AgentFormValues } from "./agent-form";
import { validateAgentForm } from "./agent-form";

export const previewTokenPlaceholder = "${AGENTS_CORE_API_KEY}";

function shellSingleQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function previewEndpointArgument(baseUrl: string, agentId?: string): string {
  const candidate = (baseUrl.trim() || "/v1").replace(/\/+$/, "");
  const agentPath = `/agents${agentId ? `/${encodeURIComponent(agentId)}` : ""}`;
  if (candidate === "/v1") {
    return `"\${AGENTS_CORE_BASE_URL:-http://127.0.0.1:8091/v1}${agentPath}"`;
  }
  try {
    const url = new URL(candidate);
    if (url.username || url.password || url.search || url.hash || !["http:", "https:"].includes(url.protocol)) {
      return `"\${AGENTS_CORE_BASE_URL}${agentPath}"`;
    }
    return shellSingleQuote(`${url.toString().replace(/\/+$/, "")}${agentPath}`);
  } catch {
    return `"\${AGENTS_CORE_BASE_URL}${agentPath}"`;
  }
}

function safeMetadata(value: string): Record<string, string> {
  try {
    const parsed: unknown = value.trim() ? JSON.parse(value) : {};
    if (
      parsed &&
      !Array.isArray(parsed) &&
      typeof parsed === "object" &&
      Object.values(parsed).every((entry) => typeof entry === "string")
    ) return parsed as Record<string, string>;
  } catch {
    // The preview stays structurally valid while the form reports the parse error.
  }
  return {};
}

export function agentInputForPreview(values: AgentFormValues, intent: "create" | "update" = "create"): CreateAgentInput | UpdateAgentInput {
  const validated = validateAgentForm(values, intent).input;
  if (validated) return validated;

  const input: CreateAgentInput = {
    model: values.model.trim() || "<model-id>",
    name: values.name.trim() || null,
    instructions: values.instructions.trim() || null,
    metadata: safeMetadata(values.metadata),
    service_tier: values.serviceTier,
    text: { format: values.textFormat, verbosity: values.textVerbosity },
  };
  if (intent === "create") {
    if (values.harness) input.x_agents_core = { harness: values.harness };
  } else if (values.harnessModified) {
    input.x_agents_core = values.harness ? { harness: values.harness } : null;
  }
  if (values.reasoningEffort || values.reasoningSummary) {
    input.reasoning = {
      ...(values.reasoningEffort ? { effort: values.reasoningEffort } : {}),
      ...(values.reasoningSummary ? { summary: values.reasoningSummary } : {}),
    };
  }
  return input;
}

export function buildAgentRequestPreview(values: AgentFormValues, baseUrl: string, agentId?: string) {
  const input = agentInputForPreview(values, agentId ? "update" : "create");
  return {
    curl: [
      `curl --request POST ${previewEndpointArgument(baseUrl, agentId)} \\`,
      `  --header "Authorization: Bearer ${previewTokenPlaceholder}" \\`,
      '  --header "Content-Type: application/json" \\',
      '  --header "OpenAI-Beta: agents=v1" \\',
      "  --data @agent.json",
    ].join("\n"),
    json: JSON.stringify(input, null, 2),
  };
}
