import { AgentCoreError } from "@oac/agents-client";
import { describe, expect, it } from "vitest";

import { sandboxConfigurationRejection, sandboxRequestError, sandboxWriteUncertain } from "./sandbox-labels";

describe("sandbox write outcome", () => {
  it("uses fixed bilingual E2B errors without reflecting provider text or secrets", () => {
    for (const [code, status] of [["sandbox_credential_ownership", 409], ["sandbox_credential_invalid", 400], ["sandbox_configuration_invalid", 400], ["sandbox_verification_unconfirmed", 503]] as const) {
      const error = new AgentCoreError("secret-provider-response", status, code);
      for (const locale of ["en", "zh"] as const) {
        expect(sandboxRequestError(error, locale)).not.toContain("secret-provider-response");
        if (status < 500) expect(sandboxConfigurationRejection(error, locale)).toBe(sandboxRequestError(error, locale));
        else expect(sandboxConfigurationRejection(error, locale)).toBeNull();
      }
    }
  });

  it("is uncertain without a response, on a timeout or a 5xx, and certain on any other 4xx, a withheld E2B reason included", () => {
    expect(sandboxWriteUncertain(new TypeError("Failed to fetch"))).toBe(true);
    expect(sandboxWriteUncertain(new AgentCoreError("Unavailable.", 503))).toBe(true);
    expect(sandboxWriteUncertain(new AgentCoreError("Timed out.", 408))).toBe(true);
    expect(sandboxWriteUncertain(new AgentCoreError("Withheld.", 0, "sandbox_configuration_unconfirmed"))).toBe(true);
    expect(sandboxWriteUncertain(new AgentCoreError("Withheld.", 400, "sandbox_configuration_unconfirmed"))).toBe(false);
    expect(sandboxWriteUncertain(new AgentCoreError("Read-only.", 403))).toBe(false);
    expect(sandboxWriteUncertain(new AgentCoreError("Changed.", 409, "sandbox_deployment_conflict"))).toBe(false);
  });
});


describe("reset conflict recovery", () => {
  it("explains reset-required configuration changes without assuming a different backend or team", () => {
    const error = new AgentCoreError("untrusted-provider-detail", 409, "sandbox_reset_required");
    expect(sandboxConfigurationRejection(error, "en")).toBe("Reset the sandbox deployment before changing this configuration.");
    expect(sandboxRequestError(error, "zh")).toBe("请先重置沙箱部署，再更改此配置。");
  });

  it("gives bilingual safe recovery for known state conflicts without reflecting a server payload", () => {
    for (const code of ["sandbox_generation_stale", "sandbox_reset_required", "sandbox_reset_in_progress", "sandbox_not_configured", "sandbox_in_use"]) {
      const error = new AgentCoreError("untrusted-secret-like-payload", 409, code);
      for (const locale of ["en", "zh"] as const) expect(sandboxRequestError(error, locale)).not.toContain("untrusted-secret-like-payload");
      expect(sandboxWriteUncertain(error)).toBe(false);
    }
    expect(sandboxRequestError(new AgentCoreError("stale", 409, "sandbox_generation_stale"), "en")).toContain("Refresh and review");
    expect(sandboxRequestError(new AgentCoreError("reset", 409, "sandbox_reset_in_progress"), "zh")).toContain("正在重置");
  });
});
