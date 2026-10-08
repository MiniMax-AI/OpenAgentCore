import { normalizeSandboxNodeDiagnostic, type SandboxNode } from "@oac/agents-client";
import { translate, type Locale } from "./locale";
import type { MessageKey } from "./locale-strings";
export interface SandboxDiagnosticMessage { label: string; advice: string }

const diagnostics: Record<string, { label: MessageKey; advice: MessageKey }> = {
  node_unavailable: {
    label: "Node disconnected",
    advice: "Reconnect the assigned node, then refresh. Existing resources stay assigned to this node; Core does not move the Session automatically.",
  },
  resource_missing: {
    label: "Sandbox resource missing",
    advice: "Check the provider resource on the assigned node. Core retains the ownership record and does not create a replacement automatically.",
  },
  compute_unconfirmed: {
    label: "Compute state unconfirmed",
    advice: "Check the assigned node and its provider, then refresh. The last recorded compute state does not confirm that execution is running.",
  },
  ownership_mismatch: {
    label: "Sandbox ownership mismatch",
    advice: "Reconcile the assigned resource and its ownership record before resuming execution.",
  },
  provider_unavailable: {
    label: "Sandbox provider unavailable",
    advice: "Restore the provider on the assigned node, then refresh. Running the install command again on the host checks its requirements and names the fix; a manually registered node logs the local error.",
  },
  // Provider-neutral readiness classes; Core sends only the code, and the node keeps the local detail.
  host_unsupported: {
    label: "Host unsupported",
    advice: "The host lacks a capability its sandbox provider requires. Running the install command again on the host checks its requirements and names the fix; a manually registered node logs the local error.",
  },
  artifacts_unavailable: {
    label: "Provider files missing",
    advice: "Pinned provider files are missing or fail their checksum. Run the install command again.",
  },
  runtime_download_failed: {
    label: "Runtime download failed",
    advice: "Runtime files could not be downloaded or verified. Check the node's network access and the configured Runtime release.",
  },
  runtime_image_unavailable: {
    label: "Runtime image missing",
    advice: "The pinned Runtime image isn't on the host. Run the install command again.",
  },
  capacity_insufficient: {
    label: "Host too small",
    advice: "The host has less CPU or memory than one sandbox needs. Use a bigger host or a smaller sandbox size.",
  },
};

/**
 * Why an online node's provider is not ready, as one fixed code; an unknown
 * value reads as provider_unavailable. Empty while the provider is ready, and
 * for an offline node, whose last code may no longer apply.
 */
export function nodeProviderDiagnostic(node: Pick<SandboxNode, "online" | "provider_ready" | "diagnostic">): string {
  if (!node.online || (node.provider_ready && !node.diagnostic)) return "";
  return normalizeSandboxNodeDiagnostic(node.diagnostic ?? "");
}

export function sandboxDiagnosticMessage(value?: string, locale: Locale = "en"): SandboxDiagnosticMessage | null {
  if (!value) return null;
  const message = Object.hasOwn(diagnostics, value) ? diagnostics[value]! : {
    label: "Sandbox state needs attention",
    advice: "Inspect the assigned node and resource, then refresh.",
  } as const;
  return { label: translate(locale, message.label), advice: translate(locale, message.advice) };
}
