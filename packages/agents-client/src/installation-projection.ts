import { isNonnegativeInteger, isRecord } from "./response-projection";
import type { EnvironmentInstallation } from "./types";

/** Preserve Core-generated commands; clients never reconstruct authorization. */
export function projectEnvironmentInstallation(value: unknown): EnvironmentInstallation | null {
  if (!isRecord(value) || typeof value.version !== "string") return null;
  if (value.status === "unavailable" && typeof value.message === "string") return { status: "unavailable", version: value.version, message: value.message };
  if (value.status !== "available" || !isNonnegativeInteger(value.expires_at) || !isRecord(value.commands) || typeof value.commands.posix !== "string" || typeof value.commands.powershell !== "string") return null;
  return { status: "available", version: value.version, expires_at: value.expires_at, commands: { posix: value.commands.posix, powershell: value.commands.powershell } };
}
