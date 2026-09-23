import type {
  InlineAgentInput,
  InputMessage,
  SavedAgent,
} from "@agents-core-web/agents-client";

import {
  serializeAgentToolDrafts,
  toolDraftsFromAgent,
  type AgentToolDraft,
  type TextVerbosity,
} from "../../agents/agent-form";
import {
  effectiveSessionAgent,
  isCoreWhitespaceOnly,
  sessionAdmissionBlocker,
} from "../../agents/session-admission";
import type { VaultCatalog } from "../../vaults/vault-catalog";
import { validateSessionMetadata } from "../actions/session-actions";
import {
  createSessionInitialInputDraft,
  projectSessionInitialInput,
  sessionInitialInputError,
  type SessionInitialInputDraft,
} from "./session-initial-input";

export type SessionAgentMode = "inline" | "saved";
export type SessionToolsMode = "clear" | "inherit" | "replace";

export interface SessionStartDetailsValues {
  title: string;
  metadata: string;
  initialInput: SessionInitialInputDraft;
  creationStream: boolean;
  inlineModel: string;
  inlineInstructions: string;
  inlineTools: AgentToolDraft[];
  overridesEnabled: boolean;
  overrideModelEnabled: boolean;
  overrideModel: string;
  overrideInstructionsEnabled: boolean;
  overrideInstructions: string;
  overrideTextEnabled: boolean;
  overrideTextVerbosity: TextVerbosity;
  resetMultiAgent: boolean;
  resetReasoning: boolean;
  resetServiceTier: boolean;
  toolsMode: SessionToolsMode;
  overrideTools: AgentToolDraft[];
}

export interface SessionStartRequestFields {
  metadata: Record<string, string>;
  input?: string | InputMessage[];
  agent?: InlineAgentInput;
  stream: boolean;
}

export interface SessionStartDetailsValidation {
  request?: SessionStartRequestFields;
  effectiveAgent?: SavedAgent;
  agentError?: string;
  inputError?: string;
  metadataError?: string;
}

export interface SessionAgentOverrideValidation {
  agent?: InlineAgentInput;
  effectiveAgent?: SavedAgent;
  overrideError?: string;
}

export interface SessionAgentSubmissionValidation {
  effectiveAgent?: SavedAgent;
  requestAgent?: InlineAgentInput;
  sourceAgent?: SavedAgent;
  error?: string;
}

export function sessionStartDetailsFromAgent(
  agent?: SavedAgent,
  catalog: VaultCatalog | null = null,
): SessionStartDetailsValues {
  return {
    title: "",
    metadata: "{}",
    initialInput: createSessionInitialInputDraft(),
    creationStream: false,
    inlineModel: "",
    inlineInstructions: "",
    inlineTools: [],
    overridesEnabled: false,
    overrideModelEnabled: false,
    overrideModel: agent?.model ?? "",
    overrideInstructionsEnabled: false,
    overrideInstructions: agent?.instructions ?? "",
    overrideTextEnabled: false,
    overrideTextVerbosity: agent?.text.verbosity ?? "medium",
    resetMultiAgent: false,
    resetReasoning: false,
    resetServiceTier: false,
    toolsMode: "inherit",
    overrideTools: toolDraftsFromAgent(agent, catalog),
  };
}

/** Whitespace-only text input is omitted; meaningful input retains exact bytes. */
export function optionalInitialSessionInput(value: string): string | undefined {
  return isCoreWhitespaceOnly(value) ? undefined : value;
}

function instructionsInput(value: string): string | null {
  const trimmed = value.trim();
  return trimmed || null;
}

function inlineAgentSnapshot(input: InlineAgentInput): SavedAgent {
  return {
    id: "inline",
    object: "agent",
    model: input.model ?? "",
    name: null,
    instructions: input.instructions ?? null,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: {},
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools: input.tools ?? [],
    created_at: 0,
    updated_at: 0,
  };
}

