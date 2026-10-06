import { describe, expect, it } from "vitest";
import { nodeSourceUrl } from "./core-origin";

describe("node command source", () => {
  it("is the installation's public URL unless only the Core host reaches it", () => {
    expect(nodeSourceUrl({ public_url: "https://core.example.com:8443", local_only: false })).toBe("https://core.example.com:8443");
    expect(nodeSourceUrl({ public_url: "http://10.0.0.5:8080", local_only: false })).toBe("http://10.0.0.5:8080");
    expect(nodeSourceUrl({ public_url: "http://localhost:8080", local_only: true })).toBeNull();
    expect(nodeSourceUrl({ public_url: null, local_only: false })).toBeNull();
  });
});
