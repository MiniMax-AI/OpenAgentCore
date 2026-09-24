import { AgentCoreError } from "@agents-core-web/agents-client";

export type BackendFailureStatus = "502" | "503" | "504" | "network";

export const BACKEND_NOT_READY_NOTICE =
  "Agent Core backend is not ready. Open the Overview startup guide.";

export function backendFailureStatus(error: unknown): BackendFailureStatus | null {
  if (
    error instanceof AgentCoreError &&
    (error.status === 502 || error.status === 503 || error.status === 504)
  ) {
    return String(error.status) as BackendFailureStatus;
  }

  const message = typeof error === "string"
    ? error
    : error instanceof Error
      ? error.message
      : "";
  const gatewayStatus = message.match(/\((502|503|504)\)/)?.[1];
  if (gatewayStatus) return gatewayStatus as BackendFailureStatus;
  return /failed to fetch|networkerror/i.test(message) ? "network" : null;
}
