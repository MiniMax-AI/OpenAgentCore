import { requestLogEntryFields, requestLogListFields, requestLogEntryStateValues, requestLogListSurfaceValues, type RequestLogEntry, type RequestLogList } from "./generated/core-api";
import { invalidAdminResponse } from "./admin-projection";
import { exactFields, isOneOf, isNonnegativeInteger, isRecord } from "./response-projection";
import type { ReadOptions } from "./types";

export type { RequestLogEntry, RequestLogList } from "./generated/core-api";
export interface RequestLogOptions extends ReadOptions {
  limit?: number;
  request_id?: string;
  outcome?: "all" | "errors" | "active";
  surface?: "public" | "admin";
  method?: "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS" | "CONNECT" | "TRACE" | "OTHER";
}

const timestamp = (value: unknown): value is string => typeof value === "string" && Number.isFinite(Date.parse(value));
const requestId = (value: unknown): value is string => typeof value === "string" && /^req_[0-9a-f]{32}$/.test(value);

function projectEntry(value: unknown): RequestLogEntry {
  if (!isRecord(value) || !exactFields(value, requestLogEntryFields) ||
      !requestId(value.request_id) || typeof value.trace_id !== "string" || !/^[0-9a-f]{32}$/.test(value.trace_id) ||
      !timestamp(value.started_at) || typeof value.method !== "string" || !value.method || typeof value.route !== "string" || !value.route ||
      !isOneOf(requestLogEntryStateValues, value.state) ||
      !(value.status === null || (Number.isInteger(value.status) && Number(value.status) >= 100 && Number(value.status) <= 599)) ||
      (value.state === "completed" && value.status === null) ||
      typeof value.duration_ms !== "number" || !Number.isFinite(value.duration_ms) || value.duration_ms < 0 ||
      !isNonnegativeInteger(value.response_bytes) || !(value.error_code === null || typeof value.error_code === "string")) return invalidAdminResponse();
  return value as unknown as RequestLogEntry;
}

export function projectRequestLog(value: unknown): RequestLogList {
  if (!isRecord(value) || !exactFields(value, requestLogListFields) || value.object !== "core.request_log.list" ||
      typeof value.instance_id !== "string" || !value.instance_id || !timestamp(value.instance_started_at) ||
      !isOneOf(requestLogListSurfaceValues, value.surface) ||
      !isNonnegativeInteger(value.capacity) || value.capacity < 1 || !isNonnegativeInteger(value.retained_count) || value.retained_count > value.capacity ||
      !isNonnegativeInteger(value.total_matching) || value.total_matching > value.retained_count ||
      typeof value.has_more !== "boolean" || !Array.isArray(value.data) || value.data.length > value.total_matching ||
      value.has_more !== (value.data.length < value.total_matching)) return invalidAdminResponse();
  const data = value.data.map(projectEntry);
  if (new Set(data.map((entry) => entry.request_id)).size !== data.length) return invalidAdminResponse();
  return { ...value, data } as RequestLogList;
}
