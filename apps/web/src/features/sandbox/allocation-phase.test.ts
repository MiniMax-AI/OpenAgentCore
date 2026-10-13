import { describe, expect, it } from "vitest";
import { phaseTiming } from "./allocation-phase";
const now = Date.parse("2026-09-25T12:00:00Z") / 1000;
describe("allocation phase timing", () => {
  it("reports elapsed phase time without inferring an expiry from current policy", () => {
    expect(phaseTiming(new Date((now - 90000) * 1000).toISOString(), now)).toEqual({ since: now - 90000, elapsed: 90000 });
  });
  it("keeps missing timestamps unknown and clamps clock skew", () => {
    expect(phaseTiming(null, now)).toBeNull();
    expect(phaseTiming("invalid", now)).toBeNull();
    expect(phaseTiming(new Date((now + 30) * 1000).toISOString(), now)).toEqual({ since: now + 30, elapsed: 0 });
  });
});
