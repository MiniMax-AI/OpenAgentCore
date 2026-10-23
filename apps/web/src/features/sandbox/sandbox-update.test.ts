import { describe, expect, it } from "vitest";
import { credentialReady, submittedCredential } from "./sandbox-update";

describe("sandbox credential update", () => {
  it("omits a retained credential and always sends an explicit replacement", () => {
    expect(submittedCredential([["api_key", ""]])).toEqual({});
    for (let attempt = 0; attempt < 2; attempt++) expect(submittedCredential([["api_key", "test-key"]])).toEqual({ credential: { api_key: "test-key" } });
  });

  it("does not silently turn a cleared replacement secret into retained-key resubmission", () => {
    expect(credentialReady(true, false, false)).toBe(true);
    expect(credentialReady(false, false, false)).toBe(false);
    expect(credentialReady(true, true, false)).toBe(false);
    expect(credentialReady(true, true, true)).toBe(true);
  });
});
