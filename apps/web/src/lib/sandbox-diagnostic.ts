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
    advice: "Restore the provider on the assigned node, then refresh. A connected node alone does not confirm that its sandbox provider is ready.",
  },
  // Fixed Runtime preparation and provider readiness diagnostics; Core sends only the code.
  docker_unavailable: {
    label: "Docker unavailable",
    advice: "The node can't reach the Docker daemon. Check that Docker is running and the node can use its socket.",
  },
  docker_limits_unsupported: {
    label: "Docker limits unsupported",
    advice: "Docker on this host doesn't enforce CPU and memory limits. Enable cgroup limits.",
  },
  runtime_download_failed: {
    label: "Runtime download failed",
    advice: "Runtime files could not be downloaded or verified. Check the node's network access and the configured Runtime release.",
  },
  runtime_image_unavailable: {
    label: "Runtime image missing",
    advice: "The pinned Runtime image isn't on the host. Run the install command again.",
  },
  kvm_unavailable: {
    label: "KVM unavailable",
    advice: "/dev/kvm isn't available to the node. Enable virtualization or use a KVM-capable host.",
  },
  microsandbox_artifacts_unavailable: {
    label: "microsandbox components missing",
    advice: "microsandbox components are missing or fail their checksum. Run the install command again.",
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
