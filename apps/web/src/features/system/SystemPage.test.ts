import type { CoreStartupConfiguration } from "@agents-core-web/agents-client";
import { describe, expect, it } from "vitest";

import { harnessRows } from "./SystemPage";

const configuration = (configured: Partial<CoreStartupConfiguration["configured"]>): CoreStartupConfiguration => ({
  object: "agents.core.startup_configuration",
  schema_version: 1,
  supported: { harnesses: ["claude_sdk", "codex", "mcode"], managed_sandbox_providers: ["docker", "microsandbox"] },
  configured: {
    default_harness: "codex",
    enabled_harnesses: ["codex"],
    daemon_gateway: true,
    self_hosted: true,
    managed_sandbox: { enabled: true, provider: "docker", maintenance: false },
    model_providers: [{ harness: "codex", endpoint_configured: false }],
    ...configured,
  },
});

describe("system harnesses", () => {
  it("lists enabled harnesses first, then the other supported ones", () => {
    expect(harnessRows(configuration({})).map((row) => [row.harness, row.enabled])).toEqual([["codex", true], ["claude_sdk", false], ["mcode", false]]);
  });

  it("marks only the enabled default and reports endpoints only where Core does", () => {
    const rows = harnessRows(configuration({
      enabled_harnesses: ["claude_sdk", "codex"],
      model_providers: [{ harness: "claude_sdk", endpoint_configured: true }, { harness: "codex", endpoint_configured: false }],
    }));
    expect(rows.find((row) => row.harness === "codex")).toEqual({ harness: "codex", enabled: true, isDefault: true, endpointConfigured: false });
    expect(rows.find((row) => row.harness === "claude_sdk")).toMatchObject({ isDefault: false, endpointConfigured: true });
    // A harness that is not enabled has no reported endpoint: missing, not "not configured".
    expect(rows.find((row) => row.harness === "mcode")).toMatchObject({ enabled: false, isDefault: false, endpointConfigured: null });
  });

  it("has no default without a daemon gateway", () => {
    const rows = harnessRows(configuration({ daemon_gateway: false, self_hosted: false, enabled_harnesses: [], model_providers: [] }));
    expect(rows.every((row) => !row.enabled && !row.isDefault && row.endpointConfigured === null)).toBe(true);
  });
});
