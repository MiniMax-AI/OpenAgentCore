import { describe, expect, it } from "vitest";
import { storageCapacity, storageObject } from "./workspace-storage";

describe("storage configuration input", () => {
  it("requires JSON objects without echoing invalid credential content", () => {
    for (const text of ["null", "[]", '"secret"', "bad-secret"]) expect(storageObject(text)).toBeNull();
    expect(storageObject('{"root":"/srv/storage"}')).toEqual({ root: "/srv/storage" });
  });
  it("preserves explicit zero and rejects partial, negative, fractional and overflowing quotas", () => {
    expect(storageCapacity("0")).toBe(0);
    expect(storageCapacity("4294967295")).toBe(4294967295);
    for (const text of ["", "-1", "1.5", "1e3", "12MiB", "4294967296"]) expect(storageCapacity(text)).toBeNull();
  });
});
