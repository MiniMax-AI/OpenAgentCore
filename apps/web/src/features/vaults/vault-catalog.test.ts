import { describe, expect, it, vi } from "vitest";

import type { CoreProjectReader, SavedAgent, Vault, VaultCredential } from "@agents-core-web/agents-client";

import { deriveSessionVaultPlan, loadVaultCatalog, matchingCredentials, type VaultCatalog } from "./vault-catalog";

const vaultA: Vault = { id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 2, name: "A", metadata: {} };
const vaultB: Vault = { id: "22222222-2222-4222-8222-222222222222", object: "vault", created_at: 1, name: "B", metadata: {} };
const credentialA: VaultCredential = {
  id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  vault_id: vaultA.id,
  name: "A MCP",
  object: "vault.credential",
  auth: { type: "static_bearer", mcp_server_url: "https://a.example/tools" },
  created_at: 3,
  updated_at: 3,
};
const credentialB: VaultCredential = {
  id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  vault_id: vaultB.id,
  name: "B MCP",
  object: "vault.credential",
  auth: { type: "static_bearer", mcp_server_url: "https://b.example/tools" },
  created_at: 4,
  updated_at: 4,
};

function agent(tools: unknown[]): SavedAgent {
  return {
    id: "agent",
    object: "agent",
    model: "codex",
    name: "Agent",
    instructions: null,
    metadata: {},
    multi_agent: { enabled: false, max_concurrent_subagents: null },
    reasoning: { effort: null, summary: null },
    service_tier: "auto",
    text: { format: { type: "text" }, verbosity: "medium" },
    tools,
    created_at: 1,
    updated_at: 1,
  };
}

function mcp(serverLabel: string, serverURL: string, credentialId?: string | null) {
  return {
    type: "mcp",
    server_label: serverLabel,
    transport: { type: "http", server_url: serverURL },
    connection_origin: "service",
    credential_id: credentialId ?? null,
  };
}

describe("Vault catalog", () => {
  it("loads every Vault and Credential page", async () => {
    const listVaults = vi.fn(async ({ after }: { after?: string }) => after
      ? { object: "list" as const, data: [vaultB], has_more: false, first_id: vaultB.id, last_id: vaultB.id }
      : { object: "list" as const, data: [vaultA], has_more: true, first_id: vaultA.id, last_id: vaultA.id });
    const listVaultCredentials = vi.fn(async (vaultId: string) => ({
      object: "list" as const,
      data: vaultId === vaultA.id ? [credentialA] : [credentialB],
      has_more: false,
      first_id: vaultId === vaultA.id ? credentialA.id : credentialB.id,
      last_id: vaultId === vaultA.id ? credentialA.id : credentialB.id,
    }));
    const core = { listVaults, listVaultCredentials } as unknown as CoreProjectReader;

    await expect(loadVaultCatalog(core)).resolves.toEqual({
      vaults: [vaultA, vaultB],
      credentials: [credentialA, credentialB],
    });
    expect(listVaults).toHaveBeenCalledTimes(2);
    expect(listVaultCredentials).toHaveBeenCalledTimes(2);
  });

  it("derives sorted unique owning Vaults from explicit exact-URL Credentials", () => {
    const catalog: VaultCatalog = { vaults: [vaultB, vaultA], credentials: [credentialB, credentialA] };
    const plan = deriveSessionVaultPlan(agent([
      mcp("b", credentialB.auth.mcp_server_url, credentialB.id),
      mcp("a", credentialA.auth.mcp_server_url, credentialA.id),
      mcp("a-again", credentialA.auth.mcp_server_url, credentialA.id),
    ]), catalog);

    expect(plan.blocker).toBeNull();
    expect(plan.vaultIds).toEqual([vaultA.id, vaultB.id]);
    expect(plan.requiredVaultIds).toEqual([vaultA.id, vaultB.id]);
    expect(plan.bindings.map((binding) => binding.serverLabel)).toEqual(["b", "a", "a-again"]);
    expect(plan.resolutions.map((resolution) => resolution.kind)).toEqual(["explicit", "explicit", "explicit"]);
    expect(matchingCredentials(catalog, credentialA.auth.mcp_server_url)).toEqual([credentialA]);
  });

  it("blocks missing catalogs and URL mismatches", () => {
    const credentialed = agent([mcp("a", credentialA.auth.mcp_server_url, credentialA.id)]);
    expect(deriveSessionVaultPlan(credentialed, null).blocker).toContain("not fully loaded");
    expect(deriveSessionVaultPlan(agent([mcp("a", "https://wrong.example/tools", credentialA.id)]), {
      vaults: [vaultA], credentials: [credentialA],
    }).blocker).toContain("URL-mismatched");
  });

  it("previews anonymous, unique implicit, ambiguous, and stale manual Vault selection", () => {
    const anonymous = agent([mcp("docs", credentialA.auth.mcp_server_url)]);
    const catalog: VaultCatalog = {
      vaults: [vaultA, vaultB],
      credentials: [credentialA, credentialB],
    };

    expect(deriveSessionVaultPlan(anonymous, catalog)).toMatchObject({
      blocker: null,
      vaultIds: [],
      requiredVaultIds: [],
      resolutions: [{ kind: "anonymous", serverLabel: "docs" }],
    });
    expect(deriveSessionVaultPlan(anonymous, catalog, [vaultA.id])).toMatchObject({
      blocker: null,
      vaultIds: [vaultA.id],
      requiredVaultIds: [],
      resolutions: [{
        kind: "implicit",
        serverLabel: "docs",
        credential: credentialA,
        vault: vaultA,
      }],
    });

    const duplicate: VaultCredential = {
      ...credentialB,
      auth: { type: "static_bearer", mcp_server_url: credentialA.auth.mcp_server_url },
    };
    const ambiguous = deriveSessionVaultPlan(anonymous, {
      vaults: [vaultA, vaultB],
      credentials: [credentialA, duplicate],
    }, [vaultA.id, vaultB.id]);
    expect(ambiguous.blocker).toContain("multiple Credentials");
    expect(ambiguous.vaultIds).toEqual([vaultA.id, vaultB.id]);

    expect(deriveSessionVaultPlan(anonymous, catalog, ["33333333-3333-4333-8333-333333333333"]).blocker)
      .toContain("no longer available");
  });
});
