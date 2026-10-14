import { describe, expect, it } from "vitest";

import { node } from "../overview/test-fixtures";
import { capacitySummary, coreStatus, nodeHealth } from "./fleet-model";

describe("fleet model", () => {
  it("counts limits only on online nodes", () => {
    const summary = capacitySummary([
      node("a", { active: 3, max_active: 4 }),
      node("b", { online: false, active: 0, max_active: 4, cpu_count: null, available_memory_bytes: null, available_disk_bytes: null }),
      node("c", { provider_ready: false, cpu_count: null }),
    ]);
    expect(summary).toMatchObject({ nodes: 3, online: 2, available: 1, active: 4, maxActive: 8 });
  });

  it("classifies node health", () => {
    expect(nodeHealth(node("a"))).toBe("available");
    expect(nodeHealth(node("a", { diagnostic: "provider_unavailable" }))).toBe("degraded");
    expect(nodeHealth(node("a", { online: false }))).toBe("offline");
  });

  it("reports Core reachability independently of sandbox operations", () => {
    expect(coreStatus({ webApiReachable: null })).toBe("checking");
    expect(coreStatus({ webApiReachable: false })).toBe("unreachable");
    expect(coreStatus({ webApiReachable: true })).toBe("running");
  });
});
