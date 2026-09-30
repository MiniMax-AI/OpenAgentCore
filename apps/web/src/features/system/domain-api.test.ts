// QueryObserver enables browser polling only when window exists at module load.
vi.hoisted(() => vi.stubGlobal("window", {}));

import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { applyDomain, DOMAIN_RECONNECT_GRACE_MS, domainHostname, domainQuery, domainReconnecting, domainTarget, DomainRequestError } from "./domain-api";

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

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
  it("keeps polling setup while the gateway restarts and reports a disconnect only after the grace period", async () => {
    vi.useFakeTimers();
    const status = (state: string) => new Response(JSON.stringify({ supported: true, state, public_url: null, target_url: "https://core.example.com", message: null }));
    const fetch = vi.fn().mockResolvedValueOnce(status("checking")).mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockImplementation(() => Promise.resolve(new Response(null, { status: 502 })));
    vi.stubGlobal("fetch", fetch);
    const client = new QueryClient();
    const observer = new QueryObserver(client, domainQuery);
    const unsubscribe = observer.subscribe(() => undefined);
    try {
      await vi.advanceTimersByTimeAsync(2_000);
      expect(observer.getCurrentResult().isError).toBe(true);
      expect(domainReconnecting(observer.getCurrentResult())).toBe(true);
      await vi.advanceTimersByTimeAsync(DOMAIN_RECONNECT_GRACE_MS - 4_000);
      expect(domainReconnecting(observer.getCurrentResult())).toBe(true);
      await vi.advanceTimersByTimeAsync(2_000);
      expect(observer.getCurrentResult().isError).toBe(true);
      expect(domainReconnecting(observer.getCurrentResult())).toBe(false);
      fetch.mockImplementation(() => Promise.resolve(status("ready")));
      await vi.advanceTimersByTimeAsync(2_000);
      expect(observer.getCurrentResult()).toMatchObject({ isError: false, data: { state: "ready" } });
      const calls = fetch.mock.calls.length;
      await vi.advanceTimersByTimeAsync(10_000);
      expect(fetch).toHaveBeenCalledTimes(calls);
      expect(fetch.mock.calls.every(([, init]) => init.method === undefined)).toBe(true);
    } finally { unsubscribe(); client.clear(); }
  });
});