export function validateInlineSessionAgent(
  values: SessionStartDetailsValues,
  catalog: VaultCatalog | null,
): SessionAgentOverrideValidation {
  const model = values.inlineModel.trim();
  if (isCoreWhitespaceOnly(model)) {
    return { overrideError: "Enter a model ID for the inline Agent." };
  }
  const serialized = serializeAgentToolDrafts(values.inlineTools, catalog);
  if (serialized.error) return { overrideError: serialized.error };

  const instructions = instructionsInput(values.inlineInstructions);
  const agent: InlineAgentInput = {
    model,
    ...(instructions === null ? {} : { instructions }),
    ...(serialized.tools.length === 0 ? {} : { tools: serialized.tools }),
  };
  const effectiveAgent = inlineAgentSnapshot(agent);
  const blocker = sessionAdmissionBlocker(effectiveAgent, catalog);
  return blocker ? { overrideError: blocker } : { agent, effectiveAgent };
}

/** Projects only explicit whole-field overrides; every untouched field is omitted. */
export function validateSessionAgentOverrides(
  values: SessionStartDetailsValues,
  sourceAgent?: SavedAgent,
  catalog: VaultCatalog | null = null,
): SessionAgentOverrideValidation {
  if (!sourceAgent) {
    return { overrideError: "Select a saved Agent before configuring Session-only overrides." };
  }
  if (!values.overridesEnabled) {
    const blocker = sessionAdmissionBlocker(sourceAgent, catalog);
    return blocker
      ? { effectiveAgent: sourceAgent, overrideError: blocker }
      : { effectiveAgent: sourceAgent };
  }

  const agent: InlineAgentInput = {};
  if (values.overrideModelEnabled) {
    const model = values.overrideModel.trim();
    if (isCoreWhitespaceOnly(model)) return { overrideError: "Enter a non-empty Session model override." };
    agent.model = model;
  }
  if (values.overrideInstructionsEnabled) {
    agent.instructions = instructionsInput(values.overrideInstructions);
  }
  if (values.overrideTextEnabled) {
    agent.text = {
      format: { type: "text" },
      verbosity: values.overrideTextVerbosity,
    };
  }
  if (values.resetMultiAgent) agent.multi_agent = null;
  if (values.resetReasoning) agent.reasoning = null;
  if (values.resetServiceTier) agent.service_tier = null;
  if (values.toolsMode === "clear") {
    agent.tools = [];
  } else if (values.toolsMode === "replace") {
    const serialized = serializeAgentToolDrafts(values.overrideTools, catalog);
    if (serialized.error) return { overrideError: serialized.error };
    agent.tools = serialized.tools;
  }

  const requestAgent = Object.keys(agent).length > 0 ? agent : undefined;
  const effectiveAgent = effectiveSessionAgent(sourceAgent, requestAgent);
  const blocker = sessionAdmissionBlocker(effectiveAgent, catalog);
  return blocker
    ? { agent: requestAgent, effectiveAgent, overrideError: blocker }
    : { agent: requestAgent, effectiveAgent };
}

export function validateSessionStartDetails(
  values: SessionStartDetailsValues,
  mode: SessionAgentMode,
  sourceAgent?: SavedAgent,
  catalog: VaultCatalog | null = null,
  environmentType = "none",
): SessionStartDetailsValidation {
  const metadataResult = validateSessionMetadata({
    title: values.title,
    metadata: values.metadata,
  });
  const input = projectSessionInitialInput(values.initialInput);
  const inputError = input.ok ? sessionInitialInputError(input.input, environmentType) : input.error;
  const agent = mode === "inline"
    ? validateInlineSessionAgent(values, catalog)
    : validateSessionAgentOverrides(values, sourceAgent, catalog);
  if (!metadataResult.metadata || !input.ok || inputError || agent.overrideError || !agent.effectiveAgent) {
    return {
      ...(metadataResult.metadataError ? { metadataError: metadataResult.metadataError } : {}),
      ...(inputError ? { inputError } : {}),
      ...(agent.overrideError ? { agentError: agent.overrideError } : {}),
    };
  }

  return {
    effectiveAgent: agent.effectiveAgent,
    request: {
      metadata: metadataResult.metadata,
      ...(input.input === undefined ? {} : { input: input.input }),
      ...(agent.agent === undefined ? {} : { agent: agent.agent }),
      stream: values.creationStream || input.input !== undefined,
    },
  };
}

