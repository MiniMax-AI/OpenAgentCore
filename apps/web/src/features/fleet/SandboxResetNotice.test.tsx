import type { SandboxDeployment, SandboxReset } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { SandboxResetNotice } from "./SandboxResetNotice";

const reset: SandboxReset = { clear: "auto", requested_at: "2026-09-27T10:00:00Z", deadline_at: "2026-09-27T11:00:00Z", forced_at: null,
  remaining: { busy: 1, idle: 0, cleanup: 2, on_offline_nodes: 1, offline_nodes: [{ node_id: "n1", name: "Node 1", resources: 1 }] } };
const deployment = (reset: SandboxReset | null): SandboxDeployment => ({ credential_configured: false, configuration: {}, metadata: {}, rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "http://core", reset, owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 3, pending: 0 }, suspension: null });
const render = (value: SandboxDeployment | undefined, failed = false) => renderToStaticMarkup(<SandboxResetNotice deployment={value} failed={failed} onRetry={() => {}} />);

describe("reset notices on read-only surfaces", () => {
  it("shows active reset consequences and its existing management destination", () => {
    const html = render(deployment(reset));
    expect(html).toContain("Sandbox reset in progress");
    expect(html).toContain("Waiting for Sessions and cleanup");
    expect(html).toContain("Self-hosted Sessions are unaffected");
    expect(html).toContain("View reset");
  });

  it("shows force once Core reports the mode transition", () => {
    expect(render(deployment({ ...reset, clear: "force", forced_at: "2026-09-27T11:00:00Z" }))).toContain("Forced reset");
  });

  it("retains and qualifies the last reset on failure, then hides only after Core returns null", () => {
    const html = render(deployment(reset), true);
    expect(html).toContain("Sandbox reset in progress");
    expect(html).toContain("Retry");
    expect(render(deployment(null))).toBe("");
    expect(render(undefined)).toBe("");
    expect(render(undefined, true)).toContain("Retry");
  });
});
