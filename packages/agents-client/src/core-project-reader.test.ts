import { describe, expect, it, vi } from "vitest";

import { AdminClient } from "./admin-client";
import type { AdminContent, CoreProjectReader } from "./admin-types";

async function content(result: Promise<AdminContent>) {
  const value = await result;
  return { data: value.blob, bytes: value.blob.size, content_type: "application/octet-stream" as const, content_disposition: value.contentDisposition ?? "" };
}

/**
 * The console's project binding (apps/web createProjectClient), written without
 * a cast: typechecking this file proves AdminClient's project-bound methods
 * satisfy CoreProjectReader.
 */
function projectReader(admin: AdminClient, projectId: string): CoreProjectReader {
  const listSessions = async (options: Parameters<CoreProjectReader["listSessions"]>[0] = {}) => {
    const page = await admin.listSessions(projectId, { after: options.after, limit: options.limit, order: options.order, agentId: options.agentId, signal: options.signal });
    return { ...page, object: "list" as const, first_id: page.first_id ?? null, last_id: page.last_id ?? null };
  };
  const client: CoreProjectReader = {
    listAgents: (options) => admin.listAgents(projectId, options),
    retrieveAgent: (agentId: string) => admin.retrieveAgent(projectId, agentId),
    deleteAgent: (agentId: string) => admin.deleteAgent(projectId, agentId),
    listSkills: (options) => admin.listSkills(projectId, options),
    retrieveSkill: (skillId, options) => admin.retrieveSkill(projectId, skillId, options),
    deleteSkill: (skillId, options) => admin.deleteSkill(projectId, skillId, options),
    listSkillVersions: (skillId, options) => admin.listSkillVersions(projectId, skillId, options),
    deleteSkillVersion: (skillId, version, options) => admin.deleteSkillVersion(projectId, skillId, version, options),
    downloadSkill: (skillId, options) => content(admin.downloadSkill(projectId, skillId, options)),
    downloadSkillVersion: (skillId, version, options) => content(admin.downloadSkillVersion(projectId, skillId, version, options)),
    listEnvironmentTemplates: (options) => admin.listEnvironmentTemplates(projectId, options),
    retrieveEnvironmentTemplate: (templateId, options) => admin.retrieveEnvironmentTemplate(projectId, templateId, options),
    deleteEnvironmentTemplate: (templateId, options) => admin.deleteEnvironmentTemplate(projectId, templateId, options),
    listSourceFiles: (options) => admin.listSourceFiles(projectId, options),
    deleteSourceFile: (fileId, options) => admin.deleteSourceFile(projectId, fileId, options),
    listVaults: (options) => admin.listVaults(projectId, options),
    retrieveVault: (vaultId, options) => admin.retrieveVault(projectId, vaultId, options),
    listVaultCredentials: (vaultId, options) => admin.listVaultCredentials(projectId, vaultId, options),
    deleteVault: (vaultId) => admin.deleteVault(projectId, vaultId),
    deleteVaultCredential: (vaultId, credentialId) => admin.deleteVaultCredential(projectId, vaultId, credentialId),
    listSessions,
    listSessionsTolerant: async (options) => ({ ...(await listSessions(options)), unrecognized: [] }),
    retrieveSession: (sessionId, options) => admin.retrieveSession(projectId, sessionId, options),
    deleteSession: (sessionId) => admin.deleteSession(projectId, sessionId),
    listTurns: (sessionId, options) => admin.listTurns(projectId, sessionId, options),
    listItems: (sessionId, options) => admin.listItems(projectId, sessionId, options),
    retrieveRuntimeObservation: (sessionId, options) => admin.retrieveRuntimeObservation(projectId, sessionId, options),
    retrieveRuntimeHistory: (sessionId, query) => admin.retrieveRuntimeHistory(projectId, sessionId, query),
  };
  return client;
}

describe("CoreProjectReader", () => {
  it("binds AdminClient to one project under /core/v1/projects without a cast", async () => {
    const projectId = "66666666-6666-4666-8666-666666666666";
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response(JSON.stringify({ object: "list", data: [], has_more: false, first_id: null, last_id: null })));
    const reader = projectReader(new AdminClient({ fetch }), projectId);
    await expect(reader.listSkills()).resolves.toEqual({ object: "list", data: [], has_more: false, first_id: null, last_id: null });
    await reader.listSourceFiles({ purpose: "user_data" });
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([`/core/v1/projects/${projectId}/skills`, `/core/v1/projects/${projectId}/files?purpose=user_data`]);
  });
});
