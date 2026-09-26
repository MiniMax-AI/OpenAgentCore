import { afterEach, describe, expect, it, vi } from "vitest";
import { sandboxConsoleConfig } from "./console-config";

afterEach(() => vi.unstubAllGlobals());
describe("bundled console capabilities", () => {
  it("uses the existing console login without sending a project or admin bearer", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ node_installer: true, node_installer_sha256: "a".repeat(64) })));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    expect(await sandboxConsoleConfig(controller.signal)).toEqual({ sandbox_admin: true, node_installer: true, node_installer_sha256: "a".repeat(64), self_hosted_installer: false, self_hosted_installer_sha256: "" });
    expect(fetch).toHaveBeenCalledWith("/console/config", { credentials: "include", signal: controller.signal });
  });
  it.each([{}, { sandbox_admin: "true", node_installer: true }, { sandbox_admin: false, node_installer: true, node_installer_sha256: "bad" }])("does not enable installation without a verified digest %j", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.node_installer).toBe(false);
  });
  it.each([
    [{ self_hosted_installer: true, self_hosted_installer_sha256: "b".repeat(64) }, true],
    [{ self_hosted_installer: false, self_hosted_installer_sha256: "b".repeat(64) }, false],
    [{ self_hosted_installer: "true", self_hosted_installer_sha256: "b".repeat(64) }, false],
    [{ self_hosted_installer: true }, false],
    [{ self_hosted_installer: true, self_hosted_installer_sha256: "B".repeat(64) }, false],
    [{ self_hosted_installer: true, self_hosted_installer_sha256: "b".repeat(63) }, false],
  ])("offers the self-hosted installer only when enabled with a verified digest %j", async (body, offered) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.self_hosted_installer).toBe(offered);
  });
  it("reports unavailable capability on an absent console endpoint", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Not found", { status: 404 })));
    expect(await sandboxConsoleConfig(new AbortController().signal)).toBeNull();
  });
  it("reports a failed read as a failure, not as an unconfigured console", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Bad gateway", { status: 502 })));
    await expect(sandboxConsoleConfig(new AbortController().signal)).rejects.toThrow();
  });
  it("disables sandbox administration only when the console says so", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ sandbox_admin: false }))));
    expect((await sandboxConsoleConfig(new AbortController().signal))?.sandbox_admin).toBe(false);
  });
});
