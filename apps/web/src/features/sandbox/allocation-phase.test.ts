import { describe, expect, it } from "vitest";

import { formatSpan } from "../../lib/format";
import { phaseTiming } from "./allocation-phase";

const now = Date.parse("2026-09-25T12:00:00Z") / 1000;
const ago = (seconds: number) => new Date((now - seconds) * 1000).toISOString();

describe("allocation phase timing", () => {
  it("estimates when Core reclaims a suspended sandbox from the deployment's retention", () => {
    const timing = phaseTiming("suspended", ago(7_300), 86_400, now);
    expect(timing).toEqual({ since: now - 7_300, elapsed: 7_300, reclaimIn: 79_100 });
    expect([formatSpan(timing!.elapsed, "en"), formatSpan(timing!.reclaimIn!, "en")]).toEqual(["2 hours", "22 hours"]);
    expect([formatSpan(timing!.elapsed, "zh-CN"), formatSpan(timing!.reclaimIn!, "zh-CN")]).toEqual(["2 小时", "22 小时"]);
    expect(phaseTiming("running", ago(300), 86_400, now)).toEqual({ since: now - 300, elapsed: 300, reclaimIn: null });
  });

  it("reports a reclaim past its estimate as due", () => {
    expect(phaseTiming("suspended", ago(90_000), 86_400, now)?.reclaimIn).toBe(0);
  });

  it("knows nothing for allocations from before Core recorded the time", () => {
    expect(phaseTiming("suspended", null, 86_400, now)).toBeNull();
  });
});
