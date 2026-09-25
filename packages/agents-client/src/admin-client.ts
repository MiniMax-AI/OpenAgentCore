import {
  addVaultPageOptions, projectAgentSession, projectRuntimeObservation,
  projectEnvironmentTemplate, projectEnvironmentTemplateList, projectVault, projectVaultList,
  projectVaultCredential, projectVaultCredentialList, projectSourceFile, projectSourceFileDeleted,
} from "./client";
import { projectExecutionConfiguration } from "./execution-configuration-projection";
import { projectAgentTurn, projectSessionItem, projectHistoryPage, validateHistoryPageOptions } from "./history-projection";
import { projectRuntimeHistory } from "./runtime-history-projection";
import { canonicalUuid, isNonnegativeInteger, isRecord } from "./response-projection";
import { CoreRequester } from "./core-request";
import {
  invalidAdminResponse, projectAdminProject, projectAdminKey, projectIssuedAdminKey, projectAdminPage, projectAdminDeleted, projectAdminSessionArchive,
  projectResourcePage, projectSavedAgent, projectSkill, projectSkillVersion, projectArtifact, projectSummary, projectAdminRuntimePage, projectResourceOwners, projectWriteOperations, projectAdminAudit,
  projectExecutorCredentials, projectIssuedExecutorCredential, projectCoreHarnessList, projectHarnessModelProvider, projectInstallation,
} from "./admin-projection";
import type {
  CoreHarness, CoreHarnessKind, HarnessModelProvider, ModelProviderInput, PageOptions, ReadOptions, RuntimeHistoryQuery, SkillList, SkillVersionDeleted,
  SkillVersionList, SourceFileList, VaultListOptions,
} from "./types";
import type {
  AdminClientOptions, AdminAuditOptions, AdminContent, ArchiveAdminSessionInput, CreateAdminProjectInput, RenameAdminProjectInput,
  IssueAdminAPIKeyInput, AdminSummaryOptions, AdminResourceType, AdminResourceOwner, AdminWriteOperationOptions, AdminWriteOperationPage,
  ExecutorCredential, IssueExecutorCredentialInput, IssuedExecutorCredential, CoreInstallation,
} from "./admin-types";

function segment(value: string): string {
  // Dot segments are normalized by fetch before the server sees the request.
  if (!value || value === "." || value === "..") throw new TypeError("A resource ID is required.");
  return encodeURIComponent(value);
}
function scope(projectId: string): string { return `/projects/${segment(projectId)}`; }
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

/**
 * Core's `/core/v1` administration client. Browser callers authenticate through
 * the same-origin console session; trusted server callers pass the Core key.
 */
export class AdminClient {
  readonly #core: CoreRequester;

  constructor(options: AdminClientOptions = {}) {
    this.#core = new CoreRequester(options.baseUrl ?? "/core/v1", options.adminToken, options.fetch, invalidAdminResponse);
  }

