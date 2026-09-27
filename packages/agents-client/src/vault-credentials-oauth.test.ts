import { describe, expect, it } from "vitest";

import { OpenAIAgentsClient } from "./client";
import type { McpOAuthCredentialAuth, VaultCredential } from "./types";

const vaultId = "11111111-1111-4111-8111-111111111111";
const credentialId = "22222222-2222-4222-8222-222222222222";
const staticCredential: VaultCredential = {
  id: credentialId,
  vault_id: vaultId,
  object: "vault.credential",
  name: "Internal MCP",
  auth: { type: "static_bearer", mcp_server_url: "https://mcp.example/tools" },
  created_at: 1,
  updated_at: 2,
};
const oauthAuth: McpOAuthCredentialAuth = {
  type: "mcp_oauth",
  mcp_server_url: "https://mcp.example/tools",
  expires_at: "2030-01-01T00:00:00Z",
  refresh: {
    client_id: "public-client-id",
    token_endpoint: "https://issuer.example/token",
    token_endpoint_auth: { type: "none" },
    resource: "https://mcp.example/tools",
    scope: "read",
  },
};
const oauthCredential: VaultCredential = { ...staticCredential, auth: oauthAuth };
const jsonResponse = (body: unknown, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "content-type": "application/json" },
});

describe("OAuth Credential metadata", () => {
  it.each(["none", "client_secret_basic", "client_secret_post"] as const)("reads safe %s metadata", async (type) => {
    const credential = { ...oauthCredential, auth: {
      ...oauthAuth, refresh: { ...oauthAuth.refresh!, token_endpoint_auth: { type } },
    } };
    const client = new OpenAIAgentsClient({ fetch: (async () => jsonResponse(credential)) as typeof fetch });
    await expect(client.retrieveVaultCredential(vaultId, credentialId)).resolves.toEqual(credential);
  });

  it("reads a mixed list with nullable OAuth fields", async () => {
    const oauth = { ...oauthCredential, id: "33333333-3333-4333-8333-333333333333", auth: {
      ...oauthAuth, expires_at: null, refresh: null,
    } };
    const page = { object: "list", data: [oauth, staticCredential], has_more: false, first_id: oauth.id, last_id: staticCredential.id };
    const client = new OpenAIAgentsClient({ fetch: (async () => jsonResponse(page)) as typeof fetch });
    await expect(client.listVaultCredentials(vaultId)).resolves.toEqual(page);
  });

  it("preserves nullable scope and resource", async () => {
    const credential = { ...oauthCredential, auth: {
      ...oauthAuth, refresh: { ...oauthAuth.refresh!, resource: null, scope: null },
    } };
    const client = new OpenAIAgentsClient({ fetch: (async () => jsonResponse(credential)) as typeof fetch });
    await expect(client.retrieveVaultCredential(vaultId, credentialId)).resolves.toEqual(credential);
  });

  it.each([
    { ...oauthAuth, access_token: "unexpected-secret" },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, refresh_token: "unexpected-secret" } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, token_endpoint_auth: { type: "client_secret_basic", client_secret: "unexpected-secret" } } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, extra: "unexpected-secret" } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, token_endpoint_auth: { type: "unsupported" } } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, token_endpoint: "http://issuer.example/token" } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, token_endpoint: "https://user:pass@issuer.example/token" } },
    { ...oauthAuth, refresh: { ...oauthAuth.refresh, scope: 1 } },
    { ...oauthAuth, expires_at: 1 },
    { type: "mcp_oauth", mcp_server_url: oauthAuth.mcp_server_url },
  ])("rejects secret-bearing or malformed OAuth metadata case %#", async (auth) => {
    const client = new OpenAIAgentsClient({ fetch: (async () => jsonResponse({ ...oauthCredential, auth })) as typeof fetch });
    await expect(client.retrieveVaultCredential(vaultId, credentialId)).rejects.toMatchObject({
      status: 502,
      code: "invalid_vault_credential",
      message: "OpenAgentCore returned invalid Credential metadata.",
    });
  });

  it("blocks static replacement of OAuth before sending a write", async () => {
    const calls: Array<RequestInit | undefined> = [];
    const client = new OpenAIAgentsClient({ fetch: (async (_input, init) => {
      calls.push(init);
      return jsonResponse(oauthCredential);
    }) as typeof fetch });
    await expect(client.replaceVaultCredentialToken(vaultId, credentialId, {
      auth: { type: "static_bearer", token: "replacement" },
    })).rejects.toThrow("Static bearer token replacement is unavailable for OAuth credentials.");
    expect(calls).toHaveLength(1);
    expect(calls.some((call) => call?.method === "POST")).toBe(false);
  });

  it("rejects an OAuth response to static creation", async () => {
    const client = new OpenAIAgentsClient({ fetch: (async () => jsonResponse(oauthCredential, 201)) as typeof fetch });
    await expect(client.createVaultCredential(vaultId, {
      name: staticCredential.name,
      auth: { type: "static_bearer", mcp_server_url: staticCredential.auth.mcp_server_url, token: "creation" },
    })).rejects.toMatchObject({ status: 502, code: "invalid_vault_credential" });
  });

  it("rejects an auth type change after static replacement", async () => {
    const client = new OpenAIAgentsClient({ fetch: (async (_input, init) => jsonResponse(
      init?.method === "POST" ? oauthCredential : staticCredential,
    )) as typeof fetch });
    await expect(client.replaceVaultCredentialToken(vaultId, credentialId, {
      auth: { type: "static_bearer", token: "replacement" },
    })).rejects.toMatchObject({ status: 502, code: "invalid_vault_credential" });
  });
});
