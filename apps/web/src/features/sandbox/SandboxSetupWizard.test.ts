import { describe, expect, it } from "vitest";
import { validEndpoint } from "./e2b-template-manifest";

describe("E2B endpoint input", () => {
  it("accepts the official default and a paired compatible service", () => {
    expect(validEndpoint("", "")).toBe(true);
    expect(validEndpoint("https://sandbox-test.sandbase.ai", "sandbox-test.sandbase.ai")).toBe(true);
  });

  it("rejects incomplete, local and malformed selectors before submission", () => {
    expect(validEndpoint("https://sandbox-test.sandbase.ai", "")).toBe(false);
    expect(validEndpoint("https://localhost", "sandbox-test.sandbase.ai")).toBe(false);
    expect(validEndpoint("https://127.0.0.1", "sandbox-test.sandbase.ai")).toBe(false);
    expect(validEndpoint("https://bad-.example", "good.example")).toBe(false);
    expect(validEndpoint("https://good.example", "-bad.example")).toBe(false);
    expect(validEndpoint("https://unrelated.example", "sandbox-test.sandbase.ai")).toBe(false);
  });
});
