import { describe, expect, it } from "vitest";
import { nodeSourceUrl, sandboxCoreOrigin } from "./core-origin";

describe("sandbox deployment Core origin", () => {
  it.each([
    [" https://CORE.example:8443/ ", "https://core.example:8443"],
    ["http://localhost:8080", "http://localhost:8080"],
    ["http://127.0.0.2:8080/", "http://127.0.0.2:8080"],
    ["http://[::1]:8080", "http://[::1]:8080"],
  ])("normalizes %s", (input, expected) => expect(sandboxCoreOrigin(input)).toBe(expected));
  it.each(["", "/v1", "http://remote.example", "http://host.localhost", "https://core.example/v1", "https://core.example/path/..", "https://user:secret@core.example", "https://@core.example", "https://core.example?", "https://core.example#", "https://core.example//", "https://core.example\\path", "https://co\nre.example", "file://core.example"])("rejects unsafe or non-origin input %s", (input) => expect(sandboxCoreOrigin(input)).toBeNull());
});

describe("node command source", () => {
  it("is the installation's public URL, never a loopback, missing or plain HTTP one", () => {
    expect(nodeSourceUrl({ public_url: "https://Core.example.com/", local_only: false })).toBe("https://core.example.com");
    expect(nodeSourceUrl({ public_url: "http://127.0.0.1:8091", local_only: true })).toBeNull();
    expect(nodeSourceUrl({ public_url: null, local_only: false })).toBeNull();
    expect(nodeSourceUrl({ public_url: "http://core.example.com", local_only: false })).toBeNull();
  });
});
