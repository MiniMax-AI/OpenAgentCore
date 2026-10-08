import { coreHarnessKinds, modelProviderProtocols } from "./harness-catalog";
import { AgentCoreError, projectRuntimeObservation, projectSavedAgentConfiguration } from "./client";
import { projectTokenUsage } from "./usage-projection";
import { safeProvider } from "./execution-configuration-projection";
import { canonicalUuid, exactFields, type FieldNames, hasOwn, isNonnegativeInteger, isOneOf, isRecord, onlyFields, sameResourceId } from "./response-projection";
import { sessionArtifactResourceFields } from "./generated/public-api";
import {
  addressBindingsFields, adminAssetCountsFields, adminauditOperationFields, adminauditPageFields, adminRuntimeObservationFields,
  adminRuntimeObservationListFields, adminSessionCountsFields, adminSummaryResponseFields, adminSummaryRowFields, adminUsageCoverageFields,
  coreHarnessFields, coreHarnessListFields, executorConnectionFields, executorConnectionStatusValues, executorCredentialFields,
  executorCredentialListFields, harnessModelConfigurationFields, harnessModelConfigurationLastErrorCodeValues, installationConfigurationFields,
  installationFields, installationSettingFields, installationSettingRequired, installationSettingRestartsValues, issuedExecutorCredentialFields,
  managedArchiveFields, managedArchiveStateValues, modelConfigurationSupportFields, projectFields, projectsAPIKeyFields, resourceOwnerFields,
  resourceOwnerListFields, runtimeDiskObservationFields, writeauditAPIKeyFields, writeauditOperationActionValues, writeauditOperationFields,
  writeauditOperationResourceTypeValues, writeauditPageFields,
} from "./generated/core-api";
import type { CoreHarness, CoreHarnessKind, HarnessModelConfiguration, ListPage, SavedAgent, SessionArtifact } from "./types";
import type {
  AdminAPIKey, AdminAuditPage, AdminDeleted, AdminIssuedAPIKey, AdminKeyProvenance, AdminPage, AdminProject, AdminResourceOwner,
  AdminRuntimeObservation, AdminSessionArchive, AdminSummary, AdminWriteOperationPage, CoreInstallation, CoreInstallationSetting,
  ExecutorConnection, ExecutorCredentialList, IssuedExecutorCredential, RuntimeDiskObservation,
} from "./admin-types";

