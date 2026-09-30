import type { UpdateSandboxDeployment } from "@oac/agents-client";

/** Omission preserves the credential; every explicit key follows Core's replacement path. */
export function e2bUpdateSelection(template: string, apiKey: string): NonNullable<UpdateSandboxDeployment["e2b"]> {
  const key = apiKey.trim();
  return { template: template.trim(), ...(key ? { api_key: key } : {}) };
}

/** Clearing a submitted secret must not silently convert replacement into retention. */
export function e2bKeyReady(editing: boolean, replacementRequested: boolean, apiKey: string): boolean {
  return apiKey.trim().length > 0 || (editing && !replacementRequested);
}
