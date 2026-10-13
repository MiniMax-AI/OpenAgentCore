import type { McpOAuthRefreshMetadata, VaultCredential } from "./types";
import { exactFields, isRecord, variantFields } from "./response-projection";
import {
  mcpOauthRefreshResourceFields, mcpOauthTokenEndpointAuthResourceFields, vaultCredentialAuthResourceMcpOauthFields,
  vaultCredentialAuthResourceStaticBearerFields,
} from "./generated/public-api";

export function validCredentialURL(value: unknown): value is string {
  if (
    typeof value !== "string" || value !== value.trim() ||
    /[\u0000-\u0020\u007f\\]/u.test(value)
  ) return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && Boolean(url.hostname) && !url.username && !url.password && !url.hash;
  } catch {
    return false;
  }
}

// Each level is projected explicitly; unexpected fields may contain secrets.
export function projectVaultCredentialAuth(value: Record<string, unknown>): VaultCredential["auth"] | null {
  if (!validCredentialURL(value.mcp_server_url)) return null;
  if (value.type === "static_bearer" && exactFields(value, vaultCredentialAuthResourceStaticBearerFields)) {
    return { type: "static_bearer", mcp_server_url: value.mcp_server_url };
  }
  if (
    value.type !== "mcp_oauth" || !exactFields(value, vaultCredentialAuthResourceMcpOauthFields) ||
    !(value.expires_at === null || typeof value.expires_at === "string")
  ) return null;
  const refresh = value.refresh === null ? null : projectOAuthRefresh(value.refresh);
  if (value.refresh !== null && refresh === null) return null;
  return {
    type: "mcp_oauth",
    mcp_server_url: value.mcp_server_url,
    expires_at: value.expires_at,
    refresh,
  };
}

function projectOAuthRefresh(value: unknown): McpOAuthRefreshMetadata | null {
  if (!isRecord(value)) return null;
  const refresh = value;
  if (
    !exactFields(refresh, mcpOauthRefreshResourceFields) ||
    typeof refresh.client_id !== "string" || !validCredentialURL(refresh.token_endpoint) ||
    !(refresh.resource === null || typeof refresh.resource === "string") ||
    !(refresh.scope === null || typeof refresh.scope === "string") ||
    refresh.token_endpoint_auth === null || typeof refresh.token_endpoint_auth !== "object" || Array.isArray(refresh.token_endpoint_auth)
  ) return null;
  const auth = refresh.token_endpoint_auth as Record<string, unknown>;
  const authFields = variantFields(mcpOauthTokenEndpointAuthResourceFields, auth.type);
  if (!authFields || !exactFields(auth, authFields)) return null;
  return {
    client_id: refresh.client_id,
    token_endpoint: refresh.token_endpoint,
    token_endpoint_auth: { type: auth.type as McpOAuthRefreshMetadata["token_endpoint_auth"]["type"] },
    resource: refresh.resource,
    scope: refresh.scope,
  };
}