export function invalidAdminResponse(): never {
  throw new AgentCoreError("Core returned an invalid administration response.", 502, "invalid_admin_response");
}
function record(value: unknown, fields: FieldNames): Record<string, unknown> {
  if (!isRecord(value) || !exactFields(value, fields)) return invalidAdminResponse();
  return value;
}
function strings(value: Record<string, unknown>, fields: string[]): void {
  if (fields.some((field) => typeof value[field] !== "string")) invalidAdminResponse();
}
function date(value: unknown): boolean {
  return value === null || (typeof value === "string" && Number.isFinite(Date.parse(value)));
}
export function projectAdminProject(value: unknown, expectedId?: string): AdminProject {
  const project = record(value, projectFields);
  strings(project, ["id", "name", "created_at"]);
  if (!date(project.created_at) ||
    !date(project.archived_at) || !isNonnegativeInteger(project.active_key_count) ||
    (expectedId !== undefined && !sameResourceId(project.id as string, expectedId))) return invalidAdminResponse();
  return { ...project } as unknown as AdminProject;
}
export function projectAdminKey(value: unknown, projectId: string): AdminAPIKey {
  const key = record(value, projectsAPIKeyFields);
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
export function projectAdminPage<T>(value: unknown, fields: FieldNames, project: (entry: unknown) => T): AdminPage<T> {
  const page = record(value, fields);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || (page.has_more && page.data.length === 0)) return invalidAdminResponse();
  return { data: page.data.map(project), has_more: page.has_more };
}
/** A public resource page: `object`, `first_id` and `last_id` are always present. */
export type ResourcePage<T> = ListPage<T> & { object: "list"; first_id: string | null; last_id: string | null };
export function projectResourcePage<T extends { id: string }>(value: unknown, project: (entry: unknown) => T): ResourcePage<T> {
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
  const archive = record(value, managedArchiveFields);
  if (typeof archive.session_id !== "string" || !sameResourceId(archive.session_id, sessionId) ||
    canonicalUuid(archive.environment_id) === null || !isOneOf(managedArchiveStateValues, archive.state)) return invalidAdminResponse();
  return { session_id: archive.session_id, environment_id: archive.environment_id as string, state: archive.state };
}
export function projectSavedAgent(value: unknown, expectedId?: string): SavedAgent {
  if (!isRecord(value)) return invalidAdminResponse();
  const { object, metadata, created_at, updated_at, ...snapshot } = value;
  const agent = projectSavedAgentConfiguration(snapshot);
  if (object !== "agent" || !isRecord(metadata) || Object.values(metadata).some((entry) => typeof entry !== "string") ||
    !isNonnegativeInteger(created_at) || !isNonnegativeInteger(updated_at) || updated_at < created_at ||
    (expectedId !== undefined && !sameResourceId(agent.id, expectedId))) return invalidAdminResponse();
  return { ...agent, object, metadata: { ...metadata } as Record<string, string>, created_at, updated_at };
}
export function projectArtifact(value: unknown, sessionId: string, expectedId?: string): SessionArtifact {
  const artifact = record(value, sessionArtifactResourceFields);
  strings(artifact, ["id", "environment_id", "path", "session_id", "turn_id"]);
  if (artifact.object !== "agent.session.artifact" || !isNonnegativeInteger(artifact.created_at) || !isNonnegativeInteger(artifact.size_bytes) ||
    !sameResourceId(artifact.session_id as string, sessionId) || (expectedId !== undefined && !sameResourceId(artifact.id as string, expectedId))) return invalidAdminResponse();
  return { ...artifact } as unknown as SessionArtifact;
}
function projectProvenance(value: unknown): AdminKeyProvenance {
  const key = record(value, writeauditAPIKeyFields);
  strings(key, ["id", "name", "prefix"]);
  if (key.kind !== "issued" || !date(key.revoked_at)) return invalidAdminResponse();
  return { ...key } as unknown as AdminKeyProvenance;
}
export function projectResourceOwners(value: unknown, ids: string[]): { data: AdminResourceOwner[] } {
  const page = record(value, resourceOwnerListFields);
  if (!Array.isArray(page.data) || page.data.length !== ids.length) return invalidAdminResponse();
  return { data: page.data.map((entry, index) => {
    const owner = record(entry, resourceOwnerFields);
    if (owner.resource_id !== ids[index]) return invalidAdminResponse();
    return { resource_id: owner.resource_id as string, api_key: owner.api_key === null ? null : projectProvenance(owner.api_key) };
  }) };
}
export function projectWriteOperations(value: unknown): AdminWriteOperationPage {
  const page = record(value, writeauditPageFields);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const operation = record(entry, writeauditOperationFields);
    strings(operation, ["id", "created_at", "action", "resource_id", "parent_id", "request_id", "trace_id"]);
    if (!date(operation.created_at) || !isOneOf(writeauditOperationActionValues, operation.action) || !isOneOf(writeauditOperationResourceTypeValues, operation.resource_type)) return invalidAdminResponse();
    return { ...operation, api_key: projectProvenance(operation.api_key) };
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as AdminWriteOperationPage;
}
export function projectSummary(value: unknown): AdminSummary {
  const page = record(value, adminSummaryResponseFields);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const summary = record(entry, adminSummaryRowFields);
    strings(summary, ["project_id"]);
    if (!(summary.key_id === null || typeof summary.key_id === "string") ||
      !(summary.agent_id === null || typeof summary.agent_id === "string") ||
      !(summary.last_active_at === null || isNonnegativeInteger(summary.last_active_at))) return invalidAdminResponse();
    const assets = summary.assets === null ? null : record(summary.assets, adminAssetCountsFields);
    const sessions = record(summary.sessions, adminSessionCountsFields);
    const coverage = record(summary.coverage, adminUsageCoverageFields);
    const usage = projectTokenUsage(summary.usage, invalidAdminResponse);
    if (usage === null || (assets !== null && Object.values(assets).some((count) => !isNonnegativeInteger(count))) ||
      Object.values(sessions).some((count) => !isNonnegativeInteger(count)) || !isNonnegativeInteger(coverage.measured_sessions) ||
      !isNonnegativeInteger(coverage.total_sessions) || coverage.measured_sessions > coverage.total_sessions ||
      !(coverage.ratio === null || (typeof coverage.ratio === "number" && Number.isFinite(coverage.ratio) && coverage.ratio >= 0 && coverage.ratio <= 1))) return invalidAdminResponse();
    return { ...summary, assets, sessions, coverage, usage };
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as unknown as AdminSummary;
}
export function projectAdminRuntimePage(value: unknown): ListPage<AdminRuntimeObservation> {
  const page = record(value, adminRuntimeObservationListFields);
  if (page.object !== "list" || !Array.isArray(page.data) || typeof page.has_more !== "boolean") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const item = record(entry, adminRuntimeObservationFields);
    strings(item, ["project_id"]);
    if (!isRecord(item.observation) || !hasOwn(item.observation, "disk")) return invalidAdminResponse();
    const { disk, ...rest } = item.observation;
    const observation = projectRuntimeObservation(rest);
    return { project_id: item.project_id as string, observation: { ...observation, disk: projectRuntimeDisk(disk, observation.status === "observed") } };
  });
  if (page.first_id !== (data[0]?.observation.id ?? null) || page.last_id !== (data.at(-1)?.observation.id ?? null) ||
    new Set(data.map((entry) => entry.observation.id)).size !== data.length || (page.has_more && data.length === 0)) return invalidAdminResponse();
  return { object: "list", data, has_more: page.has_more, first_id: page.first_id as string | null, last_id: page.last_id as string | null };
}

