import { describe, expect, it } from "vitest";

import { canonicalUsage, formatDashboardTokens } from "./dashboard-model";
import { holdLastReported } from "./held-usage";

const usage = { input_tokens: 10, output_tokens: 5, total_tokens: 15, input_tokens_details: { cached_tokens: 2 }, output_tokens_details: { reasoning_tokens: 1 } };

describe("canonicalUsage", () => {
  it("accepts complete usage and rejects missing or unsafe figures", () => {
    expect(canonicalUsage(usage)).toBe(usage);
    expect(canonicalUsage(null)).toBeNull();
    expect(canonicalUsage({ ...usage, input_tokens_details: null })).toBeNull();
    expect(canonicalUsage({ ...usage, total_tokens: -1 })).toBeNull();
    expect(canonicalUsage({ ...usage, output_tokens: Number.MAX_SAFE_INTEGER + 1 })).toBeNull();
  });

  it("formats token totals and keeps an absent total unavailable", () => {
    expect(formatDashboardTokens(1_234, "en-US")).toBe("1,234");
    expect(formatDashboardTokens(null)).toBe("Unavailable");
  });
});

describe("holdLastReported", () => {
  it("keeps a listed Session's last value while it reports none and drops unlisted Sessions", () => {
    const held = holdLastReported(new Map([["a", 10], ["gone", 3]]), new Map<string, number | null>([["a", null], ["b", 4]]));
    expect([...held]).toEqual([["a", 10], ["b", 4]]);
  });
});
