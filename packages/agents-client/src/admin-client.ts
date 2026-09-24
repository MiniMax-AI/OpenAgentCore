import {
  AgentCoreError, addVaultPageOptions, projectStartupConfiguration, projectAgentSession, projectRuntimeObservation,
  projectEnvironmentTemplate, projectEnvironmentTemplateList, projectVault, projectVaultList,
  projectVaultCredential, projectVaultCredentialList, projectSourceFile, projectSourceFileDeleted,
} from "./client";
import { projectExecutionConfiguration } from "./execution-configuration-projection";
import { projectAgentTurn, projectSessionItem, projectHistoryPage, validateHistoryPageOptions } from "./history-projection";
import { projectRuntimeHistory } from "./runtime-history-projection";
import { canonicalUuid, isNonnegativeInteger, isRecord } from "./response-projection";
import {
  invalidAdminResponse, projectAdminKey, projectIssuedAdminKey, projectAdminPage, projectAdminDeleted,
  projectResourcePage, projectSavedAgent, projectSkill, projectSkillVersion, projectArtifact, projectCopyResult, projectSummary, projectAdminRuntimePage, projectResourceOwners, projectWriteOperations, projectAdminAudit,
} from "./admin-projection";
import type { PageOptions, ReadOptions, RuntimeHistoryQuery, VaultListOptions } from "./types";
import type {
  AdminClientOptions, AdminAuditOptions, AdminCopyInput, AdminContent, AdminWriteOptions, CreateAdminAPIKeyInput,
  ResetAdminAPIKeyInput, AdminSummaryOptions, AdminResourceType, AdminResourceOwner, AdminWriteOperationOptions, AdminWriteOperationPage,
} from "./admin-types";

