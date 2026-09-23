import { AgentCoreError, type SandboxNode } from "@agents-core-web/agents-client";
import { translate, type Locale } from "./locale";
import type { MessageKey } from "./locale-strings";

const states: Record<string, MessageKey> = {
  reserved: "Reserved state", creating: "Creating", active: "Active", releasing: "Releasing", released: "Released", failed: "Failed", pending: "Pending",
  cleanup_pending: "Cleanup pending", disabled: "Disabled", waking: "Waking",
  running: "Running", quiescing: "Quiescing", suspending: "Suspending", suspended: "Suspended", restoring: "Restoring", stopped: "Stopped",
};
export function sandboxStateLabel(state: string, locale: Locale): string {
  return translate(locale, Object.hasOwn(states, state) ? states[state]! : "Unknown state");
}
export function sandboxRequestError(error: unknown, locale: Locale): string {
  let key: MessageKey = "The sandbox request failed. Refresh to check the current state before trying again.";
  if (error instanceof AgentCoreError) {
    if (error.code === "runtime_node_in_use") key = "The node has active allocations or retained resources. Clear allocations, snapshots, reservations and pending cleanup before removal.";
    else if (error.code === "runtime_node_unavailable") key = "The selected sandbox node is unavailable or has no capacity.";
    else if (error.code === "sandbox_deployment_conflict") key = "Sandbox deployment is already configured. Refresh to inspect the saved provider and Core origin.";
    else if (error.status === 401) key = "Sign in to the console again to access sandbox management.";
    else if (error.status === 403 || error.code === "sandbox_admin_not_configured") key = "Sandbox administration is not configured on this console. Ask the deployment administrator to configure access.";
    else if (error.status >= 500) key = "The sandbox service is unavailable. Refresh to check the current state.";
    else key = "The sandbox request was rejected. Refresh to check the current state.";
  } else if (error instanceof Error && error.message === "removal_unconfirmed") key = "Core did not confirm node removal. Refresh to check its state.";
  return translate(locale, key);
}

export function sandboxNodeStatus(node: SandboxNode, stale: boolean, locale: Locale): string {
  return translate(locale, stale ? "Status unconfirmed" : !node.online ? "Offline" : node.provider_ready ? "Available" : "Unavailable");
}
