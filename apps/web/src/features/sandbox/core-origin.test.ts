import { describe, expect, it } from "vitest";
import { httpsOrigin, nodeSourceUrl } from "./core-origin";

describe("HTTPS origin", () => {
  it.each([
    ["https://core.example.com", "https://core.example.com"],
    [" https://core.example.com:443/ ", "https://core.example.com:443"],
    ["https://10.74.84.167:18443", "https://10.74.84.167:18443"],
  ])("keeps %s as written", (input, expected) => expect(httpsOrigin(input)).toBe(expected));
  it.each(["", "/v1", "http://core.example", "http://localhost:8080", "https://core.example/v1", "https://user:secret@core.example", "https://@core.example", "https://core.example?", "https://core.example#", "https://core.example//", "https://core.example\\path", "https://co\nre.example", "file://core.example"])("rejects %s", (input) => expect(httpsOrigin(input)).toBeNull());
});

describe("node command source", () => {
  it("is the installation's public URL, never a loopback, missing or plain HTTP one", () => {
    expect(nodeSourceUrl({ public_url: "https://core.example.com:8443", local_only: false })).toBe("https://core.example.com:8443");
    expect(nodeSourceUrl({ public_url: "https://127.0.0.1:8091", local_only: true })).toBeNull();
    expect(nodeSourceUrl({ public_url: null, local_only: false })).toBeNull();
    expect(nodeSourceUrl({ public_url: "http://core.example.com", local_only: false })).toBeNull();
  });
});
