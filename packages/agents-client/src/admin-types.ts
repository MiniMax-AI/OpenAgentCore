import type { PageOptions, ReadOptions } from "./types";

export interface AdminClientOptions {
  baseUrl?: string;
  /** Deployment credential for trusted server callers; console browsers use their same-origin session. */
  adminToken?: string | (() => string | undefined);
  fetch?: typeof fetch;
}

export interface AdminAPIKey {
  id: string;
  name: string;
  prefix: string;
  created_at: string | null;
  revoked_at: string | null;
  tenant_id: string;
  organization_id: string;
  project_id: string;
  kind: "issued" | "static";
}
export interface AdminIssuedAPIKey extends AdminAPIKey { key: string }
export interface CreateAdminAPIKeyInput { id: string; name: string }
export interface ResetAdminAPIKeyInput { request_id: string }
export interface AdminPage<T> { data: T[]; has_more: boolean }
export interface AdminDeleted<O extends string = string> { id: string; object: O; deleted: true }
export type AdminCopyResourceType = "agent" | "skill" | "environment_template" | "file" | "vault" | "credential";
export interface AdminCopyInput {
  source_key_id: string;
  target_key_id: string;
  resource_type: AdminCopyResourceType;
  resource_id: string;
  include_dependencies: boolean;
  target_vault_id?: string;
}
export interface AdminCopyResult {
  mappings: Array<{ type: AdminCopyResourceType | "skill_version"; source_id: string; target_id: string }>;
  skipped: Array<{ type: AdminCopyResourceType | "skill_version"; source_id: string; reason: string }>;
}
export interface AdminWriteOptions extends ReadOptions { idempotencyKey?: string }
export interface Skill {
  id: string;
  object: "skill";
  created_at: number;
  name: string;
  description: string;
  default_version: string;
  latest_version: string;
}
export interface SkillVersion {
  id: string;
  object: "skill.version";
  created_at: number;
  skill_id: string;
  version: string;
  name: string;
  description: string;
}
export interface SessionArtifact {
  id: string;
  object: "agent.session.artifact";
  created_at: number;
  environment_id: string;
  path: string;
  session_id: string;
  size_bytes: number;
  turn_id: string;
}
export interface AdminContent { blob: Blob; contentType: string | null; contentDisposition: string | null }
export type AdminResourceType = AdminCopyResourceType | "session" | "environment" | "skill_version" | "artifact";
export interface AdminKeyProvenance {
  id: string;
  name: string;
  prefix: string;
  kind: "issued" | "static";
  revoked_at: string | null;
}
export interface AdminResourceOwner {
  resource_id: string;
  api_key: AdminKeyProvenance | null;
  source: "api_key" | "admin_copy" | null;
  admin_audit_id: string | null;
}
export interface AdminWriteOperation {
  id: string;
  created_at: string;
  api_key: AdminKeyProvenance | null;
  action: string;
  resource_type: AdminResourceType;
  resource_id: string;
  parent_id: string;
  request_id: string;
  trace_id: string;
}
export interface AdminWriteOperationOptions extends Omit<PageOptions, "order"> {
  key_id?: string;
  resource_type?: AdminResourceType;
  resource_id?: string;
  created_after?: string;
  created_before?: string;
}
export interface AdminWriteOperationPage extends AdminPage<AdminWriteOperation> { next_cursor: string }

export interface AdminSummaryOptions extends PageOptions {
  key_id?: string;
  group_by?: "key" | "agent";
  created_after?: string;
  created_before?: string;
}
export interface AdminSummaryEntry {
  key_id: string;
  agent_id: string | null;
  assets: { agents: number; skills: number; environment_templates: number; files: number; vaults: number; credentials: number } | null;
  sessions: { total: number; idle: number; in_progress: number; requires_action: number; failed: number };
  usage: import("./types").TokenUsage;
  coverage: { measured_sessions: number; total_sessions: number; ratio: number | null };
  last_active_at: number | null;
}
export interface AdminSummary extends AdminPage<AdminSummaryEntry> { next_cursor: string }
export interface AdminRuntimeObservation { key_id: string; observation: import("./types").RuntimeObservation }

export interface AdminAuditOptions extends Omit<AdminWriteOperationOptions, "resource_type"> { action?: string; resource_type?: string }
export interface AdminAuditEntry {
  id: string;
  created_at: string;
  admin_credential_id: string;
  actor_label: string;
  action: string;
  target_key_id: string;
  resource_type: string;
  resource_id: string;
  result_ids: AdminCopyResult["mappings"];
  request_id: string;
  trace_id: string;
}
export interface AdminAuditPage extends AdminPage<AdminAuditEntry> { next_cursor: string }