export function applySessionAgentOverrides(
  sourceAgent: SavedAgent,
  overrides?: InlineAgentInput,
): SavedAgent {
  return effectiveSessionAgent(sourceAgent, overrides);
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function onlyKeys(value: Record<string, unknown>, allowed: readonly string[]): boolean {
  return Object.keys(value).every((key) => allowed.includes(key));
}

function strictTools(value: unknown): value is InlineAgentInput["tools"] {
  return Array.isArray(value);
}

function strictInlineAgent(value: unknown): InlineAgentInput | null {
  const agent = record(value);
  if (!agent || !onlyKeys(agent, ["model", "instructions", "tools"])) return null;
  if (
    typeof agent.model !== "string"
    || isCoreWhitespaceOnly(agent.model)
    || agent.model !== agent.model.trim()
    || (Object.hasOwn(agent, "instructions") && agent.instructions !== null
      && (typeof agent.instructions !== "string" || !agent.instructions || agent.instructions !== agent.instructions.trim()))
    || (Object.hasOwn(agent, "tools") && !strictTools(agent.tools))
  ) return null;
  return agent as unknown as InlineAgentInput;
}

function strictSavedOverride(value: unknown): InlineAgentInput | null {
  const agent = record(value);
  if (!agent || Object.keys(agent).length === 0 || !onlyKeys(agent, [
    "model", "instructions", "multi_agent", "reasoning", "service_tier", "text", "tools",
  ])) return null;
  if (
    Object.hasOwn(agent, "model")
    && (typeof agent.model !== "string" || isCoreWhitespaceOnly(agent.model) || agent.model !== agent.model.trim())
  ) return null;
  if (
    Object.hasOwn(agent, "instructions")
    && agent.instructions !== null
    && (typeof agent.instructions !== "string" || !agent.instructions || agent.instructions !== agent.instructions.trim())
  ) return null;
  if (Object.hasOwn(agent, "multi_agent") && agent.multi_agent !== null) return null;
  if (Object.hasOwn(agent, "reasoning") && agent.reasoning !== null) return null;
  if (Object.hasOwn(agent, "service_tier") && agent.service_tier !== null) return null;
  if (Object.hasOwn(agent, "tools") && !strictTools(agent.tools)) return null;
  if (Object.hasOwn(agent, "text")) {
    const text = record(agent.text);
    const format = record(text?.format);
    if (
      !text
      || !onlyKeys(text, ["format", "verbosity"])
      || !format
      || Object.keys(format).length !== 1
      || format.type !== "text"
      || !["low", "medium", "high"].includes(String(text.verbosity))
    ) return null;
  }
  return agent as unknown as InlineAgentInput;
}

/** Revalidates the finite Web profile at the App boundary before any write. */
export function validateSessionAgentSubmission(
  mode: unknown,
  agentId: unknown,
  requestAgent: unknown,
  agents: readonly SavedAgent[],
  catalog: VaultCatalog | null,
): SessionAgentSubmissionValidation {
  if (mode === "inline") {
    if (agentId !== undefined) return { error: "Inline Session creation must not send agent_id." };
    const agent = strictInlineAgent(requestAgent);
    if (!agent) return { error: "The inline Agent request is outside the supported Web profile." };
    const effectiveAgent = inlineAgentSnapshot(agent);
    const blocker = sessionAdmissionBlocker(effectiveAgent, catalog);
    return blocker ? { error: blocker } : { effectiveAgent, requestAgent: agent };
  }

  if (mode !== "saved" || typeof agentId !== "string" || !agentId) {
    return { error: "Select a loaded saved Agent or use an inline Agent." };
  }
  const sourceAgent = agents.find((agent) => agent.id === agentId);
  if (!sourceAgent) return { error: "The selected saved Agent is not loaded." };
  let agent: InlineAgentInput | undefined;
  if (requestAgent !== undefined) {
    agent = strictSavedOverride(requestAgent) ?? undefined;
    if (!agent) return { error: "The Session-only Agent override is outside the supported Web profile." };
  }
  const effectiveAgent = effectiveSessionAgent(sourceAgent, agent);
  const blocker = sessionAdmissionBlocker(effectiveAgent, catalog);
  return blocker ? { error: blocker } : {
    effectiveAgent,
    ...(agent ? { requestAgent: agent } : {}),
    sourceAgent,
  };
}