  #response(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<Response> {
    return this.#core.response(path, options, method, body);
  }
  #json(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<unknown> {
    return this.#core.json(path, options, method, body);
  }
  async #delete<O extends string>(path: string, id: string, object: O, options?: ReadOptions) {
    return projectAdminDeleted(await this.#json(path, options, "DELETE"), id, object);
  }
  async #content(path: string, options?: ReadOptions): Promise<AdminContent> {
    const response = await this.#response(path, options);
    return { blob: await response.blob(), contentType: response.headers.get("Content-Type"), contentDisposition: response.headers.get("Content-Disposition") };
  }

  /** Installation facts and process settings; readable before any sandbox deployment exists. */
  async retrieveInstallation(options?: ReadOptions): Promise<CoreInstallation> {
    return projectInstallation(await this.#json("/installation", options));
  }
  async listProjects(options?: PageOptions) {
    return projectAdminPage(await this.#json(pageQuery("/projects", options), options), (value) => projectAdminProject(value));
  }
  async createProject(input: CreateAdminProjectInput, options?: ReadOptions) {
    return projectAdminProject(await this.#json("/projects", options, "POST", { name: input.name }));
  }
  async renameProject(projectId: string, input: RenameAdminProjectInput, options?: ReadOptions) {
    return projectAdminProject(await this.#json(scope(projectId), options, "POST", { name: input.name }), projectId);
  }
  async archiveProject(projectId: string, options?: ReadOptions) {
    return projectAdminProject(await this.#json(`${scope(projectId)}/archive`, options, "POST"), projectId);
  }
  async listAPIKeys(projectId: string, options?: PageOptions) {
    return projectAdminPage(await this.#json(pageQuery(`${scope(projectId)}/keys`, options), options), (value) => projectAdminKey(value, projectId));
  }
  async issueAPIKey(projectId: string, input: IssueAdminAPIKeyInput, options?: ReadOptions) {
    return projectIssuedAdminKey(await this.#json(`${scope(projectId)}/keys`, options, "POST", { name: input.name }), projectId);
  }
  async revokeAPIKey(projectId: string, keyId: string, options?: ReadOptions): Promise<{ id: string; deleted: true }> {
    const value = await this.#json(`${scope(projectId)}/keys/${segment(keyId)}`, options, "DELETE");
    if (!isRecord(value) || Object.keys(value).length !== 2 || value.id !== keyId || value.deleted !== true) return invalidAdminResponse();
    return { id: keyId, deleted: true };
  }
  async retrieveSummary(options?: AdminSummaryOptions) {
    const path = pageQuery("/summary", options, {
      project_id: options?.project_id, group_by: options?.group_by,
      created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectSummary(await this.#json(path, options));
  }
  async listRuntimeObservations(options?: PageOptions) {
    return projectAdminRuntimePage(await this.#json(pageQuery("/sandbox/runtime-observations", options), options));
  }

  async listAgents(projectId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/agents`, options), options), (value) => projectSavedAgent(value));
  }
  async retrieveAgent(projectId: string, agentId: string, options?: ReadOptions) {
    return projectSavedAgent(await this.#json(`${scope(projectId)}/agents/${segment(agentId)}`, options), agentId);
  }
  deleteAgent(projectId: string, agentId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/agents/${segment(agentId)}`, agentId, "agent.deleted", options);
  }
  async listSkills(projectId: string, options?: PageOptions): Promise<SkillList> {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/skills`, options), options), (value) => projectSkill(value));
  }
  async retrieveSkill(projectId: string, skillId: string, options?: ReadOptions) {
    return projectSkill(await this.#json(`${scope(projectId)}/skills/${segment(skillId)}`, options), skillId);
  }
  deleteSkill(projectId: string, skillId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/skills/${segment(skillId)}`, skillId, "skill.deleted", options);
  }
  async listSkillVersions(projectId: string, skillId: string, options?: PageOptions): Promise<SkillVersionList> {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/skills/${segment(skillId)}/versions`, options), options), (value) => projectSkillVersion(value, skillId));
  }
  async retrieveSkillVersion(projectId: string, skillId: string, version: string, options?: ReadOptions) {
    return projectSkillVersion(await this.#json(`${scope(projectId)}/skills/${segment(skillId)}/versions/${segment(version)}`, options), skillId, version);
  }
  async deleteSkillVersion(projectId: string, skillId: string, version: string, options?: ReadOptions): Promise<SkillVersionDeleted> {
    const value = await this.#json(`${scope(projectId)}/skills/${segment(skillId)}/versions/${segment(version)}`, options, "DELETE");
    if (!isRecord(value) || value.version !== version || typeof value.id !== "string") return invalidAdminResponse();
    const { version: _, ...receipt } = value;
    return { ...projectAdminDeleted(receipt, value.id, "skill.version.deleted"), version };
  }
  downloadSkill(projectId: string, skillId: string, options?: ReadOptions) {
    return this.#content(`${scope(projectId)}/skills/${segment(skillId)}/content`, options);
  }
  downloadSkillVersion(projectId: string, skillId: string, version: string, options?: ReadOptions) {
    return this.#content(`${scope(projectId)}/skills/${segment(skillId)}/versions/${segment(version)}/content`, options);
  }

  async listEnvironmentTemplates(projectId: string, options?: PageOptions) {
    return projectEnvironmentTemplateList(await this.#json(pageQuery(`${scope(projectId)}/environment-templates`, options), options), options);
  }
  async retrieveEnvironmentTemplate(projectId: string, templateId: string, options?: ReadOptions) {
    return projectEnvironmentTemplate(await this.#json(`${scope(projectId)}/environment-templates/${segment(templateId)}`, options), templateId);
  }
  deleteEnvironmentTemplate(projectId: string, templateId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/environment-templates/${segment(templateId)}`, templateId, "agent.environment.template.deleted", options);
  }
  async listSourceFiles(projectId: string, options?: PageOptions & { purpose?: string }): Promise<SourceFileList> {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/files`, options, { purpose: options?.purpose }), options), (value) => projectSourceFile(value));
  }
  async retrieveSourceFile(projectId: string, fileId: string, options?: ReadOptions) {
    return projectSourceFile(await this.#json(`${scope(projectId)}/files/${segment(fileId)}`, options), fileId);
  }
  async deleteSourceFile(projectId: string, fileId: string, options?: ReadOptions) {
    return projectSourceFileDeleted(await this.#json(`${scope(projectId)}/files/${segment(fileId)}`, options, "DELETE"), fileId);
  }
  async listVaults(projectId: string, options?: VaultListOptions) {
    return projectVaultList(await this.#json(vaultQuery(`${scope(projectId)}/vaults`, options), options), options);
  }
  async retrieveVault(projectId: string, vaultId: string, options?: ReadOptions) {
    return projectVault(await this.#json(`${scope(projectId)}/vaults/${segment(vaultId)}`, options), vaultId);
  }
  deleteVault(projectId: string, vaultId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/vaults/${segment(vaultId)}`, vaultId, "vault.deleted", options);
  }
  async listVaultCredentials(projectId: string, vaultId: string, options?: VaultListOptions) {
    return projectVaultCredentialList(await this.#json(vaultQuery(`${scope(projectId)}/vaults/${segment(vaultId)}/credentials`, options), options), vaultId, options);
  }
  async retrieveVaultCredential(projectId: string, vaultId: string, credentialId: string, options?: ReadOptions) {
    return projectVaultCredential(await this.#json(`${scope(projectId)}/vaults/${segment(vaultId)}/credentials/${segment(credentialId)}`, options), vaultId, credentialId);
  }
  deleteVaultCredential(projectId: string, vaultId: string, credentialId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/vaults/${segment(vaultId)}/credentials/${segment(credentialId)}`, credentialId, "vault.credential.deleted", options);
  }

  async listSessions(projectId: string, options?: PageOptions & { agentId?: string }) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/sessions`, options, { agent_id: options?.agentId }), options), (value) => projectAgentSession(value));
  }
  async retrieveSession(projectId: string, sessionId: string, options?: ReadOptions) {
    return projectAgentSession(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}`, options), undefined, sessionId);
  }
  deleteSession(projectId: string, sessionId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/sessions/${segment(sessionId)}`, sessionId, "agent.session.deleted", options);
  }
  async archiveSession(projectId: string, sessionId: string, input: ArchiveAdminSessionInput, options?: ReadOptions) {
    if (!Number.isSafeInteger(input.expected_generation) || input.expected_generation <= 0) throw new TypeError("A positive safe integer generation is required.");
    return projectAdminSessionArchive(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/archive`, options, "POST", {
      expected_generation: input.expected_generation,
    }), sessionId);
  }
  async retrieveSessionArchive(projectId: string, sessionId: string, options?: ReadOptions) {
    return projectAdminSessionArchive(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/archive`, options), sessionId);
  }
  async listTurns(projectId: string, sessionId: string, options?: PageOptions) {
    validateHistoryPageOptions(options);
    const value = await this.#json(pageQuery(`${scope(projectId)}/sessions/${segment(sessionId)}/turns`, options), options);
    return projectHistoryPage(value, options, (entry) => projectAgentTurn(entry, sessionId, invalidAdminResponse), invalidAdminResponse);
  }
  async retrieveTurn(projectId: string, sessionId: string, turnId: string, options?: ReadOptions) {
    return projectAgentTurn(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/turns/${segment(turnId)}`, options), sessionId, invalidAdminResponse, turnId);
  }
  async listItems(projectId: string, sessionId: string, options?: PageOptions) {
    validateHistoryPageOptions(options);
    const value = await this.#json(pageQuery(`${scope(projectId)}/sessions/${segment(sessionId)}/items`, options), options);
    return projectHistoryPage(value, options, (entry) => projectSessionItem(entry, invalidAdminResponse), invalidAdminResponse);
  }
  async listArtifacts(projectId: string, sessionId: string, options?: PageOptions) {
    return projectResourcePage(await this.#json(pageQuery(`${scope(projectId)}/sessions/${segment(sessionId)}/artifacts`, options), options), (entry) => projectArtifact(entry, sessionId));
  }
  async retrieveArtifact(projectId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return projectArtifact(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}`, options), sessionId, artifactId);
  }
  deleteArtifact(projectId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return this.#delete(`${scope(projectId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}`, artifactId, "agent.session.artifact.deleted", options);
  }
  downloadArtifact(projectId: string, sessionId: string, artifactId: string, options?: ReadOptions) {
    return this.#content(`${scope(projectId)}/sessions/${segment(sessionId)}/artifacts/${segment(artifactId)}/content`, options);
  }
  async retrieveSessionExecutionConfiguration(projectId: string, sessionId: string, options?: ReadOptions) {
    return projectExecutionConfiguration(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/execution-configuration`, options), sessionId, invalidAdminResponse);
  }
  async retrieveRuntimeObservation(projectId: string, sessionId: string, options?: ReadOptions) {
    return projectRuntimeObservation(await this.#json(`${scope(projectId)}/sessions/${segment(sessionId)}/runtime-observation`, options), sessionId);
  }
  async retrieveRuntimeHistory(projectId: string, sessionId: string, query: RuntimeHistoryQuery) {
    if (canonicalUuid(sessionId) === null || !isNonnegativeInteger(query.start) || !isNonnegativeInteger(query.end) || query.end <= query.start ||
      (query.maxPoints !== undefined && (!Number.isSafeInteger(query.maxPoints) || query.maxPoints < 2 || query.maxPoints > 10_000))) throw new TypeError("Runtime history query is invalid.");
    const path = pageQuery(`${scope(projectId)}/sessions/${segment(sessionId)}/runtime-history`, undefined, {
      start: String(query.start), end: String(query.end), max_points: query.maxPoints === undefined ? undefined : String(query.maxPoints),
    });
    return projectRuntimeHistory(await this.#json(path, query), sessionId, query, invalidAdminResponse);
  }

  async retrieveResourceOwners(projectId: string, resourceType: AdminResourceType, resourceIds: string[], options?: ReadOptions): Promise<{ data: AdminResourceOwner[] }> {
    const path = pageQuery(`${scope(projectId)}/resource-owners`, undefined, { resource_type: resourceType, resource_ids: resourceIds.join(",") });
    return projectResourceOwners(await this.#json(path, options), resourceIds);
  }
  async listAuditLog(options?: AdminAuditOptions) {
    const path = pageQuery("/audit-log", options, {
      project_id: options?.project_id, resource_type: options?.resource_type, resource_id: options?.resource_id,
      action: options?.action, created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectAdminAudit(await this.#json(path, options));
  }
  async listWriteOperations(projectId: string, options?: AdminWriteOperationOptions): Promise<AdminWriteOperationPage> {
    const path = pageQuery(`${scope(projectId)}/write-operations`, options, {
      key_id: options?.key_id, resource_type: options?.resource_type, resource_id: options?.resource_id,
      created_after: options?.created_after, created_before: options?.created_before,
    });
    return projectWriteOperations(await this.#json(path, options));
  }

  /** Credential metadata for one self_hosted Environment; the credentials themselves are never listed. */
  async listExecutorCredentials(projectId: string, environmentId: string, options?: ReadOptions): Promise<{ data: ExecutorCredential[] }> {
    return projectExecutorCredentials(await this.#json(`${scope(projectId)}/environments/${segment(environmentId)}/executor-credentials`, options));
  }
  /**
   * Issues, or with `rotate: true` replaces, the credential with the caller's key ID and returns it once.
   * Sent once: after an uncertain result, refresh the list and reissue the same key ID with `rotate: true`.
   */
  async issueExecutorCredential(projectId: string, environmentId: string, input: IssueExecutorCredentialInput, options?: ReadOptions): Promise<IssuedExecutorCredential> {
    const value = await this.#json(`${scope(projectId)}/environments/${segment(environmentId)}/executor-credentials`, options, "POST", {
      key_id: input.key_id, rotate: input.rotate === true,
    });
    return projectIssuedExecutorCredential(value, input.key_id, environmentId);
  }
  /** Revokes the credential; revoking it again is safe. */
  async revokeExecutorCredential(projectId: string, environmentId: string, keyId: string, options?: ReadOptions): Promise<void> {
    await this.#response(`${scope(projectId)}/environments/${segment(environmentId)}/executor-credentials/${segment(keyId)}`, options, "DELETE");
  }

  /** Every harness this Core build supports, with its deployment default model provider or null. */
  async listHarnesses(options?: ReadOptions): Promise<{ object: "list"; data: CoreHarness[] }> {
    return projectCoreHarnessList(await this.#json("/harnesses", options));
  }
  /** The harness's deployment default; Core answers 404 when none is set. The key is never returned. */
  async retrieveHarnessModelProvider(harness: CoreHarnessKind, options?: ReadOptions): Promise<HarnessModelProvider> {
    return projectHarnessModelProvider(await this.#json(`/harnesses/${segment(harness)}/model-provider`, options), harness);
  }
  /**
   * Replaces the harness's deployment default with the complete bundle, including the write-only key.
   * New openai_hosted and none Sessions freeze it; self_hosted Sessions never use it, and existing Sessions keep the provider they froze.
   */
  async setHarnessModelProvider(harness: CoreHarnessKind, input: ModelProviderInput, options?: ReadOptions): Promise<HarnessModelProvider> {
    const body: ModelProviderInput = { protocol: input.protocol, base_url: input.base_url, api_key: input.api_key };
    if (input.context_window !== undefined) body.context_window = input.context_window;
    if (input.max_output_tokens !== undefined) body.max_output_tokens = input.max_output_tokens;
    return projectHarnessModelProvider(await this.#json(`/harnesses/${segment(harness)}/model-provider`, options, "PUT", body), harness);
  }
  /** Removes the harness's deployment default; removing it again is safe. Existing Sessions keep their frozen provider. */
  async deleteHarnessModelProvider(harness: CoreHarnessKind, options?: ReadOptions): Promise<void> {
    await this.#response(`/harnesses/${segment(harness)}/model-provider`, options, "DELETE");
  }
}
