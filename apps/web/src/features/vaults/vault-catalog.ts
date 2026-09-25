import type {
  CoreProjectReader,
  SavedAgent,
  Vault,
  VaultCredential,
} from "@agents-core-web/agents-client";

import { listAllCollectionPages } from "../../lib/collection-pagination";
import i18n from "../../i18n";

const tv = (key: string, options?: Record<string, unknown>) => i18n.t(key as never, { ns: "vaults", ...options });

export interface VaultCatalog {
  vaults: Vault[];
  credentials: VaultCredential[];
}

export interface SessionCredentialBinding {
  credential: VaultCredential;
  selection: "explicit" | "implicit";
  serverLabel: string;
  vault: Vault;
}

export interface SessionMcpResolution {
  credential?: VaultCredential;
  kind: "anonymous" | "explicit" | "implicit";
  serverLabel: string;
  serverURL: string;
  vault?: Vault;
}

export interface SessionVaultPlan {
  bindings: SessionCredentialBinding[];
  blocker: string | null;
  requiredVaultIds: string[];
  resolutions: SessionMcpResolution[];
  vaultIds: string[];
}

const CREDENTIAL_READ_CONCURRENCY = 4;

export async function loadVaultCatalog(core: Pick<CoreProjectReader, "listVaults" | "listVaultCredentials">, signal?: AbortSignal): Promise<VaultCatalog> {
  const vaults = await listAllCollectionPages(
    (options) => core.listVaults(options),
    signal,
  );
  const credentialsByVault = new Map<string, VaultCredential[]>();
  let nextIndex = 0;

  const worker = async () => {
    while (nextIndex < vaults.length) {
      signal?.throwIfAborted();
      const vault = vaults[nextIndex];
      nextIndex += 1;
      if (!vault) return;
      const credentials = await listAllCollectionPages(
        (options) => core.listVaultCredentials(vault.id, options),
        signal,
      );
      credentialsByVault.set(vault.id, credentials);
    }
  };

  await Promise.all(
    Array.from({ length: Math.min(CREDENTIAL_READ_CONCURRENCY, vaults.length) }, () => worker()),
  );
  signal?.throwIfAborted();
  return {
    vaults,
    credentials: vaults.flatMap((vault) => credentialsByVault.get(vault.id) ?? []),
  };
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function savedMCPTools(agent: SavedAgent): Array<{
  credentialId: string | null;
  serverLabel: string;
  serverURL: string;
}> {
  const tools: Array<{ credentialId: string | null; serverLabel: string; serverURL: string }> = [];
  for (const rawTool of agent.tools) {
    const tool = record(rawTool);
    const transport = record(tool?.transport);
    if (
      tool?.type !== "mcp" ||
      typeof tool.server_label !== "string" ||
      transport?.type !== "http" ||
      typeof transport.server_url !== "string"
    ) continue;
    tools.push({
      credentialId: typeof tool.credential_id === "string" ? tool.credential_id : null,
      serverLabel: tool.server_label,
      serverURL: transport.server_url,
    });
  }
  return tools;
}

export function agentNeedsCredentialCatalog(agent: SavedAgent): boolean {
  return savedMCPTools(agent).some((tool) => tool.credentialId !== null);
}

export function deriveSessionVaultPlan(
  agent: SavedAgent,
  catalog: VaultCatalog | null,
  manualVaultIds: readonly string[] = [],
): SessionVaultPlan {
  const tools = savedMCPTools(agent);
  const credentialed = tools.filter((tool) => tool.credentialId !== null);
  const empty = (blocker: string | null): SessionVaultPlan => ({
    bindings: [],
    blocker,
    requiredVaultIds: [],
    resolutions: [],
    vaultIds: [],
  });
  if (new Set(manualVaultIds).size !== manualVaultIds.length) {
    return empty(tv("errors.uniqueAttachments"));
  }
  if (!catalog && (credentialed.length > 0 || manualVaultIds.length > 0)) {
    return {
      ...empty(tv("errors.catalogIncomplete")),
      blocker: tv("errors.catalogIncomplete"),
    };
  }

  if (!catalog) {
    return {
      ...empty(null),
      resolutions: tools.map((tool) => ({
        kind: "anonymous",
        serverLabel: tool.serverLabel,
        serverURL: tool.serverURL,
      })),
    };
  }

  const credentials = new Map(catalog.credentials.map((credential) => [credential.id, credential]));
  const vaults = new Map(catalog.vaults.map((vault) => [vault.id, vault]));
  const bindings: SessionCredentialBinding[] = [];
  const requiredVaultIds = new Set<string>();
  for (const vaultId of manualVaultIds) {
    if (!vaults.has(vaultId)) {
      return empty(tv("errors.vaultMissing"));
    }
  }
  for (const tool of credentialed) {
    const credential = credentials.get(tool.credentialId as string);
    if (!credential || credential.auth.mcp_server_url !== tool.serverURL) {
      return {
        bindings: [],
        blocker: tv("errors.credentialMismatch", { server: tool.serverLabel }),
        requiredVaultIds: [],
        resolutions: [],
        vaultIds: [],
      };
    }
    const vault = vaults.get(credential.vault_id);
    if (!vault) {
      return {
        bindings: [],
        blocker: tv("errors.credentialVaultMissing", { server: tool.serverLabel }),
        requiredVaultIds: [],
        resolutions: [],
        vaultIds: [],
      };
    }
    requiredVaultIds.add(vault.id);
    bindings.push({ credential, selection: "explicit", serverLabel: tool.serverLabel, vault });
  }

  const vaultIds = [...new Set([...manualVaultIds, ...requiredVaultIds])].sort();
  const attached = new Set(vaultIds);
  const resolutions: SessionMcpResolution[] = [];
  const explicitByLabel = new Map(bindings.map((binding) => [binding.serverLabel, binding]));
  for (const tool of tools) {
    const explicit = explicitByLabel.get(tool.serverLabel);
    if (explicit) {
      resolutions.push({
        credential: explicit.credential,
        kind: "explicit",
        serverLabel: tool.serverLabel,
        serverURL: tool.serverURL,
        vault: explicit.vault,
      });
      continue;
    }
    const matches = catalog.credentials.filter((credential) => (
      attached.has(credential.vault_id) && credential.auth.mcp_server_url === tool.serverURL
    ));
    if (matches.length > 1) {
      return {
        bindings,
        blocker: tv("errors.multipleCredentials", { server: tool.serverLabel }),
        requiredVaultIds: [...requiredVaultIds].sort(),
        resolutions,
        vaultIds,
      };
    }
    const credential = matches[0];
    if (!credential) {
      resolutions.push({ kind: "anonymous", serverLabel: tool.serverLabel, serverURL: tool.serverURL });
      continue;
    }
    const vault = vaults.get(credential.vault_id);
    if (!vault) {
      return {
        bindings,
        blocker: tv("errors.matchedVaultMissing", { server: tool.serverLabel }),
        requiredVaultIds: [...requiredVaultIds].sort(),
        resolutions,
        vaultIds,
      };
    }
    const binding: SessionCredentialBinding = {
      credential,
      selection: "implicit",
      serverLabel: tool.serverLabel,
      vault,
    };
    bindings.push(binding);
    resolutions.push({
      credential,
      kind: "implicit",
      serverLabel: tool.serverLabel,
      serverURL: tool.serverURL,
      vault,
    });
  }

  return {
    bindings,
    blocker: null,
    requiredVaultIds: [...requiredVaultIds].sort(),
    resolutions,
    vaultIds,
  };
}

export function vaultName(vault: Vault): string {
  return vault.name ?? tv("unnamed");
}

export function matchingCredentials(catalog: VaultCatalog | null, serverURL: string): VaultCredential[] {
  if (!catalog) return [];
  return catalog.credentials.filter((credential) => credential.auth.mcp_server_url === serverURL);
}
