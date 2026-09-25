import { describe, expect, it } from "vitest";

import { TOPOLOGY_LIMIT, topologyNodes } from "./FleetTopology";
import { node } from "./test-fixtures";

describe("fleet topology", () => {
  it("draws every node in order when they fit", () => {
    const nodes = [node("a"), node("b", { online: false }), node("c")];
    expect(topologyNodes(nodes).map((entry) => entry.id)).toEqual(["a", "b", "c"]);
  });

  it("keeps offline and degraded nodes when not every node fits", () => {
    const nodes = Array.from({ length: TOPOLOGY_LIMIT + 2 }, (_, index) => node(`n${index}`));
    nodes.push(node("down", { online: false }), node("provider", { provider_ready: false }));
    const shown = topologyNodes(nodes).map((entry) => entry.id);
    expect(shown).toHaveLength(TOPOLOGY_LIMIT);
    expect(shown.slice(0, 2)).toEqual(["down", "provider"]);
    expect(shown.slice(2)).toEqual(["n0", "n1", "n2", "n3", "n4", "n5"]);
  });
});
