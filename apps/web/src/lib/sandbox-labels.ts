import { AgentCoreError, type SandboxNode, type SandboxProvider } from "@agents-core-web/agents-client";
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
/**
 * What to say about a failed sandbox request. A refusal (a 4xx other than 401
 * and a 408 timeout) is Core's to explain, so its message shows as sent: one
 * code, such as a 409 conflict, covers several reasons. Only codes with one
 * exact, stable meaning get the console's own words: an unconfigured console,
 * and a node that still holds sandboxes or is unavailable. An E2B configuration
 * whose reason the client withheld, since it may echo the key, is named as
 * refused without it.
 */
export function sandboxRequestError(error: unknown, locale: Locale): string {
  let key: MessageKey = "The sandbox request failed. Refresh to check the current state before trying again.";
  if (error instanceof AgentCoreError) {
    const refused = !sandboxWriteUncertain(error);
    if (error.code === "sandbox_admin_not_configured") key = "Sandbox administration is not configured on this console.";
    else if (error.status === 401) key = "Sign in to the console again to access sandbox management.";
    else if (error.code === "sandbox_configuration_unconfirmed") { if (refused) key = "Core rejected the E2B configuration."; }
    else if (refused) {
      if (error.code === "runtime_node_in_use") return translate(locale, "The node has active allocations or retained resources. Clear allocations, snapshots, reservations and pending cleanup before removal.");
      if (error.code === "runtime_node_unavailable") return translate(locale, "The selected sandbox node is unavailable or has no capacity.");
      if (error.message) return error.message;
      key = "The sandbox request was rejected. Refresh to check the current state.";
    } else if (error.status >= 500) key = "The sandbox service is unavailable. Refresh to check the current state.";
  } else if (error instanceof Error && error.message === "removal_unconfirmed") key = "Core did not confirm node removal. Refresh to check its state.";
  return translate(locale, key);
}

/**
 * Whether a failed sandbox write may still have taken effect, so the page must
 * read Core again before trusting what it shows. The status alone decides: no
 * response at all (a network failure or an abort), a 408 timeout or a 5xx (an
 * unreadable response is a 502). Any other 4xx is Core's clear refusal, and
 * nothing changed, even when the client withheld its reason for an E2B key.
 */
export function sandboxWriteUncertain(error: unknown): boolean {
  if (!(error instanceof AgentCoreError)) return true;
  return error.status < 400 || error.status === 408 || error.status >= 500;
}

/**
 * Core's own reason when it rejects a deployment configuration it cannot serve,
 * such as E2B with a loopback public_url; null for any other failure. Nothing
 * was saved, so the administrator corrects the cause and saves again.
 */
export function sandboxConfigurationRejection(error: unknown): string | null {
  return error instanceof AgentCoreError && error.status === 409 && error.code === "sandbox_configuration_error" && error.message ? error.message : null;
}

export function sandboxNodeStatus(node: SandboxNode, stale: boolean, locale: Locale): string {
  return translate(locale, stale ? "Status unconfirmed" : !node.online ? "Offline" : node.provider_ready ? "Available" : "Unavailable");
}

export function sandboxProviderLabel(provider: SandboxProvider | "", locale: Locale): string {
  if (provider === "docker") return "Docker";
  if (provider === "microsandbox") return "microsandbox";
  return translate(locale, provider === "e2b" ? "E2B cloud" : "Unknown state");
}
