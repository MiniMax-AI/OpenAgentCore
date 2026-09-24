import { afterEach, describe, expect, it, vi } from "vitest";
import { changeConsoleAuth, parseConsoleAuth, readConsoleAuth } from "./auth";

afterEach(() => vi.unstubAllGlobals());
describe("console authentication boundary", () => {
  it("accepts only finite modes and returns no additional server fields", () => {
    expect(parseConsoleAuth({ mode: "authenticated", username: "admin", token: "secret" })).toEqual({ mode: "authenticated", username: "admin" });
    for (const value of [null, {}, { mode: "admin" }, { mode: "authenticated" }, { mode: "authenticated", username: "" }]) expect(() => parseConsoleAuth(value)).toThrow();
  });
  it("allows the legacy static response only before account mode is established", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html/>", { headers: { "content-type": "text/html" } })));
    expect(await readConsoleAuth()).toEqual({ mode: "legacy" });
    await expect(readConsoleAuth(undefined, true)).rejects.toThrow();
  });
  it("does not turn transport, invalid JSON or explicit mode downgrade into legacy access", async () => {
    for (const response of [new Response("", { status: 503 }), new Response("{}", { headers: { "content-type": "application/json" } }), new Response('{"mode":"legacy"}')]) {
      vi.stubGlobal("fetch", vi.fn(async () => response));
      await expect(readConsoleAuth(undefined, true)).rejects.toThrow();
    }
  });
  it("sends credentials only in a same-origin JSON POST, not in the URL", async () => {
    const fetcher = vi.fn(async () => new Response('{"mode":"authenticated","username":"admin"}'));
    vi.stubGlobal("fetch", fetcher);
    await changeConsoleAuth("login", { username: "admin", password: "private" });
    expect(fetcher.mock.calls[0]).toEqual(["/console/auth/login", expect.objectContaining({ method: "POST", credentials: "same-origin", body: '{"username":"admin","password":"private"}' })]);
  });
});
