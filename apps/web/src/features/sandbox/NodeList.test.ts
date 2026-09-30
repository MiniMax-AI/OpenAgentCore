import type { SandboxAllocation } from "@oac/agents-client";
import { describe, expect, it } from "vitest";

import { node } from "../overview/test-fixtures";
import { nodeState } from "./NodeList";

const allocation = (nodeId: string, diagnostic: SandboxAllocation["diagnostic"]) => ({ id: `alloc_${nodeId}`, node_id: nodeId, deployment_generation: 1, diagnostic }) as SandboxAllocation;

const core = "https://core.example";

describe("node state", () => {
  it("keeps live serving health independent of target preparation and durable pins", () => {
    for (const state of ["unknown", "preparing", "failed", "update_required"] as const) {
      const serving = node("a", { online: true, provider_ready: true, rollout: { state, ready_generation: 1 } });
      expect(nodeState(serving, [], false, core)).toBe("available");
      expect(nodeState({ ...serving, online: false }, [], false, core)).toBe("offline");
    }
  });

  it("reports stale data, an old address and reachability before anything else", () => {
    expect(nodeState(node("a", { online: false }), [], true, core)).toBe("unconfirmed");
    expect(nodeState(node("a", { core_url: "https://core-old.example" }), [], false, core)).toBe("old_address");
    expect(nodeState(node("a", { online: false, core_url: "https://core-old.example" }), [], false, core)).toBe("old_address");
    // A node Core did not enroll, such as a file-managed local one, reports no address: unknown, not old.
    expect(nodeState(node("a", { core_url: "" }), [], false, core)).toBe("available");
    expect(nodeState(node("a", { online: false, cleanup_pending: 2 }), [], false, core)).toBe("offline");
    expect(nodeState(node("a", { provider_ready: false }), [], false, core)).toBe("degraded");
  });

  it("asks for attention on pending cleanup or a diagnosed allocation of this node only", () => {
    expect(nodeState(node("a", { cleanup_pending: 1 }), [], false, core)).toBe("attention");
    expect(nodeState(node("a"), [allocation("a", "resource_missing")], false, core)).toBe("attention");
    expect(nodeState(node("a"), [allocation("b", "resource_missing")], false, core)).toBe("available");
  });
});
