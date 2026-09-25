import type { SandboxNodeDiagnostic } from "@agents-core-web/agents-client";
import { describe, expect, it } from "vitest";
import { nodeProviderDiagnostic, sandboxDiagnosticMessage } from "./sandbox-diagnostic";

describe("sandbox diagnostics", () => {
  it("clears previous abnormal status after Core confirms a clean observation", () => {
    expect(sandboxDiagnosticMessage("node_unavailable")?.label).toBe("Node disconnected");
    expect(sandboxDiagnosticMessage("")).toBeNull();
  });
  it("keeps missing resources owned and does not promise automatic replacement", () => {
    expect(sandboxDiagnosticMessage("resource_missing")?.advice).toContain("retains the ownership record");
    expect(sandboxDiagnosticMessage("resource_missing")?.advice).toContain("does not create a replacement automatically");
  });
  it("distinguishes unconfirmed compute, ownership mismatch and provider failure", () => {
    expect(sandboxDiagnosticMessage("compute_unconfirmed")?.advice).toContain("does not confirm that execution is running");
    expect(sandboxDiagnosticMessage("ownership_mismatch")?.label).toBe("Sandbox ownership mismatch");
    expect(sandboxDiagnosticMessage("provider_unavailable")?.label).toBe("Sandbox provider unavailable");
  });
  it("names why a node's provider is not ready, reading an unknown code as provider_unavailable", () => {
    expect(sandboxDiagnosticMessage(nodeProviderDiagnostic({ online: true, provider_ready: false, diagnostic: "kvm_unavailable" }))?.label).toBe("KVM unavailable");
    expect(nodeProviderDiagnostic({ online: true, provider_ready: false, diagnostic: "future_code" as SandboxNodeDiagnostic })).toBe("provider_unavailable");
    expect(nodeProviderDiagnostic({ online: true, provider_ready: true, diagnostic: "" })).toBe("");
    // An offline node's last code may no longer apply.
    expect(nodeProviderDiagnostic({ online: false, provider_ready: false, diagnostic: "docker_unavailable" })).toBe("");
  });
  it("does not expose an unknown raw error or turn it into a healthy state", () => {
    const message = sandboxDiagnosticMessage("private-provider-error-with-secret");
    expect(message?.label).toBe("Sandbox state needs attention");
    expect(JSON.stringify(message)).not.toContain("private-provider-error");
  });
});
