import { describe, expect, it } from "vitest";

import { isValidDirectCoreBaseUrl } from "./connection";

describe("Core URL checks", () => {
  it.each([
    "https://core.example/v1",
    "http://localhost:8091/v1",
    "http://worker.localhost:8091/v1",
    "http://127.0.0.42:8091/v1",
    "http://2130706433:8091/v1",
    "http://[::1]:8091/v1",
    "http://[0:0:0:0:0:0:0:1]:8091/v1",
    "https://core.example/v1%3Ftenant%3Dsafe",
    "https://core.example/v1%23section",
    "https://core.example/v1/@scope",
  ])("allows HTTPS or an explicit HTTP loopback direct Core: %s", (baseUrl) => {
    expect(isValidDirectCoreBaseUrl(baseUrl)).toBe(true);
  });

  it.each([
    "http://core.example/v1",
    "http://192.168.1.20:8091/v1",
    "http://localhost.example/v1",
    "http://127.0.0.1.example/v1",
    "http://127.0.0.1%2eexample/v1",
    "http://[::2]:8091/v1",
    "ftp://core.example/v1",
    "https://user:secret@core.example/v1",
    "https://@core.example/v1",
    "https://:@core.example/v1",
    "https:@core.example/v1",
    "https://core.example/v1?",
    "https://core.example/v1#",
    "https://core.example/v1?#",
    "https://core.example/v1?token=secret",
    "https://core.example/v1#secret",
  ])("rejects an unsafe direct Core URL: %s", (baseUrl) => {
    expect(isValidDirectCoreBaseUrl(baseUrl)).toBe(false);
  });
});
