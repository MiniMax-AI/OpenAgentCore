import { createContext, useContext, useMemo, type ReactNode } from "react";
import { SandboxProjectClient } from "@agents-core-web/agents-client";
import { isLocalProxyBaseUrl, type CoreConnection } from "../../lib/connection";

const SandboxContext = createContext<SandboxProjectClient | null>(null);
export function SandboxProvider({ connection, children }: { connection: CoreConnection; children: ReactNode }) {
  const client = useMemo(() => new SandboxProjectClient({
    baseUrl: connection.baseUrl,
    token: isLocalProxyBaseUrl(connection.baseUrl) ? undefined : connection.token,
  }), [connection]);
  return <SandboxContext.Provider value={client}>{children}</SandboxContext.Provider>;
}
export function useSandboxClient() { return useContext(SandboxContext); }

export function sandboxAdminBaseUrl(projectBaseUrl: string): string {
  if (isLocalProxyBaseUrl(projectBaseUrl)) return "/core/v1/sandbox";
  return `${projectBaseUrl.replace(/\/+$/, "").replace(/\/v1$/, "")}/core/v1/sandbox`;
}
