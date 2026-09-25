import { describe, expect, it } from "vitest";

import { niceTicks } from "./chart-scale";

describe("chart ticks", () => {
  it("keeps count axes on whole numbers", () => {
    expect(niceTicks(1, 4, true)).toEqual([0, 1]);
    expect(niceTicks(3, 4, true)).toEqual([0, 1, 2, 3]);
    expect(niceTicks(9, 4, true)).toEqual([0, 5, 10]);
    expect(niceTicks(10, 4, true)).toEqual([0, 5, 10]);
    expect(niceTicks(10)).toEqual([0, 2.5, 5, 7.5, 10]);
  });
});
