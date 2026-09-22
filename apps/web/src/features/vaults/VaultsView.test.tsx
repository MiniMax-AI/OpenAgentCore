import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { VaultsView, type VaultOperations } from "./VaultsView";

const operations: VaultOperations = {
  createVault: async () => undefined,
  createCredential: async () => undefined,
  replaceCredential: async () => undefined,
  deleteCredential: async () => undefined,
  deleteVault: async () => undefined,
  refresh: () => undefined,
};

describe("Vaults view", () => {
  it("shows safe Credential metadata without inventing status or token values", () => {
    const html = renderToStaticMarkup(<VaultsView
      busy={false}
      coreError={null}
      coreState="ready"
      operations={operations}
      catalog={{
        vaults: [{ id: "11111111-1111-4111-8111-111111111111", object: "vault", created_at: 1, name: "Runtime", metadata: {} }],
        credentials: [{
          id: "22222222-2222-4222-8222-222222222222",
          vault_id: "11111111-1111-4111-8111-111111111111",
          object: "vault.credential",
          name: "Internal MCP",
          auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
          created_at: 2,
          updated_at: 2,
        }],
      }}
    />);

    expect(html).toContain("Runtime");
    expect(html).toContain("Internal MCP");
    expect(html).toContain("https://mcp.example/tools");
    expect(html).toContain("token hidden");
    expect(html).not.toContain("active");
    expect(html).not.toContain("archived");
  });

  it("shows OAuth metadata with application guidance and only offers static token replacement", () => {
    const vaultId = "11111111-1111-4111-8111-111111111111";
    const html = renderToStaticMarkup(<VaultsView
      busy={false} coreError={null} coreState="ready" operations={operations}
      catalog={{
        vaults: [{ id: vaultId, object: "vault", created_at: 1, name: "Runtime", metadata: {} }],
        credentials: [{
          id: "22222222-2222-4222-8222-222222222222", vault_id: vaultId,
          object: "vault.credential", name: "OAuth MCP", created_at: 2, updated_at: 2,
          auth: { type: "mcp_oauth", mcp_server_url: "https://oauth.example/tools", expires_at: null, refresh: null },
        }, {
          id: "33333333-3333-4333-8333-333333333333", vault_id: vaultId,
          object: "vault.credential", name: "Static MCP", created_at: 2, updated_at: 2,
          auth: { type: "static_bearer", mcp_server_url: "https://static.example/tools" },
        }],
      }}
    />);
    expect(html).toContain("OAuth MCP");
    expect(html).toContain("Manage authorization and token replacement in your application.");
    expect(html).not.toContain('aria-label="Replace token for OAuth MCP"');
    expect(html).toContain('aria-label="Replace token for Static MCP"');
    expect(html).toContain('aria-label="Delete OAuth MCP"');
  });

  it("keeps an incomplete catalog in a durable failed state", () => {
    const html = renderToStaticMarkup(<VaultsView busy={false} catalog={null} coreError="safe failure" coreState="failed" operations={operations} />);
    expect(html).toContain("Couldn’t load Vaults");
    expect(html).toContain("Credentialed Sessions stay blocked");
  });
});
