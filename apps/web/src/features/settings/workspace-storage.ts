import { AgentCoreError } from "@oac/agents-client";
import { queryOptions } from "@tanstack/react-query";
import { admin } from "../../lib/projects";

export const workspaceStorageQuery = queryOptions({
  queryKey: ["workspace-storage"],
  queryFn: async ({ signal }) => {
    try { return await admin.retrieveWorkspaceStorage({ signal }); }
    catch (error) {
      if (error instanceof AgentCoreError && error.status === 404 && error.code === "workspace_storage_not_found") return null;
      throw error;
    }
  },
});

export function storageObject(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
  } catch { return null; }
}

export function storageCapacity(text: string): number | null {
  if (!/^\d+$/.test(text)) return null;
  const value = Number(text);
  return Number.isSafeInteger(value) && value <= 4_294_967_295 ? value : null;
}
