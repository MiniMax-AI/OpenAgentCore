import { randomUUID } from "node:crypto";
import {
  auth,
  selectClientAuthMethod,
  selectResourceURL,
} from "@modelcontextprotocol/sdk/client/auth.js";
import { AppError, text, uuid } from "./store.mjs";

const https = (value) => {
  const url = new URL(value);
  if (url.protocol !== "https:" || url.username || url.password || url.hash)
    throw new AppError(
      400,
      "OAuth requires an HTTPS URL without credentials or a fragment.",
    );
  return url.toString();
};

// The MCP SDK owns discovery, issuer binding, registration and PKCE. Product
// state only survives the browser redirect; the public Vault owns saved tokens.
export function mcpOAuthAPI(store, core, origin, fetchImpl = fetch) {
  const active = new Set();
  const network = async (url, options) => {
    https(String(url));
    return fetchImpl(url, {
      ...options,
      redirect: "error",
      signal: AbortSignal.timeout(30000),
    });
  };
  return async (method, path, body) => {
    const match = path.match(/^\/app\/mcp-oauth\/([a-f0-9-]{36})$/);
    if (!match || !uuid.test(match[1]) || method !== "POST")
      throw new AppError(404, "Not found.");
    const id = match[1];
    if (active.has(id))
      throw new AppError(409, "Authorization is already processing.");
    active.add(id);
    try {
      let row = store.get("mcp_oauth", id);
      if (!row) {
        if (body.action !== "begin")
          throw new AppError(404, "Authorization not found.");
        if (!uuid.test(body.vault_id || ""))
          throw new AppError(400, "Select a Vault.");
        await core(`/v1/vaults/${body.vault_id}`, "GET");
        row = {
          id,
          state: randomUUID(),
          name: text(body.name, "Credential name", 80, true),
          url: https(body.url),
          vault_id: body.vault_id,
          redirect: `${origin}/oauth/callback`,
          expires_at: Date.now() + 15 * 60000,
        };
        store.put("mcp_oauth", row);
      }
      if (body.action === "complete" && body.state !== row.state)
        throw new AppError(400, "Authorization state does not match.");
      if (row.credential_id)
        return { credential_id: row.credential_id, vault_id: row.vault_id };
      if (Date.now() > row.expires_at) {
        store.remove("mcp_oauth", id);
        throw new AppError(410, "Authorization expired; start again.");
      }
      const save = (values) => {
        row = { ...row, ...values };
        store.put("mcp_oauth", row);
      };
      const provider = {
        redirectUrl: row.redirect,
        clientMetadata: {
          client_name: "Parsar",
          redirect_uris: [row.redirect],
          grant_types: ["authorization_code", "refresh_token"],
          response_types: ["code"],
          token_endpoint_auth_method: "none",
        },
        state: () => `${id}:${row.state}`,
        clientInformation: () => row.client,
        saveClientInformation: (client) => save({ client }),
        tokens: () => row.tokens,
        saveTokens: (tokens) => save({ tokens, token_time: Date.now() }),
        redirectToAuthorization: (url) =>
          save({ authorization_url: https(String(url)) }),
        saveCodeVerifier: (verifier) => save({ verifier }),
        codeVerifier: () => row.verifier,
        discoveryState: () => row.discovery,
        saveDiscoveryState: (discovery) => save({ discovery }),
      };
      if (body.action === "begin") {
        if (!row.authorization_url)
          await auth(provider, { serverUrl: row.url, fetchFn: network });
        return { authorization_url: row.authorization_url };
      }
      if (body.action !== "complete" || !body.code)
        throw new AppError(400, "Missing authorization code.");
      if (!row.tokens)
        await auth(provider, {
          serverUrl: row.url,
          authorizationCode: text(body.code, "Authorization code", 10000, true),
          fetchFn: network,
        });
      if (!row.tokens?.access_token)
        throw new AppError(
          502,
          "Authorization did not return an access token.",
        );
      // Vault creation has no idempotency key. Recover a lost response through
      // a unique product operation name before issuing another create request.
      const name = `${row.name} · ${id}`;
      let after, existing;
      do {
        const page = await core(
          `/v1/vaults/${row.vault_id}/credentials?limit=100${after ? `&after=${encodeURIComponent(after)}` : ""}`,
          "GET",
        );
        existing = page.data.find(
          (item) => item.name === name && item.auth.mcp_server_url === row.url,
        );
        after = page.has_more ? page.last_id : undefined;
      } while (after && !existing);
      if (!existing) {
        const authValue = {
          type: "mcp_oauth",
          mcp_server_url: row.url,
          access_token: row.tokens.access_token,
        };
        if (row.tokens.expires_in)
          authValue.expires_at = new Date(
            row.token_time + row.tokens.expires_in * 1000,
          ).toISOString();
        if (row.tokens.refresh_token) {
          const metadata = row.discovery.authorizationServerMetadata;
          const method = selectClientAuthMethod(
            row.client,
            metadata?.token_endpoint_auth_methods_supported || [],
          );
          const resource = await selectResourceURL(
            row.url,
            provider,
            row.discovery.resourceMetadata,
          );
          authValue.refresh = {
            token_endpoint: https(
              metadata?.token_endpoint ||
                new URL("/token", row.discovery.authorizationServerUrl).href,
            ),
            client_id: row.client.client_id,
            refresh_token: row.tokens.refresh_token,
            token_endpoint_auth: {
              type: method,
              ...(method !== "none"
                ? { client_secret: row.client.client_secret }
                : {}),
            },
            ...(resource ? { resource: String(resource) } : {}),
            ...(row.tokens.scope ? { scope: row.tokens.scope } : {}),
          };
        }
        existing = await core(
          `/v1/vaults/${row.vault_id}/credentials`,
          "POST",
          { name, auth: authValue },
        );
      }
      store.put("mcp_oauth", {
        id,
        state: row.state,
        vault_id: row.vault_id,
        credential_id: existing.id,
        expires_at: row.expires_at,
      });
      return { credential_id: existing.id, vault_id: row.vault_id };
    } finally {
      active.delete(id);
    }
  };
}
