import { canonicalUuid, exactFields, isOneOf, isRecord, sameResourceId } from "./response-projection";
import { invalidAdminResponse } from "./admin-projection";
import { turnStatusResourceValues } from "./generated/public-api";
import {
  diagnosticFailureFields, itemDiagnosticTimingFields, sessionDiagnosticFailureFields, sessionDiagnosticFailureRequired,
  sessionDiagnosticFailureSourceValues, sessionDiagnosticsFields, sessionDiagnosticsStatusValues, turnDiagnosticsFields,
  type DiagnosticFailure as DiagnosticFailureResource, type DiagnosticFailureCode, type ItemDiagnosticTiming,
  type SessionDiagnosticFailure as SessionDiagnosticFailureResource, type SessionDiagnostics as SessionDiagnosticsResource,
  type TurnDiagnostics as TurnDiagnosticsResource,
} from "./generated/core-api";

// ItemDiagnosticTiming holds Core receipt intervals, distinct from the public tool-reported duration_ms.
export type { DiagnosticFailureCode, ItemDiagnosticTiming };
// The schema types params as a free-form object; each code that carries params has one of these shapes.
export type ProvisioningFailureParams = { step: "setup" | "python" | "npm" | "system" | "file" | "skill" | null; index: number | null; exit_code: number | null };
export type ConnectionFailureParams = { http_status: number | null };
export type DiagnosticFailure = Omit<DiagnosticFailureResource, "params"> & { params: Record<string, never> | ProvisioningFailureParams | ConnectionFailureParams };
export type SessionDiagnosticFailure = DiagnosticFailure & Pick<SessionDiagnosticFailureResource, "source" | "turn_id">;
export type SessionDiagnostics = Omit<SessionDiagnosticsResource, "failure"> & { failure: SessionDiagnosticFailure | null };
export type TurnDiagnostics = Omit<TurnDiagnosticsResource, "failure"> & { failure: DiagnosticFailure | null };

// The schema's one code enum covers every failure source; each source produces only its subset.
const turnCodes: readonly DiagnosticFailureCode[] = ["authentication_error", "rate_limit_exceeded", "usage_limit_exceeded", "server_overloaded", "server_error", "invalid_request", "resource_not_found", "request_timeout", "context_length_exceeded", "cyber_policy", "connection_failed", "harness_error", "model_provider_required", "runtime_unavailable", "runtime_disconnected", "runtime_preparation_failed", "execution_interrupted", "delivery_unconfirmed", "input_rejected", "executor_protocol_error", "core_storage_failed", "internal_error"];
const inputCodes: readonly DiagnosticFailureCode[] = ["environment_connection_timeout", "environment_unavailable", "model_provider_required", "internal_error"];
const environmentCodes: readonly DiagnosticFailureCode[] = ["environment_provisioning_failed"];
const fields = (...values: string[]) => new Set(values);
function timestamp(value: unknown): value is string {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(value) && Number.isFinite(Date.parse(value));
}
function failure(value: unknown, codes: readonly DiagnosticFailureCode[], session = false): DiagnosticFailure {
  if (!isRecord(value) || !exactFields(value, !session ? diagnosticFailureFields : value.source === "turn" ? sessionDiagnosticFailureFields : sessionDiagnosticFailureRequired) ||
      !isOneOf(codes, value.code) || !isRecord(value.params) || (value.failed_at !== null && !timestamp(value.failed_at))) return invalidAdminResponse();
  let params: DiagnosticFailure["params"] = {};
  if (value.code === "environment_provisioning_failed") {
    const p = value.params;
    if (!exactFields(p, fields("step", "index", "exit_code")) || (p.step !== null && (typeof p.step !== "string" || !["setup", "python", "npm", "system", "file", "skill"].includes(p.step))) ||
        (p.index !== null && (!Number.isSafeInteger(p.index) || (p.index as number) < 0 || p.step !== "setup")) ||
        (p.exit_code !== null && (!Number.isSafeInteger(p.exit_code) || (p.exit_code as number) < 1 || (p.exit_code as number) > 255 || !["setup", "python", "npm", "system"].includes(p.step ?? "")))) return invalidAdminResponse();
    params = { step: p.step as ProvisioningFailureParams["step"], index: p.index as number | null, exit_code: p.exit_code as number | null };
  } else if (value.code === "connection_failed") {
    const p = value.params;
    if (!exactFields(p, fields("http_status")) || (p.http_status !== null && (!Number.isSafeInteger(p.http_status) || (p.http_status as number) < 100 || (p.http_status as number) > 599))) return invalidAdminResponse();
    params = { http_status: p.http_status as number | null };
  } else if (Object.keys(value.params).length !== 0) return invalidAdminResponse();
  return { code: value.code, params, failed_at: value.failed_at as string | null };
}

