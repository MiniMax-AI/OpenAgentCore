export interface SandboxConsoleConfig {
  node_installer: boolean;
  node_installer_sha256: string;
  /** The providers whose node files this console serves; a null or malformed value reads as none. */
  node_artifacts: string[];
}

const SHA256 = /^[a-f0-9]{64}$/;

/**
 * The console's node installer, its digest and the providers it has node
 * files for. An installer is offered only with a well-formed SHA-256 digest.
 * A failed read is thrown so callers report it.
 */
export async function sandboxConsoleConfig(signal: AbortSignal): Promise<SandboxConsoleConfig> {
  const response = await fetch("/console/config", { credentials: "include", signal });
  if (!response.ok) throw new Error(`The console configuration could not be read (HTTP ${response.status}).`);
  const config = await response.json() as Partial<Omit<SandboxConsoleConfig, "node_artifacts">> & { node_artifacts?: unknown };
  return {
    node_installer: config.node_installer === true && SHA256.test(config.node_installer_sha256 ?? ""),
    node_installer_sha256: config.node_installer_sha256 ?? "",
    node_artifacts: nodeArtifacts(config.node_artifacts),
  };
}

/** A reported list keeps the providers it names; null or any other value means none. */
function nodeArtifacts(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : [];
}

/** Whether a node of this provider can install from the console's files. */
export function nodeFilesAvailable(config: SandboxConsoleConfig, provider: string): boolean {
  return config.node_artifacts.some((entry) => entry === provider);
}
