import { afterEach, describe, expect, it, vi } from "vitest";
import { changeConsoleAuth, ConsoleAuthError, parseConsoleAuth, readConsoleAuth } from "./auth";

afterEach(() => vi.unstubAllGlobals());
describe("console authentication boundary", () => {
  it("accepts only the login and authenticated modes and keeps no other server field", () => {
    expect(parseConsoleAuth({ mode: "authenticated", username: "admin", token: "secret" })).toEqual({ mode: "authenticated" });
    expect(parseConsoleAuth({ mode: "login" })).toEqual({ mode: "login" });
    for (const value of [null, {}, { mode: "admin" }, { mode: "setup" }, { mode: "legacy" }]) expect(() => parseConsoleAuth(value)).toThrow();
  });
  it("treats an HTML page, a failed read or invalid JSON as a failure, never as access", async () => {
    for (const response of [new Response("<html/>", { headers: { "content-type": "text/html" } }), new Response("", { status: 404 }), new Response("", { status: 503 }), new Response("{}")]) {
      vi.stubGlobal("fetch", vi.fn(async () => response));
      await expect(readConsoleAuth()).rejects.toBeInstanceOf(ConsoleAuthError);
    }
  });
  it("sends the Core key only in a same-origin JSON POST body", async () => {
    const fetcher = vi.fn(async () => new Response('{"mode":"authenticated"}'));
    vi.stubGlobal("fetch", fetcher);
    expect(await changeConsoleAuth({ action: "login", coreKey: "private" })).toEqual({ mode: "authenticated" });
    expect(fetcher.mock.calls[0]).toEqual(["/console/auth/login", expect.objectContaining({ method: "POST", credentials: "same-origin", body: '{"core_key":"private"}' })]);
  });
  it("reports the status of a refused sign-in and how long to wait after too many attempts", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"error":"Too many attempts"}', { status: 429, headers: { "retry-after": "42" } })));
    await expect(changeConsoleAuth({ action: "login", coreKey: "k" })).rejects.toMatchObject({ status: 429, retryAfterSeconds: 42 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"error":"Invalid Core key"}', { status: 401 })));
    await expect(changeConsoleAuth({ action: "login", coreKey: "k" })).rejects.toMatchObject({ status: 401, retryAfterSeconds: null });
  });
});
