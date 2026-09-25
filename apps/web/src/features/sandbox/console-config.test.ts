import { afterEach, describe, expect, it, vi } from "vitest";
import { sandboxConsoleConfig } from "./console-config";

afterEach(() => vi.unstubAllGlobals());
describe("bundled console capabilities", () => {
  it("uses the existing console login without sending a project or admin bearer", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) })));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    expect(await sandboxConsoleConfig(controller.signal)).toEqual({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64) });
    expect(fetch).toHaveBeenCalledWith("/console/config", { credentials: "include", signal: controller.signal });
  });
  it.each([{}, { sandbox_admin: "true", node_installer: true }, { sandbox_admin: false, node_installer: true, node_installer_sha256: "bad" }])("does not enable installation without a verified digest %j", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.node_installer).toBe(false);
  });
  it("reports unavailable capability on an absent console endpoint", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Not found", { status: 404 })));
    expect(await sandboxConsoleConfig(new AbortController().signal)).toBeNull();
  });
  it("reports a failed read as a failure, not as an unconfigured console", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Bad gateway", { status: 502 })));
    await expect(sandboxConsoleConfig(new AbortController().signal)).rejects.toThrow();
  });
  it.each([true, false])("preserves explicit API key management capability %s", async (enabled) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ api_keys: enabled }))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.api_keys).toBe(enabled);
  });
  it("keeps an absent or malformed API key capability unknown", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ api_keys: "false" }))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.api_keys).toBeUndefined();
  });
});