function segment(value: string): string {
  // Dot segments are normalized by fetch before the server sees the request.
  if (!value || value === "." || value === "..") throw new TypeError("A resource ID is required.");
  return encodeURIComponent(value);
}
function scope(keyId: string): string { return `/api-keys/${segment(keyId)}`; }
function pageQuery(path: string, options?: PageOptions, extra: Record<string, string | undefined> = {}): string {
  const params = new URLSearchParams();
  if (options?.after !== undefined) params.set("after", options.after);
  if (options?.limit !== undefined) params.set("limit", String(options.limit));
  if (options?.order !== undefined) params.set("order", options.order);
  for (const [key, value] of Object.entries(extra)) if (value !== undefined) params.set(key, value);
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

function vaultQuery(path: string, options?: VaultListOptions): string {
  const params = new URLSearchParams();
  addVaultPageOptions(params, options);
  return params.size ? `${path}?${params}` : path;
}

/** Management-only client. Browser callers authenticate through the same-origin console session. */
export class AdminClient {
  readonly #baseUrl: string;
  readonly #adminToken: AdminClientOptions["adminToken"];
  readonly #fetch: typeof fetch;

  constructor(options: AdminClientOptions = {}) {
    this.#baseUrl = (options.baseUrl ?? "/core/v1/admin").replace(/\/+$/, "");
    this.#adminToken = options.adminToken;
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  async #response(path: string, options?: ReadOptions, method = "GET", body?: unknown, idempotencyKey?: string): Promise<Response> {
    const headers = new Headers({ Accept: "application/json" });
    const token = typeof this.#adminToken === "function" ? this.#adminToken() : this.#adminToken;
    if (token) headers.set("Authorization", `Bearer ${token}`);
    if (body !== undefined) headers.set("Content-Type", "application/json");
    if (idempotencyKey !== undefined) {
      if (!idempotencyKey.trim()) throw new TypeError("An Idempotency-Key must not be empty.");
      headers.set("Idempotency-Key", idempotencyKey);
    }
    const response = await this.#fetch(`${this.#baseUrl}${path}`, {
      method, headers, signal: options?.signal, credentials: "same-origin", redirect: "error",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (!response.ok) {
      let envelope: unknown;
      try { envelope = await response.json(); } catch { /* An intermediary may return a non-JSON error. */ }
      const error = isRecord(envelope) && isRecord(envelope.error) ? envelope.error : {};
      throw new AgentCoreError(
        typeof error.message === "string" ? error.message : `Core administration request failed (${response.status}).`,
        response.status,
        typeof error.code === "string" || error.code === null ? error.code : undefined,
        typeof error.param === "string" || error.param === null ? error.param : undefined,
        typeof error.type === "string" ? error.type : undefined,
      );
    }
    return response;
  }
  async #json(path: string, options?: ReadOptions, method?: string, body?: unknown, idempotencyKey?: string): Promise<unknown> {
    const response = await this.#response(path, options, method, body, idempotencyKey);
    try { return await response.json(); } catch { return invalidAdminResponse(); }
  }
  async #delete<O extends string>(path: string, id: string, object: O, options?: ReadOptions) {
    return projectAdminDeleted(await this.#json(path, options, "DELETE"), id, object);
  }
  async #content(path: string, options?: ReadOptions): Promise<AdminContent> {
    const response = await this.#response(path, options);
    return { blob: await response.blob(), contentType: response.headers.get("Content-Type"), contentDisposition: response.headers.get("Content-Disposition") };
  }

  async listAPIKeys(options?: PageOptions) {
    return projectAdminPage(await this.#json(pageQuery("/api-keys", options), options), (value) => projectAdminKey(value));
  }
  async retrieveAPIKey(keyId: string, options?: ReadOptions) {
    return projectAdminKey(await this.#json(scope(keyId), options), keyId);
  }
  async createAPIKey(input: CreateAdminAPIKeyInput, options?: ReadOptions) {
    return projectIssuedAdminKey(await this.#json("/api-keys", options, "POST", { id: input.id, name: input.name }), input.id);
  }
  async resetAPIKey(keyId: string, input: ResetAdminAPIKeyInput, options?: ReadOptions) {
    return projectIssuedAdminKey(await this.#json(`${scope(keyId)}/reset`, options, "POST", { request_id: input.request_id }), keyId);
  }
  async revokeAPIKey(keyId: string, options?: ReadOptions): Promise<{ id: string; deleted: true }> {
    const value = await this.#json(scope(keyId), options, "DELETE");
    if (!isRecord(value) || Object.keys(value).length !== 2 || value.id !== keyId || value.deleted !== true) return invalidAdminResponse();
    return { id: keyId, deleted: true };
  }
  async copyResources(input: AdminCopyInput, options?: AdminWriteOptions) {
    const body = {
      source_key_id: input.source_key_id, target_key_id: input.target_key_id,
      resource_type: input.resource_type, resource_id: input.resource_id, include_dependencies: input.include_dependencies,
      ...(input.target_vault_id === undefined ? {} : { target_vault_id: input.target_vault_id }),
    };
    return projectCopyResult(await this.#json("/copies", options, "POST", body, options?.idempotencyKey));
  }
  async retrieveSummary(options?: AdminSummaryOptions) {
    const path = pageQuery("/summary", options, {
      key_id: options?.key_id, group_by: options?.group_by,
      created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectSummary(await this.#json(path, options));
  }
  async listRuntimeObservations(options?: PageOptions) {
    return projectAdminRuntimePage(await this.#json(pageQuery("/runtime-observations", options), options));
  }
  async retrieveStartupConfiguration(options?: ReadOptions) {
    return projectStartupConfiguration(await this.#json("/startup-configuration", options));
  }

  async listAgents(keyId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/agents`, options), options), (value) => projectSavedAgent(value));
  }
  async retrieveAgent(keyId: string, agentId: string, options?: ReadOptions) {
    return projectSavedAgent(await this.#json(`${scope(keyId)}/agents/${segment(agentId)}`, options), agentId);
  }
  deleteAgent(keyId: string, agentId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/agents/${segment(agentId)}`, agentId, "agent.deleted", options);
  }
  async listSkills(keyId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/skills`, options), options), (value) => projectSkill(value));
  }
  async retrieveSkill(keyId: string, skillId: string, options?: ReadOptions) {
    return projectSkill(await this.#json(`${scope(keyId)}/skills/${segment(skillId)}`, options), skillId);
  }
  deleteSkill(keyId: string, skillId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/skills/${segment(skillId)}`, skillId, "skill.deleted", options);
  }
  async listSkillVersions(keyId: string, skillId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/skills/${segment(skillId)}/versions`, options), options), (value) => projectSkillVersion(value, skillId));
  }
  async retrieveSkillVersion(keyId: string, skillId: string, version: string, options?: ReadOptions) {
    return projectSkillVersion(await this.#json(`${scope(keyId)}/skills/${segment(skillId)}/versions/${segment(version)}`, options), skillId, version);
  }
  async deleteSkillVersion(keyId: string, skillId: string, version: string, options?: ReadOptions) {
    const value = await this.#json(`${scope(keyId)}/skills/${segment(skillId)}/versions/${segment(version)}`, options, "DELETE");
    if (!isRecord(value) || value.version !== version || typeof value.id !== "string") return invalidAdminResponse();
    const { version: deletedVersion, ...receipt } = value;
    return { ...projectAdminDeleted(receipt, value.id, "skill.version.deleted"), version: deletedVersion };
  }
  downloadSkill(keyId: string, skillId: string, options?: ReadOptions) {
    return this.#content(`${scope(keyId)}/skills/${segment(skillId)}/content`, options);
  }
  downloadSkillVersion(keyId: string, skillId: string, version: string, options?: ReadOptions) {
    return this.#content(`${scope(keyId)}/skills/${segment(skillId)}/versions/${segment(version)}/content`, options);
  }

  async listEnvironmentTemplates(keyId: string, options?: PageOptions) {
    return projectEnvironmentTemplateList(await this.#json(pageQuery(`${scope(keyId)}/environment-templates`, options), options), options);
  }
  async retrieveEnvironmentTemplate(keyId: string, templateId: string, options?: ReadOptions) {
    return projectEnvironmentTemplate(await this.#json(`${scope(keyId)}/environment-templates/${segment(templateId)}`, options), templateId);
  }
  deleteEnvironmentTemplate(keyId: string, templateId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/environment-templates/${segment(templateId)}`, templateId, "agent.environment.template.deleted", options);
  }
  async listSourceFiles(keyId: string, options?: PageOptions & { purpose?: string }) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/files`, options, { purpose: options?.purpose }), options), (value) => projectSourceFile(value));
  }
  async retrieveSourceFile(keyId: string, fileId: string, options?: ReadOptions) {
    return projectSourceFile(await this.#json(`${scope(keyId)}/files/${segment(fileId)}`, options), fileId);
  }
  async deleteSourceFile(keyId: string, fileId: string, options?: ReadOptions) {
    return projectSourceFileDeleted(await this.#json(`${scope(keyId)}/files/${segment(fileId)}`, options, "DELETE"), fileId);
  }
  async listVaults(keyId: string, options?: VaultListOptions) {
    return projectVaultList(await this.#json(vaultQuery(`${scope(keyId)}/vaults`, options), options), options);
  }
  async retrieveVault(keyId: string, vaultId: string, options?: ReadOptions) {
    return projectVault(await this.#json(`${scope(keyId)}/vaults/${segment(vaultId)}`, options), vaultId);
  }
  deleteVault(keyId: string, vaultId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/vaults/${segment(vaultId)}`, vaultId, "vault.deleted", options);
  }
  async listVaultCredentials(keyId: string, vaultId: string, options?: VaultListOptions) {
    return projectVaultCredentialList(await this.#json(vaultQuery(`${scope(keyId)}/vaults/${segment(vaultId)}/credentials`, options), options), vaultId, options);
  }
  async retrieveVaultCredential(keyId: string, vaultId: string, credentialId: string, options?: ReadOptions) {
    return projectVaultCredential(await this.#json(`${scope(keyId)}/vaults/${segment(vaultId)}/credentials/${segment(credentialId)}`, options), vaultId, credentialId);
  }
  deleteVaultCredential(keyId: string, vaultId: string, credentialId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/vaults/${segment(vaultId)}/credentials/${segment(credentialId)}`, credentialId, "vault.credential.deleted", options);
  }

  async listSessions(keyId: string, options?: PageOptions & { agentId?: string }) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/sessions`, options, { agent_id: options?.agentId }), options), (value) => projectAgentSession(value));
  }
  async retrieveSession(keyId: string, sessionId: string, options?: ReadOptions) {
    return projectAgentSession(await this.#json(`${scope(keyId)}/sessions/${segment(sessionId)}`, options), undefined, sessionId);
  }
  deleteSession(keyId: string, sessionId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/sessions/${segment(sessionId)}`, sessionId, "agent.session.deleted", options);
  }
  async listTurns(keyId: string, sessionId: string, options?: PageOptions) {
    validateHistoryPageOptions(options);
    const value = await this.#json(pageQuery(`${scope(keyId)}/sessions/${segment(sessionId)}/turns`, options), options);
    return projectHistoryPage(value, options, (entry) => projectAgentTurn(entry, sessionId, invalidAdminResponse), invalidAdminResponse);
  }
  async retrieveTurn(keyId: string, sessionId: string, turnId: string, options?: ReadOptions) {
    return projectAgentTurn(await this.#json(`${scope(keyId)}/sessions/${segment(sessionId)}/turns/${segment(turnId)}`, options), sessionId, invalidAdminResponse, turnId);
  }
  async listItems(keyId: string, sessionId: string, options?: PageOptions) {
    validateHistoryPageOptions(options);
    const value = await this.#json(pageQuery(`${scope(keyId)}/sessions/${segment(sessionId)}/items`, options), options);
    return projectHistoryPage(value, options, (entry) => projectSessionItem(entry, invalidAdminResponse), invalidAdminResponse);
  }
  async listArtifacts(keyId: string, sessionId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(keyId)}/sessions/${segment(sessionId)}/artifacts`, options), options), (entry) => projectArtifact(entry, sessionId));
  }
  async retrieveArtifact(keyId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return projectArtifact(await this.#json(`${scope(keyId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}`, options), sessionId, artifactId);
  }
  deleteArtifact(keyId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return this.#delete(`${scope(keyId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}`, artifactId, "agent.session.artifact.deleted", options);
  }
  downloadArtifact(keyId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return this.#content(`${scope(keyId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}/content`, options);
  }
  async retrieveSessionExecutionConfiguration(keyId: string, sessionId: string, options?: ReadOptions) {
    return projectExecutionConfiguration(await this.#json(`${scope(keyId)}/sessions/${segment(sessionId)}/execution-configuration`, options), sessionId, invalidAdminResponse);
  }
  async retrieveRuntimeObservation(keyId: string, sessionId: string, options?: ReadOptions) {
    return projectRuntimeObservation(await this.#json(`${scope(keyId)}/sessions/${segment(sessionId)}/runtime-observation`, options), sessionId);
  }
  async retrieveRuntimeHistory(keyId: string, sessionId: string, query: RuntimeHistoryQuery) {
    if (canonicalUuid(sessionId) === null || !isNonnegativeInteger(query.start) || !isNonnegativeInteger(query.end) || query.end <= query.start ||
      (query.maxPoints !== undefined && (!Number.isSafeInteger(query.maxPoints) || query.maxPoints < 2 || query.maxPoints > 10_000))) throw new TypeError("Runtime history query is invalid.");
    const path = pageQuery(`${scope(keyId)}/sessions/${segment(sessionId)}/runtime-history`, undefined, {
      start: String(query.start), end: String(query.end), max_points: query.maxPoints === undefined ? undefined : String(query.maxPoints),
    });
    return projectRuntimeHistory(await this.#json(path, query), sessionId, query, invalidAdminResponse);
  }

  async retrieveResourceOwners(keyId: string, resourceType: AdminResourceType, resourceIds: string[], options?: ReadOptions): Promise<{ data: AdminResourceOwner[] }> {
    const path = pageQuery(`${scope(keyId)}/resource-owners`, undefined, { resource_type: resourceType, resource_ids: resourceIds.join(",") });
    return projectResourceOwners(await this.#json(path, options), resourceIds);
  }
  async listAuditLog(options?: AdminAuditOptions) {
    const path = pageQuery("/audit-log", options, {
      key_id: options?.key_id, resource_type: options?.resource_type, resource_id: options?.resource_id,
      action: options?.action, created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectAdminAudit(await this.#json(path, options));
  }
  async listWriteOperations(keyId: string, options?: AdminWriteOperationOptions): Promise<AdminWriteOperationPage> {
    const path = pageQuery(`${scope(keyId)}/write-operations`, options, {
      key_id: options?.key_id, resource_type: options?.resource_type, resource_id: options?.resource_id,
      created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectWriteOperations(await this.#json(path, options));
  }
}
