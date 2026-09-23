import { describe, expect, it } from "vitest";
import { sandboxDiagnosticMessage } from "./sandbox-diagnostic";

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
  it("does not expose an unknown raw error or turn it into a healthy state", () => {
    const message = sandboxDiagnosticMessage("private-provider-error-with-secret");
    expect(message?.label).toBe("Sandbox state needs attention");
    expect(JSON.stringify(message)).not.toContain("private-provider-error");
  });
});
