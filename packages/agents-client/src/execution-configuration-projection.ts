import { canonicalUuid, exactFields, isNonnegativeInteger, isRecord, onlyFields, sameResourceId } from "./response-projection";
import type { ExecutionConfigurationSource, ModelProviderView, SessionExecutionConfiguration } from "./types";

type Invalid = () => never;
const sources = new Set(["session", "agent", "deployment", "unknown"]);
const selectionFields = new Set(["value", "source"]);
const providerFields = new Set(["protocol", "base_url", "api_key_configured", "context_window", "max_output_tokens"]);

function selection(value: unknown, invalid: Invalid): SessionExecutionConfiguration["model"] {
  if (!isRecord(value) || !exactFields(value, selectionFields) || !sources.has(String(value.source)) ||
    (value.value !== null && (typeof value.value !== "string" || value.value.length === 0)) ||
    (value.value === null && value.source !== "unknown")) return invalid();
  return { value: value.value as string | null, source: value.source as ExecutionConfigurationSource };
}

function safeProvider(value: unknown, invalid: Invalid): ModelProviderView {
  if (!isRecord(value) || !onlyFields(value, providerFields) ||
    (value.protocol !== "responses" && value.protocol !== "anthropic") ||
    typeof value.base_url !== "string" || typeof value.api_key_configured !== "boolean" ||
    (value.context_window !== undefined && !isNonnegativeInteger(value.context_window)) ||
    (value.max_output_tokens !== undefined && !isNonnegativeInteger(value.max_output_tokens)) ||
    Number(value.max_output_tokens ?? 0) > Number(value.context_window ?? 0)) return invalid();
  try {
    const url = new URL(value.base_url);
    if (url.protocol !== "https:" || !url.hostname || url.username || url.password || url.search || url.hash || /[\r\n\0]/u.test(value.base_url)) return invalid();
  } catch { return invalid(); }
  return {
    protocol: value.protocol, base_url: value.base_url, api_key_configured: value.api_key_configured,
    ...(value.context_window === undefined ? {} : { context_window: value.context_window as number }),
    ...(value.max_output_tokens === undefined ? {} : { max_output_tokens: value.max_output_tokens as number }),
  };
}

export function projectExecutionConfiguration(value: unknown, sessionId: string, invalid: Invalid): SessionExecutionConfiguration {
  if (!isRecord(value) || !exactFields(value, new Set(["object", "schema_version", "session_id", "model", "harness", "model_provider"])) ||
    value.object !== "agent.session.execution_configuration" || value.schema_version !== 1 ||
    typeof value.session_id !== "string" || !canonicalUuid(value.session_id) || !sameResourceId(value.session_id, sessionId) ||
    !isRecord(value.model_provider) || !exactFields(value.model_provider, new Set(["source", "status", "configuration"]))) return invalid();
  const provider = value.model_provider;
  let configuration: ModelProviderView | null = null;
  if (provider.status === "available" && (provider.source === "session" || provider.source === "agent")) {
    configuration = safeProvider(provider.configuration, invalid);
  } else if (!((provider.status === "redacted" && provider.source === "deployment") ||
    (provider.status === "unavailable" && provider.source === "unknown")) || provider.configuration !== null) return invalid();
  return {
    object: "agent.session.execution_configuration", schema_version: 1, session_id: value.session_id,
    model: selection(value.model, invalid), harness: selection(value.harness, invalid),
    model_provider: { source: provider.source as ExecutionConfigurationSource, status: provider.status as SessionExecutionConfiguration["model_provider"]["status"], configuration },
  };
}