function projectRuntimeDisk(value: unknown, observed: boolean): RuntimeDiskObservation | null {
  if (value === null) return null;
  const disk = record(value, runtimeDiskObservationFields);
  const known = (field: unknown) => field === null || isNonnegativeInteger(field);
  if (!observed || !known(disk.usage_bytes) || !known(disk.limit_bytes) || disk.limit_bytes === 0 ||
    (disk.usage_bytes === null && disk.limit_bytes === null)) return invalidAdminResponse();
  return { usage_bytes: disk.usage_bytes as number | null, limit_bytes: disk.limit_bytes as number | null };
}

export function projectAdminAudit(value: unknown): AdminAuditPage {
  const page = record(value, adminauditPageFields);
  if (!Array.isArray(page.data) || typeof page.has_more !== "boolean" || typeof page.next_cursor !== "string") return invalidAdminResponse();
  const data = page.data.map((entry) => {
    const audit = record(entry, adminauditOperationFields);
    strings(audit, ["id", "created_at", "admin_credential_id", "actor_label", "action", "resource_type", "resource_id", "request_id", "trace_id"]);
    if (!date(audit.created_at) || !(audit.project_id === null || typeof audit.project_id === "string")) return invalidAdminResponse();
    return audit;
  });
  return { data, has_more: page.has_more, next_cursor: page.next_cursor } as unknown as AdminAuditPage;
}
export function projectExecutorCredentials(value: unknown): ExecutorCredentialList {
  const page = record(value, executorCredentialListFields);
  if (!Array.isArray(page.data)) return invalidAdminResponse();
  const connection = record(page.connection, executorConnectionFields);
  if (!isOneOf(executorConnectionStatusValues, connection.status) ||
      !(connection.bound_key_id === null || (typeof connection.bound_key_id === "string" && connection.bound_key_id.length > 0)) ||
      !date(connection.enrolled_at) || !date(connection.last_seen_at)) return invalidAdminResponse();
  if (connection.status === "never_enrolled" && [connection.bound_key_id, connection.enrolled_at, connection.last_seen_at].some(value => value !== null)) return invalidAdminResponse();
  if (connection.status !== "never_enrolled" && connection.enrolled_at === null) return invalidAdminResponse();
  if (connection.status === "connected" && connection.bound_key_id === null) return invalidAdminResponse();
  return { connection: { ...connection } as unknown as ExecutorConnection, data: page.data.map((entry) => {
    const credential = record(entry, executorCredentialFields);
    if (typeof credential.key_id !== "string" || typeof credential.created_at !== "string" || !date(credential.created_at) || !date(credential.revoked_at)) return invalidAdminResponse();
    return { key_id: credential.key_id, created_at: credential.created_at, revoked_at: credential.revoked_at as string | null };
  }) };
}
/** The one-time credential must belong to the requested key ID and Environment. */
export function projectIssuedExecutorCredential(value: unknown, keyId: string, environmentId: string): IssuedExecutorCredential {
  const issued = record(value, issuedExecutorCredentialFields);
  if (typeof issued.key_id !== "string" || !sameResourceId(issued.key_id, keyId) ||
    typeof issued.environment_id !== "string" || !sameResourceId(issued.environment_id, environmentId) ||
    typeof issued.executor_token !== "string" || !issued.executor_token) return invalidAdminResponse();
  return { key_id: issued.key_id, environment_id: issued.environment_id, executor_token: issued.executor_token };
}

