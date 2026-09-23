import { canonicalUuid, exactFields, isNonnegativeInteger, isRecord, onlyFields, sameResourceId } from "./response-projection";
import type { CoreConfigurationCapabilities, ExecutionConfigurationSource, ModelProviderView, SessionExecutionConfiguration } from "./types";

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

function exactList(value: unknown, expected: string[]): boolean {
  return Array.isArray(value) && value.length === expected.length && value.every((entry, index) => entry === expected[index]);
}

// Public declarations are projected explicitly; private adapter fields cannot
// become public accidentally when an internal capability object grows.
export function projectConfigurationCapabilities(value: unknown, invalid: Invalid): CoreConfigurationCapabilities {
  if (!isRecord(value) || !exactFields(value, new Set(["schema_version", "scope", "runtime_availability", "admission", "harnesses"])) ||
    value.schema_version !== 1 || value.scope !== "core_build_provider_configuration" || value.runtime_availability !== "unknown" ||
    !isRecord(value.admission) || !exactFields(value.admission, new Set(["credential_environment_types", "base_url", "token_limits"])) ||
    !Array.isArray(value.harnesses)) return invalid();
  const admission = value.admission;
  if (!exactList(admission.credential_environment_types, ["openai_hosted"]) ||
    !isRecord(admission.base_url) || !exactFields(admission.base_url, new Set(["schemes", "user_info", "query", "fragment"])) ||
    !exactList(admission.base_url.schemes, ["https"]) || admission.base_url.user_info !== false || admission.base_url.query !== false || admission.base_url.fragment !== false ||
    !isRecord(admission.token_limits) || !exactFields(admission.token_limits, new Set(["minimum", "max_output_not_above_context"])) ||
    admission.token_limits.minimum !== 0 || admission.token_limits.max_output_not_above_context !== true) return invalid();
  const harnesses: CoreConfigurationCapabilities["harnesses"] = [];
  for (const entry of value.harnesses) {
    if (!isRecord(entry) || !exactFields(entry, new Set(["harness", "support", "enabled", "default", "providers"])) ||
      typeof entry.harness !== "string" || !/^[a-z][a-z0-9_-]{0,63}$/u.test(entry.harness) ||
      (harnesses.length > 0 && harnesses[harnesses.length - 1]!.harness >= entry.harness) ||
      (entry.support !== "supported" && entry.support !== "unknown") || typeof entry.enabled !== "boolean" || typeof entry.default !== "boolean" ||
      !Array.isArray(entry.providers) || (entry.support === "unknown" && entry.providers.length !== 0) ||
      (entry.support === "supported" && entry.providers.length === 0)) return invalid();
    const providers: CoreConfigurationCapabilities["harnesses"][number]["providers"] = [];
    for (const provider of entry.providers) {
      if (!isRecord(provider) || !exactFields(provider, new Set(["protocol", "required_fields", "positive_fields"])) ||
        typeof provider.protocol !== "string" || !/^[a-z][a-z0-9_-]{0,63}$/u.test(provider.protocol) ||
        providers.some((p) => p.protocol === provider.protocol) || !Array.isArray(provider.required_fields) || !Array.isArray(provider.positive_fields) ||
        new Set(provider.required_fields).size !== provider.required_fields.length || new Set(provider.positive_fields).size !== provider.positive_fields.length ||
        !["protocol", "base_url", "api_key"].every((field) => (provider.required_fields as unknown[]).includes(field)) ||
        !provider.required_fields.every((field) => ["protocol", "base_url", "api_key", "context_window", "max_output_tokens"].includes(field)) ||
        !provider.positive_fields.every((field) => ["context_window", "max_output_tokens"].includes(field) && (provider.required_fields as unknown[]).includes(field))) return invalid();
      providers.push({ protocol: provider.protocol, required_fields: [...provider.required_fields], positive_fields: [...provider.positive_fields] });
    }
    harnesses.push({ harness: entry.harness, support: entry.support, enabled: entry.enabled, default: entry.default, providers });
  }
  return {
    schema_version: 1, scope: "core_build_provider_configuration", runtime_availability: "unknown",
    admission: { credential_environment_types: ["openai_hosted"], base_url: { schemes: ["https"], user_info: false, query: false, fragment: false }, token_limits: { minimum: 0, max_output_not_above_context: true } },
    harnesses,
  };
}
