import { describe, expect, it } from "vitest";
import { isProviderUrl } from "./ModelProviderDialog";

// The dialog must accept exactly what Core's admission rule accepts: an http or
// https URL with a host and no credentials, query or fragment. It previously
// rejected plain http, which blocked the form for an operator-chosen endpoint.
describe("model provider base URL", () => {
  it.each([
    "http://192.168.20.15:3721",
    "https://api.example.com/v1",
    "http://localhost:7351",
    "http://10.0.0.7:8080/v1",
    "http://[::1]:8443/v1",
  ])("accepts %s", (url) => expect(isProviderUrl(url)).toBe(true));

  it.each([
    "ftp://192.168.20.15:3721",
    "http://user:pw@192.168.20.15:3721",
    "http://192.168.20.15:3721/?key=secret",
    "http://192.168.20.15:3721/#frag",
    "http:///v1",
    "http://host/ v1",
    "not a url",
    "",
  ])("rejects %s", (url) => expect(isProviderUrl(url)).toBe(false));
});
