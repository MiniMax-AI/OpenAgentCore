import { AgentCoreError, projectAgentSnapshot, projectRuntimeObservation } from "./client";
import { projectTokenUsage } from "./usage-projection";
import { canonicalUuid, exactFields, isNonnegativeInteger, isRecord, sameResourceId } from "./response-projection";
import type { ListPage, SavedAgent } from "./types";
import type { AdminAPIKey, AdminProject, AdminAuditPage, AdminSummary, AdminRuntimeObservation, RuntimeDiskObservation, AdminKeyProvenance, AdminResourceOwner, AdminWriteOperationPage, AdminAuditResultID, AdminDeleted, AdminIssuedAPIKey, AdminPage, AdminSessionArchive, SessionArtifact, Skill, SkillVersion } from "./admin-types";

export function invalidAdminResponse(): never {
  throw new AgentCoreError("Core returned an invalid administration response.", 502, "invalid_admin_response");
}
function record(value: unknown, fields: string[]): Record<string, unknown> {
  if (!isRecord(value) || !exactFields(value, new Set(fields))) return invalidAdminResponse();
  return value;
}
function strings(value: Record<string, unknown>, fields: string[]): void {
  if (fields.some((field) => typeof value[field] !== "string")) invalidAdminResponse();
}
function date(value: unknown): boolean {
  return value === null || (typeof value === "string" && Number.isFinite(Date.parse(value)));
}
export function projectAdminProject(value: unknown, expectedId?: string): AdminProject {
  const project = record(value, ["id", "name", "created_at", "archived_at", "active_key_count"]);
  strings(project, ["id", "name", "created_at"]);
  if (!date(project.created_at) ||
    !date(project.archived_at) || !isNonnegativeInteger(project.active_key_count) ||
    (expectedId !== undefined && !sameResourceId(project.id as string, expectedId))) return invalidAdminResponse();
  return { ...project } as unknown as AdminProject;
}
export function projectAdminKey(value: unknown, projectId: string): AdminAPIKey {
  const key = record(value, ["id", "project_id", "name", "prefix", "created_at", "revoked_at"]);
  strings(key, ["id", "project_id", "name", "prefix", "created_at"]);
  if (!date(key.created_at) || !date(key.revoked_at) ||
    !sameResourceId(key.project_id as string, projectId)) return invalidAdminResponse();
  return { ...key } as unknown as AdminAPIKey;
}
export function projectIssuedAdminKey(value: unknown, projectId: string): AdminIssuedAPIKey {
  if (!isRecord(value) || typeof value.key !== "string" || value.key.length === 0) return invalidAdminResponse();
  const { key, ...metadata } = value;
  const projected = projectAdminKey(metadata, projectId);
  if (projected.revoked_at !== null) return invalidAdminResponse();
  return { ...projected, key };
}
export function projectAdminPage<T>(value: unknown, project: (entry: unknown) => T): AdminPage<T> {
  const page = record(value, ["data", "has_more"]);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || (page.has_more && page.data.length === 0)) return invalidAdminResponse();
  return { data: page.data.map(project), has_more: page.has_more };
}
export function projectResourcePage<T extends { id: string }>(value: unknown, project: (entry: unknown) => T): ListPage<T> {
  const page = record(value, ["object", "data", "has_more", "first_id", "last_id"]);
  if (page.object !== "list" || !Array.isArray(page.data) || typeof page.has_more !== "boolean") return invalidAdminResponse();
  const data = page.data.map(project);
  if (page.first_id !== (data[0]?.id ?? null) || page.last_id !== (data.at(-1)?.id ?? null) ||
    new Set(data.map((entry) => entry.id)).size !== data.length) return invalidAdminResponse();
  return { object: "list", data, has_more: page.has_more, first_id: page.first_id as string | null, last_id: page.last_id as string | null };
}
export function projectAdminDeleted<O extends string>(value: unknown, expectedId: string, object: O): AdminDeleted<O> {
  const deleted = record(value, ["id", "object", "deleted"]);
  if (typeof deleted.id !== "string" || !sameResourceId(deleted.id, expectedId) || deleted.object !== object || deleted.deleted !== true) return invalidAdminResponse();
  return { id: deleted.id, object, deleted: true };
}
export function projectAdminSessionArchive(value: unknown, sessionId: string): AdminSessionArchive {
  const archive = record(value, ["session_id", "environment_id", "state"]);
  if (typeof archive.session_id !== "string" || !sameResourceId(archive.session_id, sessionId) ||
    canonicalUuid(archive.environment_id) === null ||
    (archive.state !== "active" && archive.state !== "cleanup_pending" && archive.state !== "released")) return invalidAdminResponse();
  return { session_id: archive.session_id, environment_id: archive.environment_id as string, state: archive.state };
}
export function projectSavedAgent(value: unknown, expectedId?: string): SavedAgent {
  if (!isRecord(value)) return invalidAdminResponse();
  const { object, metadata, created_at, updated_at, ...snapshot } = value;
  const agent = projectAgentSnapshot(snapshot);
  if (object !== "agent" || !isRecord(metadata) || Object.values(metadata).some((entry) => typeof entry !== "string") ||
    !isNonnegativeInteger(created_at) || !isNonnegativeInteger(updated_at) || updated_at < created_at ||
    (expectedId !== undefined && !sameResourceId(agent.id, expectedId))) return invalidAdminResponse();
  return { ...agent, object, metadata: { ...metadata } as Record<string, string>, created_at, updated_at };
}
export function projectSkill(value: unknown, expectedId?: string): Skill {
  const skill = record(value, ["id", "object", "created_at", "name", "description", "default_version", "latest_version"]);
  strings(skill, ["id", "name", "description", "default_version", "latest_version"]);
  if (skill.object !== "skill" || !isNonnegativeInteger(skill.created_at) ||
    (expectedId !== undefined && !sameResourceId(skill.id as string, expectedId))) return invalidAdminResponse();
  return { ...skill } as unknown as Skill;
}
export function projectSkillVersion(value: unknown, skillId: string, version?: string): SkillVersion {
  const skill = record(value, ["id", "object", "created_at", "skill_id", "version", "name", "description"]);
  strings(skill, ["id", "skill_id", "version", "name", "description"]);
  if (skill.object !== "skill.version" || !isNonnegativeInteger(skill.created_at) || !sameResourceId(skill.skill_id as string, skillId) ||
    (version !== undefined && skill.version !== version)) return invalidAdminResponse();
  return { ...skill } as unknown as SkillVersion;
}
export function projectArtifact(value: unknown, sessionId: string, expectedId?: string): SessionArtifact {
  const artifact = record(value, ["id", "object", "created_at", "environment_id", "path", "session_id", "size_bytes", "turn_id"]);
  strings(artifact, ["id", "environment_id", "path", "session_id", "turn_id"]);
  if (artifact.object !== "agent.session.artifact" || !isNonnegativeInteger(artifact.created_at) || !isNonnegativeInteger(artifact.size_bytes) ||
    !sameResourceId(artifact.session_id as string, sessionId) || (expectedId !== undefined && !sameResourceId(artifact.id as string, expectedId))) return invalidAdminResponse();
  return { ...artifact } as unknown as SessionArtifact;
}
// Historical copy audit entries retain their result mappings.
function projectAuditResultIDs(value: unknown): AdminAuditResultID[] {
  if (!Array.isArray(value)) return invalidAdminResponse();
  const types = new Set(["agent", "skill", "skill_version", "environment_template", "file", "vault", "credential"]);
  return value.map((entry) => {
    const item = record(entry, ["type", "source_id", "target_id"]);
    strings(item, ["type", "source_id", "target_id"]);
    if (!types.has(item.type as string)) return invalidAdminResponse();
    return { ...item } as unknown as AdminAuditResultID;
  });
}

