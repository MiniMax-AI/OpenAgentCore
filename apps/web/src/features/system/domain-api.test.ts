import { afterEach, describe, expect, it, vi } from "vitest";
import { applyDomain, domainHostname, domainTarget, DomainRequestError } from "./domain-api";

afterEach(() => vi.unstubAllGlobals());

describe("domain navigation and writes", () => {
  it("accepts DNS names while rejecting URLs, IPs and path-like input", () => {
    expect(domainHostname(" CORE.Example.COM ")).toBe("core.example.com");
    expect(domainHostname("例子.com")).toBe("xn--fsqu00a.com");
    for (const input of ["https://core.example.com", "127.0.0.1", "2130706433", "localhost", "core.example.com:443", "user@core.example.com", "core.example.com/path", "core.example.com?x=1", "-core.example.com"]) expect(domainHostname(input)).toBeNull();
    for (const target of ["javascript:alert(1)", "http://core.example.com", "https://core.example.com@evil.example.com", "https://core.example.com/path"]) expect(domainTarget(target)).toBeNull();
    expect(domainTarget("https://core.example.com")).toBe("https://core.example.com");
  });
  it("sends explicit confirmation to the same-origin manager exactly once", async () => {
    const snapshot = { supported: true, state: "checking", public_url: null, target_url: "https://core.example.com", message: null };
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(snapshot), { status: 202 }));
    vi.stubGlobal("fetch", fetch);
    expect(await applyDomain("core.example.com", true, new AbortController().signal)).toEqual(snapshot);
    expect(fetch).toHaveBeenCalledExactlyOnceWith("/console/installation/domain", expect.objectContaining({ method: "POST", credentials: "same-origin", body: JSON.stringify({ hostname: "core.example.com", confirm_public_url_change: "https://core.example.com" }) }));
  });
  it("preserves confirmation errors and never retries a failed write", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: "public_url_confirmation_required", message: "Confirm address" } }), { status: 409 }));
    vi.stubGlobal("fetch", fetch);
    await expect(applyDomain("core.example.com", false, new AbortController().signal)).rejects.toMatchObject({ status: 409, code: "public_url_confirmation_required" });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("rejects a malformed or unsafe manager response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ supported: true, state: "ready", public_url: null, target_url: "https://evil.example.com/path", message: null }))));
    await expect(applyDomain("core.example.com", false, new AbortController().signal)).rejects.toBeInstanceOf(DomainRequestError);
  });
});
