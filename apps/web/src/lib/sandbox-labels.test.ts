import { AgentCoreError } from "@agents-core-web/agents-client";
import { describe, expect, it } from "vitest";

import { sandboxWriteUncertain } from "./sandbox-labels";

describe("sandbox write outcome", () => {
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
