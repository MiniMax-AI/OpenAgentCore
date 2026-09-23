import { isLocalProxyBaseUrl, isValidDirectCoreBaseUrl } from "./connection";

export type CoreProbeKind =
  | "authenticated"
  | "invalid_configuration"
  | "unauthorized"
  | "protocol_mismatch"
  | "http_error"
  | "unreachable";

export interface CoreProbeResult {
  kind: CoreProbeKind;
  executionReadiness: "unknown";
  httpStatus?: number;
}

export interface CoreProbeOptions {
  baseUrl: string;
  token?: string;
  signal?: AbortSignal;
  fetch?: typeof fetch;
  timeoutMs?: number;
}

function trimTrailingSlash(value: string): string {
  return (value.trim() || "/v1").replace(/\/+$/, "");
}

export function coreProbeUrl(baseUrl: string): string {
  return `${trimTrailingSlash(baseUrl)}/agents?limit=1`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function isNullableString(value: unknown): value is string | null {
  return value === null || typeof value === "string";
}

function isStringRecord(value: unknown): boolean {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}

function isCanonicalReasoning(value: unknown): boolean {
  if (!isRecord(value)) return false;
  const efforts = new Set(["none", "minimal", "low", "medium", "high", "xhigh", "max"]);
  const summaries = new Set(["concise", "detailed", "auto"]);
  return (
    (value.effort === undefined || value.effort === null || (typeof value.effort === "string" && efforts.has(value.effort))) &&
    (value.summary === undefined || value.summary === null || (typeof value.summary === "string" && summaries.has(value.summary)))
  );
}

function isCanonicalText(value: unknown): boolean {
  if (!isRecord(value) || !isRecord(value.format)) return false;
  const format = value.format;
  const validFormat = format.type === "text" || (format.type === "json_schema" && isRecord(format.schema));
  return validFormat && typeof value.verbosity === "string" && ["low", "medium", "high"].includes(value.verbosity);
}

function isCanonicalMultiAgent(value: unknown): boolean {
  if (!isRecord(value) || typeof value.enabled !== "boolean") return false;
  if (!value.enabled) return value.max_concurrent_subagents === null;
  return (
    Number.isSafeInteger(value.max_concurrent_subagents) &&
    Number(value.max_concurrent_subagents) > 0 &&
    Number(value.max_concurrent_subagents) <= 4_294_967_295
  );
}

function isCanonicalSavedAgentTool(value: unknown): boolean {
  return isRecord(value) && typeof value.type === "string" && value.type.length > 0;
}

function isCanonicalSavedAgent(value: unknown): value is Record<string, unknown> & { id: string } {
  if (!isRecord(value)) return false;
  return (
    typeof value.id === "string" && value.id.length > 0 &&
    value.object === "agent" &&
    typeof value.model === "string" &&
    isNullableString(value.name) &&
    isNullableString(value.instructions) &&
    isStringRecord(value.metadata) &&
    isCanonicalMultiAgent(value.multi_agent) &&
    isCanonicalReasoning(value.reasoning) &&
    typeof value.service_tier === "string" &&
    ["auto", "default", "flex", "priority", "fast"].includes(value.service_tier) &&
    isCanonicalText(value.text) &&
    Array.isArray(value.tools) && value.tools.every(isCanonicalSavedAgentTool) &&
    Number.isSafeInteger(value.created_at) &&
    Number.isSafeInteger(value.updated_at)
  );
}

function isAgentListPage(value: unknown): boolean {
  if (!isRecord(value) || value.object !== "list" || !Array.isArray(value.data)) return false;
  if (typeof value.has_more !== "boolean" || !isNullableString(value.first_id) || !isNullableString(value.last_id)) {
    return false;
  }
  if (!value.data.every(isCanonicalSavedAgent)) return false;
  if (value.data.length === 0) return value.first_id === null && value.last_id === null && !value.has_more;
  return value.first_id === value.data[0]?.id && value.last_id === value.data.at(-1)?.id;
}

interface ProbeError {
  type?: unknown;
  code?: unknown;
}

async function readError(response: Response): Promise<ProbeError | undefined> {
  const envelope: unknown = await response.json();
  if (!isRecord(envelope) || !isRecord(envelope.error)) return undefined;
  return envelope.error;
}

// Core reports a rejected Agents API caller as invalid_request_error with a
// null code, as the official service does; older Core used invalid_api_key.
// Other 401 bodies, such as an intermediary's login, are not Core's answer.
function isCoreUnauthorized(error: ProbeError | undefined): boolean {
  return error?.code === "invalid_api_key" || (error?.type === "invalid_request_error" && error.code === null);
}

function classifyBodyReadFailure(
  error: unknown,
  callerSignal: AbortSignal | undefined,
  deadlineSignal: AbortSignal,
): "invalid_json" | "unreachable" {
  if (callerSignal?.aborted) throw error;
  if (deadlineSignal.aborted) return "unreachable";
  return error instanceof SyntaxError ? "invalid_json" : "unreachable";
}

function probeSignal(callerSignal: AbortSignal | undefined, timeoutMs: number) {
  const controller = new AbortController();
  const abortFromCaller = () => controller.abort(callerSignal?.reason);
  if (callerSignal?.aborted) abortFromCaller();
  else callerSignal?.addEventListener("abort", abortFromCaller, { once: true });
  const timer = globalThis.setTimeout(
    () => controller.abort(new DOMException("Core connection probe timed out", "TimeoutError")),
    timeoutMs,
  );
  return {
    signal: controller.signal,
    dispose() {
      globalThis.clearTimeout(timer);
      callerSignal?.removeEventListener("abort", abortFromCaller);
    },
  };
}

export async function probeCore(options: CoreProbeOptions): Promise<CoreProbeResult> {
  const localProxy = isLocalProxyBaseUrl(options.baseUrl);
  if (!localProxy && !isValidDirectCoreBaseUrl(options.baseUrl)) {
    return { kind: "invalid_configuration", executionReadiness: "unknown" };
  }
  const timeoutMs = options.timeoutMs ?? 5_000;
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    return { kind: "invalid_configuration", executionReadiness: "unknown" };
  }
  const headers = new Headers({
    Accept: "application/json",
    "OpenAI-Beta": "agents=v1",
  });
  const token = localProxy ? undefined : options.token?.trim();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const deadline = probeSignal(options.signal, timeoutMs);

  try {
    const response = await (options.fetch ?? globalThis.fetch.bind(globalThis))(coreProbeUrl(options.baseUrl), {
      method: "GET",
      headers,
      signal: deadline.signal,
      cache: "no-store",
    });

    if (response.status === 200) {
      let payload: unknown;
      try {
        payload = await response.json();
      } catch (error) {
        if (classifyBodyReadFailure(error, options.signal, deadline.signal) === "unreachable") {
          return { kind: "unreachable", executionReadiness: "unknown" };
        }
        return {
          kind: "protocol_mismatch",
          executionReadiness: "unknown",
          httpStatus: response.status,
        };
      }

      return {
        kind: isAgentListPage(payload) ? "authenticated" : "protocol_mismatch",
        executionReadiness: "unknown",
        httpStatus: response.status,
      };
    }

    if (response.ok) {
      return {
        kind: "protocol_mismatch",
        executionReadiness: "unknown",
        httpStatus: response.status,
      };
    }

    let error: ProbeError | undefined;
    try {
      error = await readError(response);
    } catch (reason) {
      if (classifyBodyReadFailure(reason, options.signal, deadline.signal) === "unreachable") {
        return { kind: "unreachable", executionReadiness: "unknown" };
      }
    }
    if (response.status === 401 && isCoreUnauthorized(error)) {
      return { kind: "unauthorized", executionReadiness: "unknown", httpStatus: response.status };
    }
    if (
      (response.status === 400 && error?.code === "invalid_beta") ||
      response.status === 404 ||
      response.status === 405
    ) {
      return { kind: "protocol_mismatch", executionReadiness: "unknown", httpStatus: response.status };
    }
    return { kind: "http_error", executionReadiness: "unknown", httpStatus: response.status };
  } catch (error) {
    if (options.signal?.aborted) throw error;
    return { kind: "unreachable", executionReadiness: "unknown" };
  } finally {
    deadline.dispose();
  }
}