/** A deployment default model provider: exactly the safe view, never `api_key`. */
export function projectHarnessModelConfiguration(value: unknown, harness?: CoreHarnessKind): HarnessModelConfiguration {
  if (!isRecord(value) || !onlyFields(value, harnessModelConfigurationFields)) return invalidAdminResponse();
  const { object, harness: kind, updated_at, last_used_at, last_error_code, last_error_at, model_provider, model, harness_config } = value;
  if ((last_used_at !== null && (typeof last_used_at !== "string" || !date(last_used_at))) ||
    (last_error_at !== null && (typeof last_error_at !== "string" || !date(last_error_at))) ||
    (last_error_code !== null && !isOneOf(harnessModelConfigurationLastErrorCodeValues, last_error_code)) ||
    ((last_error_code === null) !== (last_error_at === null))) return invalidAdminResponse();
  if (object !== "core.model_configuration" || !isOneOf(coreHarnessKinds, kind) || (harness !== undefined && kind !== harness) ||
    typeof updated_at !== "string" || !date(updated_at)) return invalidAdminResponse();
  if (typeof model !== "string" || !model.trim() || !isRecord(harness_config)) return invalidAdminResponse();
  const provider = safeProvider(model_provider, invalidAdminResponse);
  if (!provider.api_key_configured) return invalidAdminResponse();
  return { object, harness: kind, model_provider: provider, model, harness_config: { ...harness_config }, updated_at, last_used_at, last_error_at, last_error_code };
}
function projectModelConfigurationSupport(value: unknown): CoreHarness["model_configuration_support"] {
  const support = record(value, modelConfigurationSupportFields);
  const protocols = support.protocols;
  if (!Array.isArray(protocols) || protocols.length === 0 || !protocols.every((protocol) => isOneOf(modelProviderProtocols, protocol)) ||
    new Set(protocols).size !== protocols.length ||
    typeof support.accepts_harness_config !== "boolean" || typeof support.token_limits_required !== "boolean") return invalidAdminResponse();
  return {
    protocols: [...protocols],
    accepts_harness_config: support.accepts_harness_config, token_limits_required: support.token_limits_required,
  };
}
export function projectCoreHarnessList(value: unknown): { object: "list"; data: CoreHarness[] } {
  const page = record(value, coreHarnessListFields);
  if (page.object !== "list" || !Array.isArray(page.data)) return invalidAdminResponse();
  const data = page.data.map((entry): CoreHarness => {
    const harness = record(entry, coreHarnessFields);
    if (harness.object !== "core.harness" || !isOneOf(coreHarnessKinds, harness.id) ||
      typeof harness.enabled !== "boolean" || typeof harness.default !== "boolean" || (harness.default && !harness.enabled)) return invalidAdminResponse();
    const id = harness.id;
    return {
      object: "core.harness", id, enabled: harness.enabled, default: harness.default,
      model_configuration_support: projectModelConfigurationSupport(harness.model_configuration_support),
      model_configuration: harness.model_configuration === null ? null : projectHarnessModelConfiguration(harness.model_configuration, id),
    };
  });
  if (new Set(data.map((entry) => entry.id)).size !== data.length || data.filter((entry) => entry.default).length > 1) return invalidAdminResponse();
  return { object: "list", data };
}
const settingKey = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/;
function projectInstallationSetting(value: unknown): CoreInstallationSetting {
  if (!isRecord(value) || typeof value.sensitive !== "boolean") return invalidAdminResponse();
  // `configured` is present exactly for a sensitive setting.
  const setting = record(value, value.sensitive ? installationSettingFields : installationSettingRequired);
  if (typeof setting.key !== "string" || !settingKey.test(setting.key) || typeof setting.changeable !== "boolean" ||
    !Array.isArray(setting.restarts) || !setting.restarts.every((service) => isOneOf(installationSettingRestartsValues, service)) ||
    (setting.sensitive && (setting.value !== null || setting.default !== null || typeof setting.configured !== "boolean"))) return invalidAdminResponse();
  return { ...setting } as unknown as CoreInstallationSetting;
}
/** Sensitive settings carry no value, keys are unique and binding counts are consistent. */
export function projectInstallation(value: unknown): CoreInstallation {
  const installation = record(value, installationFields);
  const origin = installation.public_url;
  if (installation.object !== "core.installation" || canonicalUuid(installation.installation_id) === null ||
    typeof origin !== "string" || installation.api_base_url !== `${origin}/v1` ||
    typeof installation.local_only !== "boolean" ||
    (installation.source_commit !== null && (typeof installation.source_commit !== "string" || !/^[0-9a-f]{40}$/.test(installation.source_commit)))) return invalidAdminResponse();
  const bindings = record(installation.address_bindings, addressBindingsFields);
  if (![bindings.nodes, bindings.nodes_on_other_address, bindings.hosted_sandboxes, bindings.self_hosted_executors].every(isNonnegativeInteger) ||
    (bindings.nodes_on_other_address as number) > (bindings.nodes as number)) return invalidAdminResponse();
  const configuration = record(installation.configuration, installationConfigurationFields);
  if (!Array.isArray(configuration.settings)) return invalidAdminResponse();
  const settings = configuration.settings.map(projectInstallationSetting);
  if (new Set(settings.map((setting) => setting.key)).size !== settings.length) return invalidAdminResponse();
  return { ...installation, address_bindings: { ...bindings }, configuration: { settings } } as unknown as CoreInstallation;
}
