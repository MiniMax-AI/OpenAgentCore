export interface SandboxConsoleConfig {
  sandbox_admin: boolean;
  api_keys?: boolean;
  node_installer: boolean;
  node_installer_sha256: string;
}

/**
 * The console's capability flags. An absent endpoint (404, an older console)
 * means no sandbox administration; any other failure is thrown so callers
 * report a failed read instead of "not configured".
 */
export async function sandboxConsoleConfig(signal: AbortSignal): Promise<SandboxConsoleConfig | null> {
  const response = await fetch("/console/config", { credentials: "include", signal });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`The console configuration could not be read (HTTP ${response.status}).`);
  const config = await response.json() as Partial<SandboxConsoleConfig>;
  return {
    sandbox_admin: config.sandbox_admin === true,
    ...(typeof config.api_keys === "boolean" ? { api_keys: config.api_keys } : {}),
    node_installer: config.node_installer === true && /^[a-f0-9]{64}$/.test(config.node_installer_sha256 ?? ""),
    node_installer_sha256: config.node_installer_sha256 ?? "",
  };
}
