export interface SandboxDiagnosticMessage { label: string; advice: string }

const diagnostics: Record<string, SandboxDiagnosticMessage> = {
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
    advice: "Ask the deployment administrator to reconcile the assigned resource and its ownership record before resuming execution.",
  },
  provider_unavailable: {
    label: "Sandbox provider unavailable",
    advice: "Restore the provider on the assigned node, then refresh. A connected node alone does not confirm that its sandbox provider is ready.",
  },
};

export function sandboxDiagnosticMessage(value?: string): SandboxDiagnosticMessage | null {
  if (!value) return null;
  return Object.hasOwn(diagnostics, value) ? diagnostics[value]! : {
    label: "Sandbox state needs attention",
    advice: "Ask the deployment administrator to inspect the assigned node and resource, then refresh.",
  };
}
