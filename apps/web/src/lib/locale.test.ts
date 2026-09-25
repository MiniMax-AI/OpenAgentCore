import { describe, expect, it } from "vitest";
import { resolveLocale } from "./locale";
import { AgentCoreError } from "@agents-core-web/agents-client";
import { sandboxRequestError, sandboxStateLabel } from "./sandbox-labels";
import { sandboxDiagnosticMessage } from "./sandbox-diagnostic";

describe("sandbox localization", () => {
  it("uses saved choices and defaults to the browser's preferred language", () => {
    expect(resolveLocale(null, ["zh-CN", "en-US"])).toBe("zh");
    expect(resolveLocale("en", ["zh-TW"])).toBe("en");
    expect(resolveLocale("zh", ["en-US"])).toBe("zh");
    expect(resolveLocale("invalid", ["fr"])).toBe("en");
  });
  it("localizes every persisted allocation and compute state without exposing unknown values", () => {
    for (const state of ["creating", "running", "cleanup_pending", "released", "disabled", "quiescing", "suspending", "suspended", "restoring", "waking"]) {
      expect(sandboxStateLabel(state, "zh")).toMatch(/[\u4e00-\u9fff]/);
      expect(sandboxStateLabel(state, "zh")).not.toBe("未知状态");
    }
    expect(sandboxStateLabel("internal-value", "zh")).toBe("未知状态");
  });
  it("localizes all diagnostic labels and advice", () => {
    for (const code of ["node_unavailable", "resource_missing", "compute_unconfirmed", "ownership_mismatch", "provider_unavailable", "docker_unavailable",
      "docker_limits_unsupported", "runtime_image_unavailable", "kvm_unavailable", "microsandbox_artifacts_unavailable", "capacity_insufficient", "unknown"]) {
      const message = sandboxDiagnosticMessage(code, "zh");
      expect(message?.label).toMatch(/[\u4e00-\u9fff]/);
      expect(message?.advice).toMatch(/[\u4e00-\u9fff]/);
    }
    expect(sandboxDiagnosticMessage("", "zh")).toBeNull();
  });
  it("maps conflicts and unconfigured access without leaking raw backend diagnostics", () => {
    expect(sandboxRequestError(new AgentCoreError("raw secret", 409, "runtime_node_in_use"), "zh")).toContain("保留资源");
    expect(sandboxRequestError(new AgentCoreError("raw secret", 503, "sandbox_admin_not_configured"), "zh")).toContain("尚未配置");
    expect(sandboxRequestError(new Error("raw secret"), "zh")).not.toContain("raw secret");
  });
});
