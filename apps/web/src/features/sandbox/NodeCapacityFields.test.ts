import { describe, expect, it } from "vitest";
import { nodeCapacity } from "./NodeCapacityFields";

describe("node capacity admission input", () => {
  it("uses the same bounds for enrollment and later edits", () => {
    for (const value of ["", "1.5", "-1", "0", "1000001", "1e2"]) expect(nodeCapacity(value, "8").activeError).not.toBeNull();
    expect(nodeCapacity("1000000", "1000000")).toEqual({ active: 1000000, retained: 1000000, activeError: null, retainedError: null });
    expect(nodeCapacity("4", "3").retainedError).toBe("Enter at least the number of sandboxes at once.");
  });
  it("permits small limits without deriving them from existing usage", () => {
    expect(nodeCapacity("1", "1")).toEqual({ active: 1, retained: 1, activeError: null, retainedError: null });
  });
});
