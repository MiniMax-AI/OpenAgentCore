import { sandboxNodeDiagnostics, type SandboxNodeDiagnostic } from "@oac/agents-client";
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
  it("preserves the typed download diagnostic on an online node and suppresses stale offline health", () => {
    const diagnostic: SandboxNodeDiagnostic = "runtime_download_failed";
    expect(nodeProviderDiagnostic({ online: true, provider_ready: false, diagnostic })).toBe(diagnostic);
    expect(nodeProviderDiagnostic({ online: false, provider_ready: false, diagnostic })).toBe("");
  });
  it("explains download and verification failures separately from provider image failures in both languages", () => {
    const diagnostic: SandboxNodeDiagnostic = "runtime_download_failed";
    expect(sandboxDiagnosticMessage(diagnostic, "en")).toEqual({
      label: "Runtime download failed",
      advice: "Runtime files could not be downloaded or verified. Check the node's network access and the configured Runtime release.",
    });
    expect(sandboxDiagnosticMessage(diagnostic, "zh")).toEqual({
      label: "Runtime 下载失败",
      advice: "Runtime 文件下载或验证失败。请检查节点网络连接及配置的 Runtime 发布版本。",
    });
    for (const locale of ["en", "zh"] as const) {
      expect(sandboxDiagnosticMessage(diagnostic, locale)).not.toEqual(sandboxDiagnosticMessage("runtime_image_unavailable", locale));
    }
  });
});

describe("shared node readiness diagnostics", () => {
  it.each(sandboxNodeDiagnostics)("preserves %s with localized messages", (diagnostic) => {
    expect(nodeProviderDiagnostic({ online: true, provider_ready: false, diagnostic })).toBe(diagnostic);
    const english = sandboxDiagnosticMessage(diagnostic, "en");
    const chinese = sandboxDiagnosticMessage(diagnostic, "zh");
    expect(english?.label).not.toBe("Sandbox state needs attention");
    expect(english?.advice).not.toBe("Inspect the assigned node and resource, then refresh.");
    expect(chinese?.label).not.toBe(english?.label);
    expect(chinese?.advice).not.toBe(english?.advice);
  });
});
