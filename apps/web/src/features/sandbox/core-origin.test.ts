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
    expect(nodeSourceUrl({ public_url: "https://core.example.com:8443", local_only: false, insecure_public_url: false })).toBe("https://core.example.com:8443");
    expect(nodeSourceUrl({ public_url: "https://127.0.0.1:8091", local_only: true, insecure_public_url: false })).toBeNull();
    expect(nodeSourceUrl({ public_url: null, local_only: false, insecure_public_url: false })).toBeNull();
    expect(nodeSourceUrl({ public_url: "http://core.example.com", local_only: false, insecure_public_url: false })).toBeNull();
  });
  it("keeps a plain HTTP one only for an installation that opted into a trusted network", () => {
    expect(nodeSourceUrl({ public_url: "http://core.internal:8091", local_only: false, insecure_public_url: true })).toBe("http://core.internal:8091");
    expect(nodeSourceUrl({ public_url: " http://core.internal:8091/ ", local_only: false, insecure_public_url: true })).toBe("http://core.internal:8091");
    for (const public_url of ["http://user:secret@core.internal", "http://core.internal/v1", "ws://core.internal:8091", "https://core.example#", "", "core.internal:8091"]) {
      expect(nodeSourceUrl({ public_url, local_only: false, insecure_public_url: true })).toBeNull();
    }
    expect(nodeSourceUrl({ public_url: "http://127.0.0.1:8091", local_only: true, insecure_public_url: true })).toBeNull();
  });
});
