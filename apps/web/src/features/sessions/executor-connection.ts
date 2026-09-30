import type { ExecutorCredentialList } from "@oac/agents-client";

export type ExecutorConnectionState = "never_enrolled" | "connected" | "disconnected" | "revoked" | "unknown";

export function boundExecutorCredential(read: ExecutorCredentialList | undefined) {
  const boundId = read?.connection.bound_key_id;
  return boundId ? read.data.find((credential) => credential.key_id.toLowerCase() === boundId.toLowerCase()) : undefined;
}

/** Core alone decides connectivity; a revoked matching row adds the recovery reason. */
export function executorConnectionState(read: ExecutorCredentialList | undefined, stale: boolean): ExecutorConnectionState {
  if (!read || stale) return "unknown";
  if (read.connection.status === "disconnected" && boundExecutorCredential(read)?.revoked_at) return "revoked";
  return read.connection.status;
}
