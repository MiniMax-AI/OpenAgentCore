import { describe, expect, it } from "vitest";

import type { SandboxAllocation } from "@oac/agents-client";

import { hostedObservation, node, session } from "../overview/test-fixtures";
import { hostedRuntimeRows, hostedRuntimeUsage, loadHostedRuntimes, matchesRuntime, runtimeSnapshot } from "./sandbox-runtime";

const none = { ...hostedObservation("plain", "p1"), mode: "none", instance: { kind: "none", allocation_id: null, device_id: null, connection_generation: null }, lifecycle_state: null, status: "unsupported", reason: "runtime_mode_not_observable", cpu: null, memory: null } as unknown as ReturnType<typeof hostedObservation>;

describe("loadHostedRuntimes", () => {
  it("keeps hosted observations, reads their Sessions through their project and bounds the reads", async () => {
    const reads: Array<[string, string]> = [];
    const load = await loadHostedRuntimes({
      observations: async () => [
        none,
        hostedObservation("asleep", "p2", { lifecycle_state: "sleeping" }),
        hostedObservation("running", "p1"),
        hostedObservation("broken", "p1", { observed_at: 900 }),
      ],
      sessionReader: (projectId) => ({
        retrieveSession: async (id: string) => {
          reads.push([projectId, id]);
          if (id === "broken") throw new Error("HTTP 500");
          return session(id);
        },
      }) as never,
    }, new AbortController().signal, 2);
    // Active Runtimes are read first; the sleeping one is over the limit.
    expect(load.observations.map((observation) => observation.session_id)).toEqual(["running", "broken", "asleep"]);
    expect(reads.sort()).toEqual([["p1", "broken"], ["p1", "running"]]);
    expect(load).toMatchObject({ unread: 1, failed: 1 });
    expect([...load.sessions.keys()]).toEqual(["running"]);
  });
});

describe("hosted Runtime projections", () => {
  const load = {
    observations: [
      hostedObservation("a", "p1"),
      hostedObservation("b", "p2", { lifecycle_state: "sleeping", status: "observed", cpu: null, memory: null }),
      hostedObservation("c", "p2", { lifecycle_state: "pending", status: "unavailable", reason: "allocation_pending", cpu: null, memory: null, observed_at: null }),
    ],
    sessions: new Map([["a", session("a", { metadata: { title: "Nightly build" } })], ["b", session("b")]]),
    unread: 0,
    failed: 0,
    loadedAt: 5_000,
  };

  it("filters the trend snapshot by project and remembers each Session's project", () => {
    const all = runtimeSnapshot(load, "");
    expect(all.observations).toHaveLength(3);
    expect(all.sessions.map((entry) => entry.id)).toEqual(["a", "b"]);
    expect(all.owners?.get("c")).toBe("p2");
    const one = runtimeSnapshot(load, "p2");
    expect(one.observations.map((observation) => observation.session_id)).toEqual(["b", "c"]);
    expect(one.sessions.map((entry) => entry.id)).toEqual(["b"]);
  });

  it("sums only reported CPU and memory and counts lifecycle states", () => {
    expect(hostedRuntimeUsage(load.observations)).toEqual({
      hosted: 3, active: 1, sleeping: 1, pending: 1, observed: 2,
      cpuUsageCores: 0.5, cpuCapacityCores: 2, memoryUsageBytes: 100, memoryLimitBytes: 400,
    });
    expect(hostedRuntimeUsage([load.observations[1]!]).cpuUsageCores).toBeNull();
  });

  it("places each Runtime on its host and derives uptime only from a current sample", () => {
    const allocation = { id: "alloc_a", node_id: "n1", deployment_generation: 3 } as SandboxAllocation;
    const rows = hostedRuntimeRows(load, "", { nodes: [node("n1", { name: "core-01" })], allocations: [allocation] });
    expect(rows.map((row) => [row.observation.session_id, row.node?.name ?? null, row.uptimeSeconds])).toEqual([["a", "core-01", 600], ["b", null, 600], ["c", null, null]]);
    expect(hostedRuntimeRows(load, "", null)[0]?.node).toBeNull();
    expect(rows.map((row) => row.deploymentGeneration)).toEqual([3, null, null]);
    expect(hostedRuntimeRows(load, "", null)[0]?.deploymentGeneration).toBeNull();
    expect(matchesRuntime(rows[0]!, "nightly")).toBe(true);
    expect(matchesRuntime(rows[0]!, "core-01")).toBe(true);
    expect(matchesRuntime(rows[2]!, "nightly")).toBe(false);
  });
});
