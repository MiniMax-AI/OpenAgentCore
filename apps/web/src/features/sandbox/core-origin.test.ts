import { describe, expect, it } from "vitest";
import { sandboxCoreOrigin, sandboxSetupOrigin } from "./core-origin";

describe("sandbox deployment Core origin", () => {
  it.each([
    [" https://CORE.example:8443/ ", "https://core.example:8443"],
    ["http://localhost:8080", "http://localhost:8080"],
    ["http://127.0.0.2:8080/", "http://127.0.0.2:8080"],
    ["http://[::1]:8080", "http://[::1]:8080"],
  ])("normalizes %s", (input, expected) => expect(sandboxCoreOrigin(input)).toBe(expected));
  it.each(["", "/v1", "http://remote.example", "http://host.localhost", "https://core.example/v1", "https://core.example/path/..", "https://user:secret@core.example", "https://@core.example", "https://core.example?", "https://core.example#", "https://core.example//", "https://core.example\\path", "https://co\nre.example", "file://core.example"])("rejects unsafe or non-origin input %s", (input) => expect(sandboxCoreOrigin(input)).toBeNull());
});

describe("hosted sandbox setup origin", () => {
  it.each(["http://localhost:8080", "https://localhost:8080", "https://localhost.", "https://host.localhost", "https://127.0.0.1", "https://127.1", "https://127.0.0.2", "https://[::1]", "https://[0:0:0:0:0:0:0:1]", "http://core.example", "https://core.example/v1", "https://0.0.0.0", "https://0", "https://[::]", "https://[0:0:0:0:0:0:0:0]", "https://[::ffff:127.0.0.1]", "https://[::ffff:7f00:2]", "https://[::ffff:0.0.0.0]"])("rejects an origin guests cannot use: %s", (origin) => {
    expect(sandboxSetupOrigin(origin)).toBeNull();
  });
  it.each(["https://10.74.84.167:18443", "https://[2001:db8::1]", "https://[::ffff:a4a:54a7]"])("accepts a routable address: %s", (origin) => {
    expect(sandboxSetupOrigin(origin)).toBe(origin);
  });
  it("keeps normal HTTPS setup automatic", () => {
    expect(sandboxSetupOrigin("https://CORE.example:8443/")).toBe("https://core.example:8443");
  });
});
