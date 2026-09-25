import type { SandboxConsoleConfig } from "../sandbox/console-config";
import { selfHostedInstallCommand } from "../sandbox/enrollment-command";

/**
 * What the Connect a host panel shows for a self-hosted Environment: nothing
 * unless the console serves the self-hosted installer with a verified digest;
 * a note when Core has no public address, or when the Session's `remote_url`
 * is not `wss://` (the installer connects only over TLS); otherwise the
 * command.
 */
export type ExecutorInstall =
  | { kind: "unavailable" }
  | { kind: "no_address" }
  | { kind: "not_wss"; remoteUrl: string }
  | { kind: "ready"; command: string; publicUrl: string };

export function executorInstall({ config, publicUrl, environmentId, remoteUrl }: {
  config: SandboxConsoleConfig | null | undefined;
  publicUrl: string | null;
  environmentId: string;
  remoteUrl: string;
}): ExecutorInstall {
  if (!config?.self_hosted_installer || !/^[a-f0-9]{64}$/.test(config.self_hosted_installer_sha256)) return { kind: "unavailable" };
  if (!publicUrl) return { kind: "no_address" };
  if (!remoteUrl.startsWith("wss://")) return { kind: "not_wss", remoteUrl };
  return { kind: "ready", publicUrl, command: selfHostedInstallCommand({ publicUrl, digest: config.self_hosted_installer_sha256, environmentId, remoteUrl }) };
}
