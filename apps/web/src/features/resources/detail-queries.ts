import { AgentCoreError, type EnvironmentTemplateResource, type SavedAgent, type Skill, type Vault, type VaultCredential } from "@oac/agents-client";
import { queryOptions, useQuery, useQueryClient, type QueryClient, type QueryKey, type UseQueryOptions } from "@tanstack/react-query";
import { useCallback } from "react";

import { projectClient, readAllPages, type ProjectClient } from "../../lib/projects";
import { collectionQuery, collections, queryClient, type CollectionSpec } from "../../lib/queries";
import { readSkillVersionsPage } from "../skills/skill-operations";

type SkillVersionsPage = Awaited<ReturnType<typeof readSkillVersionsPage>>;

/**
 * One resource's detail reads, cached like the lists under
 * `[kind, projectId, id, …]`. A detail already seen opens from the cache and
 * refreshes behind it; a row the list has already read stands in until Core
 * answers, so opening a row is instant. Only a true first load has nothing to
 * show.
 */

/** A definite 4xx answer (a missing resource above all) does not change on a retry. */
export function retryTransient(failureCount: number, error: unknown): boolean {
  if (error instanceof AgentCoreError && error.status >= 400 && error.status < 500) return false;
  return failureCount < 1;
}

/** The row the project's cached list holds for `id`, if the list was read. */
function listedRow<T extends { id: string }>(spec: CollectionSpec<T>, projectId: string, id: string): T | undefined {
  return queryClient.getQueryData(collectionQuery(spec, projectId).queryKey)?.find((row) => row.id === id);
}

/** After a delete from a detail page: the list reads again and the detail's reads are dropped. */
export function forgetDeleted(client: QueryClient, list: { key: readonly unknown[] }, detailKey: QueryKey): void {
  void client.invalidateQueries({ queryKey: ["collection", ...list.key] });
  client.removeQueries({ queryKey: detailKey });
}

export function agentQuery(projectId: string, agentId: string) {
  return queryOptions<SavedAgent>({
    queryKey: ["agent", projectId, agentId],
    queryFn: () => projectClient(projectId).retrieveAgent(agentId),
    placeholderData: () => listedRow(collections.agents, projectId, agentId),
    retry: retryTransient,
  });
}

export function templateQuery(projectId: string, templateId: string) {
  return queryOptions<EnvironmentTemplateResource>({
    queryKey: ["template", projectId, templateId],
    queryFn: ({ signal }) => projectClient(projectId).retrieveEnvironmentTemplate(templateId, { signal }),
    placeholderData: () => listedRow(collections.templates, projectId, templateId),
    retry: retryTransient,
  });
}

export function vaultQuery(projectId: string, vaultId: string) {
  return queryOptions<Vault>({
    queryKey: ["vault", projectId, vaultId],
    queryFn: ({ signal }) => projectClient(projectId).retrieveVault(vaultId, { signal }),
    placeholderData: () => listedRow(collections.vaults, projectId, vaultId),
    retry: retryTransient,
  });
}

export function vaultCredentialsQuery(projectId: string, vaultId: string) {
  return queryOptions<VaultCredential[]>({
    queryKey: ["vault", projectId, vaultId, "credentials"],
    queryFn: ({ signal }) => {
      const client = projectClient(projectId);
      return readAllPages((after) => client.listVaultCredentials(vaultId, { after, limit: 100, signal }));
    },
    retry: retryTransient,
  });
}

type SkillReader = Pick<ProjectClient, "retrieveSkill" | "listSkillVersions">;

/** `seed` stands in before the list cache when the caller already holds the Skill. */
export function skillQuery(projectId: string, skillId: string, core: SkillReader = projectClient(projectId), seed: Skill | null = null) {
  return queryOptions<Skill>({
    queryKey: ["skill", projectId, skillId],
    queryFn: ({ signal }) => core.retrieveSkill(skillId, { signal }),
    placeholderData: () => seed ?? listedRow(collections.skills, projectId, skillId),
    retry: retryTransient,
  });
}

/** The first version page; "Load more" appends later pages to this entry. */
export function skillVersionsQuery(projectId: string, skillId: string, core: SkillReader = projectClient(projectId)) {
  return queryOptions<SkillVersionsPage>({
    queryKey: ["skill", projectId, skillId, "versions"],
    queryFn: ({ signal }) => readSkillVersionsPage(core, skillId, [], undefined, signal),
    retry: retryTransient,
  });
}

/** One cached detail read and how to forget it once the resource is deleted. */
function useDetail<T>(options: UseQueryOptions<T, Error, T, QueryKey>, list: { key: readonly unknown[] }) {
  const client = useQueryClient();
  const read = useQuery(options);
  const key = JSON.stringify(options.queryKey);
  const forget = useCallback(() => forgetDeleted(client, list, JSON.parse(key) as QueryKey), [client, list, key]);
  return { read, forget };
}

export function useAgentDetail(projectId: string, agentId: string) {
  return useDetail(agentQuery(projectId, agentId), collections.agents);
}

export function useTemplateDetail(projectId: string, templateId: string) {
  return useDetail(templateQuery(projectId, templateId), collections.templates);
}

/** The Vault (opened from its list row when cached) and its Credentials, read side by side. */
export function useVaultDetail(projectId: string, vaultId: string) {
  const { read, forget } = useDetail(vaultQuery(projectId, vaultId), collections.vaults);
  const credentials = useQuery(vaultCredentialsQuery(projectId, vaultId));
  return { read, credentials, forget };
}