function projectProvenance(value: unknown): AdminKeyProvenance | null {
  if (value === null) return null;
  const key = record(value, ["id", "name", "prefix", "kind", "revoked_at"]);
  strings(key, ["id", "name", "prefix"]);
  if ((key.kind !== "issued" && key.kind !== "static" && key.kind !== "console") || !date(key.revoked_at)) return invalidAdminResponse();
  return { ...key } as unknown as AdminKeyProvenance;
}
export function projectResourceOwners(value: unknown, ids: string[]): { data: AdminResourceOwner[] } {
  const page = record(value, ["data"]);
  if (!Array.isArray(page.data) || page.data.length !== ids.length) return invalidAdminResponse();
  return { data: page.data.map((entry, index) => {
    const owner = record(entry, ["resource_id", "api_key", "source", "admin_audit_id"]);
    if (owner.resource_id !== ids[index]) return invalidAdminResponse();
    if (!(owner.source === null || owner.source === "api_key" || owner.source === "admin_copy") ||
      !(owner.admin_audit_id === null || typeof owner.admin_audit_id === "string")) return invalidAdminResponse();
    const apiKey = projectProvenance(owner.api_key);
    if ((owner.source === "api_key") !== (apiKey !== null) || (owner.source === "admin_copy") !== (owner.admin_audit_id !== null)) return invalidAdminResponse();
    return { resource_id: owner.resource_id as string, api_key: apiKey, source: owner.source, admin_audit_id: owner.admin_audit_id } as AdminResourceOwner;
  }) };
}
export function projectWriteOperations(value: unknown): AdminWriteOperationPage {
  const page = record(value, ["data", "has_more", "next_cursor"]);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const operation = record(entry, ["id", "created_at", "api_key", "action", "resource_type", "resource_id", "parent_id", "request_id", "trace_id"]);
    strings(operation, ["id", "created_at", "action", "resource_type", "resource_id", "parent_id", "request_id", "trace_id"]);
    if (!date(operation.created_at)) return invalidAdminResponse();
    return { ...operation, api_key: projectProvenance(operation.api_key) };
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as AdminWriteOperationPage;
}
export function projectSummary(value: unknown): AdminSummary {
  const page = record(value, ["data", "has_more", "next_cursor"]);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const summary = record(entry, ["project_id", "key_id", "agent_id", "assets", "sessions", "usage", "coverage", "last_active_at"]);
    strings(summary, ["project_id"]);
    if (!(summary.key_id === null || typeof summary.key_id === "string") ||
      !(summary.agent_id === null || typeof summary.agent_id === "string") ||
      !(summary.last_active_at === null || isNonnegativeInteger(summary.last_active_at))) return invalidAdminResponse();
    const assets = summary.assets === null ? null : record(summary.assets, ["agents", "skills", "environment_templates", "files", "vaults", "credentials"]);
    const sessions = record(summary.sessions, ["total", "idle", "in_progress", "requires_action", "failed"]);
    const coverage = record(summary.coverage, ["measured_sessions", "total_sessions", "ratio"]);
    const usage = projectTokenUsage(summary.usage, invalidAdminResponse);
    if (usage === null || (assets !== null && Object.values(assets).some((count) => !isNonnegativeInteger(count))) ||
      Object.values(sessions).some((count) => !isNonnegativeInteger(count)) || !isNonnegativeInteger(coverage.measured_sessions) ||
      !isNonnegativeInteger(coverage.total_sessions) || coverage.measured_sessions > coverage.total_sessions ||
      !(coverage.ratio === null || (typeof coverage.ratio === "number" && Number.isFinite(coverage.ratio) && coverage.ratio >= 0 && coverage.ratio <= 1))) return invalidAdminResponse();
    return { ...summary, assets, sessions, coverage, usage };
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as AdminSummary;
}
export function projectAdminRuntimePage(value: unknown): ListPage<AdminRuntimeObservation> {
  const page = record(value, ["object", "data", "has_more", "first_id", "last_id"]);
  if (page.object !== "list" || !Array.isArray(page.data) || typeof page.has_more !== "boolean") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const item = record(entry, ["project_id", "observation"]);
    strings(item, ["project_id"]);
    if (!isRecord(item.observation)) return invalidAdminResponse();
    // A Core without disk observations omits the field; disk is then unknown.
    const { disk = null, ...rest } = item.observation;
    const observation = projectRuntimeObservation(rest);
    return { project_id: item.project_id as string, observation: { ...observation, disk: projectRuntimeDisk(disk, observation.status === "observed") } };
  });
  if (page.first_id !== (data[0]?.observation.id ?? null) || page.last_id !== (data.at(-1)?.observation.id ?? null) ||
    new Set(data.map((entry) => entry.observation.id)).size !== data.length || (page.has_more && data.length === 0)) return invalidAdminResponse();
  return { object: "list", data, has_more: page.has_more, first_id: page.first_id as string | null, last_id: page.last_id as string | null };
}

