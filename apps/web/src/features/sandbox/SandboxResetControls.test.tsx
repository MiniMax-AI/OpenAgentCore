import type { SandboxDeployment, SandboxReset } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SandboxResetControls } from "./SandboxResetControls";

const reset: SandboxReset = { clear: "auto", requested_at: "2020-01-01T10:00:00Z", deadline_at: "2020-01-01T11:00:00Z", forced_at: null,
  remaining: { busy: 2, idle: 1, cleanup: 3, on_offline_nodes: 2, offline_nodes: [{ node_id: "offline-a", name: "Offline A", resources: 2 }] } };
const deployment = (value: SandboxReset | null): SandboxDeployment => ({ rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "https://core.example", reset: value, owner_epoch: 1, generation: 1, mode: "nodes", resources: { allocations: 5, pending: 1 }, suspension: null });
const render = (value: SandboxReset | null, stale = false) => renderToStaticMarkup(<SandboxResetControls deployment={deployment(value)} disabled={stale} stale={stale} onStart={async () => true} onCancel={async () => true} />);

describe("authoritative reset progress", () => {
  it("uses Core's partition and named offline blockers and never advances from the browser clock", () => {
    const html = render(reset);
    expect(html).toContain("Busy</dt><dd>2");
    expect(html).toContain("Idle</dt><dd>1");
    expect(html).toContain("Awaiting cleanup</dt><dd>3");
    expect(html).toContain("On offline nodes</dt><dd>2");
    expect(html).toContain("Offline A");
    expect(html).toContain("Busy work can finish until the deadline");
    expect(html).toContain("Force reset now");
    expect(html).not.toContain("Remaining hosted Sessions are being archived");
  });

  it("keeps zero remaining resources in progress until Core completes, and force still requires offline cleanup", () => {
    const empty = { ...reset, remaining: { busy: 0, idle: 0, cleanup: 0, on_offline_nodes: 0, offline_nodes: [] } };
    expect(render(empty)).toContain("Reset in progress");
    const forced = render({ ...reset, clear: "force", forced_at: "2020-01-01T11:00:00Z" });
    expect(forced).toContain("Force reset does not bypass them");
    expect(forced).not.toContain("Force reset now");
    expect(forced).toContain("Cancel reset");
  });

  it("visibly qualifies stale progress and removes it only when Core returns null", () => {
    expect(render(reset, true)).toContain("last confirmed counts");
    expect(render(null)).not.toContain("Reset in progress");
    expect(render(null)).toContain("Reset deployment");
  });
});
