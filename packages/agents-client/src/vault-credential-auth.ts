import type { McpOAuthRefreshMetadata, VaultCredential } from "./types";

const oauthFields = new Set(["type", "mcp_server_url", "expires_at", "refresh"]);
const refreshFields = new Set(["client_id", "token_endpoint", "token_endpoint_auth", "resource", "scope"]);

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
  const fields = Object.keys(value);
  if (value.type === "static_bearer" && fields.length === 2) {
    return { type: "static_bearer", mcp_server_url: value.mcp_server_url };
  }
  if (
    value.type !== "mcp_oauth" || fields.length !== oauthFields.size || !fields.every((field) => oauthFields.has(field)) ||
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
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  const refresh = value as Record<string, unknown>;
  const fields = Object.keys(refresh);
  if (
    fields.length !== refreshFields.size || !fields.every((field) => refreshFields.has(field)) ||
    typeof refresh.client_id !== "string" || !validCredentialURL(refresh.token_endpoint) ||
    !(refresh.resource === null || typeof refresh.resource === "string") ||
    !(refresh.scope === null || typeof refresh.scope === "string") ||
    refresh.token_endpoint_auth === null || typeof refresh.token_endpoint_auth !== "object" || Array.isArray(refresh.token_endpoint_auth)
  ) return null;
  const auth = refresh.token_endpoint_auth as Record<string, unknown>;
  if (
    Object.keys(auth).length !== 1 ||
    (auth.type !== "none" && auth.type !== "client_secret_basic" && auth.type !== "client_secret_post")
  ) return null;
  return {
    client_id: refresh.client_id,
    token_endpoint: refresh.token_endpoint,
    token_endpoint_auth: { type: auth.type },
    resource: refresh.resource,
    scope: refresh.scope,
  };
}
