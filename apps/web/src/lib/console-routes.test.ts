import { describe, expect, it } from "vitest";

import { assignStableSlots, niceTicks, tickIndices } from "../components/charts/chart-scale";
import { consoleHashForView, consoleNavGroups, consoleNavParent, consoleViewFromHash } from "./console-routes";
import { formatBytes, formatCompact, formatDuration, formatRelative, MISSING } from "./format";

describe("console routes", () => {
  it("maps hashes to views, keeping legacy bookmarks", () => {
    expect(consoleViewFromHash("", { templates: true })).toBe("overview");
    expect(consoleViewFromHash("#agent-metrics", { templates: true })).toBe("agent-metrics");
    expect(consoleViewFromHash("#dashboard", { templates: true })).toBe("overview");
    expect(consoleViewFromHash("#sandbox", { templates: true })).toBe("nodes");
    expect(consoleViewFromHash("#system", { templates: true })).toBe("overview");
    expect(consoleViewFromHash("#agents", { templates: true })).toBe("builder");
    expect(consoleViewFromHash("#unknown", { templates: true })).toBe("overview");
    expect(consoleViewFromHash("#templates", { templates: false })).toBe("overview");
  });

  it("round-trips every navigable view", () => {
    for (const view of consoleNavGroups.flatMap((group) => group.views)) {
      expect(consoleViewFromHash(consoleHashForView(view), { templates: true })).toBe(view);
    }
    expect(consoleHashForView("overview")).toBe("");
  });

  it("leads with monitoring and keeps debugging tools in the Playground", () => {
    expect(consoleNavGroups[0]?.views[0]).toBe("overview");
    expect(consoleNavGroups.at(-1)).toEqual({ id: "playground", views: ["workbench"] });
    // The Session console is a secondary page of the Session log.
    expect(consoleViewFromHash("#playground", { templates: true })).toBe("playground");
    expect(consoleNavParent("playground")).toBe("sessions");
    expect(consoleNavGroups.map((group) => group.id)).toEqual(["monitor", "resources", "platform", "playground"]);
  });
});

describe("chart scales", () => {
  it("rounds axis limits to clean steps", () => {
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(9)).toEqual([0, 2.5, 5, 7.5, 10]);
    expect(niceTicks(1234)).toEqual([0, 500, 1000, 1500]);
  });

  it("keeps the first and last tick", () => {
    expect(tickIndices(3)).toEqual([0, 1, 2]);
    const ticks = tickIndices(48, 6);
    expect(ticks[0]).toBe(0);
    expect(ticks.at(-1)).toBe(47);
    expect(ticks.length).toBeLessThanOrEqual(6);
  });

  it("keeps an entity's colour slot while it stays visible", () => {
    const first = assignStableSlots(new Map(), ["a", "b", "c"]);
    expect([...first.entries()]).toEqual([["a", 1], ["b", 2], ["c", 3]]);
    const second = assignStableSlots(first, ["c", "d"]);
    expect(second.get("c")).toBe(3);
    expect(second.get("d")).toBe(4);
    const third = assignStableSlots(second, ["a", "c", "d"]);
    expect(third.get("a")).toBe(1);
  });
});

describe("formatting", () => {
  it("renders missing values as a dash", () => {
    expect(formatBytes(null)).toBe(MISSING);
    expect(formatDuration(undefined)).toBe(MISSING);
    expect(formatRelative(null, 0, "en")).toBe(MISSING);
  });

  it("formats sizes, durations and past times", () => {
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatDuration(0.25)).toBe("250 ms");
    expect(formatDuration(75)).toBe("1m 15s");
    expect(formatDuration(59.7)).toBe("1m 0s");
    expect(formatDuration(9.97)).toBe("10 s");
    expect(formatCompact(1_036_000, "en")).toBe("1.04M");
    expect(formatCompact(1_100_000, "en")).toBe("1.1M");
    expect(formatCompact(4_157_362, "zh-CN")).toBe("415.7万");
    expect(formatCompact(1_036_000, "zh-CN")).toBe("103.6万");
    expect(formatRelative(1_000 - 7_200, 1_000, "en")).toBe("2 hours ago");
    // A timestamp ahead of the browser clock is skew, never "in 4 minutes".
    expect(formatRelative(1_240, 1_000, "en")).toBe("now");
  });
});
