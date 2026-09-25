import { describe, expect, it } from "vitest";

import { node } from "../overview/test-fixtures";
import { capacitySummary, coreStatus, nodeHealth } from "./fleet-model";

describe("fleet model", () => {
  it("counts limits only on online nodes", () => {
    const summary = capacitySummary([
      node("a", { active: 3, max_active: 4, retained: 1, max_retained: 8 }),
      node("b", { online: false, active: 0, max_active: 4, cpu_count: null, available_memory_bytes: null, available_disk_bytes: null }),
      node("c", { provider_ready: false, cpu_count: null }),
    ]);
    expect(summary).toMatchObject({ nodes: 3, online: 2, available: 1, active: 4, maxActive: 8, retained: 1, maxRetained: 16 });
  });

  it("classifies node health", () => {
    expect(nodeHealth(node("a"))).toBe("available");
    expect(nodeHealth(node("a", { diagnostic: "provider_unavailable" }))).toBe("degraded");
    expect(nodeHealth(node("a", { online: false }))).toBe("offline");
  });

  it("derives Core's own status from the Web API and maintenance", () => {
    expect(coreStatus({ webApiReachable: null, maintenance: null })).toBe("checking");
    expect(coreStatus({ webApiReachable: false, maintenance: false })).toBe("unreachable");
    expect(coreStatus({ webApiReachable: true, maintenance: null })).toBe("running");
    expect(coreStatus({ webApiReachable: true, maintenance: true })).toBe("maintenance");
  });
});
