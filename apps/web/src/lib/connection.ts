import { OpenAIAgentsClient, type AgentCore } from "@agents-core-web/agents-client";

export interface CoreConnection {
  baseUrl: string;
  token: string;
}

export type CoreConnectionState = "connecting" | "ready" | "failed";

export class InvalidCoreConnectionError extends Error {
  readonly code = "invalid_core_base_url";

  constructor() {
    super("The Agent Core base URL is invalid or unsafe.");
    this.name = "InvalidCoreConnectionError";
  }
}

const BASE_URL_KEY = "agents-core-web.core-base-url";
const TOKEN_KEY = "agents-core-web.core-token";
const devProxyAuthEnabled = import.meta.env.DEV && __AGENTS_CORE_WEB_DEV_PROXY_AUTH__;

export function isLocalProxyBaseUrl(baseUrl: string): boolean {
  return (baseUrl.trim() || "/v1").replace(/\/+$/, "") === "/v1";
}

export function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase();
  return (
    normalized === "localhost" ||
    normalized.endsWith(".localhost") ||
    normalized === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(normalized)
  );
}

function hasExplicitUserInfo(candidate: string): boolean {
  const schemeEnd = candidate.indexOf(":");
  if (schemeEnd < 0) return false;
  const remainder = candidate.slice(schemeEnd + 1).replace(/^[\\/]+/, "");
  const authorityEnd = remainder.search(/[\\/?#]/);
  const authority = authorityEnd < 0 ? remainder : remainder.slice(0, authorityEnd);
  return authority.includes("@");
}

export function isValidDirectCoreBaseUrl(value: string): boolean {
  try {
    const candidate = value.trim();
    if (candidate.includes("?") || candidate.includes("#") || hasExplicitUserInfo(candidate)) return false;
    const url = new URL(candidate);
    const secureTransport = url.protocol === "https:" || (url.protocol === "http:" && isLoopbackHostname(url.hostname));
    return secureTransport && !url.username && !url.password && !url.search && !url.hash;
  } catch {
    return false;
  }
}

function usesCurrentTabToken(baseUrl: string): boolean {
  return !isLocalProxyBaseUrl(baseUrl) && isValidDirectCoreBaseUrl(baseUrl);
}

function safeStoredBaseUrl(baseUrl: string): string {
  const candidate = baseUrl.trim() || "/v1";
  return isLocalProxyBaseUrl(candidate) || isValidDirectCoreBaseUrl(candidate) ? candidate : "/v1";
}

function runtimeBaseUrl(baseUrl: string): string {
  const candidate = baseUrl.trim() || "/v1";
  if (isLocalProxyBaseUrl(candidate) || isValidDirectCoreBaseUrl(candidate)) return candidate;
  throw new InvalidCoreConnectionError();
}

export function loadConnection(_proxyAuthEnabled = devProxyAuthEnabled): CoreConnection {
  const storedBaseUrl = localStorage.getItem(BASE_URL_KEY) ?? "/v1";
  const baseUrl = safeStoredBaseUrl(storedBaseUrl);
  if (baseUrl !== storedBaseUrl) localStorage.setItem(BASE_URL_KEY, baseUrl);
  const currentTabToken = usesCurrentTabToken(baseUrl);
  if (!currentTabToken) sessionStorage.removeItem(TOKEN_KEY);
  return {
    baseUrl,
    token: currentTabToken ? sessionStorage.getItem(TOKEN_KEY) ?? "" : "",
  };
}

export function saveConnection(connection: CoreConnection, _proxyAuthEnabled = devProxyAuthEnabled): void {
  const baseUrl = safeStoredBaseUrl(connection.baseUrl);
  localStorage.setItem(BASE_URL_KEY, baseUrl);
  if (connection.token && usesCurrentTabToken(baseUrl)) {
    sessionStorage.setItem(TOKEN_KEY, connection.token);
  } else sessionStorage.removeItem(TOKEN_KEY);
}

export function createCore(connection: CoreConnection, _proxyAuthEnabled = devProxyAuthEnabled): AgentCore {
  const baseUrl = runtimeBaseUrl(connection.baseUrl);
  const currentTabToken = usesCurrentTabToken(baseUrl);
  if (!currentTabToken) sessionStorage.removeItem(TOKEN_KEY);
  return new OpenAIAgentsClient({
    baseUrl,
    token: currentTabToken ? () => sessionStorage.getItem(TOKEN_KEY) ?? connection.token : undefined,
  });
}
