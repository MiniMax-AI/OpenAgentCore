import { afterEach, describe, expect, it, vi } from "vitest";
import { nodeFilesAvailable, sandboxConsoleConfig } from "./console-config";

afterEach(() => vi.unstubAllGlobals());
describe("bundled console capabilities", () => {
  it("uses the existing console login without sending a project or admin bearer", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ node_installer: true, node_installer_sha256: "a".repeat(64), node_artifacts: ["docker"] })));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    expect(await sandboxConsoleConfig(controller.signal)).toEqual({ node_installer: true, node_installer_sha256: "a".repeat(64), node_artifacts: ["docker"] });
    expect(fetch).toHaveBeenCalledWith("/console/config", { credentials: "include", signal: controller.signal });
  });
  it.each([{}, { node_installer: "true", node_installer_sha256: "a".repeat(64) }, { node_installer: true, node_installer_sha256: "bad" }])("does not enable installation without a verified digest %j", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    expect((await sandboxConsoleConfig(new AbortController().signal)).node_installer).toBe(false);
  });
  it.each([404, 502])("reports a failed read (HTTP %i) as a failure", async (status) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Failed", { status })));
    await expect(sandboxConsoleConfig(new AbortController().signal)).rejects.toThrow();
  });
  it("blocks a provider's command when the console reports no node files for it", async () => {
    const read = async (body: object) => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ node_installer: true, node_installer_sha256: "a".repeat(64), ...body }))));
      return sandboxConsoleConfig(new AbortController().signal);
    };
    const docker = await read({ node_artifacts: ["docker"] });
    expect(nodeFilesAvailable(docker, "docker")).toBe(true);
    expect(nodeFilesAvailable(docker, "microsandbox")).toBe(false);
    // An absent, null or malformed value reports none.
    for (const body of [{}, { node_artifacts: null }, { node_artifacts: "docker" }, { node_artifacts: { docker: true } }]) {
      const config = await read(body);
      expect(config.node_artifacts).toEqual([]);
      expect(nodeFilesAvailable(config, "docker")).toBe(false);
    }
  });
});
