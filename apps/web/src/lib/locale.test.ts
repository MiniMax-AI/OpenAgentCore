import { describe, expect, it } from "vitest";
import { resolveLocale } from "./locale";
import { AgentCoreError } from "@oac/agents-client";
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
      "docker_limits_unsupported", "runtime_download_failed", "runtime_image_unavailable", "kvm_unavailable", "microsandbox_artifacts_unavailable", "capacity_insufficient", "unknown"]) {
      const message = sandboxDiagnosticMessage(code, "zh");
      expect(message?.label).toMatch(/[\u4e00-\u9fff]/);
      expect(message?.advice).toMatch(/[\u4e00-\u9fff]/);
    }
    expect(sandboxDiagnosticMessage("", "zh")).toBeNull();
  });
  it("shows Core's reason for a refusal, names an unconfigured console, and keeps other failures to the console's words", () => {
    expect(sandboxRequestError(new AgentCoreError("raw secret", 503, "sandbox_admin_not_configured"), "zh")).toBe("此控制台尚未配置沙箱管理权限。");
    // A code with one exact meaning keeps the console's localized words.
    expect(sandboxRequestError(new AgentCoreError("Node node-edge still has allocations.", 409, "runtime_node_in_use"), "zh")).toBe("节点仍有活跃分配或保留资源。请先清理资源分配、快照、预留资源和待清理项，再移除节点。");
    // A refusal is Core's to explain: one code, such as a 409 conflict, covers several reasons.
    expect(sandboxRequestError(new AgentCoreError("This console is read-only.", 403), "zh")).toBe("This console is read-only.");
    expect(sandboxRequestError(new AgentCoreError("expected_generation is stale.", 409, "sandbox_deployment_conflict"), "zh")).toBe("expected_generation is stale.");
    // An E2B refusal whose reason the client withheld is named without it.
    expect(sandboxRequestError(new AgentCoreError("withheld", 400, "sandbox_configuration_unconfirmed"), "zh")).toBe("Core 拒绝了这个 E2B 配置。");
    expect(sandboxRequestError(new AgentCoreError("raw secret", 502), "zh")).not.toContain("raw secret");
    expect(sandboxRequestError(new Error("raw secret"), "zh")).not.toContain("raw secret");
  });
});