function projectRuntimeDisk(value: unknown, observed: boolean): RuntimeDiskObservation | null {
  if (value === null) return null;
  const disk = record(value, ["usage_bytes", "limit_bytes"]);
  const known = (field: unknown) => field === null || isNonnegativeInteger(field);
  if (!observed || !known(disk.usage_bytes) || !known(disk.limit_bytes) || disk.limit_bytes === 0 ||
    (disk.usage_bytes === null && disk.limit_bytes === null)) return invalidAdminResponse();
  return { usage_bytes: disk.usage_bytes as number | null, limit_bytes: disk.limit_bytes as number | null };
}

export function projectAdminAudit(value: unknown): AdminAuditPage {
  const page = record(value, ["data", "has_more", "next_cursor"]);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const audit = record(entry, ["id", "created_at", "admin_credential_id", "actor_label", "action", "project_id", "resource_type", "resource_id", "result_ids", "request_id", "trace_id"]);
    strings(audit, ["id", "created_at", "admin_credential_id", "actor_label", "action", "project_id", "resource_type", "resource_id", "request_id", "trace_id"]);
    if (!date(audit.created_at)) return invalidAdminResponse();
    return { ...audit, result_ids: projectAuditResultIDs(audit.result_ids) };
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as AdminAuditPage;
}
