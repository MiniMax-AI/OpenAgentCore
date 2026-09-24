import type { SandboxAllocation } from "@agents-core-web/agents-client";
import { describe, expect, it } from "vitest";

import { node } from "../overview/test-fixtures";
import { nodeState } from "./NodeList";

const allocation = (nodeId: string, diagnostic: SandboxAllocation["diagnostic"]) => ({ id: `alloc_${nodeId}`, node_id: nodeId, diagnostic }) as SandboxAllocation;

describe("node state", () => {
  it("reports stale data and reachability before anything else", () => {
    expect(nodeState(node("a", { online: false }), [], true)).toBe("unconfirmed");
    expect(nodeState(node("a", { online: false, cleanup_pending: 2 }), [], false)).toBe("offline");
    expect(nodeState(node("a", { provider_ready: false }), [], false)).toBe("degraded");
  });

  it("asks for attention on pending cleanup or a diagnosed allocation of this node only", () => {
    expect(nodeState(node("a", { cleanup_pending: 1 }), [], false)).toBe("attention");
    expect(nodeState(node("a"), [allocation("a", "resource_missing")], false)).toBe("attention");
    expect(nodeState(node("a"), [allocation("b", "resource_missing")], false)).toBe("available");
  });
});