export function projectSessionDiagnostics(value: unknown, sessionId: string): SessionDiagnostics {
  if (!isRecord(value) || !exactFields(value, sessionDiagnosticsFields) || value.object !== "core.session_diagnostics" ||
      canonicalUuid(value.session_id) === null || !sameResourceId(value.session_id as string, sessionId) || !isOneOf(sessionDiagnosticsStatusValues, value.status)) return invalidAdminResponse();
  let projected: SessionDiagnosticFailure | null = null;
  if (value.status === "failed") {
    if (!isRecord(value.failure) || !isOneOf(sessionDiagnosticFailureSourceValues, value.failure.source)) return invalidAdminResponse();
    const f = value.failure;
    const source = value.failure.source;
    projected = { ...failure(f, source === "turn" ? turnCodes : source === "environment_input" ? inputCodes : environmentCodes, true), source };
    if (source === "turn") {
      if (canonicalUuid(f.turn_id) === null) return invalidAdminResponse();
      projected.turn_id = f.turn_id as string;
    }
  } else if (value.failure !== null) return invalidAdminResponse();
  return { object: "core.session_diagnostics", session_id: value.session_id as string, status: value.status, failure: projected };
}

export function projectTurnDiagnostics(value: unknown, sessionId: string, turnId: string): TurnDiagnostics {
  if (!isRecord(value) || !exactFields(value, turnDiagnosticsFields) || value.object !== "core.turn_diagnostics" ||
      canonicalUuid(value.session_id) === null || !sameResourceId(value.session_id as string, sessionId) || canonicalUuid(value.turn_id) === null || !sameResourceId(value.turn_id as string, turnId) ||
      !isOneOf(turnStatusResourceValues, value.status) || !Array.isArray(value.items) || value.items.length > 1000 || typeof value.items_truncated !== "boolean" || (value.items_truncated && value.items.length !== 1000)) return invalidAdminResponse();
  const projected = value.status === "failed" ? failure(value.failure, turnCodes) : null;
  if (value.status !== "failed" && value.failure !== null) return invalidAdminResponse();
  const seen = new Set<string>();
  const items: ItemDiagnosticTiming[] = value.items.map((item: unknown) => {
    if (!isRecord(item) || !exactFields(item, itemDiagnosticTimingFields) || canonicalUuid(item.item_id) === null || !timestamp(item.started_at) ||
        (item.completed_at !== null && !timestamp(item.completed_at)) || (item.completed_at === null ? item.observed_duration_ms !== null : !Number.isSafeInteger(item.observed_duration_ms))) return invalidAdminResponse();
    const id = canonicalUuid(item.item_id)!;
    if (seen.has(id)) return invalidAdminResponse();
    seen.add(id);
    return { item_id: item.item_id as string, started_at: item.started_at, completed_at: item.completed_at as string | null, observed_duration_ms: item.observed_duration_ms as number | null };
  });
  return { object: "core.turn_diagnostics", session_id: value.session_id as string, turn_id: value.turn_id as string, status: value.status, failure: projected, items, items_truncated: value.items_truncated };
}
