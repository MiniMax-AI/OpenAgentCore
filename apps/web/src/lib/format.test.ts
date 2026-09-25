import { describe, expect, it } from "vitest";
import { formatPeriod } from "./format";

describe("policy periods", () => {
  it("reads microsandbox's suspension policy in words, in both languages", () => {
    expect([formatPeriod(300, "en"), formatPeriod(86_400, "en")]).toEqual(["5 minutes", "24 hours"]);
    expect([formatPeriod(300, "zh-CN"), formatPeriod(86_400, "zh-CN")]).toEqual(["5 分钟", "24 小时"]);
  });
});
