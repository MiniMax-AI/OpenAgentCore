export interface SandboxConsoleConfig {
  sandbox_admin: boolean;
  node_installer: boolean;
  node_installer_sha256: string;
}

export async function sandboxConsoleConfig(signal: AbortSignal): Promise<SandboxConsoleConfig | null> {
  try {
    const response = await fetch("/console/config", { credentials: "include", signal });
    if (!response.ok) return null;
    const config = await response.json() as Partial<SandboxConsoleConfig>;
    return {
      sandbox_admin: config.sandbox_admin === true,
      node_installer: config.node_installer === true && /^[a-f0-9]{64}$/.test(config.node_installer_sha256 ?? ""),
      node_installer_sha256: config.node_installer_sha256 ?? "",
    };
  } catch { return null; }
}
