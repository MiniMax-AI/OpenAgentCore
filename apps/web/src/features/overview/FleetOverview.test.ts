import { describe, expect, it } from "vitest";

import { FLEET_LIMIT, overviewNodes } from "./FleetOverview";
import { node } from "./test-fixtures";

describe("fleet overview", () => {
  it("shows every node in order when they fit", () => {
    const nodes = [node("a"), node("b", { online: false }), node("c")];
    expect(overviewNodes(nodes).map((entry) => entry.id)).toEqual(["a", "b", "c"]);
  });

  it("keeps offline and degraded nodes when not every node fits", () => {
    const nodes = Array.from({ length: FLEET_LIMIT + 2 }, (_, index) => node(`n${index}`));
    nodes.push(node("down", { online: false }), node("provider", { provider_ready: false }));
    const shown = overviewNodes(nodes).map((entry) => entry.id);
    expect(shown).toHaveLength(FLEET_LIMIT);
    expect(shown.slice(0, 2)).toEqual(["down", "provider"]);
    expect(shown.slice(2)).toEqual(["n0", "n1"]);
  });
});
