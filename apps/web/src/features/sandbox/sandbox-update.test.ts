import { describe, expect, it } from "vitest";
import { e2bKeyReady, e2bUpdateSelection } from "./sandbox-update";

describe("E2B update selection", () => {
  it("omits a retained key while keeping the complete template selection", () => {
    expect(e2bUpdateSelection(" template:build ", "   ")).toEqual({ configuration: { template: "template:build" } });
    expect(e2bKeyReady(true, false, "")).toBe(true);
    expect(e2bKeyReady(false, false, "")).toBe(false);
  });

  it("always sends an explicit replacement, including repeated identical values", () => {
    for (let attempt = 0; attempt < 2; attempt++) expect(e2bUpdateSelection("template:build", " test-key ")).toEqual({ configuration: { template: "template:build" }, credential: { api_key: "test-key" } });
  });

  it("does not silently turn a cleared replacement secret into retained-key resubmission", () => {
    expect(e2bKeyReady(true, true, "")).toBe(false);
    expect(e2bKeyReady(true, true, "replacement")).toBe(true);
    expect(e2bKeyReady(true, false, "")).toBe(true);
  });
});
